# L2c — Batch Execution Loop Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement `ExecuteBatch` on `Planner` to sequentially execute batch items via CreatePlan + ExecutePlan, with pause/resume for quota, checklists, and failures. Wire into Telegram bot with start, cancel, retry, skip, and resume commands. Add quota auto-resume.

**Architecture:** `ExecuteBatch` lives on `Planner` (alongside `CreatePlan`/`ExecutePlan`). Uses sentinel error types to signal pause conditions back to the Telegram layer, which handles notifications and approvals. A shared `startBatchExecution` helper on `TelegramBot` encapsulates the goroutine + RunHandle + error type-switch pattern. Resume commands re-spawn the same goroutine — the DB state drives which item runs next.

**Tech Stack:** Go 1.22, telegram-bot-api/v5, SQLite (via state.Store), planner.Planner

**Design doc:** `docs/plans/2026-03-13-l2c-batch-execution-loop-design.md`

---

### Task 1: Sentinel error types for batch pause conditions

**Files:**
- Create: `internal/planner/batch_errors.go`
- Create: `internal/planner/batch_errors_test.go`

**Step 1: Write the failing test**

Add to `internal/planner/batch_errors_test.go`:

```go
package planner

import (
	"errors"
	"fmt"
	"testing"
)

func TestBatchErrorTypes(t *testing.T) {
	t.Run("ErrBatchPausedQuota", func(t *testing.T) {
		err := &ErrBatchPausedQuota{Reason: "daily limit reached (18/18)"}
		if !errors.As(err, &ErrBatchPausedQuota{}) {
			t.Fatal("expected errors.As to match")
		}
		if err.Error() == "" {
			t.Fatal("expected non-empty error message")
		}
		if err.Reason != "daily limit reached (18/18)" {
			t.Fatalf("unexpected reason: %q", err.Reason)
		}
	})

	t.Run("ErrBatchPausedChecklist", func(t *testing.T) {
		err := &ErrBatchPausedChecklist{
			BatchID: "batch-123",
			PlanID:  "plan-456",
			ItemID:  7,
			Checks:  []string{"Review UI changes", "Verify accessibility"},
		}
		if err.Error() == "" {
			t.Fatal("expected non-empty error message")
		}
		if err.BatchID != "batch-123" {
			t.Fatalf("unexpected batchID: %q", err.BatchID)
		}
	})

	t.Run("ErrBatchItemFailed", func(t *testing.T) {
		inner := fmt.Errorf("worker crashed")
		err := &ErrBatchItemFailed{ItemID: 3, Err: inner}
		if err.Error() == "" {
			t.Fatal("expected non-empty error message")
		}
		if !errors.Is(err, inner) {
			t.Fatal("expected errors.Is to match inner error")
		}
	})

	t.Run("ErrBatchPhaseDependency", func(t *testing.T) {
		err := &ErrBatchPhaseDependency{Phase: "setup", FailedItems: []int{1, 2}}
		if err.Error() == "" {
			t.Fatal("expected non-empty error message")
		}
		if err.Phase != "setup" {
			t.Fatalf("unexpected phase: %q", err.Phase)
		}
	})
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/planner/ -run TestBatchErrorTypes -v`
Expected: FAIL — types undefined

**Step 3: Write implementation**

Create `internal/planner/batch_errors.go`:

```go
package planner

import "fmt"

// ErrBatchPausedQuota is returned when the usage tracker blocks invocation.
type ErrBatchPausedQuota struct {
	Reason string
}

func (e *ErrBatchPausedQuota) Error() string {
	return fmt.Sprintf("batch paused: quota exhausted (%s)", e.Reason)
}

// ErrBatchPausedChecklist is returned when a plan requires human review.
type ErrBatchPausedChecklist struct {
	BatchID string
	PlanID  string
	ItemID  int64
	Checks  []string
}

func (e *ErrBatchPausedChecklist) Error() string {
	return fmt.Sprintf("batch %s paused: item %d requires checklist approval (plan %s)", e.BatchID, e.ItemID, e.PlanID)
}

// ErrBatchItemFailed is returned when a batch item's plan creation or execution fails.
type ErrBatchItemFailed struct {
	ItemID int64
	Err    error
}

func (e *ErrBatchItemFailed) Error() string {
	return fmt.Sprintf("batch item %d failed: %v", e.ItemID, e.Err)
}

func (e *ErrBatchItemFailed) Unwrap() error {
	return e.Err
}

// ErrBatchPhaseDependency is returned when a phase dependency has failed/skipped items.
type ErrBatchPhaseDependency struct {
	Phase       string
	FailedItems []int
}

func (e *ErrBatchPhaseDependency) Error() string {
	return fmt.Sprintf("batch paused: phase %q dependency has failed items %v", e.Phase, e.FailedItems)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/planner/ -run TestBatchErrorTypes -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/planner/batch_errors.go internal/planner/batch_errors_test.go
git commit -m "feat(planner): add sentinel error types for batch pause conditions"
```

---

### Task 2: ExecuteBatch method on Planner

**Files:**
- Create: `internal/planner/batch.go`
- Create: `internal/planner/batch_test.go`

This is the core execution loop. It uses the Planner's existing `CreatePlan` and `ExecutePlan` methods internally, plus a `CanInvoke func() (bool, string)` function for quota checking.

**Step 1: Add CanInvoke field to Planner**

In `internal/planner/planner.go`, add to the `Planner` struct (after `planByID`):

