package planner

import (
	"errors"
	"fmt"
	"testing"
)

func TestBatchErrorTypes(t *testing.T) {
	t.Run("ErrBatchPausedQuota", func(t *testing.T) {
		err := &ErrBatchPausedQuota{Reason: "daily limit reached (18/18)"}
		var target *ErrBatchPausedQuota
		if !errors.As(err, &target) {
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
		var target *ErrBatchPausedChecklist
		if !errors.As(err, &target) {
			t.Fatal("expected errors.As to match")
		}
		if err.Error() == "" {
			t.Fatal("expected non-empty error message")
		}
		if err.BatchID != "batch-123" {
			t.Fatalf("unexpected batchID: %q", err.BatchID)
		}
		if len(err.Checks) != 2 || err.Checks[0] != "Review UI changes" {
			t.Fatalf("unexpected checks: %v", err.Checks)
		}
	})

	t.Run("ErrBatchItemFailed", func(t *testing.T) {
		inner := fmt.Errorf("worker crashed")
		err := &ErrBatchItemFailed{ItemID: 3, Err: inner}
		var target *ErrBatchItemFailed
		if !errors.As(err, &target) {
			t.Fatal("expected errors.As to match")
		}
		if err.Error() == "" {
			t.Fatal("expected non-empty error message")
		}
		if !errors.Is(err, inner) {
			t.Fatal("expected errors.Is to match inner error")
		}
	})

	t.Run("ErrBatchPhaseDependency", func(t *testing.T) {
		err := &ErrBatchPhaseDependency{Phase: "setup", FailedItems: []int64{1, 2}}
		var target *ErrBatchPhaseDependency
		if !errors.As(err, &target) {
			t.Fatal("expected errors.As to match")
		}
		if err.Error() == "" {
			t.Fatal("expected non-empty error message")
		}
		if err.Phase != "setup" {
			t.Fatalf("unexpected phase: %q", err.Phase)
		}
	})
}
