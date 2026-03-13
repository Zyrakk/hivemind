package planner

import (
	"testing"

	"github.com/zyrakk/hivemind/internal/engine"
)

func TestValidateMetaPlanDirectives_AllValid(t *testing.T) {
	t.Parallel()

	result := &engine.MetaPlanResult{
		Phases: []engine.RoadmapPhase{
			{
				Name:       "core",
				Directives: []string{"Add a config parser module for YAML-based settings in the file system"},
			},
		},
	}

	validated := ValidateMetaPlanDirectives(result)

	if len(validated) != 1 {
		t.Fatalf("got %d phases, want 1", len(validated))
	}
	if validated[0].Directives[0].Valid != true {
		t.Fatal("expected directive to be valid")
	}
	if validated[0].Directives[0].Error != "" {
		t.Fatalf("expected no error, got %q", validated[0].Directives[0].Error)
	}
}

func TestValidateMetaPlanDirectives_SomeInvalid(t *testing.T) {
	t.Parallel()

	result := &engine.MetaPlanResult{
		Phases: []engine.RoadmapPhase{
			{
				Name: "mixed",
				Directives: []string{
					"Add a config parser module for YAML-based settings in the file system",
					"do stuff", // too short, no noun
				},
			},
		},
	}

	validated := ValidateMetaPlanDirectives(result)

	if validated[0].Directives[0].Valid != true {
		t.Fatal("first directive should be valid")
	}
	if validated[0].Directives[1].Valid != false {
		t.Fatal("second directive should be invalid")
	}
	if validated[0].Directives[1].Error == "" {
		t.Fatal("expected error message for invalid directive")
	}
}

func TestValidateMetaPlanDirectives_CountsValid(t *testing.T) {
	t.Parallel()

	result := &engine.MetaPlanResult{
		Phases: []engine.RoadmapPhase{
			{
				Name: "p1",
				Directives: []string{
					"Add a config parser module for YAML-based settings in the file system",
					"too short",
					"Implement the validation handler for incoming scoring requests on the endpoint",
				},
			},
		},
	}

	validated := ValidateMetaPlanDirectives(result)

	validCount := 0
	for _, d := range validated[0].Directives {
		if d.Valid {
			validCount++
		}
	}
	if validCount != 2 {
		t.Fatalf("got %d valid directives, want 2", validCount)
	}
}

func TestValidateMetaPlanDirectives_NilResult(t *testing.T) {
	t.Parallel()
	validated := ValidateMetaPlanDirectives(nil)
	if validated != nil {
		t.Fatalf("expected nil, got %v", validated)
	}
}

func TestValidateMetaPlanDirectives_EmptyPhases(t *testing.T) {
	t.Parallel()
	result := &engine.MetaPlanResult{Phases: nil}
	validated := ValidateMetaPlanDirectives(result)
	if len(validated) != 0 {
		t.Fatalf("expected 0 phases, got %d", len(validated))
	}
}

func TestValidateMetaPlanDirectives_AllInvalid(t *testing.T) {
	t.Parallel()
	result := &engine.MetaPlanResult{
		Phases: []engine.RoadmapPhase{
			{
				Name:       "bad",
				Directives: []string{"too short", "also short"},
			},
		},
	}
	validated := ValidateMetaPlanDirectives(result)
	for _, d := range validated[0].Directives {
		if d.Valid {
			t.Fatal("expected all directives to be invalid")
		}
	}
}
