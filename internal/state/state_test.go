package state

import (
	"context"
	"testing"
)

func TestMigrationsExist(t *testing.T) {
	m := Migrations()
	if len(m) == 0 {
		t.Fatal("expected at least one migration")
	}
}

func TestStoreCRUD(t *testing.T) {
	store, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	ctx := context.Background()

	projectID, err := store.CreateProject(ctx, Project{
		Name:   "flux",
		Status: ProjectStatusWorking,
	})
	if err != nil {
		t.Fatalf("CreateProject() failed: %v", err)
	}

	taskID, err := store.CreateTask(ctx, Task{
		ProjectID: projectID,
		Title:     "Implementar endpoint",
		Priority:  3,
		DependsOn: "[]",
	})
	if err != nil {
		t.Fatalf("CreateTask() failed: %v", err)
	}
	if taskID <= 0 {
		t.Fatalf("expected task ID > 0, got %d", taskID)
	}

	workerID, err := store.CreateWorker(ctx, Worker{
		ProjectID:       projectID,
		SessionID:       "flux-economy-001",
		TaskDescription: "Implementar endpoint",
		Branch:          "feature/flux-economy",
		Status:          WorkerStatusRunning,
	})
	if err != nil {
		t.Fatalf("CreateWorker() failed: %v", err)
	}
	if workerID <= 0 {
		t.Fatalf("expected worker ID > 0, got %d", workerID)
	}

	if err := store.UpdateProjectStatus(ctx, projectID, ProjectStatusPendingReview); err != nil {
		t.Fatalf("UpdateProjectStatus() failed: %v", err)
	}

	if err := store.AppendEvent(ctx, Event{
		ProjectID:   projectID,
		WorkerID:    &workerID,
		EventType:   "worker_started",
		Description: "Worker lanzado",
	}); err != nil {
		t.Fatalf("AppendEvent() failed: %v", err)
	}

	global, err := store.GetGlobalState(ctx)
	if err != nil {
		t.Fatalf("GetGlobalState() failed: %v", err)
	}

	if len(global.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(global.Projects))
	}
	if len(global.ActiveWorkers) != 1 {
		t.Fatalf("expected 1 active worker, got %d", len(global.ActiveWorkers))
	}
	if global.Counters.PendingReview != 1 {
		t.Fatalf("expected 1 pending review project, got %d", global.Counters.PendingReview)
	}
}
