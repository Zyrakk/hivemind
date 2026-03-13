package planner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/state"
)

// pauseContext returns a short-lived context suitable for DB writes when the
// main loop context may already be cancelled.
func pauseContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// ExecuteBatch runs the batch execution loop for the given batch. It iterates
// over pending items in sequence order, creating and executing a plan for each.
// The loop pauses (returning a typed error) on cancellation, quota exhaustion,
// user checklist requirements, plan failures, or phase dependency failures.
// It returns nil only when every item has been completed.
func (p *Planner) ExecuteBatch(ctx context.Context, batchID string) error {
	if p == nil {
		return fmt.Errorf("planner is nil")
	}
	if p.db == nil {
		return fmt.Errorf("state store is not configured")
	}

	// Use a background context for the initial load so we can still pause the
	// batch even if the caller's context is already cancelled.
	batch, err := p.db.GetBatch(context.Background(), batchID)
	if err != nil {
		return fmt.Errorf("load batch %s: %w", batchID, err)
	}
	if batch.Status != state.BatchStatusRunning {
		return fmt.Errorf("batch %s has status %q, expected %q", batchID, batch.Status, state.BatchStatusRunning)
	}

	// Resolve the project name for CreatePlan (it takes a project ref string).
	project, err := p.db.GetProjectByID(context.Background(), batch.ProjectID)
	if err != nil {
		return fmt.Errorf("resolve project for batch %s: %w", batchID, err)
	}
	projectRef := project.Name

	for {
		// 2a. Check context cancellation.
		select {
		case <-ctx.Done():
			// Use a fresh context for the DB update since the original is cancelled.
			pCtx, pCancel := pauseContext()
			if pauseErr := p.db.UpdateBatchStatus(pCtx, batchID, state.BatchStatusPaused); pauseErr != nil {
				p.logger.Error("failed to pause batch on cancellation",
					slog.String("batch_id", batchID), slog.Any("error", pauseErr))
			}
			pCancel()
			return ctx.Err()
		default:
		}

		// 2b. Check quota via canInvoke.
		p.mu.Lock()
		ci := p.canInvoke
		p.mu.Unlock()
		if ci != nil {
			allowed, reason := ci()
			if !allowed {
				_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
				return &ErrBatchPausedQuota{Reason: reason}
			}
		}

		// 2c. Get next pending item.
		item, err := p.db.GetNextPendingBatchItem(ctx, batchID)
		if err != nil {
			return fmt.Errorf("get next pending item for batch %s: %w", batchID, err)
		}
		if item == nil {
			// All items done — mark batch completed.
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusCompleted)
			return nil
		}

		// 2d. Check phase dependencies.
		if err := p.checkPhaseDeps(ctx, batchID, item); err != nil {
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
			return err
		}

		// 2e. Mark item running.
		if err := p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusRunning, "", ""); err != nil {
			return fmt.Errorf("mark item %d running: %w", item.ID, err)
		}

		// 2f. Notify progress — planning.
		p.notifyProgress(ctx, projectRef, batchID, "planning",
			fmt.Sprintf("batch item %d/%d: %s", item.Sequence, batch.TotalItems, item.Directive))

		// 2g. Create plan.
		planResult, planErr := p.CreatePlan(ctx, item.Directive, projectRef)
		if planErr != nil {
			errMsg := planErr.Error()
			_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusFailed, "", errMsg)
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
			return &ErrBatchItemFailed{ItemID: item.ID, Err: planErr}
		}

		// 2h. Check for user checklist — pause if found.
		if planResult.Plan != nil && planHasUserChecklist(planResult.Plan) {
			checks := collectUserChecks(planResult.Plan)
			// Update item with the plan ID so it can be resumed later.
			_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusPending, planResult.PlanID, "")
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
			return &ErrBatchPausedChecklist{
				BatchID: batchID,
				PlanID:  planResult.PlanID,
				ItemID:  item.ID,
				Checks:  checks,
			}
		}

		// 2i. Mark plan approved.
		if err := p.db.UpdatePlanStatus(ctx, planResult.PlanID, state.PlanStatusApproved); err != nil {
			p.logger.Warn("failed to approve plan", slog.String("plan_id", planResult.PlanID), slog.Any("error", err))
		}

		// 2j. Notify progress — executing.
		p.notifyProgress(ctx, projectRef, batchID, "executing",
			fmt.Sprintf("batch item %d/%d: %s", item.Sequence, batch.TotalItems, item.Directive))

		// 2k. Execute plan.
		execErr := p.ExecutePlan(ctx, planResult.PlanID)
		if execErr != nil {
			errMsg := execErr.Error()
			_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusFailed, planResult.PlanID, errMsg)
			_ = p.db.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused)
			return &ErrBatchItemFailed{ItemID: item.ID, Err: execErr}
		}

		// 2l. Mark item completed, increment batch progress.
		_ = p.db.UpdateBatchItemStatus(ctx, item.ID, state.BatchItemStatusCompleted, planResult.PlanID, "")
		_ = p.db.IncrementBatchProgress(ctx, batchID)

		// 2m. Notify progress — completed.
		p.notifyProgress(ctx, projectRef, batchID, "completed",
			fmt.Sprintf("batch item %d/%d: %s", item.Sequence, batch.TotalItems, item.Directive))
	}
}

// planHasUserChecklist returns true if any task in the plan has user checklist items.
func planHasUserChecklist(plan *llm.TaskPlan) bool {
	if plan == nil {
		return false
	}
	for _, task := range plan.Tasks {
		if len(task.UserChecklist) > 0 {
			return true
		}
	}
	return false
}

// collectUserChecks gathers all user checklist descriptions across all tasks.
func collectUserChecks(plan *llm.TaskPlan) []string {
	if plan == nil {
		return nil
	}
	var checks []string
	for _, task := range plan.Tasks {
		for _, check := range task.UserChecklist {
			desc := check.Description
			if desc == "" {
				desc = check.ID
			}
			checks = append(checks, desc)
		}
	}
	return checks
}

// checkPhaseDeps verifies that all items in the dependent phase have completed
// successfully. If any have failed or been skipped, it returns an
// ErrBatchPhaseDependency error.
func (p *Planner) checkPhaseDeps(ctx context.Context, batchID string, item *state.BatchItem) error {
	if item.PhaseDependsOn == nil || *item.PhaseDependsOn == "" {
		return nil
	}

	depPhase := *item.PhaseDependsOn
	items, err := p.db.GetBatchItems(ctx, batchID)
	if err != nil {
		return fmt.Errorf("check phase deps: %w", err)
	}

	var failedItems []int64
	for _, other := range items {
		if other.Phase == nil || *other.Phase != depPhase {
			continue
		}
		if other.Status == state.BatchItemStatusFailed || other.Status == state.BatchItemStatusSkipped {
			failedItems = append(failedItems, other.ID)
		}
		// Defensive: if any items in the dependent phase are still non-terminal, don't proceed
		if other.Status != state.BatchItemStatusCompleted &&
			other.Status != state.BatchItemStatusFailed &&
			other.Status != state.BatchItemStatusSkipped {
			return fmt.Errorf("phase %q item %d not yet terminal (status: %s)", *item.PhaseDependsOn, other.Sequence, other.Status)
		}
	}

	if len(failedItems) > 0 {
		return &ErrBatchPhaseDependency{
			Phase:       depPhase,
			FailedItems: failedItems,
		}
	}

	return nil
}
