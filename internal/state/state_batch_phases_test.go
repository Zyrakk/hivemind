package state

import (
	"context"
	"testing"
)

func TestCreateBatchWithPhases(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	projectID := createTestProject(t, store)

	type phaseItem struct {
		Directive      string
		Phase          string
		PhaseDependsOn string
	}

	items := []phaseItem{
		{Directive: "Add config parser module for YAML settings in the file system", Phase: "data-layer", PhaseDependsOn: ""},
		{Directive: "Add migration to create users table in the schema", Phase: "data-layer", PhaseDependsOn: ""},
		{Directive: "Add REST endpoint handlers for user CRUD in the service", Phase: "api", PhaseDependsOn: "data-layer"},
	}

	directives := make([]string, len(items))
	phases := make([]string, len(items))
	phaseDeps := make([]string, len(items))
	for i, item := range items {
		directives[i] = item.Directive
		phases[i] = item.Phase
		phaseDeps[i] = item.PhaseDependsOn
	}

	batchID, err := store.CreateBatchWithPhases(ctx, projectID, "roadmap-test", directives, phases, phaseDeps)
	if err != nil {
		t.Fatalf("CreateBatchWithPhases() error = %v", err)
	}
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}

	batch, err := store.GetBatch(ctx, batchID)
	if err != nil {
		t.Fatalf("GetBatch() error = %v", err)
	}
	if batch.TotalItems != 3 {
		t.Fatalf("batch.TotalItems = %d, want 3", batch.TotalItems)
	}
	if batch.Name != "roadmap-test" {
		t.Fatalf("batch.Name = %q, want %q", batch.Name, "roadmap-test")
	}

	batchItems, err := store.GetBatchItems(ctx, batchID)
	if err != nil {
		t.Fatalf("GetBatchItems() error = %v", err)
	}
	if len(batchItems) != 3 {
		t.Fatalf("got %d items, want 3", len(batchItems))
	}

	// Check phase fields are set.
	if batchItems[0].Phase == nil || *batchItems[0].Phase != "data-layer" {
		t.Fatalf("item[0].Phase = %v, want 'data-layer'", batchItems[0].Phase)
	}
	if batchItems[0].PhaseDependsOn != nil {
		t.Fatalf("item[0].PhaseDependsOn = %v, want nil", batchItems[0].PhaseDependsOn)
	}
	if batchItems[2].Phase == nil || *batchItems[2].Phase != "api" {
		t.Fatalf("item[2].Phase = %v, want 'api'", batchItems[2].Phase)
	}
	if batchItems[2].PhaseDependsOn == nil || *batchItems[2].PhaseDependsOn != "data-layer" {
		t.Fatalf("item[2].PhaseDependsOn = %v, want 'data-layer'", batchItems[2].PhaseDependsOn)
	}
}

func TestCreateBatchWithPhases_MismatchedLengths(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	projectID := createTestProject(t, store)

	_, err := store.CreateBatchWithPhases(ctx, projectID, "", []string{"d1", "d2"}, []string{"p1"}, []string{""})
	if err == nil {
		t.Fatal("expected error for mismatched slice lengths")
	}
}