```go
type Planner struct {
	// ... existing fields ...
	planByID map[string]storedPlan

	canInvoke func() (bool, string) // returns (allowed, blockReason)
}
```

Add a setter after `SetNotifier`:

```go
func (p *Planner) SetCanInvoke(fn func() (bool, string)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.canInvoke = fn
}
```

**Step 2: Write the failing test**

Create `internal/planner/batch_test.go`:

```go
package planner

import (
	"context"
	"fmt"
	"testing"

	"github.com/zyrakk/hivemind/internal/state"
)

// mockBatchDB implements the subset of *state.Store methods used by ExecuteBatch.
// Because Planner takes *state.Store (concrete), we test via integration with a
// real in-memory SQLite store. See helper below.

func newTestStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedProject(t *testing.T, store *state.Store, name string) int64 {
	t.Helper()
	id, err := store.CreateProject(context.Background(), name, "", "", "")
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}
	return id
}

func TestExecuteBatch_AllItemsComplete(t *testing.T) {
	store := newTestStore(t)
	projID := seedProject(t, store, "test-proj")
	ctx := context.Background()

	batchID, err := store.CreateBatch(ctx, projID, "", []string{
		"Add YAML config parser for scoring rules",
		"Add dry-run flag to the audit command module",
	})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if err := store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning); err != nil {
		t.Fatalf("update batch status: %v", err)
	}

	// Stub planner that auto-completes every plan.
	p := newStubPlanner(store, stubPlannerOpts{
		autoComplete: true,
	})

	err = p.ExecuteBatch(ctx, batchID)
	if err != nil {
		t.Fatalf("ExecuteBatch: %v", err)
	}

	batch, _ := store.GetBatch(ctx, batchID)
	if batch.Status != state.BatchStatusCompleted {
		t.Fatalf("expected batch completed, got %q", batch.Status)
	}
	if batch.CompletedItems != 2 {
		t.Fatalf("expected 2 completed items, got %d", batch.CompletedItems)
	}
}

func TestExecuteBatch_CancellationPausesBatch(t *testing.T) {
	store := newTestStore(t)
	projID := seedProject(t, store, "test-proj")
	ctx, cancel := context.WithCancel(context.Background())

	batchID, _ := store.CreateBatch(ctx, projID, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning)

	// Cancel immediately so the loop sees ctx.Done() before processing.
	cancel()

	p := newStubPlanner(store, stubPlannerOpts{autoComplete: true})
	err := p.ExecuteBatch(ctx, batchID)

	if err == nil || err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	batch, _ := store.GetBatch(context.Background(), batchID)
	if batch.Status != state.BatchStatusPaused {
		t.Fatalf("expected paused, got %q", batch.Status)
	}
}

func TestExecuteBatch_QuotaExhaustedPausesBatch(t *testing.T) {
	store := newTestStore(t)
	projID := seedProject(t, store, "test-proj")
	ctx := context.Background()

	batchID, _ := store.CreateBatch(ctx, projID, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning)

	p := newStubPlanner(store, stubPlannerOpts{autoComplete: true})
	p.SetCanInvoke(func() (bool, string) {
		return false, "daily limit reached (18/18)"
	})

	err := p.ExecuteBatch(ctx, batchID)

	var quotaErr *ErrBatchPausedQuota
	if err == nil || !isErrType(err, &quotaErr) {
		t.Fatalf("expected ErrBatchPausedQuota, got %v", err)
	}

	batch, _ := store.GetBatch(ctx, batchID)
	if batch.Status != state.BatchStatusPaused {
		t.Fatalf("expected paused, got %q", batch.Status)
	}
}

func TestExecuteBatch_PlanFailurePausesBatch(t *testing.T) {
	store := newTestStore(t)
	projID := seedProject(t, store, "test-proj")
	ctx := context.Background()

	batchID, _ := store.CreateBatch(ctx, projID, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning)

	p := newStubPlanner(store, stubPlannerOpts{
		createPlanErr: fmt.Errorf("LLM unavailable"),
	})

	err := p.ExecuteBatch(ctx, batchID)

	var itemErr *ErrBatchItemFailed
	if err == nil || !isErrType(err, &itemErr) {
		t.Fatalf("expected ErrBatchItemFailed, got %v", err)
	}

	batch, _ := store.GetBatch(ctx, batchID)
	if batch.Status != state.BatchStatusPaused {
		t.Fatalf("expected paused, got %q", batch.Status)
	}

	items, _ := store.GetBatchItems(ctx, batchID)
	if items[0].Status != state.BatchItemStatusFailed {
		t.Fatalf("expected item failed, got %q", items[0].Status)
	}
}

// isErrType is a generic helper for error type checking in tests.
func isErrType[T error](err error, target *T) bool {
	return err != nil && (func() bool {
		var t T
		return errors.As(err, &t)
	})()
}

// --- Stub planner infrastructure ---

type stubPlannerOpts struct {
	autoComplete  bool   // if true, ExecutePlan succeeds immediately
	createPlanErr error  // if set, CreatePlan returns this error
	execPlanErr   error  // if set, ExecutePlan returns this error
	hasChecklist  bool   // if true, created plans have UserChecklist items
}

// newStubPlanner creates a minimal Planner wired to a real store
// but with stubbed LLM/launcher/engine for testing ExecuteBatch.
// The actual implementation of this helper depends on how ExecuteBatch
// calls CreatePlan and ExecutePlan internally — see Step 3 notes.
```

