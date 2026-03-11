package state

import (
	"context"
	"testing"
)

func TestRecoverFromRestart(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a project.
	projectID, err := store.CreateProject(ctx, Project{
		Name: "test-project", Status: ProjectStatusWorking, RepoURL: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Create a plan stuck in "executing".
	_ = store.CreatePlan(ctx, projectID, "plan-stuck", "directive", "claude-code", []byte(`{}`))
	_ = store.UpdatePlanStatus(ctx, "plan-stuck", PlanStatusExecuting)

	// Create a worker stuck in "running".
	_, _ = store.CreateWorker(ctx, Worker{
		ProjectID:       projectID,
		SessionID:       "session-stuck",
		TaskDescription: "stuck task",
		Branch:          "branch-stuck",
		Status:          WorkerStatusRunning,
	})

	// Run recovery.
	recovered, err := store.RecoverFromRestart(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered == 0 {
		t.Fatal("expected at least 1 recovered item")
	}

	// Verify plan is now failed.
	plan, _ := store.GetPlan(ctx, "plan-stuck")
	if plan.Status != PlanStatusFailed {
		t.Errorf("plan status = %q, want %q", plan.Status, PlanStatusFailed)
	}

	// Verify no running workers remain.
	workers, _ := store.ListActiveWorkers(ctx)
	if len(workers) != 0 {
		t.Errorf("expected 0 active workers, got %d", len(workers))
	}
}

func TestRecoverFromRestartBatches(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	projectID, err := store.CreateProject(ctx, Project{
		Name: "test-project", Status: ProjectStatusWorking, RepoURL: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Create a batch stuck in "running" with an item stuck in "running".
	batchID, _ := store.CreateBatch(ctx, projectID, "stuck batch", []string{"dir1", "dir2"})
	_ = store.UpdateBatchStatus(ctx, batchID, BatchStatusRunning)

	items, _ := store.GetBatchItems(ctx, batchID)
	_ = store.UpdateBatchItemStatus(ctx, items[0].ID, BatchItemStatusRunning, "", "")

	// Run recovery.
	recovered, err := store.RecoverFromRestart(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered < 2 {
		t.Fatalf("expected at least 2 recovered items (batch + item), got %d", recovered)
	}

	// Verify batch is now paused.
	batch, _ := store.GetBatch(ctx, batchID)
	if batch.Status != BatchStatusPaused {
		t.Errorf("batch status = %q, want %q", batch.Status, BatchStatusPaused)
	}

	// Verify item is now failed.
	updatedItems, _ := store.GetBatchItems(ctx, batchID)
	if updatedItems[0].Status != BatchItemStatusFailed {
		t.Errorf("item status = %q, want %q", updatedItems[0].Status, BatchItemStatusFailed)
	}

	// Item 2 should still be pending (untouched).
	if updatedItems[1].Status != BatchItemStatusPending {
		t.Errorf("item 2 status = %q, want %q", updatedItems[1].Status, BatchItemStatusPending)
	}
}

func TestRecoverFromRestartNoZombies(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	recovered, err := store.RecoverFromRestart(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered != 0 {
		t.Errorf("expected 0 recovered, got %d", recovered)
	}
}
