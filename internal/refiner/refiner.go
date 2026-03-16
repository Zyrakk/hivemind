package refiner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zyrakk/hivemind/internal/llm"
)

// RefinerLLM is the minimal interface the refiner needs.
// GLMClient already satisfies this.
type RefinerLLM interface {
	Chat(ctx context.Context, systemPrompt, userMessage string) (string, llm.TokenUsage, error)
}

type Refiner struct {
	improver  RefinerLLM
	evaluator RefinerLLM
	logger    *slog.Logger
}

func New(improver, evaluator RefinerLLM, logger *slog.Logger) *Refiner {
	if evaluator == nil {
		evaluator = improver
	}
	return &Refiner{
		improver:  improver,
		evaluator: evaluator,
		logger:    logger,
	}
}

type RefinementResult struct {
	OriginalDocument string
	FinalDocument    string
	Iterations       int
	FinalScore       float64
	Converged        bool
	History          []IterationLog
}

type IterationLog struct {
	Iteration    int
	Score        float64
	Deficiencies []Deficiency
	TokensUsed   llm.TokenUsage
}

type Deficiency struct {
	Criterion   string `json:"criterion"`
	Section     string `json:"section"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
}

type RubricScore struct {
	OverallScore float64      `json:"overall_score"`
	Verdict      string       `json:"verdict"`
	Summary      string       `json:"summary"`
	Deficiencies []Deficiency `json:"deficiencies"`
}

func extractDocument(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```markdown")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimSpace(trimmed)
	}
	return trimmed
}

func parseRubricScore(content string) (*RubricScore, error) {
	jsonPayload, err := llm.ExtractJSONObject(content)
	if err != nil {
		return nil, err
	}
	var score RubricScore
	if err := json.Unmarshal(jsonPayload, &score); err != nil {
		return nil, fmt.Errorf("decode rubric score: %w", err)
	}
	return &score, nil
}

func buildImproveMessage(document string, deficiencies []Deficiency) string {
	if len(deficiencies) == 0 {
		return "Improve the following document:\n\n" + document
	}
	var b strings.Builder
	b.WriteString("Improve the following document. Fix these specific deficiencies:\n\n")
	for _, d := range deficiencies {
		fmt.Fprintf(&b, "- [%s] %s: %s (suggestion: %s)\n", d.Criterion, d.Section, d.Description, d.Suggestion)
	}
	b.WriteString("\n---\n\nDocument:\n\n")
	b.WriteString(document)
	return b.String()
}

const (
	maxIterations  = 4
	scoreThreshold = 0.85
)

func (r *Refiner) Run(ctx context.Context, document, rubric, improvementPrompt string) (*RefinementResult, error) {
	result := &RefinementResult{
		OriginalDocument: document,
	}

	current := document
	var lastDeficiencies []Deficiency

	for i := 0; i < maxIterations; i++ {
		// Step 1: Improve
		userMsg := buildImproveMessage(current, lastDeficiencies)
		improved, usage1, err := r.improver.Chat(ctx, improvementPrompt, userMsg)
		if err != nil {
			return nil, fmt.Errorf("improve iteration %d: %w", i+1, err)
		}
		current = extractDocument(improved)

		// Step 2: Evaluate
		scoreJSON, usage2, err := r.evaluator.Chat(ctx, rubric, current)
		if err != nil {
			return nil, fmt.Errorf("evaluate iteration %d: %w", i+1, err)
		}

		score, parseErr := parseRubricScore(scoreJSON)
		if parseErr != nil {
			r.logger.Warn("failed to parse rubric score, terminating loop",
				"iteration", i+1,
				"error", parseErr,
			)
			score = &RubricScore{}
		}

		iterLog := IterationLog{
			Iteration:    i + 1,
			Score:        score.OverallScore,
			Deficiencies: score.Deficiencies,
			TokensUsed: llm.TokenUsage{
				PromptTokens:     usage1.PromptTokens + usage2.PromptTokens,
				CompletionTokens: usage1.CompletionTokens + usage2.CompletionTokens,
				TotalTokens:      usage1.TotalTokens + usage2.TotalTokens,
			},
		}
		result.History = append(result.History, iterLog)
		result.Iterations = i + 1
		result.FinalScore = score.OverallScore
		result.FinalDocument = current

		// Step 3: Convergence checks
		if score.OverallScore >= scoreThreshold {
			result.Converged = true
			return result, nil
		}
		if i > 0 && len(score.Deficiencies) >= len(lastDeficiencies) {
			result.Converged = true
			return result, nil
		}

		lastDeficiencies = score.Deficiencies
	}

	return result, nil
}
