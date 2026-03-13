package planner

import (
	"context"
	"errors"
	"testing"

	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/state"
)

// setupBatchTestEnv creates an in-memory store, a "flux" project, and returns
// a helper that creates batches conveniently.
func setupBatchTestEnv(t *testing.T) (*state.Store, int64, func()) {
	t.Helper()
	store, cleanup := setupPlannerTestEnv(t)

	// Retrieve the project ID for "flux" (created by setupPlannerTestEnv).
	projectID, err := store.ResolveProjectID(context.Background(), "flux")
	if err != nil {
		t.Fatalf("ResolveProjectID: %v", err)
	}

	return store, projectID, cleanup
}

// newBatchPlanner creates a Planner suitable for batch tests. The GLM mock
// returns a simple single-task plan with high confidence (no consultant loop,
// no questions). The launcher auto-completes workers immediately.
func newBatchPlanner(t *testing.T, store *state.Store, opts ...func(*mockPlannerGLM, *mockPlannerLauncher)) (*Planner, *mockPlannerGLM, *mockPlannerLauncher) {
	t.Helper()

	glm := &mockPlannerGLM{}
	launch := newMockPlannerLauncher(true)

	for _, opt := range opts {
		opt(glm, launch)
	}

	// Default: produce a simple plan for every call (high confidence, one task).
	if len(glm.plans) == 0 {
		glm.plans = []*llm.TaskPlan{
			{
				Confidence: 0.95,
				Tasks: []llm.Task{
					{ID: "task-1", Title: "Auto task", Description: "auto task", BranchName: "auto"},
				},
			},
		}
	}

	p := NewWithDeps(glm, nil, launch, store, "prompts", nil)
	return p, glm, launch
}

func TestExecuteBatch_AllItemsComplete(t *testing.T) {
	store, projectID, cleanup := setupBatchTestEnv(t)
	defer cleanup()

	batchID, err := store.CreateBatch(context.Background(), projectID, "test-all-complete", []string{
		"Add a unit test for the config parser module",
		"Add a unit test for the validator module",
	})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if err := store.UpdateBatchStatus(context.Background(), batchID, state.BatchStatusRunning); err != nil {
		t.Fatalf("UpdateBatchStatus: %v", err)
	}

	p, _, _ := newBatchPlanner(t, store)

	err = p.ExecuteBatch(context.Background(), batchID)
	if err != nil {
		t.Fatalf("ExecuteBatch returned error: %v", err)
	}

	batch, err := store.GetBatch(context.Background(), batchID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.Status != state.BatchStatusCompleted {
		t.Errorf("batch status = %q, want %q", batch.Status, state.BatchStatusCompleted)
	}
	if batch.CompletedItems != 2 {
		t.Errorf("completed_items = %d, want 2", batch.CompletedItems)
	}

	items, err := store.GetBatchItems(context.Background(), batchID)
	if err != nil {
		t.Fatalf("GetBatchItems: %v", err)
	}
	for _, item := range items {
		if item.Status != state.BatchItemStatusCompleted {
			t.Errorf("item %d status = %q, want %q", item.ID, item.Status, state.BatchItemStatusCompleted)
		}
		if item.PlanID == nil {
			t.Errorf("item %d has nil plan_id", item.ID)
		}
	}
}

func TestExecuteBatch_CancellationPausesBatch(t *testing.T) {
	store, projectID, cleanup := setupBatchTestEnv(t)
	defer cleanup()

	batchID, err := store.CreateBatch(context.Background(), projectID, "test-cancel", []string{
		"Add a unit test for the pipeline module",
	})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if err := store.UpdateBatchStatus(context.Background(), batchID, state.BatchStatusRunning); err != nil {
		t.Fatalf("UpdateBatchStatus: %v", err)
	}

	p, _, _ := newBatchPlanner(t, store)

	// Cancel context before execution.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = p.ExecuteBatch(ctx, batchID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	batch, err := store.GetBatch(context.Background(), batchID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.Status != state.BatchStatusPaused {
		t.Errorf("batch status = %q, want %q", batch.Status, state.BatchStatusPaused)
	}
}

