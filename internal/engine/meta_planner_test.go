package engine

import (
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
