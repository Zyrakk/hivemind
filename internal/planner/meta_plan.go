package planner

import (
	"github.com/zyrakk/hivemind/internal/engine"
)

// ValidatedDirective pairs a directive string with its L1 validation result.
type ValidatedDirective struct {
	Text  string `json:"text"`
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

// ValidatedPhase is a RoadmapPhase with per-directive validation results.
type ValidatedPhase struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Directives  []ValidatedDirective `json:"directives"`
	DependsOn   []string             `json:"depends_on"`
}

// ValidateMetaPlanDirectives runs L1 ValidateDirective on every directive
// in the MetaPlanResult. Failed directives are flagged but NOT removed.
func ValidateMetaPlanDirectives(result *engine.MetaPlanResult) []ValidatedPhase {
	if result == nil {
		return nil
	}

	phases := make([]ValidatedPhase, 0, len(result.Phases))
	for _, phase := range result.Phases {
		vp := ValidatedPhase{
			Name:        phase.Name,
			Description: phase.Description,
			DependsOn:   phase.DependsOn,
			Directives:  make([]ValidatedDirective, 0, len(phase.Directives)),
		}

		for _, directive := range phase.Directives {
			_, err := ValidateDirective(directive)
			vd := ValidatedDirective{
				Text:  directive,
				Valid: err == nil,
			}
			if err != nil {
				vd.Error = err.Error()
			}
			vp.Directives = append(vp.Directives, vd)
		}

		phases = append(phases, vp)
	}

	return phases
}
