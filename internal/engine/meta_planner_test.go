package engine

import (
	"context"
	"testing"
)

// Compile-time check: ClaudeCodeEngine must implement MetaPlanner.
var _ MetaPlanner = (*ClaudeCodeEngine)(nil)

func TestMetaPlanRequestFields(t *testing.T) {
	t.Parallel()
	req := MetaPlanRequest{
		ProjectName: "test-project",
		AgentsMD:    "agents content",
		ReconData:   "recon output",
		Roadmap:     "Build a REST API with auth",
		Feedback:    "",
	}
	if req.ProjectName != "test-project" {
		t.Fatal("ProjectName mismatch")
	}
	if req.Roadmap != "Build a REST API with auth" {
		t.Fatal("Roadmap mismatch")
	}
}

func TestMetaPlanResultPhases(t *testing.T) {
	t.Parallel()
	result := MetaPlanResult{
		Phases: []RoadmapPhase{
			{
				Name:        "phase-1",
				Description: "Foundation",
				Directives:  []string{"Add a config parser module for YAML settings"},
				DependsOn:   nil,
			},
			{
				Name:        "phase-2",
				Description: "API layer",
				Directives:  []string{"Add REST endpoint handlers for the auth service"},
				DependsOn:   []string{"phase-1"},
			},
		},
	}
	if len(result.Phases) != 2 {
		t.Fatalf("got %d phases, want 2", len(result.Phases))
	}
	if result.Phases[1].DependsOn[0] != "phase-1" {
		t.Fatal("phase-2 should depend on phase-1")
	}
}

func TestManagerMetaPlannerEngine_PrimaryImplements(t *testing.T) {
	t.Parallel()

	primary := &mockMetaPlannerEngine{
		mockEngine: mockEngine{name: "claude-code", available: true},
	}
	fallback := &mockEngine{name: "glm", available: true}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "glm"},
		map[string]Engine{"claude-code": primary, "glm": fallback},
		testLogger(),
	)

	mp := mgr.MetaPlannerEngine(context.Background())
	if mp == nil {
		t.Fatal("expected MetaPlanner from primary, got nil")
	}
}

func TestManagerMetaPlannerEngine_PrimaryUnavailableFallbackImplements(t *testing.T) {
	t.Parallel()

	primary := &mockMetaPlannerEngine{
		mockEngine: mockEngine{name: "claude-code", available: false},
	}
	fallback := &mockMetaPlannerEngine{
		mockEngine: mockEngine{name: "other", available: true},
	}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "other"},
		map[string]Engine{"claude-code": primary, "other": fallback},
		testLogger(),
	)

	mp := mgr.MetaPlannerEngine(context.Background())
	if mp == nil {
		t.Fatal("expected MetaPlanner from fallback, got nil")
	}
}

func TestManagerMetaPlannerEngine_NeitherImplements(t *testing.T) {
	t.Parallel()

	primary := &mockEngine{name: "glm", available: true}
	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "glm"},
		map[string]Engine{"glm": primary},
		testLogger(),
	)

	mp := mgr.MetaPlannerEngine(context.Background())
	if mp != nil {
		t.Fatal("expected nil MetaPlanner, got non-nil")
	}
}

func TestManagerMetaPlannerEngine_PrimaryUnavailableFallbackPlainEngine(t *testing.T) {
	t.Parallel()

	primary := &mockMetaPlannerEngine{
		mockEngine: mockEngine{name: "claude-code", available: false},
	}
	fallback := &mockEngine{name: "glm", available: true}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "glm"},
		map[string]Engine{"claude-code": primary, "glm": fallback},
		testLogger(),
	)

	mp := mgr.MetaPlannerEngine(context.Background())
	if mp != nil {
		t.Fatal("expected nil when primary unavailable and fallback doesn't implement MetaPlanner")
	}
}

// mockMetaPlannerEngine satisfies both Engine and MetaPlanner.
type mockMetaPlannerEngine struct {
	mockEngine
	metaPlanResult *MetaPlanResult
	metaPlanErr    error
}

func (m *mockMetaPlannerEngine) MetaPlan(_ context.Context, _ MetaPlanRequest) (*MetaPlanResult, error) {
	if m.metaPlanErr != nil {
		return nil, m.metaPlanErr
	}
	if m.metaPlanResult != nil {
		return m.metaPlanResult, nil
	}
	return &MetaPlanResult{}, nil
}
