# L2c — Batch Execution Loop Design

**Goal:** Implement `ExecuteBatch` on `Planner` to sequentially execute batch items (CreatePlan + ExecutePlan per item), with pause/resume for quota exhaustion, user checklists, and failures. Wire into Telegram commands for start, cancel, retry, skip, and resume. Add quota auto-resume callback.

**Design doc reference:** `docs/roadmap-automation-revised-design.md` §L2c

---

## Architecture

### ExecuteBatch on Planner

```go
func (p *Planner) ExecuteBatch(ctx context.Context, batchID string) error
```

Lives on `Planner` alongside `CreatePlan` and `ExecutePlan`. Uses sentinel errors to communicate pause conditions back to the Telegram layer.

**Dependencies:**
- `p.db` (state store) — already available
- `p.notifier` — already available
- `canInvoke func() (bool, string)` — wraps UsageTracker, injected via field or param

### Loop

1. Load batch from DB, verify status is `running`
2. For each iteration:
   a. Check `ctx.Done()` → pause batch, return `context.Canceled`
   b. Check `canInvoke()` → if blocked, pause batch with reason, return `ErrBatchPausedQuota`
   c. `GetNextPendingBatchItem(batchID)` → if nil, mark batch completed, return nil
   d. Check `phase_depends_on` — if dependency items failed/skipped, pause batch, return `ErrBatchPhaseDependency`
   e. Mark item `running` via `UpdateBatchItemStatus`
   f. Notify progress (edit-in-place: item ▸, "planning...")
   g. `CreatePlan(ctx, item.Directive, projectID)` → if fails, mark item failed, pause batch, return `ErrBatchItemFailed`
   h. Inspect plan tasks for `UserChecklist` items → if found, pause batch, return `ErrBatchPausedChecklist`
   i. Mark plan approved
   j. Notify progress (edit-in-place: "executing...")
   k. `ExecutePlan(ctx, planID)` → if fails, mark item failed, pause batch, return `ErrBatchItemFailed`
   l. Mark item completed, `IncrementBatchProgress`
   m. Notify progress (edit-in-place: item ✓)
   n. Continue
3. All done → mark batch completed, return nil

---

## Sentinel Error Types

```go
// internal/planner/batch_errors.go

type ErrBatchPausedQuota struct{ Reason string }
type ErrBatchPausedChecklist struct{ BatchID, PlanID string; ItemID int64; Checks []string }
type ErrBatchItemFailed struct{ ItemID int64; Err error }
type ErrBatchPhaseDependency struct{ Phase string; FailedItems []int }
```

Each implements `error`. TelegramBot type-switches on the return value.

---

## TelegramBot Integration

### Shared helper

```go
func (t *TelegramBot) startBatchExecution(batchID string)
```

Goroutine + RunHandle + error type switch. Shared by: `cmdStartBatch`, `cmdRetry`, `cmdSkip`, `cmdResumeBatch`, and quota auto-resume callback.

### cmdStartBatch changes

After existing validation/status update, call `startBatchExecution(batchID)`.

### cmdCancelBatch changes

Look up RunHandle, call `Cancel()`, wait on `Done` channel, then update DB status to paused.

### plannerExecutor interface extension

```go
type plannerExecutor interface {
    ExecutePlan(ctx context.Context, planID string) error
    ExecuteBatch(ctx context.Context, batchID string) error
}
```

---

## Resume Commands

### /retry {batch-id}
- Find last failed item, reset to `pending`
- `startBatchExecution(batchID)`

### /skip {batch-id}
- Find last failed item, set to `skipped`, increment progress
- `startBatchExecution(batchID)`

### /resume_batch {batch-id}
- Validate batch is paused, set to `running`
- `startBatchExecution(batchID)`
- Used after checklist approval or manual intervention

All share the `startBatchExecution` tail.

---

## Quota Auto-Resume

### UsageTracker extension

```go
func (t *UsageTracker) OnResumeFromQuota(cb func())
```

Called internally when `CanInvoke()` transitions false → true. TelegramBot registers a callback at startup that finds batches paused for quota and calls `startBatchExecution` for each.

---

## Phase Dependency Checking

Checked inside ExecuteBatch before each item. With sequential execution, phase dependencies are naturally satisfied by sequence order. The check is a safety net for failed/skipped items breaking the chain.

If `item.PhaseDependsOn` is set, scan batch items for matching phase — if any are failed/skipped, pause batch with `ErrBatchPhaseDependency`.

---

## UserChecklist Handling

After `CreatePlan`, inspect plan tasks for `UserChecklist` items. If found:
1. Pause batch, return `ErrBatchPausedChecklist` (includes `BatchID`)
2. TelegramBot registers PendingApproval linked to the batch
3. On `/approve {planID}` → approve plan, call `startBatchExecution(batchID)`
4. Loop resumes, calls `ExecutePlan` for the already-approved plan

---

## Edit-in-Place Notifications

1. `startBatchExecution` sends initial `FormatBatchStatusMessage`, stores message ID
2. Each `NotifyProgress` call for a batch edits that message with updated status
3. On completion/pause, final edit with terminal state

Reuses existing `sendAndTrackMessage` / `editTrackedMessage` plumbing.

---

## Error handling in type switch

| Return value | Action |
|---|---|
| `nil` | Edit final status (all ✓), send "Batch completed" |
| `context.Canceled` | Silent (user sent /cancel_batch) |
| `ErrBatchPausedQuota` | Edit status, send quota warning, register auto-resume |
| `ErrBatchPausedChecklist` | Edit status, register PendingApproval, send prompt |
| `ErrBatchItemFailed` | Edit status (item ✗), send failure details |
| `ErrBatchPhaseDependency` | Edit status, send phase dependency message |

---

## Constraints

- Sequential execution only (one directive at a time)
- No automatic retry — pauses and asks the human
- One batch per project at a time (enforced at /start_batch)
