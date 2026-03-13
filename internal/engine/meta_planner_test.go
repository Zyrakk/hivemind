package engine

import (
	"context"
	"strings"
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

// --- buildMetaPlanPrompt tests ---

func TestBuildMetaPlanPrompt(t *testing.T) {
	t.Parallel()

	req := MetaPlanRequest{
		ProjectName: "nhi-watch",
		AgentsMD:    "Project conventions here.",
		ReconData:   "tree output here",
		Roadmap:     "Build a REST API with auth and metrics",
		Feedback:    "",
	}

	got := buildMetaPlanPrompt(req)

	assertContains(t, got, "PROJECT: nhi-watch")
	assertContains(t, got, "ROADMAP:\nBuild a REST API with auth and metrics")
	assertContains(t, got, "AGENTS.MD:\nProject conventions here.")
	assertContains(t, got, "REPOSITORY STATE:\ntree output here")

	if strings.Contains(got, "PREVIOUS FEEDBACK") {
		t.Fatal("should not contain PREVIOUS FEEDBACK when Feedback is empty")
	}
}

func TestBuildMetaPlanPromptWithFeedback(t *testing.T) {
	t.Parallel()

	req := MetaPlanRequest{
		ProjectName: "nhi-watch",
		AgentsMD:    "conventions",
		ReconData:   "recon",
		Roadmap:     "Build auth",
		Feedback:    "Split the auth phase into two: middleware and handlers",
	}

	got := buildMetaPlanPrompt(req)

	assertContains(t, got, "PREVIOUS FEEDBACK:\nSplit the auth phase into two")
}

// --- parseMetaPlanResult tests ---

func TestParseMetaPlanResult(t *testing.T) {
	t.Parallel()

	raw := `{"phases":[{"name":"data-layer","description":"Set up data models","directives":["Add a migration to create the users table in the SQLite schema","Add a user model struct and CRUD functions in the service module"],"depends_on":[]},{"name":"api","description":"REST endpoints","directives":["Add REST endpoint handlers for user CRUD operations in the service layer"],"depends_on":["data-layer"]}]}`

	result, err := parseMetaPlanResult(raw)
	if err != nil {
		t.Fatalf("parseMetaPlanResult() error = %v", err)
	}
	if len(result.Phases) != 2 {
		t.Fatalf("got %d phases, want 2", len(result.Phases))
	}
	if result.Phases[0].Name != "data-layer" {
		t.Fatalf("phase[0].Name = %q, want %q", result.Phases[0].Name, "data-layer")
	}
	if len(result.Phases[0].Directives) != 2 {
		t.Fatalf("phase[0] got %d directives, want 2", len(result.Phases[0].Directives))
	}
	if len(result.Phases[1].DependsOn) != 1 || result.Phases[1].DependsOn[0] != "data-layer" {
		t.Fatalf("phase[1].DependsOn = %v, want [data-layer]", result.Phases[1].DependsOn)
	}
}

func TestParseMetaPlanResultEmpty(t *testing.T) {
	t.Parallel()

	_, err := parseMetaPlanResult(`{"phases":[]}`)
	if err == nil {
		t.Fatal("expected error for empty phases")
	}
	if !strings.Contains(err.Error(), "no phases") {
		t.Fatalf("error = %q, want 'no phases' message", err.Error())
	}
}

func TestParseMetaPlanResultMarkdownFences(t *testing.T) {
	t.Parallel()

	raw := "```json\n{\"phases\":[{\"name\":\"p1\",\"description\":\"d\",\"directives\":[\"Add a config file parser for the module settings\"],\"depends_on\":[]}]}\n```"

	result, err := parseMetaPlanResult(raw)
	if err != nil {
		t.Fatalf("parseMetaPlanResult() error = %v", err)
	}
	if len(result.Phases) != 1 {
		t.Fatalf("got %d phases, want 1", len(result.Phases))
	}
}

// --- validateMetaPlanResult edge case tests ---

func TestValidateMetaPlanResult_DuplicatePhase(t *testing.T) {
	t.Parallel()
	result := &MetaPlanResult{
		Phases: []RoadmapPhase{
			{Name: "dup", Description: "a", Directives: []string{"Add a test config file"}},
			{Name: "dup", Description: "b", Directives: []string{"Add a test config file"}},
		},
	}
	err := validateMetaPlanResult(result)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestValidateMetaPlanResult_ForwardDependency(t *testing.T) {
	t.Parallel()
	result := &MetaPlanResult{
		Phases: []RoadmapPhase{
			{Name: "a", Description: "first", Directives: []string{"Add a command handler"}, DependsOn: []string{"b"}},
			{Name: "b", Description: "second", Directives: []string{"Add a test command"}},
		},
	}
	err := validateMetaPlanResult(result)
	if err == nil || !strings.Contains(err.Error(), "not defined or comes later") {
		t.Fatalf("expected forward dependency error, got %v", err)
	}
}

func TestValidateMetaPlanResult_EmptyDirectives(t *testing.T) {
	t.Parallel()
	result := &MetaPlanResult{
		Phases: []RoadmapPhase{
			{Name: "empty", Description: "no directives", Directives: nil},
		},
	}
	err := validateMetaPlanResult(result)
	if err == nil || !strings.Contains(err.Error(), "no directives") {
		t.Fatalf("expected no directives error, got %v", err)
	}
}

func TestValidateMetaPlanResult_SelfDependency(t *testing.T) {
	t.Parallel()
	result := &MetaPlanResult{
		Phases: []RoadmapPhase{
			{Name: "a", Description: "self-ref", Directives: []string{"Add a test command"}, DependsOn: []string{"a"}},
		},
	}
	err := validateMetaPlanResult(result)
	if err == nil || !strings.Contains(err.Error(), "self-dependency") {
		t.Fatalf("expected self-dependency error, got %v", err)
	}
}
