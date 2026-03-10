# Roadmap Automation — Revised Design

Revision of `plan/roadmap-automation-implementation.md` incorporating reliability
hardening, architectural fixes, and design refinements identified during review.

## Vision (unchanged)

Transform Hivemind from a single-directive tool into a roadmap execution engine.
Three levels: L1 (validation) → L2 (batch execution) → L3 (roadmap decomposition).

## What Changed From Original Plan

| Area | Original | Revised |
|------|----------|---------|
| Plan storage | In-memory `planByID` map | SQLite `plans` table (new L1.5a) |
| Execution control | No cancellation | Context cancellation + RunHandle (new L1.5b) |
| Crash recovery | None | Startup recovery detects zombie states (new L1.5c) |
| L1 scope nouns | 9 nouns | ~30 nouns (expanded L1.5d) |
| L2a batch_items | No phase fields | `phase` + `phase_depends_on` included upfront |
| L2b notifications | Per-item messages | Batch-level edit-in-place message |
| L2c auto-approve | Bool parameter | Conditional: inspect UserChecklist per task |
| L2c quota | Not addressed | Quota gating between items + auto-resume |
| L3a Engine interface | MetaPlan on Engine | Separate MetaPlanner optional interface |
| L3a GLM fallback | GLM implements MetaPlan | GLM does not implement MetaPlanner |
| L3b /edit-roadmap | Free-text edit commands | Dropped — /reject-roadmap covers the use case |
| L3b validation | Not addressed | L1 validates generated directives, flags failures |

## Implementation Order

```
L1   ✅ Directive validation (done)
L1.5    Foundation hardening (~1-1.5 days)
  ├─ L1.5a Plan persistence (plans table, replace in-memory map)
  ├─ L1.5b Context cancellation + RunHandle
  ├─ L1.5c Startup recovery
  └─ L1.5d Expand L1 scope nouns
L2a  Batch data model (with phase fields)
L2b  Telegram batch commands (with edit-in-place)
L2c  Batch execution loop (with quota gating + cancellation + UserChecklist pause)
L3a  Meta-planner engine (optional interface)
L3b  /roadmap command (without /edit-roadmap)
```

## Timeline

```
          L1 ✅
           │
         L1.5
      ┌────┴────┐
      │         │
    L1.5a    L1.5d
      │
    L1.5b
      │
    L1.5c
      │
    L2a ─────── L3a
      │          │
    L2b          │
      │          │
    L2c ─────── L3b
      │          │
   TEST L2    TEST L3
      │          │
      └────┬─────┘
           │
         DONE
```

| Phase | Effort | Dependencies |
|-------|--------|--------------|
| L1.5a — Plan persistence | 3-4 hours | None |
| L1.5b — Context cancellation | 2-3 hours | L1.5a (plans in DB for status updates) |
| L1.5c — Startup recovery | 1-2 hours | L1.5a + L1.5b |
| L1.5d — Expand scope nouns | 0.5 hours | None (can parallel with L1.5a) |
| L2a — Batch data model | 2-3 hours | L1.5a (plans table exists) |
| L2b — Batch commands | 3-4 hours | L2a |
| L2c — Batch execution loop | 5-7 hours | L2a + L2b + L1.5b |
| L3a — Meta-planner engine | 3-4 hours | None (can parallel with L2b/L2c) |
| L3b — /roadmap command | 4-6 hours | L2c + L3a |

**Total remaining: ~3.5-4.5 days of Claude Code work**

---

## L1.5a — Plan Persistence

**Goal**: Replace the in-memory `planByID` map with SQLite-backed storage.

### New table

