package planner

import (
	"fmt"
	"strings"
	"time"

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

// RoadmapResult is returned by Planner.MetaPlan. Contains the raw engine
// result, validated phases, and cached recon data for reject-and-revise.
type RoadmapResult struct {
	ID              string           `json:"id"`
	ProjectRef      string           `json:"project_ref"`
	Phases          []ValidatedPhase `json:"phases"`
	ReconData       string           `json:"-"` // cached for re-use on reject
	AgentsMD        string           `json:"-"` // cached for re-use on reject
	Roadmap         string           `json:"roadmap"`
	TotalDirectives int              `json:"total_directives"`
	ValidDirectives int              `json:"valid_directives"`
}

func generateRoadmapID() string {
	return fmt.Sprintf("roadmap-%d", time.Now().UnixNano())
}

// FlattenValidatedPhases converts validated phases into flat slices suitable
// for CreateBatchWithPhases. Invalid directives are dropped. Returns the
// count of dropped directives. Each phase's depends_on is joined with commas
// for multi-dependency phases.
func FlattenValidatedPhases(phases []ValidatedPhase) (directives, phaseNames, phaseDeps []string, dropped int) {
	for _, phase := range phases {
		dep := ""
		if len(phase.DependsOn) > 0 {
			dep = strings.Join(phase.DependsOn, ",")
		}

		for _, d := range phase.Directives {
			if !d.Valid {
				dropped++
				continue
			}
			directives = append(directives, d.Text)
			phaseNames = append(phaseNames, phase.Name)
			phaseDeps = append(phaseDeps, dep)
		}
	}

	return directives, phaseNames, phaseDeps, dropped
}
