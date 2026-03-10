package state

import (
	"context"
	"testing"
)

func createTestProject(t *testing.T, store *Store) int64 {
	t.Helper()
	ctx := context.Background()
	projectID, err := store.CreateProject(ctx, Project{
		Name:    "test-project",
		Status:  ProjectStatusWorking,
		RepoURL: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return projectID
}

func TestCreateAndGetBatch(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	directives := []string{
		"Add YAML config parser for scoring rules",
		"Add --dry-run flag to the audit command",
		"Add --json output flag to the audit command",
	}

	batchID, err := store.CreateBatch(ctx, projectID, "CLI improvements", directives)
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}

	batch, err := store.GetBatch(ctx, batchID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if batch.ID != batchID {
		t.Errorf("batch ID = %q, want %q", batch.ID, batchID)
	}
	if batch.ProjectID != projectID {
		t.Errorf("project ID = %d, want %d", batch.ProjectID, projectID)
	}
	if batch.Name != "CLI improvements" {
		t.Errorf("name = %q, want %q", batch.Name, "CLI improvements")
	}
	if batch.Status != BatchStatusPending {
		t.Errorf("status = %q, want %q", batch.Status, BatchStatusPending)
	}
	if batch.TotalItems != 3 {
		t.Errorf("total_items = %d, want 3", batch.TotalItems)
	}
	if batch.CompletedItems != 0 {
		t.Errorf("completed_items = %d, want 0", batch.CompletedItems)
	}
}

func TestCreateBatchEmptyDirectives(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	_, err := store.CreateBatch(ctx, projectID, "empty", []string{})
	if err == nil {
		t.Fatal("expected error for empty directives")
	}
}

func TestCreateBatchInvalidProject(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := store.CreateBatch(context.Background(), 0, "test", []string{"directive"})
	if err == nil {
		t.Fatal("expected error for invalid project ID")
	}
}

func TestGetBatchNotFound(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := store.GetBatch(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent batch")
	}
}

func TestGetBatchItems(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	directives := []string{"directive A", "directive B", "directive C"}
	batchID, _ := store.CreateBatch(ctx, projectID, "test", directives)

	items, err := store.GetBatchItems(ctx, batchID)
	if err != nil {
		t.Fatalf("get batch items: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Verify sequence order and directives.
	for i, item := range items {
		if item.Sequence != i+1 {
			t.Errorf("item[%d] sequence = %d, want %d", i, item.Sequence, i+1)
		}
		if item.Directive != directives[i] {
			t.Errorf("item[%d] directive = %q, want %q", i, item.Directive, directives[i])
		}
		if item.Status != BatchItemStatusPending {
			t.Errorf("item[%d] status = %q, want %q", i, item.Status, BatchItemStatusPending)
		}
		if item.BatchID != batchID {
			t.Errorf("item[%d] batch_id = %q, want %q", i, item.BatchID, batchID)
		}
	}
}

func TestGetNextPendingBatchItem(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	directives := []string{"first", "second", "third"}
	batchID, _ := store.CreateBatch(ctx, projectID, "test", directives)

	// First call: should return sequence 1.
	item, err := store.GetNextPendingBatchItem(ctx, batchID)
	if err != nil {
		t.Fatalf("get next pending: %v", err)
	}
	if item.Sequence != 1 {
		t.Errorf("sequence = %d, want 1", item.Sequence)
	}
	if item.Directive != "first" {
		t.Errorf("directive = %q, want %q", item.Directive, "first")
	}

	// Mark item 1 as completed, next should be item 2.
	_ = store.UpdateBatchItemStatus(ctx, item.ID, BatchItemStatusCompleted, "", "")
	item2, err := store.GetNextPendingBatchItem(ctx, batchID)
	if err != nil {
		t.Fatalf("get next pending after complete: %v", err)
	}
	if item2.Sequence != 2 {
		t.Errorf("sequence = %d, want 2", item2.Sequence)
	}

	// Mark item 2 as skipped, next should be item 3.
	_ = store.UpdateBatchItemStatus(ctx, item2.ID, BatchItemStatusSkipped, "", "")
	item3, err := store.GetNextPendingBatchItem(ctx, batchID)
	if err != nil {
		t.Fatalf("get next pending after skip: %v", err)
	}
	if item3.Sequence != 3 {
		t.Errorf("sequence = %d, want 3", item3.Sequence)
	}
}

func TestGetNextPendingBatchItemNoneLeft(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	batchID, _ := store.CreateBatch(ctx, projectID, "test", []string{"only one"})

	item, _ := store.GetNextPendingBatchItem(ctx, batchID)
	_ = store.UpdateBatchItemStatus(ctx, item.ID, BatchItemStatusCompleted, "", "")

	// No more pending items.
	next, err := store.GetNextPendingBatchItem(ctx, batchID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != nil {
		t.Fatalf("expected nil, got item with sequence %d", next.Sequence)
	}
}

func TestUpdateBatchStatus(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	batchID, _ := store.CreateBatch(ctx, projectID, "test", []string{"dir"})

	err := store.UpdateBatchStatus(ctx, batchID, BatchStatusRunning)
	if err != nil {
		t.Fatalf("update batch status: %v", err)
	}

	batch, _ := store.GetBatch(ctx, batchID)
	if batch.Status != BatchStatusRunning {
		t.Errorf("status = %q, want %q", batch.Status, BatchStatusRunning)
	}
}

func TestUpdateBatchStatusInvalid(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	batchID, _ := store.CreateBatch(ctx, projectID, "test", []string{"dir"})

	err := store.UpdateBatchStatus(ctx, batchID, "invalid-status")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestUpdateBatchStatusNotFound(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	err := store.UpdateBatchStatus(context.Background(), "nonexistent", BatchStatusRunning)
	if err == nil {
		t.Fatal("expected error for nonexistent batch")
	}
}

func TestUpdateBatchItemStatus(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	batchID, _ := store.CreateBatch(ctx, projectID, "test", []string{"directive"})
	items, _ := store.GetBatchItems(ctx, batchID)

	// Mark as running.
	err := store.UpdateBatchItemStatus(ctx, items[0].ID, BatchItemStatusRunning, "", "")
	if err != nil {
		t.Fatalf("update item status to running: %v", err)
	}

	// Mark as failed with plan_id and error.
	err = store.UpdateBatchItemStatus(ctx, items[0].ID, BatchItemStatusFailed, "plan-abc", "worker crashed")
	if err != nil {
		t.Fatalf("update item status to failed: %v", err)
	}

	// Verify.
	updatedItems, _ := store.GetBatchItems(ctx, batchID)
	item := updatedItems[0]
	if item.Status != BatchItemStatusFailed {
		t.Errorf("status = %q, want %q", item.Status, BatchItemStatusFailed)
	}
	if item.PlanID == nil || *item.PlanID != "plan-abc" {
		t.Errorf("plan_id = %v, want %q", item.PlanID, "plan-abc")
	}
	if item.Error == nil || *item.Error != "worker crashed" {
		t.Errorf("error = %v, want %q", item.Error, "worker crashed")
	}
}

func TestIncrementBatchProgress(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	batchID, _ := store.CreateBatch(ctx, projectID, "test", []string{"a", "b", "c"})

	err := store.IncrementBatchProgress(ctx, batchID)
	if err != nil {
		t.Fatalf("increment: %v", err)
	}

	batch, _ := store.GetBatch(ctx, batchID)
	if batch.CompletedItems != 1 {
		t.Errorf("completed_items = %d, want 1", batch.CompletedItems)
	}

	// Increment again.
	_ = store.IncrementBatchProgress(ctx, batchID)
	batch, _ = store.GetBatch(ctx, batchID)
	if batch.CompletedItems != 2 {
		t.Errorf("completed_items = %d, want 2", batch.CompletedItems)
	}
}

func TestGetRunningBatches(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	projectID := createTestProject(t, store)

	batchA, _ := store.CreateBatch(ctx, projectID, "a", []string{"dir"})
	batchB, _ := store.CreateBatch(ctx, projectID, "b", []string{"dir"})
	_, _ = store.CreateBatch(ctx, projectID, "c", []string{"dir"})

	_ = store.UpdateBatchStatus(ctx, batchA, BatchStatusRunning)
	_ = store.UpdateBatchStatus(ctx, batchB, BatchStatusRunning)
	// batchC stays pending.

	running, err := store.GetRunningBatches(ctx)
	if err != nil {
		t.Fatalf("get running batches: %v", err)
	}
	if len(running) != 2 {
		t.Fatalf("expected 2 running batches, got %d", len(running))
	}
}
