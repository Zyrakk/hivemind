package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/zyrakk/hivemind/internal/llm"
)

type mockLLMClient struct {
	planFn     func(ctx context.Context, directive, agentsMD string) (*llm.TaskPlan, error)
	evaluateFn func(ctx context.Context, task, diff, agentsMD string) (*llm.Evaluation, error)

	lastPlanDirective    string
	lastPlanAgentsMD     string
	lastEvaluateTask     string
	lastEvaluateDiff     string
	lastEvaluateAgentsMD string
}

func (m *mockLLMClient) Plan(ctx context.Context, directive, agentsMD string) (*llm.TaskPlan, error) {
	m.lastPlanDirective = directive
	m.lastPlanAgentsMD = agentsMD
	if m.planFn != nil {
		return m.planFn(ctx, directive, agentsMD)
	}
	return &llm.TaskPlan{}, nil
}

func (m *mockLLMClient) Evaluate(ctx context.Context, task, diff, agentsMD string) (*llm.Evaluation, error) {
	m.lastEvaluateTask = task
	m.lastEvaluateDiff = diff
	m.lastEvaluateAgentsMD = agentsMD
	if m.evaluateFn != nil {
		return m.evaluateFn(ctx, task, diff, agentsMD)
	}
	return &llm.Evaluation{}, nil
}

func (m *mockLLMClient) Chat(context.Context, string, string) (string, llm.TokenUsage, error) {
	return "", llm.TokenUsage{}, nil
}

func TestThinkAlwaysReturnsReady(t *testing.T) {
	t.Parallel()

	engine := NewGLMEngine(nil, nil)
	req := ThinkRequest{
		Directive: "Implement the GLM engine adapter for hivemind.",
		AgentsMD:  "No secrets.",
		ReconData: "engine.go exists",
		Cache:     "session-cache",
	}

	got, err := engine.Think(context.Background(), req)
	if err != nil {
		t.Fatalf("Think() error = %v", err)
	}
	if got.Type != "ready" {
		t.Fatalf("Think() type = %q, want %q", got.Type, "ready")
	}
	assertContainsString(t, got.Summary, "Directive: Implement the GLM engine adapter for hivemind.")
	assertContainsString(t, got.Summary, "AGENTS.md provided (11 chars)")
	assertContainsString(t, got.Summary, "repo recon provided (16 chars)")
	assertContainsString(t, got.Summary, "Session cache available.")
}

func TestProposeConversion(t *testing.T) {
	t.Parallel()

	mock := &mockLLMClient{
		planFn: func(context.Context, string, string) (*llm.TaskPlan, error) {
			return &llm.TaskPlan{
				Confidence: 0.87,
				Notes:      "Plan notes",
				Tasks: []llm.Task{
					{
						ID:          "T1",
						Title:       "Implement adapter",
						Description: "Add the GLM adapter",
						DependsOn:   []string{"T0"},
						BranchName:  "feature/glm-adapter",
					},
				},
			}, nil
		},
	}

	engine := NewGLMEngine(mock, nil)
	req := ProposeRequest{
		Directive:       "Implement GLM engine",
		AgentsMD:        "Keep changes scoped.",
		ReconData:       "internal/engine exists",
		ThinkingSummary: "GLM should wrap the existing llm client.",
	}

	got, err := engine.Propose(context.Background(), req)
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	if got.Confidence != 0.87 || got.Summary != "Plan notes" {
		t.Fatalf("unexpected plan metadata: %#v", *got)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(got.Tasks))
	}

	task := got.Tasks[0]
	if task.ID != "T1" || task.Title != "Implement adapter" {
		t.Fatalf("unexpected task identity: %#v", task)
	}
	if task.Description != "Add the GLM adapter" || task.Prompt != "Add the GLM adapter" {
		t.Fatalf("unexpected task description/prompt: %#v", task)
	}
	if task.BranchName != "feature/glm-adapter" {
		t.Fatalf("branch name = %q, want %q", task.BranchName, "feature/glm-adapter")
	}
	if task.Priority != 0 || task.Type != "coding" {
		t.Fatalf("unexpected task priority/type: %#v", task)
	}
	if !reflect.DeepEqual(task.Dependencies, []string{"T0"}) {
		t.Fatalf("dependencies = %#v, want %#v", task.Dependencies, []string{"T0"})
	}
	assertContainsString(t, mock.lastPlanDirective, "Analysis: GLM should wrap the existing llm client.")
}

func TestProposeIncludesReconData(t *testing.T) {
	t.Parallel()

	mock := &mockLLMClient{
		planFn: func(context.Context, string, string) (*llm.TaskPlan, error) {
			return &llm.TaskPlan{
				Confidence: 0.5,
				Notes:      "ok",
				Tasks: []llm.Task{
					{ID: "T1", Title: "Task", Description: "Desc"},
				},
			}, nil
		},
	}

	engine := NewGLMEngine(mock, nil)
	_, err := engine.Propose(context.Background(), ProposeRequest{
		Directive: "Directive",
		AgentsMD:  "AGENTS",
		ReconData: "git status and tree output",
	})
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	assertContainsString(t, mock.lastPlanAgentsMD, "AGENTS")
	assertContainsString(t, mock.lastPlanAgentsMD, "Repository state:\ngit status and tree output")
}