```sql
CREATE TABLE IF NOT EXISTS plans (
    id TEXT PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id),
    directive TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','approved','executing','completed','failed','cancelled')),
    engine TEXT NOT NULL DEFAULT '',
    summary TEXT DEFAULT '',
    confidence REAL DEFAULT 0.0,
    plan_data TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

`plan_data` stores the serialized `PlanResult` as JSON.

### New state methods

- `CreatePlan(projectID int64, planID, directive, engine string, planData []byte) error`
- `GetPlan(planID string) (*Plan, error)`
- `UpdatePlanStatus(planID, status string) error`
- `UpdatePlanData(planID string, planData []byte) error`
- `GetActivePlans() ([]Plan, error)`

### Planner changes

- `finalizePlan` writes to DB instead of only the in-memory map
- `ExecutePlan` loads plan from DB, not memory
- In-memory `planByID` becomes a cache — DB is source of truth
- Plan status transitions (`executing`, `completed`, `failed`, `cancelled`) written to DB

### What doesn't change

The `PlanResult` struct, Engine interface, Think/Propose flow, Telegram approval flow.

---

## L1.5b — Context Cancellation and Execution Control

**Goal**: Make `ExecutePlan` cancellable from the outside.

### RunHandle pattern

```go
type RunHandle struct {
    Cancel context.CancelFunc
    Done   <-chan error
}
```

### Planner changes

- `ExecutePlan`: check `ctx.Err()` before spawning each task
- `ExecutePlan`: add `case <-ctx.Done()` in worker completion select loop
- On cancellation: mark remaining tasks as `cancelled`, update plan status in DB

### Telegram changes

- New field: `activeRuns map[string]*RunHandle` (mutex-protected)
- On `/approve` or batch start: create handle, store it, launch goroutine
- On cancellation: look up handle, call `Cancel()`, wait on `Done`
- On completion: remove handle from map

---

## L1.5c — Startup Recovery

**Goal**: Detect zombie states left by a crash and recover gracefully.

### Recovery function

```go
func (s *Store) RecoverFromRestart() (recovered int, err error)
```

**Actions**:
1. Plans stuck in `executing` → `failed` ("process restarted")
2. Batch items stuck in `running` → `failed` ("process restarted")
3. Batches stuck in `running` → `paused` ("process restarted")
4. Workers stuck in `running` → `failed`

### Telegram notification

After recovery, send a single message listing paused batches with
`/resume-batch` and `/cancel-batch` options.

### Placement

Called in `main.go` after `state.Open()` + migrations, before `TelegramBot.Start()`.
Synchronous — bot doesn't accept commands until recovery completes.

Does NOT auto-resume. A crash may leave repos dirty. Pauses and asks the human.

---

## L1.5d — Expand L1 Scope Nouns

**Current** (9): command, flag, endpoint, function, module, file, test, config

**Expanded** (~30):

```
Structure:  command, flag, endpoint, function, module, file, test, config
Code:       handler, middleware, client, service, worker, parser, reporter, writer
Data:       table, column, schema, migration, model, route
Infra:      metric, logger, webhook, probe, pipeline
UI:         page, component, view, dashboard
```

Update `scopeNouns` in `validate.go`, add test cases.

---

## L2a — Batch Data Model

### Tables

```sql
CREATE TABLE IF NOT EXISTS batches (
    id TEXT PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id),
    name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','running','completed','failed','paused')),
    total_items INTEGER NOT NULL DEFAULT 0,
    completed_items INTEGER NOT NULL DEFAULT 0,
    recovery_note TEXT DEFAULT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS batch_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    batch_id TEXT NOT NULL REFERENCES batches(id),
    sequence INTEGER NOT NULL,
    directive TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','running','completed','failed','skipped')),
    plan_id TEXT DEFAULT NULL REFERENCES plans(id),
    phase TEXT DEFAULT NULL,
    phase_depends_on TEXT DEFAULT NULL,
    error TEXT DEFAULT NULL,
    started_at DATETIME DEFAULT NULL,
    completed_at DATETIME DEFAULT NULL,
    UNIQUE(batch_id, sequence)
);
```

### Key differences from original

- `phase` + `phase_depends_on` included from the start (L3-ready)
- `plan_id` references `plans` table (proper FK from L1.5a)
- `total_items` + `completed_items` on batches for cheap progress queries
- `recovery_note` for startup recovery messages

### State methods

Same as original plan, plus:
- `GetRunningBatches() ([]Batch, error)` — for startup recovery
- `IncrementBatchProgress(batchID string) error` — atomic counter update

---

## L2b — Telegram Batch Commands

### Commands

- `/batch {project} {directives}` — create batch (pipe or newline separated)
- `/start-batch {id}` — begin execution
- `/cancel-batch {id}` — cancel/pause
- `/batch-status {id}` — show progress

### Batch-level edit-in-place

Single message created at `/start-batch`, edited as items progress:

```
┌─ BATCH ────────────────────
│ Project: nhi-watch     2/4
├────────────────────────────
│ 1 ✓ Add YAML config parser
│ 2 ▸ Add --dry-run flag
│     evaluating...
│ 3 ◻ Add --json output flag
│ 4 ◻ Add CSV export
└────────────────────────────
```

### Notifier methods

```go
NotifyBatchProgress(ctx, batchID, items []BatchItemStatus) (messageID int, error)
UpdateBatchProgress(ctx, messageID int, items []BatchItemStatus) error
```

### Failure display

```
┌─ BATCH PAUSED ─────────────
│ Project: nhi-watch     2/4
├────────────────────────────
│ 1 ✓ Add YAML config parser
│ 2 ✓ Add --dry-run flag
│ 3 ✗ Add --json output flag
│     worker failed after 3 retries
│ 4 ◻ Add CSV export
├────────────────────────────
│ /skip  ← skip item 3, continue
│ /retry ← retry item 3
│ /cancel-batch batch-xxx
└────────────────────────────
```

### Quota exhaustion display

```
┌─ BATCH PAUSED ─────────────
│ Project: nhi-watch     2/4
│ ⚠ quota exhausted
├────────────────────────────
│ ...items...
├────────────────────────────
│ Resumes automatically when
│ quota resets, or:
│ /cancel-batch batch-xxx
└────────────────────────────
```

### Simplification

`/skip` and `/retry` operate on the current failed item of the most recent
paused batch. No need for item IDs.

---

## L2c — Batch Execution Loop

### Method

```go
func (p *Planner) ExecuteBatch(ctx context.Context, batchID string) error
```

### Loop

```
1. Load batch from DB
2. For each iteration:
   a. Check ctx.Done() → if cancelled, pause batch, return
   b. Check usageTracker.CanInvoke() → if blocked, pause with
      "quota exhausted", register resume callback, return
   c. GetNextPendingBatchItem(batchID)
   d. If none → batch complete, update status, notify, return
   e. If item has phase_depends_on (non-null):
      - Check all items with matching phase names are completed
      - If any failed/skipped → pause, show phase dependency message
      - If all completed → proceed
   f. Mark item "running"
   g. Update edit-in-place (item ▸, "planning...")
   h. CreatePlan(ctx, item.Directive, projectID)
   i. If plan fails → mark item "failed", pause batch, notify, return
   j. Inspect plan tasks for UserChecklist items:
      - If found → pause batch, create PendingApproval, return
      - If none → proceed
   k. Update plan status to "approved"
   l. Update edit-in-place ("executing...")
   m. ExecutePlan(ctx, planID)
   n. If fails → mark item "failed", pause batch, notify, return
   o. Mark item "completed", increment batch progress
   p. Update edit-in-place (item ✓)
   q. Continue