**Note on test architecture:** Because `Planner` uses `*state.Store` (concrete type), we use a real in-memory SQLite store. The LLM, launcher, and engine are stubbed via their interfaces. The `newStubPlanner` helper wires these together. The exact implementation of `newStubPlanner` depends on how ExecuteBatch calls CreatePlan/ExecutePlan — the implementer should create mock implementations of `plannerLLM`, `plannerLauncher`, and `plannerEngine` that auto-complete or fail as configured.

**Step 2b: Run test to verify it fails**

Run: `go test ./internal/planner/ -run TestExecuteBatch -v`
Expected: FAIL — `ExecuteBatch` undefined

**Step 3: Implement ExecuteBatch**

Create `internal/planner/batch.go`:

```go
package planner

import (
	"context"
	"fmt"

	"github.com/zyrakk/hivemind/internal/state"
)

// ExecuteBatch runs batch items sequentially: CreatePlan + ExecutePlan per item.
// Returns nil on completion, or a sentinel error on pause/failure.
func (p *Planner) ExecuteBatch(ctx context.Context, batchID string) error {
	batch, err := p.db.GetBatch(ctx, batchID)
	if err != nil {
		return fmt.Errorf("load batch: %w", err)
	}
	if batch.Status != state.BatchStatusRunning {
		return fmt.Errorf("batch %s is %s, expected running", batchID, batch.Status)
	}

	// Resolve project ref for CreatePlan (which takes project ref string).
	project, err := p.db.GetProjectByID(ctx, batch.ProjectID)
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}
	projectRef := project.Name

	for {
		// 2a. Check cancellation.
		select {
		case <-ctx.Done():
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
			return context.Canceled
		default:
		}

		// 2b. Check quota.
		p.mu.Lock()
		canInvoke := p.canInvoke
		p.mu.Unlock()
		if canInvoke != nil {
			if ok, reason := canInvoke(); !ok {
				_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
				return &ErrBatchPausedQuota{Reason: reason}
			}
		}

		// 2c. Get next pending item.
		item, err := p.db.GetNextPendingBatchItem(ctx, batchID)
		if err != nil {
			return fmt.Errorf("get next item: %w", err)
		}
		if item == nil {
			// All items done.
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusCompleted)
			return nil
		}

		// 2d. Check phase dependencies.
		if item.PhaseDependsOn != nil && *item.PhaseDependsOn != "" {
			if phaseErr := p.checkPhaseDeps(ctx, batchID, item); phaseErr != nil {
				_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
				return phaseErr
			}
		}

		// 2e. Mark item running.
		_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusRunning, "", "")

		// 2f. Notify progress.
		if p.notifier != nil {
			_ = p.notifier.NotifyProgress(ctx, projectRef, batchID, "planning",
				fmt.Sprintf("Item %d/%d: %s", item.Sequence, batch.TotalItems, item.Directive))
		}

		// 2g. Create plan.
		planResult, planErr := p.CreatePlan(ctx, item.Directive, projectRef)
		if planErr != nil {
			_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusFailed, "", planErr.Error())
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
			return &ErrBatchItemFailed{ItemID: item.ID, Err: planErr}
		}

		// 2h. Check for UserChecklist items.
		if planResult.Plan != nil && planHasUserChecklist(planResult.Plan) {
			_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusRunning, planResult.PlanID, "")
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
			checks := collectUserChecks(planResult.Plan)
			return &ErrBatchPausedChecklist{
				BatchID: batchID,
				PlanID:  planResult.PlanID,
				ItemID:  item.ID,
				Checks:  checks,
			}
		}

		// 2i. Mark plan approved.
		_ = p.db.UpdatePlanStatus(ctx, planResult.PlanID, state.PlanStatusApproved)

		// 2j. Notify progress — executing.
		if p.notifier != nil {
			_ = p.notifier.NotifyProgress(ctx, projectRef, batchID, "executing",
				fmt.Sprintf("Item %d/%d: executing...", item.Sequence, batch.TotalItems))
		}

		// 2k. Execute plan.
		execErr := p.ExecutePlan(ctx, planResult.PlanID)
		if execErr != nil {
			_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusFailed, planResult.PlanID, execErr.Error())
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
			return &ErrBatchItemFailed{ItemID: item.ID, Err: execErr}
		}

		// 2l. Mark item completed, increment progress.
		_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusCompleted, planResult.PlanID, "")
		_ = p.db.IncrementBatchProgress(ctx, batchID)

		// 2m. Notify progress — item done.
		if p.notifier != nil {
			_ = p.notifier.NotifyProgress(ctx, projectRef, batchID, "completed",
				fmt.Sprintf("Item %d/%d: done", item.Sequence, batch.TotalItems))
		}
	}
}

func planHasUserChecklist(plan *llm.TaskPlan) bool {
	for _, task := range plan.Tasks {
		if len(task.UserChecklist) > 0 {
			return true
		}
	}
	return false
}

func collectUserChecks(plan *llm.TaskPlan) []string {
	var checks []string
	for _, task := range plan.Tasks {
		for _, c := range task.UserChecklist {
			checks = append(checks, c.Description)
		}
	}
	return checks
}

func (p *Planner) checkPhaseDeps(ctx context.Context, batchID string, item *state.BatchItem) error {
	items, err := p.db.GetBatchItems(ctx, batchID)
	if err != nil {
		return fmt.Errorf("load batch items for phase check: %w", err)
	}

	var failedSeqs []int
	for _, other := range items {
		if other.Phase == nil || *other.Phase != *item.PhaseDependsOn {
			continue
		}
		if other.Status == state.BatchItemStatusFailed || other.Status == state.BatchItemStatusSkipped {
			failedSeqs = append(failedSeqs, other.Sequence)
		}
		if other.Status != state.BatchItemStatusCompleted &&
			other.Status != state.BatchItemStatusFailed &&
			other.Status != state.BatchItemStatusSkipped {
			// Phase item not yet terminal — shouldn't happen with sequential execution
			// but defensively treat as not ready.
			return fmt.Errorf("phase %q item %d not yet terminal (status: %s)", *item.PhaseDependsOn, other.Sequence, other.Status)
		}
	}

	if len(failedSeqs) > 0 {
		return &ErrBatchPhaseDependency{Phase: *item.PhaseDependsOn, FailedItems: failedSeqs}
	}
	return nil
}
```