func TestRebuildAppendsFeedback(t *testing.T) {
	t.Parallel()

	mock := &mockLLMClient{
		planFn: func(context.Context, string, string) (*llm.TaskPlan, error) {
			return &llm.TaskPlan{
				Confidence: 0.6,
				Notes:      "rebuilt",
				Tasks: []llm.Task{
					{ID: "T1", Title: "Task", Description: "Desc"},
				},
			}, nil
		},
	}

	engine := NewGLMEngine(mock, nil)
	_, err := engine.Rebuild(context.Background(), RebuildRequest{
		Directive:   "Original directive",
		Feedback:    "Need smaller tasks.",
		AgentsMD:    "AGENTS",
		ReconData:   "repo state",
		ProjectName: "hivemind",
	})
	if err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}

	assertContainsString(t, mock.lastPlanDirective, "Original directive")
	assertContainsString(t, mock.lastPlanDirective, "Previous plan was rejected. Feedback: Need smaller tasks.")
	assertContainsString(t, mock.lastPlanAgentsMD, "Repository state:\nrepo state")
}

func TestEvaluateConversion(t *testing.T) {
	t.Parallel()

	mock := &mockLLMClient{
		evaluateFn: func(context.Context, string, string, string) (*llm.Evaluation, error) {
			return &llm.Evaluation{
				Verdict:    "changes_requested",
				Summary:    "Needs more validation.",
				Confidence: 0.72,
				Issues: []llm.Issue{
					{Severity: "medium", Description: "Missing tests", Suggestion: "Add unit tests"},
					{Severity: "low", Description: "Missing guard", Suggestion: "Handle nil client"},
				},
			}, nil
		},
	}

	engine := NewGLMEngine(mock, nil)
	got, err := engine.Evaluate(context.Background(), EvalRequest{
		TaskID:      "task-9",
		TaskDesc:    "Review worker output",
		DiffContent: "diff --git",
		BuildOutput: "build ok",
		TestOutput:  "tests ok",
		VetOutput:   "vet ok",
		AgentsMD:    "AGENTS",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if got.TaskID != "task-9" {
		t.Fatalf("task id = %q, want %q", got.TaskID, "task-9")
	}
	if got.Verdict != "retry" {
		t.Fatalf("verdict = %q, want %q", got.Verdict, "retry")
	}
	if got.Analysis != "Needs more validation." || got.Confidence != 0.72 {
		t.Fatalf("unexpected analysis/confidence: %#v", *got)
	}
	if !reflect.DeepEqual(got.Suggestions, []string{"Add unit tests", "Handle nil client"}) {
		t.Fatalf("suggestions = %#v", got.Suggestions)
	}
	if got.RetryPrompt != "Add unit tests\nHandle nil client" {
		t.Fatalf("retry prompt = %q", got.RetryPrompt)
	}
}

func TestEvaluateIncludesBuildTestVet(t *testing.T) {
	t.Parallel()

	mock := &mockLLMClient{
		evaluateFn: func(context.Context, string, string, string) (*llm.Evaluation, error) {
			return &llm.Evaluation{
				Verdict: "approved",
				Summary: "Looks good.",
			}, nil
		},
	}

	engine := NewGLMEngine(mock, nil)
	_, err := engine.Evaluate(context.Background(), EvalRequest{
		TaskDesc:    "Task description",
		DiffContent: "diff content",
		BuildOutput: "build output",
		TestOutput:  "test output",
		VetOutput:   "vet output",
		AgentsMD:    "AGENTS",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	assertContainsString(t, mock.lastEvaluateDiff, "DIFF:\ndiff content")
	assertContainsString(t, mock.lastEvaluateDiff, "BUILD OUTPUT:\nbuild output")
	assertContainsString(t, mock.lastEvaluateDiff, "TEST OUTPUT:\ntest output")
	assertContainsString(t, mock.lastEvaluateDiff, "VET OUTPUT:\nvet output")
	if mock.lastEvaluateTask != "Task description" {
		t.Fatalf("task passed to Evaluate() = %q, want %q", mock.lastEvaluateTask, "Task description")
	}
	if mock.lastEvaluateAgentsMD != "AGENTS" {
		t.Fatalf("agentsMD passed to Evaluate() = %q, want %q", mock.lastEvaluateAgentsMD, "AGENTS")
	}
}

func TestAvailableNilClient(t *testing.T) {
	t.Parallel()

	engine := NewGLMEngine(nil, nil)
	if engine.Available(context.Background()) {
		t.Fatal("Available() = true, want false")
	}
}

func TestAvailableWithClient(t *testing.T) {
	t.Parallel()

	engine := NewGLMEngine(&mockLLMClient{}, nil)
	if !engine.Available(context.Background()) {
		t.Fatal("Available() = false, want true")
	}
}

func assertContainsString(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}