3. All done → mark batch "completed", final notification
```

### Integration with RunHandle

`/start-batch` creates cancellable context, stores RunHandle.
`/cancel-batch` calls Cancel(). Loop sees ctx.Done() at step 2a.

### Resume after pause

`/retry`, `/skip`, `/resume-batch` each create a new context + RunHandle,
call `ExecuteBatch` again. Loop picks up from next pending item via DB state.

### Quota auto-resume

Register callback with `usageTracker.OnStateChange()`. When state transitions
to `normal` or `warning`, resume paused-for-quota batches. No polling.

### Constraints

- Sequential execution only (one directive at a time)
- No automatic retry — pauses and asks the human
- One batch per project at a time (enforced at `/start-batch`)

---

## L3a — Meta-Planner Engine

### Optional interface

```go
type MetaPlanner interface {
    MetaPlan(ctx context.Context, req MetaPlanRequest) (*MetaPlanResult, error)
}
```

Separate from `Engine`. Call sites type-assert.

### Types

```go
type MetaPlanRequest struct {
    ProjectName string
    AgentsMD    string
    ReconData   string
    Roadmap     string
    Feedback    string
}

type MetaPlanResult struct {
    Phases []RoadmapPhase `json:"phases"`
}

type RoadmapPhase struct {
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Directives  []string `json:"directives"`
    DependsOn   []string `json:"depends_on"`
}
```

### Implementations

- **ClaudeCodeEngine**: Implements MetaPlanner. New prompt
  `prompts/meta_planner_claude_code.txt`. Single invocation.
- **GLMEngine**: Does NOT implement MetaPlanner. `/roadmap` returns
  clear error if only GLM is available.
- **Manager**: Checks primary then fallback, but only if engine
  implements the interface.

### Validation gate

After MetaPlan returns, every directive validated with L1. Failed directives
are flagged with `⚠` in the display but not silently dropped. User sees
the full decomposition and decides.

### Prompt

Same rules as original plan. Addition: include L1 validation rules in the
prompt so Claude Code generates directives that satisfy constraints.

---

## L3b — /roadmap Telegram Command

### Commands

- `/roadmap {project} {roadmap text}` — decompose and preview
- `/approve-roadmap {id}` — create batch from decomposition
- `/reject-roadmap {id} {feedback}` — revise with feedback

### Flow

1. Parse project + roadmap text
2. Send "analyzing roadmap..." message
3. Run recon on project repo
4. Call `engine.MetaPlan(ctx, req)`
5. Validate each directive with L1
6. Store result as PendingApproval (roadmap type)
7. Display decomposition with phase structure and `⚠` markers

### /approve-roadmap

1. Drop `⚠` directives (note them in confirmation)
2. Flatten phases into ordered batch_items with `phase` and `phase_depends_on`
3. Create batch via `state.CreateBatch()`
4. User starts with `/start-batch`

### /reject-roadmap

1. Call MetaPlan again with `Feedback` field populated
2. Reuse cached recon data (no re-running recon)
3. Display revised decomposition for re-approval

### Phase dependencies in batch execution

Handled by L2c step 2e: before starting an item with `phase_depends_on`,
check all items from dependency phases are completed. If any failed/skipped,
pause and ask user to `/continue` or `/cancel-batch`.

### Dropped: /edit-roadmap

`/reject-roadmap` with feedback covers the use case. If manual editing
proves necessary, add as separate L3c later.

---

## Risks

| Risk | Mitigation |
|------|------------|
| Plan persistence migration breaks existing data | plans table is new — no existing data to migrate |
| Restart recovery marks a still-running worker as failed | Worker process has own timeout; recovery runs only at startup when no workers can be running |
| Quota exhaustion mid-batch | Pause + auto-resume on quota reset callback |
| Meta-planner generates bad directives | L1 validation + visual flagging in Telegram |
| Long-running batches (8+ items, hours) | Edit-in-place message + crash recovery |
| Cancellation leaves repo dirty | Cancel stops launching new tasks; running worker finishes naturally |
| Concurrent batches compete for workers | Enforce one batch per project at a time |
| /reject-roadmap burns quota | Each rejection = 1 invocation; recon cached; visible in quota tracker |

## Dependencies

```
L1.5a → L1.5b → L1.5c (strict chain)
L1.5d (independent, parallel with L1.5a)
L1.5a → L2a (plans table must exist for FK)
L2a → L2b → L2c (strict chain)
L3a (independent, parallel with L2b/L2c)
L2c + L3a → L3b
```