**Note for implementer:** The `llm.TaskPlan` type has `Tasks []Task` where each `Task` has `UserChecklist []CheckItem`. You need to import `"github.com/zyrakk/hivemind/internal/llm"` in batch.go. Check `internal/llm/types.go` for the exact field names.

**Step 4: Run tests**

Run: `go test ./internal/planner/ -run TestExecuteBatch -v -count=1`
Expected: All PASS (after wiring the stub planner correctly)

**Step 5: Run full planner tests**

Run: `go test ./internal/planner/ -v -count=1 2>&1 | tail -20`
Expected: All PASS, no regressions

**Step 6: Commit**

```bash
git add internal/planner/batch.go internal/planner/batch_test.go internal/planner/planner.go
git commit -m "feat(planner): add ExecuteBatch method with sequential item execution"
```

---

### Task 3: Extend plannerExecutor interface + stateStore interface for batch execution

**Files:**
- Modify: `internal/notify/telegram.go` (plannerExecutor + stateStore interfaces)
- Modify: `internal/notify/telegram_test.go` (mock updates)

**Step 1: Add ExecuteBatch to plannerExecutor interface**

In `internal/notify/telegram.go`, extend the `plannerExecutor` interface:

```go
type plannerExecutor interface {
	ExecutePlan(ctx context.Context, planID string) error
	ExecuteBatch(ctx context.Context, batchID string) error
}
```

**Step 2: Add batch execution methods to stateStore interface**

The commands need `UpdateBatchItemStatus` and `GetRunningBatches`. Add to `stateStore`:

```go
UpdateBatchItemStatus(ctx context.Context, itemID int64, status, planID, errorMsg string) error
GetRunningBatches(ctx context.Context) ([]state.Batch, error)
```

**Step 3: Update mockStore**

Add fields and methods to mockStore for the new interface methods:

```go
// In mockStore struct — no new fields needed, reuse existing batches/batchItems maps.

func (m *mockStore) UpdateBatchItemStatus(ctx context.Context, itemID int64, status, planID, errorMsg string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	for bID, items := range m.batchItems {
		for i, item := range items {
			if item.ID == itemID {
				m.batchItems[bID][i].Status = status
				if planID != "" {
					m.batchItems[bID][i].PlanID = &planID
				}
				if errorMsg != "" {
					m.batchItems[bID][i].Error = &errorMsg
				}
				return nil
			}
		}
	}
	return state.ErrNotFound
}

func (m *mockStore) GetRunningBatches(ctx context.Context) ([]state.Batch, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []state.Batch
	for _, b := range m.batches {
		if b.Status == state.BatchStatusRunning {
			result = append(result, *b)
		}
	}
	return result, nil
}
```

**Step 4: Update newTestBot in telegram_test.go**

Add a mock `ExecuteBatch` to the mockPlannerExec (if it exists) or update the plannerExecutor mock. Check how `newTestBot` currently wires `plannerExec` — if it's nil, that's fine for commands that don't call ExecuteBatch directly.

**Step 5: Verify it compiles**

Run: `go build ./...`
Expected: BUILD SUCCESS

**Step 6: Run existing tests**

Run: `go test ./internal/notify/... -v -count=1 2>&1 | tail -20`
Expected: All PASS

**Step 7: Commit**

```bash
git add internal/notify/telegram.go internal/notify/telegram_test.go
git commit -m "feat(notify): extend plannerExecutor and stateStore interfaces for batch execution"
```

---

### Task 4: startBatchExecution shared helper on TelegramBot

**Files:**
- Modify: `internal/notify/telegram.go` (add startBatchExecution + FormatBatchPausedMessage)
- Modify: `internal/notify/formatter.go` (add FormatBatchCompletedMessage + FormatBatchFailedMessage)
- Modify: `internal/notify/formatter_test.go` (tests for new formatters)
- Modify: `internal/notify/telegram_test.go` (test startBatchExecution via cmdStartBatch)

**Step 1: Write formatter tests**

Add to `internal/notify/formatter_test.go`:

```go
func TestFormatBatchCompletedMessage(t *testing.T) {
	msg := FormatBatchCompletedMessage("flux", "batch-123", 4)
	for _, want := range []string{"BATCH COMPLETED", "flux", "4/4", "┌", "└"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected %q in: %s", want, msg)
		}
	}
}

func TestFormatBatchFailedMessage(t *testing.T) {
	msg := FormatBatchFailedMessage("flux", "batch-123", 3, "worker crashed on item 3")
	for _, want := range []string{"BATCH PAUSED", "flux", "worker crashed", "/retry", "/skip"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected %q in: %s", want, msg)
		}
	}
}
```

