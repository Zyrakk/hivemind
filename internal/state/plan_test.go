package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func setupTestDB(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	store, err := New(path)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	return store, func() {
		store.Close()
		os.Remove(path)
	}
}

func TestCreateAndGetPlan(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a project first (plans require project_id FK).
	projectID, err := store.CreateProject(ctx, Project{
		Name:    "test-project",
		Status:  ProjectStatusWorking,
		RepoURL: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	planData := []byte(`{"tasks":[],"confidence":0.8}`)
	err = store.CreatePlan(ctx, projectID, "plan-123", "add a flag to the command", "claude-code", planData)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	plan, err := store.GetPlan(ctx, "plan-123")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if plan.ID != "plan-123" {
		t.Errorf("plan ID = %q, want %q", plan.ID, "plan-123")
	}
	if plan.ProjectID != projectID {
		t.Errorf("project ID = %d, want %d", plan.ProjectID, projectID)
	}
	if plan.Directive != "add a flag to the command" {
		t.Errorf("directive = %q, want %q", plan.Directive, "add a flag to the command")
	}
	if plan.Status != PlanStatusPending {
		t.Errorf("status = %q, want %q", plan.Status, PlanStatusPending)
	}
	if plan.Engine != "claude-code" {
		t.Errorf("engine = %q, want %q", plan.Engine, "claude-code")
	}
	if string(plan.PlanData) != string(planData) {
		t.Errorf("plan data mismatch")
	}
}

func TestUpdatePlanStatus(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID, _ := store.CreateProject(ctx, Project{
		Name: "test-project", Status: ProjectStatusWorking, RepoURL: "/tmp/test",
	})

	_ = store.CreatePlan(ctx, projectID, "plan-456", "implement feature", "glm", []byte(`{}`))

	err := store.UpdatePlanStatus(ctx, "plan-456", PlanStatusExecuting)
	if err != nil {
		t.Fatalf("update plan status: %v", err)
	}

	plan, _ := store.GetPlan(ctx, "plan-456")
	if plan.Status != PlanStatusExecuting {
		t.Errorf("status = %q, want %q", plan.Status, PlanStatusExecuting)
	}
}

func TestGetPlanNotFound(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := store.GetPlan(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent plan")
	}
}

func TestGetActivePlans(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID, _ := store.CreateProject(ctx, Project{
		Name: "test-project", Status: ProjectStatusWorking, RepoURL: "/tmp/test",
	})

	_ = store.CreatePlan(ctx, projectID, "plan-a", "directive a", "claude-code", []byte(`{}`))
	_ = store.CreatePlan(ctx, projectID, "plan-b", "directive b", "claude-code", []byte(`{}`))
	_ = store.CreatePlan(ctx, projectID, "plan-c", "directive c", "claude-code", []byte(`{}`))

	// Set plan-a to executing, plan-b to approved.
	_ = store.UpdatePlanStatus(ctx, "plan-a", PlanStatusExecuting)
	_ = store.UpdatePlanStatus(ctx, "plan-b", PlanStatusApproved)

	active, err := store.GetActivePlans(ctx)
	if err != nil {
		t.Fatalf("get active plans: %v", err)
	}
	// "executing" and "approved" are active; "pending" is not.
	if len(active) != 2 {
		t.Fatalf("expected 2 active plans, got %d", len(active))
	}
}
