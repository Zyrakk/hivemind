package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/zyrakk/hivemind/internal/llm"
)

type GLMEngine struct {
	client llm.LLMClient
	logger *slog.Logger
}

func NewGLMEngine(client llm.LLMClient, logger *slog.Logger) *GLMEngine {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &GLMEngine{
		client: client,
		logger: logger,
	}
}

func (e *GLMEngine) Think(ctx context.Context, req ThinkRequest) (*ThinkResult, error) {
	_ = ctx
	return &ThinkResult{
		Type:    "ready",
		Summary: buildGLMThinkSummary(req),
	}, nil
}

func (e *GLMEngine) Propose(ctx context.Context, req ProposeRequest) (*PlanResult, error) {
	if e == nil || e.client == nil {
		return nil, llm.ErrNotImplemented
	}

	directive, agentsMD := buildGLMProposeInputs(req)
	plan, err := e.client.Plan(ctx, directive, agentsMD)
	if err != nil {
		return nil, err
	}

	return convertGLMPlan(plan)
}

func (e *GLMEngine) Rebuild(ctx context.Context, req RebuildRequest) (*PlanResult, error) {
	if e == nil || e.client == nil {
		return nil, llm.ErrNotImplemented
	}

	directive, agentsMD := buildGLMRebuildInputs(req)
	plan, err := e.client.Plan(ctx, directive, agentsMD)
	if err != nil {
		return nil, err
	}

	return convertGLMPlan(plan)
}

func (e *GLMEngine) Evaluate(ctx context.Context, req EvalRequest) (*EvalResult, error) {
	if e == nil || e.client == nil {
		return nil, llm.ErrNotImplemented
	}

	evaluation, err := e.client.Evaluate(ctx, req.TaskDesc, buildGLMEvalDiff(req), req.AgentsMD)
	if err != nil {
		return nil, err
	}

	return convertGLMEvaluation(req.TaskID, evaluation)
}

func (e *GLMEngine) Name() string {
	return "glm"
}

func (e *GLMEngine) Available(context.Context) bool {
	return e != nil && e.client != nil
}

func buildGLMThinkSummary(req ThinkRequest) string {
	summary := fmt.Sprintf(
		"Directive: %s. Context: AGENTS.md provided (%d chars), repo recon provided (%d chars).",
		truncateForSummary(req.Directive, 100),
		len(req.AgentsMD),
		len(req.ReconData),
	)
	if strings.TrimSpace(req.Cache) != "" {
		summary += " Session cache available."
	}
	return summary
}

func buildGLMProposeInputs(req ProposeRequest) (directive string, agentsMD string) {
	directive = req.Directive
	if strings.TrimSpace(req.ThinkingSummary) != "" {
		directive += "\n\nAnalysis: " + req.ThinkingSummary
	}

	agentsMD = req.AgentsMD
	if strings.TrimSpace(req.ReconData) != "" {
		agentsMD += "\n\nRepository state:\n" + req.ReconData
	}

	return directive, agentsMD
}

func buildGLMRebuildInputs(req RebuildRequest) (directive string, agentsMD string) {
	directive = req.Directive + "\n\nPrevious plan was rejected. Feedback: " + req.Feedback
	agentsMD = req.AgentsMD
	if strings.TrimSpace(req.ReconData) != "" {
		agentsMD += "\n\nRepository state:\n" + req.ReconData
	}
	return directive, agentsMD
}

func buildGLMEvalDiff(req EvalRequest) string {
	var builder strings.Builder
	builder.WriteString("DIFF:\n")
	builder.WriteString(req.DiffContent)
	builder.WriteString("\n\nBUILD OUTPUT:\n")
	builder.WriteString(req.BuildOutput)
	builder.WriteString("\n\nTEST OUTPUT:\n")
	builder.WriteString(req.TestOutput)
	builder.WriteString("\n\nVET OUTPUT:\n")
	builder.WriteString(req.VetOutput)
	return builder.String()
}

func convertGLMPlan(plan *llm.TaskPlan) (*PlanResult, error) {
	if plan == nil {
		return nil, errors.New("glm plan is nil")
	}

	tasks := make([]PlanTask, 0, len(plan.Tasks))
	for idx, task := range plan.Tasks {
		desc := strings.TrimSpace(task.Description)
		briefing := firstSentence(desc)
		tasks = append(tasks, PlanTask{
			ID:              task.ID,
			Title:           task.Title,
			Description:     desc,
			BranchName:      task.BranchName,
			Dependencies:    append([]string(nil), task.DependsOn...),
			Priority:        idx,
			Type:            "coding",
			Prompt:          desc,
			Briefing:        briefing,
			ExecutionPrompt: desc,
		})
	}

	return &PlanResult{
		Tasks:      tasks,
		Summary:    plan.Notes,
		Confidence: plan.Confidence,
	}, nil
}

func convertGLMEvaluation(taskID string, evaluation *llm.Evaluation) (*EvalResult, error) {
	if evaluation == nil {
		return nil, errors.New("glm evaluation is nil")
	}

	suggestions := collectGLMSuggestions(evaluation)
	verdict := mapGLMVerdict(evaluation.Verdict)

	return &EvalResult{
		TaskID:      taskID,
		Verdict:     verdict,
		Analysis:    evaluation.Summary,
		Suggestions: suggestions,
		RetryPrompt: buildGLMRetryPrompt(verdict, evaluation, suggestions),
		Confidence:  evaluation.Confidence,
	}, nil
}

func collectGLMSuggestions(evaluation *llm.Evaluation) []string {
	if evaluation == nil {
		return nil
	}

	suggestions := make([]string, 0, len(evaluation.Issues))
	for _, issue := range evaluation.Issues {
		suggestion := strings.TrimSpace(issue.Suggestion)
		if suggestion != "" {
			suggestions = append(suggestions, suggestion)
		}
	}

	return suggestions
}

func buildGLMRetryPrompt(verdict string, evaluation *llm.Evaluation, suggestions []string) string {
	if verdict != "retry" || evaluation == nil {
		return ""
	}
	if len(suggestions) > 0 {
		return strings.Join(suggestions, "\n")
	}
	return strings.TrimSpace(evaluation.Summary)
}

func mapGLMVerdict(verdict string) string {
	switch strings.ToLower(strings.TrimSpace(verdict)) {
	case "accept", "accepted", "approved", "pass", "passed", "ok":
		return "pass"
	case "iterate", "retry", "changes_requested", "rework", "revise", "fix":
		return "retry"
	default:
		return "escalate"
	}
}

func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for i, r := range text {
		if r == '.' || r == '!' || r == '?' {
			if i+1 < len(text) && (text[i+1] == ' ' || text[i+1] == '\n') {
				return text[:i+1]
			}
			if i+1 == len(text) {
				return text
			}
		}
	}
	// No sentence boundary found, truncate at 200 chars
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}

func truncateForSummary(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}

	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}

	return string(runes[:maxChars])
}