**Step 2: Write formatter implementations**

Add to `internal/notify/formatter.go`:

```go
func FormatBatchCompletedMessage(projectRef, batchID string, totalItems int) string {
	var box strings.Builder
	box.WriteString("┌─ BATCH COMPLETED ───────────\n")
	box.WriteString(fmt.Sprintf("│ Project: %s\n", projectRef))
	box.WriteString(fmt.Sprintf("│ Items:   %d/%d\n", totalItems, totalItems))
	box.WriteString("│ Status:  done\n")
	box.WriteString("└────────────────────────────")
	return TruncateTelegramMessage(codeBlock(box.String()))
}

func FormatBatchFailedMessage(projectRef, batchID string, itemSeq int, errMsg string) string {
	var box strings.Builder
	box.WriteString("┌─ BATCH PAUSED ──────────────\n")
	box.WriteString(fmt.Sprintf("│ Project: %s\n", projectRef))
	box.WriteString(fmt.Sprintf("│ Failed:  item %d\n", itemSeq))
	box.WriteString(fmt.Sprintf("│ Error:   %s\n", errMsg))
	box.WriteString("├────────────────────────────\n")
	box.WriteString(fmt.Sprintf("│ /retry %s\n", batchID))
	box.WriteString(fmt.Sprintf("│ /skip %s\n", batchID))
	box.WriteString("└────────────────────────────")
	return TruncateTelegramMessage(codeBlock(box.String()))
}
```

**Step 3: Implement startBatchExecution**

Add to `internal/notify/telegram.go`:

```go
// startBatchExecution spawns a goroutine to run ExecuteBatch with a RunHandle.
// Shared by cmdStartBatch, cmdRetry, cmdSkip, cmdResumeBatch, and quota auto-resume.
func (t *TelegramBot) startBatchExecution(batchID, projectRef string) {
	go func() {
		if t.plannerExec == nil {
			_ = t.enqueueMessage(context.Background(), formatEscapedLines(
				fmt.Sprintf("✗ Batch %s failed: planner is not configured", batchID),
			))
			return
		}

		runCtx, runCancel := context.WithCancel(context.Background())
		handle := &RunHandle{
			Cancel: runCancel,
			Done:   make(chan error, 1),
			PlanID: batchID, // reuse PlanID field for batch ID
		}

		t.activeRunsMu.Lock()
		t.activeRuns[batchID] = handle
		t.activeRunsMu.Unlock()

		defer func() {
			t.activeRunsMu.Lock()
			delete(t.activeRuns, batchID)
			t.activeRunsMu.Unlock()
			runCancel()
		}()

		err := t.plannerExec.ExecuteBatch(runCtx, batchID)
		handle.Done <- err

		t.handleBatchResult(batchID, projectRef, err)
	}()
}

func (t *TelegramBot) handleBatchResult(batchID, projectRef string, err error) {
	ctx := context.Background()

	switch {
	case err == nil:
		batch, _ := t.store.GetBatch(ctx, batchID)
		total := 0
		if batch != nil {
			total = batch.TotalItems
		}
		_ = t.enqueueMessage(ctx, FormatBatchCompletedMessage(projectRef, batchID, total))

	case err == context.Canceled:
		// Silent — user already sent /cancel_batch.

	default:
		var quotaErr *planner.ErrBatchPausedQuota
		var checkErr *planner.ErrBatchPausedChecklist
		var itemErr  *planner.ErrBatchItemFailed
		var phaseErr *planner.ErrBatchPhaseDependency

		switch {
		case errors.As(err, &quotaErr):
			_ = t.enqueueMessage(ctx, formatEscapedLines(
				fmt.Sprintf("⏸ Batch %s paused: %s. Will auto-resume when quota resets.", batchID, quotaErr.Reason),
			))

		case errors.As(err, &checkErr):
			t.RegisterPendingApproval(PendingApproval{
				ID:          checkErr.PlanID,
				Type:        "plan",
				ProjectID:   projectRef,
				Description: fmt.Sprintf("batch %s item %d checklist", checkErr.BatchID, checkErr.ItemID),
				AcceptsText: false,
			})
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("⏸ Batch %s paused — item needs review:\n", batchID))
			for _, c := range checkErr.Checks {
				sb.WriteString(fmt.Sprintf("  • %s\n", c))
			}
			sb.WriteString(fmt.Sprintf("\n/approve %s", checkErr.PlanID))
			_ = t.enqueueMessage(ctx, formatEscapedLines(sb.String()))

		case errors.As(err, &itemErr):
			// Find the item sequence for display.
			items, _ := t.store.GetBatchItems(ctx, batchID)
			seq := 0
			errMsg := "unknown error"
			for _, item := range items {
				if item.ID == itemErr.ItemID {
					seq = item.Sequence
					if item.Error != nil {
						errMsg = *item.Error
					}
					break
				}
			}
			_ = t.enqueueMessage(ctx, FormatBatchFailedMessage(projectRef, batchID, seq, errMsg))

		case errors.As(err, &phaseErr):
			_ = t.enqueueMessage(ctx, formatEscapedLines(
				fmt.Sprintf("⏸ Batch %s paused: phase %q has failed items %v.\n/skip %s or fix and /retry %s",
					batchID, phaseErr.Phase, phaseErr.FailedItems, batchID, batchID),
			))
		}
	}
}
```

**Step 4: Update cmdStartBatch to call startBatchExecution**