func TestExecuteBatch_QuotaExhaustedPausesBatch(t *testing.T) {
	store, projectID, cleanup := setupBatchTestEnv(t)
	defer cleanup()

	batchID, err := store.CreateBatch(context.Background(), projectID, "test-quota", []string{
		"Add a unit test for the usage tracker module",
	})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if err := store.UpdateBatchStatus(context.Background(), batchID, state.BatchStatusRunning); err != nil {
		t.Fatalf("UpdateBatchStatus: %v", err)
	}

	p, _, _ := newBatchPlanner(t, store)
	p.SetCanInvoke(func() (bool, string) {
		return false, "daily limit reached"
	})

	err = p.ExecuteBatch(context.Background(), batchID)

	var quotaErr *ErrBatchPausedQuota
	if !errors.As(err, &quotaErr) {
		t.Fatalf("expected ErrBatchPausedQuota, got: %v", err)
	}
	if quotaErr.Reason != "daily limit reached" {
		t.Errorf("quota reason = %q, want %q", quotaErr.Reason, "daily limit reached")
	}

	batch, err := store.GetBatch(context.Background(), batchID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.Status != state.BatchStatusPaused {
		t.Errorf("batch status = %q, want %q", batch.Status, state.BatchStatusPaused)
	}
}

func TestExecuteBatch_PlanFailurePausesBatch(t *testing.T) {
	store, projectID, cleanup := setupBatchTestEnv(t)
	defer cleanup()

	batchID, err := store.CreateBatch(context.Background(), projectID, "test-plan-fail", []string{
		"Add a unit test for the broken module",
	})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if err := store.UpdateBatchStatus(context.Background(), batchID, state.BatchStatusRunning); err != nil {
		t.Fatalf("UpdateBatchStatus: %v", err)
	}

	// Use a GLM mock that returns an error.
	glm := &mockPlannerGLM{} // no plans configured → returns "no plan configured" error
	launch := newMockPlannerLauncher(true)
	p := NewWithDeps(glm, nil, launch, store, "prompts", nil)

	err = p.ExecuteBatch(context.Background(), batchID)

	var itemErr *ErrBatchItemFailed
	if !errors.As(err, &itemErr) {
		t.Fatalf("expected ErrBatchItemFailed, got: %v", err)
	}

	batch, err := store.GetBatch(context.Background(), batchID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.Status != state.BatchStatusPaused {
		t.Errorf("batch status = %q, want %q", batch.Status, state.BatchStatusPaused)
	}

	items, err := store.GetBatchItems(context.Background(), batchID)
	if err != nil {
		t.Fatalf("GetBatchItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != state.BatchItemStatusFailed {
		t.Errorf("item status = %q, want %q", items[0].Status, state.BatchItemStatusFailed)
	}
	if items[0].Error == nil || *items[0].Error == "" {
		t.Error("expected item to have error message")
	}
}

func TestPlanHasUserChecklist(t *testing.T) {
	t.Run("no checklist", func(t *testing.T) {
		plan := &llm.TaskPlan{
			Tasks: []llm.Task{{ID: "t1", Title: "T", Description: "d"}},
		}
		if planHasUserChecklist(plan) {
			t.Error("expected false for plan without user checklist")
		}
	})

	t.Run("with checklist", func(t *testing.T) {
		plan := &llm.TaskPlan{
			Tasks: []llm.Task{
				{
					ID: "t1", Title: "T", Description: "d",
					UserChecklist: []llm.CheckItem{
						{ID: "c1", Description: "Verify output"},
					},
				},
			},
		}
		if !planHasUserChecklist(plan) {
			t.Error("expected true for plan with user checklist")
		}
	})

	t.Run("nil plan", func(t *testing.T) {
		if planHasUserChecklist(nil) {
			t.Error("expected false for nil plan")
		}
	})
}

func TestCollectUserChecks(t *testing.T) {
	plan := &llm.TaskPlan{
		Tasks: []llm.Task{
			{
				ID: "t1", Title: "T1",
				UserChecklist: []llm.CheckItem{
					{ID: "c1", Description: "Check A"},
					{ID: "c2", Description: "Check B"},
				},
			},
			{
				ID: "t2", Title: "T2",
				UserChecklist: []llm.CheckItem{
					{ID: "c3", Description: "Check C"},
				},
			},
		},
	}

	checks := collectUserChecks(plan)
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(checks))
	}
	expected := []string{"Check A", "Check B", "Check C"}
	for i, want := range expected {
		if checks[i] != want {
			t.Errorf("checks[%d] = %q, want %q", i, checks[i], want)
		}
	}
}
