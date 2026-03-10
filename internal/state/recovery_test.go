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