In `internal/notify/telegram.go`, modify `cmdStartBatch` — after the existing status update and items fetch, add:

```go
	t.startBatchExecution(batchID, projectRef)
```

This requires resolving projectRef from batch.ProjectID. Add the lookup before the existing `return`:

```go
	// Resolve project ref for display.
	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	if detail, err := t.store.GetProjectDetail(ctx, projectRef); err == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	t.startBatchExecution(batchID, projectRef)

	return formatEscapedLines(fmt.Sprintf("▸ Batch started. Executing directive 1/%d...", len(items))), nil
```

**Step 5: Update cmdCancelBatch to cancel RunHandle**

In `cmdCancelBatch`, before the existing `UpdateBatchStatus`, add RunHandle cancellation:

```go
	// Cancel active execution if running.
	t.activeRunsMu.Lock()
	handle, running := t.activeRuns[batchID]
	t.activeRunsMu.Unlock()
	if running {
		handle.Cancel()
		<-handle.Done
	}
```

**Step 6: Run tests**

Run: `go test ./internal/notify/... -v -count=1 2>&1 | tail -30`
Expected: All PASS

**Step 7: Commit**

```bash
git add internal/notify/telegram.go internal/notify/telegram_test.go internal/notify/formatter.go internal/notify/formatter_test.go
git commit -m "feat(notify): add startBatchExecution helper and wire into start/cancel commands"
```

---

### Task 5: /retry command

**Files:**
- Modify: `internal/notify/telegram.go` (add cmdRetry + wire)
- Modify: `internal/notify/telegram_test.go` (add tests)

**Step 1: Write the failing tests**

Add to `internal/notify/telegram_test.go`:

```go
func TestCmdRetry_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
		"Add dry-run flag to the audit command",
	})
	// Simulate: batch paused, first item failed.
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.batchItems[batchID][0].Status = state.BatchItemStatusFailed
	errMsg := "LLM unavailable"
	store.batchItems[batchID][0].Error = &errMsg
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "retry", batchID)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !strings.Contains(msg, "Retrying") {
		t.Fatalf("expected retry message, got %q", msg)
	}

	store.mu.Lock()
	if store.batchItems[batchID][0].Status != state.BatchItemStatusPending {
		t.Fatalf("expected item reset to pending, got %q", store.batchItems[batchID][0].Status)
	}
	if store.batches[batchID].Status != state.BatchStatusRunning {
		t.Fatalf("expected batch running, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdRetry_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "retry", "")
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage, got %q", msg)
	}
}

func TestCmdRetry_NoFailedItem(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "retry", batchID)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !strings.Contains(msg, "no failed item") {
		t.Fatalf("expected no failed item message, got %q", msg)
	}
}
```

**Step 2: Implement cmdRetry**

```go
func (t *TelegramBot) cmdRetry(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /retry {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}
	if batch.Status != state.BatchStatusPaused {
		return formatEscapedLines(fmt.Sprintf("✗ Batch is %s, not paused.", batch.Status)), nil
	}

	// Find last failed item.
	items, err := t.store.GetBatchItems(ctx, batchID)
	if err != nil {
		return "", err
	}
	var failedItem *state.BatchItem
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Status == state.BatchItemStatusFailed {
			failedItem = &items[i]
			break
		}
	}
	if failedItem == nil {
		return formatEscapedLines("✗ No failed item found to retry."), nil
	}

	// Reset failed item to pending.
	if err := t.store.UpdateBatchItemStatus(ctx, failedItem.ID, state.BatchItemStatusPending, "", ""); err != nil {
		return "", err
	}

	// Set batch to running.
	if err := t.store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning); err != nil {
		return "", err
	}

	// Resolve project ref.
	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	if detail, detailErr := t.store.GetProjectDetail(ctx, projectRef); detailErr == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	t.startBatchExecution(batchID, projectRef)

	return formatEscapedLines(fmt.Sprintf("▸ Retrying item %d...", failedItem.Sequence)), nil
}
```

**Step 3: Wire into handleCommand**

```go
	case "retry":
		return t.cmdRetry(ctx, args)
```

**Step 4: Run tests**

Run: `go test ./internal/notify/ -run TestCmdRetry -v`
Expected: All PASS

**Step 5: Commit**

```bash
git add internal/notify/telegram.go internal/notify/telegram_test.go
git commit -m "feat(notify): add /retry command for retrying failed batch items"
```

---

### Task 6: /skip command

**Files:**
- Modify: `internal/notify/telegram.go` (add cmdSkip + wire)
- Modify: `internal/notify/telegram_test.go` (add tests)

**Step 1: Write the failing tests**

```go
func TestCmdSkip_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
		"Add dry-run flag to the audit command",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.batchItems[batchID][0].Status = state.BatchItemStatusFailed
	errMsg := "LLM unavailable"
	store.batchItems[batchID][0].Error = &errMsg
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "skip", batchID)
	if err != nil {
		t.Fatalf("skip failed: %v", err)
	}
	if !strings.Contains(msg, "Skipped") {
		t.Fatalf("expected skip message, got %q", msg)
	}

	store.mu.Lock()
	if store.batchItems[batchID][0].Status != state.BatchItemStatusSkipped {
		t.Fatalf("expected item skipped, got %q", store.batchItems[batchID][0].Status)
	}
	if store.batches[batchID].Status != state.BatchStatusRunning {
		t.Fatalf("expected batch running, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdSkip_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "skip", "")
	if err != nil {
		t.Fatalf("skip failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage, got %q", msg)
	}
}
```

