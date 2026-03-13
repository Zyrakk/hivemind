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
	FailedItems []int64
}

func (e *ErrBatchPhaseDependency) Error() string {
	return fmt.Sprintf("batch paused: phase %q dependency has failed items %v", e.Phase, e.FailedItems)
}