**Step 2: Implement cmdSkip**

```go
func (t *TelegramBot) cmdSkip(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /skip {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}
	if batch.Status != state.BatchStatusPaused {
		return formatEscapedLines(fmt.Sprintf("✗ Batch is %s, not paused.", batch.Status)), nil
	}

	items, err := t.store.GetBatchItems(ctx, batchID)
	if err != nil {
		return "", err
	}
	var failedItem *state.BatchItem
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Status == state.BatchItemStatusFailed {
			failedItem = &items[i]
			break
		}
	}
	if failedItem == nil {
		return formatEscapedLines("✗ No failed item found to skip."), nil
	}

	if err := t.store.UpdateBatchItemStatus(ctx, failedItem.ID, state.BatchItemStatusSkipped, "", ""); err != nil {
		return "", err
	}

	if err := t.store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning); err != nil {
		return "", err
	}

	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	if detail, detailErr := t.store.GetProjectDetail(ctx, projectRef); detailErr == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	t.startBatchExecution(batchID, projectRef)

	return formatEscapedLines(fmt.Sprintf("⊘ Skipped item %d. Continuing...", failedItem.Sequence)), nil
}
```

**Step 3: Wire into handleCommand**

```go
	case "skip":
		return t.cmdSkip(ctx, args)
```

**Step 4: Run tests, commit**

```bash
git add internal/notify/telegram.go internal/notify/telegram_test.go
git commit -m "feat(notify): add /skip command for skipping failed batch items"
```

---

### Task 7: /resume_batch command

**Files:**
- Modify: `internal/notify/telegram.go` (add cmdResumeBatch + wire)
- Modify: `internal/notify/telegram_test.go` (add tests)

**Step 1: Write the failing tests**

```go
func TestCmdResumeBatch_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "resume_batch", batchID)
	if err != nil {
		t.Fatalf("resume_batch failed: %v", err)
	}
	if !strings.Contains(msg, "Resuming") {
		t.Fatalf("expected resume message, got %q", msg)
	}

	store.mu.Lock()
	if store.batches[batchID].Status != state.BatchStatusRunning {
		t.Fatalf("expected running, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdResumeBatch_NotPaused(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})

	msg, err := bot.handleCommand(ctx, "resume_batch", batchID)
	if err != nil {
		t.Fatalf("resume_batch failed: %v", err)
	}
	if !strings.Contains(msg, "not paused") {
		t.Fatalf("expected not paused message, got %q", msg)
	}
}

func TestCmdResumeBatch_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "resume_batch", "")
	if err != nil {
		t.Fatalf("resume_batch failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage, got %q", msg)
	}
}
```

**Step 2: Implement cmdResumeBatch**

```go
func (t *TelegramBot) cmdResumeBatch(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /resume_batch {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}
	if batch.Status != state.BatchStatusPaused {
		return formatEscapedLines(fmt.Sprintf("✗ Batch is %s, not paused.", batch.Status)), nil
	}

	if err := t.store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning); err != nil {
		return "", err
	}

	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	if detail, detailErr := t.store.GetProjectDetail(ctx, projectRef); detailErr == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	t.startBatchExecution(batchID, projectRef)

	return formatEscapedLines(fmt.Sprintf("▸ Resuming batch %s...", batchID)), nil
}
```

**Step 3: Wire into handleCommand**

```go
	case "resume_batch":
		return t.cmdResumeBatch(ctx, args)
```

**Step 4: Run tests, commit**

```bash
git add internal/notify/telegram.go internal/notify/telegram_test.go
git commit -m "feat(notify): add /resume_batch command"
```

---

### Task 8: Quota auto-resume callback on UsageTracker

**Files:**
- Modify: `internal/engine/usage_tracker.go` (add OnResumeFromQuota)
- Modify: `internal/engine/usage_tracker_test.go` (add test)

**Step 1: Write the failing test**

Add to `internal/engine/usage_tracker_test.go`:

```go
func TestOnResumeFromQuota(t *testing.T) {
	cfg := UsageTrackerConfig{
		SoftLimitDaily:  2,
		HardLimitDaily:  3,
		SoftLimitWeekly: 10,
		HardLimitWeekly: 15,
	}
	tracker := NewUsageTracker(cfg, nil)

	resumed := false
	tracker.OnResumeFromQuota(func() {
		resumed = true
	})

	// Record up to hard limit.
	tracker.Record(100, 50)
	tracker.Record(100, 50)
	tracker.Record(100, 50)

	if tracker.CanInvoke() {
		t.Fatal("expected blocked at hard limit")
	}
	if resumed {
		t.Fatal("should not have resumed yet")
	}

	// Simulate day rollover by advancing time.
	tracker.mu.Lock()
	tracker.dailyResetAt = tracker.dailyResetAt.Add(-25 * time.Hour)
	tracker.mu.Unlock()

	// CanInvoke resets counters on new day → should trigger resume.
	if !tracker.CanInvoke() {
		t.Fatal("expected unblocked after day reset")
	}
	if !resumed {
		t.Fatal("expected resume callback to fire after quota reset")
	}
}

func TestOnResumeFromQuota_NilTracker(t *testing.T) {
	var tracker *UsageTracker
	tracker.OnResumeFromQuota(func() {}) // should not panic
}
```

**Step 2: Implement OnResumeFromQuota**

Add to `internal/engine/usage_tracker.go`:

Add a field to `UsageTracker`:

```go
type UsageTracker struct {
	// ... existing fields ...
	onAlert          func(message string)
	onResume         func() // called when quota transitions blocked → unblocked
	wasBlocked       bool   // tracks previous blocked state for edge detection
	logger           *slog.Logger
	nowFn            func() time.Time
}
```

Add the setter:

```go
func (t *UsageTracker) OnResumeFromQuota(cb func()) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onResume = cb
}
```

Modify `CanInvoke` to detect the blocked→unblocked transition:

```go
func (t *UsageTracker) CanInvoke() bool {
	if t == nil {
		return true
	}

	t.mu.Lock()
	t.resetIfNewDay()
	t.resetIfNewWeek()

	allowed := t.dailyCalls < t.config.HardLimitDaily && t.weeklyCalls < t.config.HardLimitWeekly
	wasBlocked := t.wasBlocked
	t.wasBlocked = !allowed
	onResume := t.onResume
	t.mu.Unlock()

	// Fire resume callback on blocked → unblocked transition.
	if allowed && wasBlocked && onResume != nil {
		onResume()
	}

	return allowed
}
```

Also update `Record` to track `wasBlocked`:

```go
func (t *UsageTracker) Record(inputTokens, outputTokens int) {
	// ... existing code ...
	// After incrementing counters, before unlock:
	t.wasBlocked = t.dailyCalls >= t.config.HardLimitDaily || t.weeklyCalls >= t.config.HardLimitWeekly
	// ... rest unchanged ...
}
```

**Step 3: Run test**

Run: `go test ./internal/engine/ -run TestOnResumeFromQuota -v`
Expected: PASS

**Step 4: Run full engine tests**

Run: `go test ./internal/engine/ -v -count=1 2>&1 | tail -20`
Expected: All PASS

**Step 5: Commit**

```bash
git add internal/engine/usage_tracker.go internal/engine/usage_tracker_test.go
git commit -m "feat(engine): add OnResumeFromQuota callback to UsageTracker"
```

---

### Task 9: Wire quota auto-resume into TelegramBot startup

**Files:**
- Modify: `internal/notify/telegram.go` (register quota callback)
- Modify: `internal/notify/telegram_test.go` (test)

**Step 1: Add usageTracker field to TelegramBot**

In `internal/notify/telegram.go`, add to the `TelegramBot` struct:

```go
usageTracker interface {
	OnResumeFromQuota(cb func())
	CanInvoke() bool
	BlockReason() string
}
```

Or simpler — just store the concrete `*engine.UsageTracker` reference:

Add a setter method:

```go
func (t *TelegramBot) SetUsageTracker(tracker *engine.UsageTracker) {
	if t == nil || tracker == nil {
		return
	}
	tracker.OnResumeFromQuota(func() {
		t.resumeQuotaPausedBatches()
	})
}

func (t *TelegramBot) resumeQuotaPausedBatches() {
	if t.store == nil {
		return
	}
	ctx := context.Background()
	// Find all paused batches — a simple approach. In practice, we'd store
	// which batches were paused for quota. For now, resume all paused batches
	// since manually-paused batches will just re-pause if the user wants.
	// A more targeted approach: check batch recovery_note for "quota".
	// For now, keep it simple and let the loop handle it.
}
```

**Note for implementer:** The exact wiring depends on how the orchestrator passes the UsageTracker to the TelegramBot. Check `cmd/orchestrator/` for where `NewTelegramBot` is called and where `UsageTracker` is created. Wire them together there. The `resumeQuotaPausedBatches` function should query for paused batches and call `startBatchExecution` for each. Use `GetRunningBatches` pattern — you may need a `GetPausedBatches` query, or filter the existing data.

**Step 2: Write test**

```go
func TestResumeQuotaPausedBatches(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.mu.Unlock()

	bot.resumeQuotaPausedBatches()

	// Verify batch was set to running (startBatchExecution would be called
	// but without plannerExec, it just fails silently — check status change).
	store.mu.Lock()
	// The batch should still be paused since plannerExec is nil in test.
	// This test verifies the method doesn't panic and attempts to resume.
	store.mu.Unlock()
}
```

**Step 3: Implement, verify, commit**

```bash
git add internal/notify/telegram.go internal/notify/telegram_test.go
git commit -m "feat(notify): wire quota auto-resume callback into TelegramBot"
```

---

### Task 10: Update help message + hyphen fallback for new commands + full verification

**Files:**
- Modify: `internal/notify/formatter.go` (FormatHelpMessage)
- Modify: `internal/notify/formatter_test.go` (update help test)
- Modify: `internal/notify/telegram_test.go` (hyphen fallback tests for new commands)

**Step 1: Update FormatHelpMessage**

Add `/retry`, `/skip`, `/resume_batch` to the help message in `internal/notify/formatter.go`.

**Step 2: Update TestFormatHelpMessage**

Add the new commands to the expected strings list.

**Step 3: Add hyphen fallback tests**

Test that `/resume-batch`, `/start-batch` work via the hyphen fallback (already in place from L2b Task 8).

**Step 4: Full test suite verification**

Run: `go test ./... -v -count=1 2>&1 | tail -30`
Expected: All PASS

**Step 5: Build verification**

Run: `go build ./...`
Expected: BUILD SUCCESS

**Step 6: Commit**

```bash
git add internal/notify/formatter.go internal/notify/formatter_test.go internal/notify/telegram.go internal/notify/telegram_test.go
git commit -m "feat(notify): update help with retry/skip/resume_batch and full L2c verification"
```
