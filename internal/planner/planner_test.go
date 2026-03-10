package planner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zyrakk/hivemind/internal/checklist"
	"github.com/zyrakk/hivemind/internal/engine"
	"github.com/zyrakk/hivemind/internal/evaluator"
	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/recon"
	"github.com/zyrakk/hivemind/internal/state"
)

func TestCreatePlanReturnsValidPlanAndPersistsTasks(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	glm := &mockPlannerGLM{
		plans: []*llm.TaskPlan{
			{
				Confidence: 0.82,
				Tasks: []llm.Task{
					{
						ID:          "task-1",
						Title:       "Implement planner",
						Description: "Add planner workflow",
						BranchName:  "planner-workflow",
					},
				},
				Questions: nil,
				Notes:     "ok",
			},
		},
	}

	planner := NewWithDeps(glm, nil, newMockPlannerLauncher(true), store, "prompts", nil)
	result, err := planner.CreatePlan(context.Background(), "Implement the planner module command with a new test endpoint", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if result == nil || result.Plan == nil {
		t.Fatal("expected non-nil plan result")
	}
	if result.NeedsInput {
		t.Fatal("expected needs_input=false")
	}
	if result.Status != plannerReadyStatus {
		t.Fatalf("unexpected status: %s", result.Status)
	}

	detail, err := store.GetProjectDetail(context.Background(), "flux")
	if err != nil {
		t.Fatalf("GetProjectDetail returned error: %v", err)
	}
	if len(detail.Tasks) != 1 {
		t.Fatalf("expected 1 task in db, got %d", len(detail.Tasks))
	}
	if detail.Tasks[0].Status != state.TaskStatusPending {
		t.Fatalf("expected pending task status, got %s", detail.Tasks[0].Status)
	}
}

func TestCreatePlanLowConfidenceConsultsAndRefines(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	glm := &mockPlannerGLM{
		plans: []*llm.TaskPlan{
			{
				Confidence: 0.42,
				Tasks:      []llm.Task{{ID: "task-1", Title: "Initial", Description: "initial", BranchName: "initial"}},
				Notes:      "initial",
			},
			{
				Confidence: 0.91,
				Tasks:      []llm.Task{{ID: "task-1", Title: "Refined", Description: "refined", BranchName: "refined"}},
				Notes:      "refined",
			},
		},
	}

	consultant := &mockConsultant{
		name:      "claude",
		available: true,
		opinion: &llm.Opinion{
			ConsultationType:  "plan_validation",
			AgreeWithOriginal: false,
			Analysis:          "The plan misses risk analysis",
			Recommendations:   []string{"Add risk task"},
			RiskFlags:         []string{"insufficient validation"},
			Confidence:        0.8,
		},
	}

	planner := NewWithDeps(glm, []llm.ConsultantClient{consultant}, newMockPlannerLauncher(true), store, "prompts", nil)
	result, err := planner.CreatePlan(context.Background(), "Implement the risky feature command with a new validation endpoint", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if !result.ConsultantUsed {
		t.Fatal("expected consultant_used=true")
	}
	if result.Plan.Confidence < 0.9 {
		t.Fatalf("expected refined confidence >=0.9, got %f", result.Plan.Confidence)
	}
	if glm.calls() != 2 {
		t.Fatalf("expected 2 glm calls, got %d", glm.calls())
	}
	if consultant.calls() != 1 {
		t.Fatalf("expected 1 consultant call, got %d", consultant.calls())
	}
}

func TestCreatePlanWithQuestionsReturnsNeedsInput(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	glm := &mockPlannerGLM{
		plans: []*llm.TaskPlan{
			{
				Confidence: 0.75,
				Tasks:      []llm.Task{{ID: "task-1", Title: "Task", Description: "desc", BranchName: "task"}},
				Questions:  []string{"Need target API version?"},
				Notes:      "input needed",
			},
		},
	}

	planner := NewWithDeps(glm, nil, newMockPlannerLauncher(true), store, "prompts", nil)
	result, err := planner.CreatePlan(context.Background(), "Create a new config file for the ambiguity resolution module", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if !result.NeedsInput {
		t.Fatal("expected needs_input=true")
	}
	if result.Status != plannerNeedsInputStatus {
		t.Fatalf("expected status needs_input, got %s", result.Status)
	}
}

func TestExecutePlanRespectsDependencies(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	glm := &mockPlannerGLM{
		plans: []*llm.TaskPlan{
			{
				Confidence: 0.9,
				Tasks: []llm.Task{
					{ID: "task-a", Title: "A", Description: "task a", BranchName: "a", DependsOn: nil},
					{ID: "task-b", Title: "B", Description: "task b", BranchName: "b", DependsOn: []string{"task-a"}},
				},
			},
		},
	}

	launch := newMockPlannerLauncher(true)
	planner := NewWithDeps(glm, nil, launch, store, "prompts", nil)
	planResult, err := planner.CreatePlan(context.Background(), "Add a new command to run dependent tasks for the test module", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}

	if err := planner.ExecutePlan(context.Background(), planResult.PlanID); err != nil {
		t.Fatalf("ExecutePlan returned error: %v", err)
	}

	order := launch.launchOrder()
	if len(order) != 2 {
		t.Fatalf("expected 2 launched tasks, got %d", len(order))
	}
	// DB task IDs are auto-increment; first task gets "1", second gets "2".
	if order[0] != "1" || order[1] != "2" {
		t.Fatalf("expected launch order [1 2], got %v", order)
	}

	detail, err := store.GetProjectDetail(context.Background(), "flux")
	if err != nil {
		t.Fatalf("GetProjectDetail returned error: %v", err)
	}
	if len(detail.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in db, got %d", len(detail.Tasks))
	}
	for _, task := range detail.Tasks {
		if task.Status != state.TaskStatusCompleted {
			t.Fatalf("expected task %s completed, got %s", task.Title, task.Status)
		}
	}
}

func TestExecutePlanRunsEvaluatorAndHandlesIterate(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	glm := &mockPlannerGLM{
		plans: []*llm.TaskPlan{
			{
				Confidence: 0.93,
				Tasks: []llm.Task{
					{ID: "task-main", Title: "Main", Description: "run task", BranchName: "main-task"},
				},
			},
		},
	}

	launch := newMockPlannerLauncher(true)
	planner := NewWithDeps(glm, nil, launch, store, "prompts", nil)

	mockEval := &mockPlanEvaluator{launcher: launch}
	planner.SetEvaluator(mockEval)

	planResult, err := planner.CreatePlan(context.Background(), "Implement the full flow command with endpoint and test support", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}

	if err := planner.ExecutePlan(context.Background(), planResult.PlanID); err != nil {
		t.Fatalf("ExecutePlan returned error: %v", err)
	}

	if mockEval.calls() != 2 {
		t.Fatalf("expected evaluator to run 2 times (iterate + accept), got %d", mockEval.calls())
	}

	detail, err := store.GetProjectDetail(context.Background(), "flux")
	if err != nil {
		t.Fatalf("GetProjectDetail returned error: %v", err)
	}
	if len(detail.Tasks) != 1 {
		t.Fatalf("expected 1 task in db, got %d", len(detail.Tasks))
	}
	if detail.Tasks[0].Status != state.TaskStatusCompleted {
		t.Fatalf("expected task status completed, got %s", detail.Tasks[0].Status)
	}
}

func TestNotificationsWired(t *testing.T) {
	t.Run("task completed", func(t *testing.T) {
		store, cleanup := setupPlannerTestEnv(t)
		defer cleanup()

		glm := &mockPlannerGLM{
			plans: []*llm.TaskPlan{
				{
					Confidence: 0.9,
					Tasks:      []llm.Task{{ID: "task-1", Title: "Parser", Description: "task", BranchName: "parser"}},
				},
			},
		}

		launch := newMockPlannerLauncher(true)
		launch.completionStatus = state.WorkerStatusCompleted

		planner := NewWithDeps(glm, nil, launch, store, "prompts", nil)
		notifier := &mockPlannerNotifier{}
		planner.SetNotifier(notifier)

		planResult, err := planner.CreatePlan(context.Background(), "Add a command to run the task executor for the test module", "flux")
		if err != nil {
			t.Fatalf("CreatePlan returned error: %v", err)
		}
		if err := planner.ExecutePlan(context.Background(), planResult.PlanID); err != nil {
			t.Fatalf("ExecutePlan returned error: %v", err)
		}

		if notifier.completedCount() == 0 {
			t.Fatalf("expected task completion notification")
		}
	})

	t.Run("worker failed", func(t *testing.T) {
		store, cleanup := setupPlannerTestEnv(t)
		defer cleanup()

		glm := &mockPlannerGLM{
			plans: []*llm.TaskPlan{
				{
					Confidence: 0.9,
					Tasks:      []llm.Task{{ID: "task-1", Title: "Parser", Description: "task", BranchName: "parser"}},
				},
			},
		}

		launch := newMockPlannerLauncher(true)
		launch.completionStatus = state.WorkerStatusFailed
		launch.completionError = "timeout"

		planner := NewWithDeps(glm, nil, launch, store, "prompts", nil)
		notifier := &mockPlannerNotifier{}
		planner.SetNotifier(notifier)

		planResult, err := planner.CreatePlan(context.Background(), "Add a command to run the task executor for the test module", "flux")
		if err != nil {
			t.Fatalf("CreatePlan returned error: %v", err)
		}
		if err := planner.ExecutePlan(context.Background(), planResult.PlanID); err == nil {
			t.Fatalf("expected execution error for failed worker")
		}
		if notifier.failedCount() == 0 {
			t.Fatalf("expected worker failure notification")
		}
	})
}

func TestCreatePlanViaEngineInfoRequestFlow(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	if err := os.Setenv("HIVEMIND_REPOS_DIR", "."); err != nil {
		t.Fatalf("Setenv failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("HIVEMIND_REPOS_DIR")
	})
	if err := os.MkdirAll("flux", 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}

	planner := NewWithDeps(&mockPlannerGLM{}, nil, newMockPlannerLauncher(true), store, "prompts", nil)
	mockEngine := &mockPlannerEngine{
		activeEngine: "claude-code",
		lastUsed:     "claude-code",
		thinkResults: []*engine.ThinkResult{
			{Type: "info_request", Commands: []string{"echo repo context"}},
			{Type: "ready", Summary: "Enough context collected."},
		},
		proposeResult: &engine.PlanResult{
			Summary:    "engine plan",
			Confidence: 0.88,
			Tasks: []engine.PlanTask{
				{
					ID:          "task-1",
					Title:       "Engine task",
					Description: "Generated by engine",
					BranchName:  "feature/engine-task",
				},
			},
		},
	}
	mockRecon := &mockPlannerRecon{
		runDefaultOutput: "repo snapshot",
		runOutput:        "command output",
	}
	planner.SetEngine(mockEngine)
	planner.SetRecon(mockRecon)

	result, err := planner.CreatePlan(context.Background(), "Implement the engine flow command with a new config endpoint", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if result == nil || result.Plan == nil {
		t.Fatal("expected non-nil plan result")
	}
	if result.Engine != "claude-code" {
		t.Fatalf("Engine = %q, want %q", result.Engine, "claude-code")
	}
	if mockRecon.runDefaultCalls != 1 {
		t.Fatalf("RunDefault calls = %d, want 1", mockRecon.runDefaultCalls)
	}
	if mockRecon.runCalls != 1 {
		t.Fatalf("Run calls = %d, want 1", mockRecon.runCalls)
	}
	if mockEngine.thinkCalls() != 2 {
		t.Fatalf("Think calls = %d, want 2", mockEngine.thinkCalls())
	}
	if mockEngine.proposeCalls() != 1 {
		t.Fatalf("Propose calls = %d, want 1", mockEngine.proposeCalls())
	}
	if got := mockEngine.thinkRequest(0).ReconData; got != "repo snapshot" {
		t.Fatalf("first ReconData = %q, want %q", got, "repo snapshot")
	}
	if got := mockEngine.thinkRequest(1).Response; got != "command output" {
		t.Fatalf("second Response = %q, want %q", got, "command output")
	}
}

func TestCreatePlanViaEngineQuestionReturnsNeedsInputError(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	planner := NewWithDeps(&mockPlannerGLM{}, nil, newMockPlannerLauncher(true), store, "prompts", nil)
	planner.SetEngine(&mockPlannerEngine{
		activeEngine: "claude-code",
		lastUsed:     "claude-code",
		thinkResults: []*engine.ThinkResult{
			{Type: "question", Question: "Need deployment target?"},
		},
	})

	result, err := planner.CreatePlan(context.Background(), "Create a new command for operator question handling in the module", "flux")
	if err == nil {
		t.Fatal("CreatePlan error = nil, want error")
	}
	if result != nil {
		t.Fatalf("CreatePlan result = %#v, want nil", result)
	}
	if !strings.Contains(err.Error(), "engine needs input: Need deployment target?") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreatePlanViaEngineStopsAfterFiveIterations(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	planner := NewWithDeps(&mockPlannerGLM{}, nil, newMockPlannerLauncher(true), store, "prompts", nil)
	mockEngine := &mockPlannerEngine{
		activeEngine: "claude-code",
		lastUsed:     "claude-code",
		thinkResults: []*engine.ThinkResult{
			{Type: "info_request", Commands: []string{"echo 1"}},
			{Type: "info_request", Commands: []string{"echo 2"}},
			{Type: "info_request", Commands: []string{"echo 3"}},
			{Type: "info_request", Commands: []string{"echo 4"}},
			{Type: "info_request", Commands: []string{"echo 5"}},
		},
		proposeResult: &engine.PlanResult{
			Confidence: 0.8,
			Tasks: []engine.PlanTask{
				{ID: "task-1", Title: "Engine task", Description: "desc", BranchName: "engine-task"},
			},
		},
	}
	planner.SetEngine(mockEngine)
	planner.SetRecon(&mockPlannerRecon{runOutput: "context"})

	result, err := planner.CreatePlan(context.Background(), "Add a new command for long think loop processing in the module", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil plan result")
	}
	if mockEngine.thinkCalls() != 5 {
		t.Fatalf("Think calls = %d, want 5", mockEngine.thinkCalls())
	}
	if got := mockEngine.lastProposeReq().ThinkingSummary; got != "Max thinking iterations reached. Proceeding with available context." {
		t.Fatalf("ThinkingSummary = %q, want max-iterations summary", got)
	}
}

func TestCreatePlanUsesLastUsedEngineNameAfterFallback(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	planner := NewWithDeps(&mockPlannerGLM{}, nil, newMockPlannerLauncher(true), store, "prompts", nil)
	planner.SetEngine(&mockPlannerEngine{
		activeEngine: "claude-code",
		lastUsed:     "glm",
		thinkResults: []*engine.ThinkResult{
			{Type: "ready", Summary: "fallback generated"},
		},
		proposeResult: &engine.PlanResult{
			Confidence: 0.84,
			Tasks: []engine.PlanTask{
				{ID: "task-1", Title: "Fallback task", Description: "desc", BranchName: "fallback-task"},
			},
		},
	})

	result, err := planner.CreatePlan(context.Background(), "Implement the fallback engine command for the config module test", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if result.Engine != "glm" {
		t.Fatalf("Engine = %q, want %q", result.Engine, "glm")
	}
}

func TestRebuildPlanUsesEngineRebuild(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	glm := &mockPlannerGLM{
		plans: []*llm.TaskPlan{
			{
				Confidence: 0.82,
				Tasks: []llm.Task{
					{ID: "task-1", Title: "Initial task", Description: "initial", BranchName: "initial"},
				},
			},
		},
	}

	planner := NewWithDeps(glm, nil, newMockPlannerLauncher(true), store, "prompts", nil)
	initial, err := planner.CreatePlan(context.Background(), "Create the initial plan command for the test module config", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}

	mockEngine := &mockPlannerEngine{
		activeEngine: "claude-code",
		lastUsed:     "claude-code",
		rebuildResult: &engine.PlanResult{
			Confidence: 0.9,
			Tasks: []engine.PlanTask{
				{ID: "task-2", Title: "Rebuilt task", Description: "rebuilt", BranchName: "rebuilt"},
			},
		},
	}
	planner.SetEngine(mockEngine)
	planner.SetRecon(&mockPlannerRecon{runDefaultOutput: "repo snapshot"})

	rebuilt, err := planner.RebuildPlan(context.Background(), initial.PlanID, "Add more detail")
	if err != nil {
		t.Fatalf("RebuildPlan returned error: %v", err)
	}
	if rebuilt == nil || rebuilt.Plan == nil {
		t.Fatal("expected rebuilt plan")
	}
	if mockEngine.rebuildCalls() != 1 {
		t.Fatalf("Rebuild calls = %d, want 1", mockEngine.rebuildCalls())
	}
	if got := mockEngine.lastRebuildReq().Feedback; got != "Add more detail" {
		t.Fatalf("Feedback = %q, want %q", got, "Add more detail")
	}
}

type mockPlannerGLM struct {
	mu    sync.Mutex
	plans []*llm.TaskPlan
	idx   int
	count int
}

type mockPlannerEngine struct {
	mu sync.Mutex

	activeEngine string
	lastUsed     string

	thinkResults []*engine.ThinkResult
	thinkErr     error
	thinkReqs    []engine.ThinkRequest

	proposeResult *engine.PlanResult
	proposeErr    error
	proposeReqs   []engine.ProposeRequest

	rebuildResult *engine.PlanResult
	rebuildErr    error
	rebuildReqs   []engine.RebuildRequest
}

func (m *mockPlannerEngine) Think(ctx context.Context, req engine.ThinkRequest) (*engine.ThinkResult, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	m.thinkReqs = append(m.thinkReqs, req)
	if m.thinkErr != nil {
		return nil, m.thinkErr
	}

	if len(m.thinkResults) == 0 {
		return &engine.ThinkResult{Type: "ready", Summary: "ready"}, nil
	}

	idx := len(m.thinkReqs) - 1
	if idx >= len(m.thinkResults) {
		idx = len(m.thinkResults) - 1
	}

	result := *m.thinkResults[idx]
	if result.Commands != nil {
		result.Commands = append([]string(nil), result.Commands...)
	}
	if result.WebResearch != nil {
		result.WebResearch = append([]engine.WebFinding(nil), result.WebResearch...)
	}
	return &result, nil
}

func (m *mockPlannerEngine) Propose(ctx context.Context, req engine.ProposeRequest) (*engine.PlanResult, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	m.proposeReqs = append(m.proposeReqs, req)
	if m.proposeErr != nil {
		return nil, m.proposeErr
	}
	if m.proposeResult == nil {
		return &engine.PlanResult{}, nil
	}

	return cloneEnginePlanResult(m.proposeResult), nil
}

func (m *mockPlannerEngine) Rebuild(ctx context.Context, req engine.RebuildRequest) (*engine.PlanResult, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	m.rebuildReqs = append(m.rebuildReqs, req)
	if m.rebuildErr != nil {
		return nil, m.rebuildErr
	}
	if m.rebuildResult == nil {
		return &engine.PlanResult{}, nil
	}

	return cloneEnginePlanResult(m.rebuildResult), nil
}

func (m *mockPlannerEngine) ActiveEngine(context.Context) string {
	return m.activeEngine
}

func (m *mockPlannerEngine) LastUsedEngine() string {
	return m.lastUsed
}

func (m *mockPlannerEngine) thinkCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.thinkReqs)
}

func (m *mockPlannerEngine) proposeCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.proposeReqs)
}

func (m *mockPlannerEngine) rebuildCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rebuildReqs)
}

func (m *mockPlannerEngine) thinkRequest(idx int) engine.ThinkRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.thinkReqs[idx]
}

func (m *mockPlannerEngine) lastProposeReq() engine.ProposeRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proposeReqs[len(m.proposeReqs)-1]
}

func (m *mockPlannerEngine) lastRebuildReq() engine.RebuildRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rebuildReqs[len(m.rebuildReqs)-1]
}

type mockPlannerRecon struct {
	mu sync.Mutex

	runDefaultOutput string
	runOutput        string
	runDefaultCalls  int
	runCalls         int
	lastRepoPath     string
	lastCommands     []string
}

func (m *mockPlannerRecon) RunDefault(ctx context.Context, repoPath string) (*recon.Result, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	m.runDefaultCalls++
	m.lastRepoPath = repoPath
	return &recon.Result{Output: m.runDefaultOutput}, nil
}

func (m *mockPlannerRecon) Run(ctx context.Context, commands []string) (*recon.Result, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	m.runCalls++
	m.lastCommands = append([]string(nil), commands...)
	return &recon.Result{Output: m.runOutput}, nil
}

func (m *mockPlannerRecon) RunInDir(ctx context.Context, dir string, commands []string) (*recon.Result, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	m.runCalls++
	m.lastCommands = append([]string(nil), commands...)
	return &recon.Result{Output: m.runOutput}, nil
}

func cloneEnginePlanResult(in *engine.PlanResult) *engine.PlanResult {
	if in == nil {
		return nil
	}

	out := *in
	if in.Tasks != nil {
		out.Tasks = append([]engine.PlanTask(nil), in.Tasks...)
	}
	return &out
}

func (m *mockPlannerGLM) Plan(ctx context.Context, directive, agentsMD string) (*llm.TaskPlan, error) {
	_ = ctx
	_ = directive
	_ = agentsMD

	m.mu.Lock()
	defer m.mu.Unlock()
	m.count++

	if len(m.plans) == 0 {
		return nil, fmt.Errorf("no plan configured")
	}

	if m.idx >= len(m.plans) {
		last := m.plans[len(m.plans)-1]
		cloned := *last
		return &cloned, nil
	}

	plan := m.plans[m.idx]
	m.idx++
	cloned := *plan
	cloned.Tasks = append([]llm.Task(nil), plan.Tasks...)
	cloned.Questions = append([]string(nil), plan.Questions...)
	return &cloned, nil
}

func (m *mockPlannerGLM) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

type mockConsultant struct {
	name      string
	available bool
	opinion   *llm.Opinion
	err       error

	mu        sync.Mutex
	callCount int
}

func (m *mockConsultant) Consult(ctx context.Context, consultationType string, context string, question string) (*llm.Opinion, error) {
	_ = ctx
	_ = consultationType
	_ = context
	_ = question

	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}
	return m.opinion, nil
}

func (m *mockConsultant) GetName() string {
	return m.name
}

func (m *mockConsultant) GetBudgetRemaining() float64 {
	if m.available {
		return 1
	}
	return 0
}

func (m *mockConsultant) IsAvailable() bool {
	return m.available
}

func (m *mockConsultant) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

type mockPlannerLauncher struct {
	autoComplete     bool
	doneCh           chan launcher.Session
	completionStatus string
	completionError  string

	mu       sync.Mutex
	launched []string
	seq      int
}

type mockPlanEvaluator struct {
	launcher *mockPlannerLauncher

	mu        sync.Mutex
	callCount int
}

func (m *mockPlanEvaluator) EvaluateWorkerOutput(ctx context.Context, session launcher.Session) (*evaluator.EvalResult, error) {
	_ = ctx

	m.mu.Lock()
	m.callCount++
	call := m.callCount
	m.mu.Unlock()

	if call == 1 {
		nextSessionID := "session-retry-1"
		go func() {
			time.Sleep(20 * time.Millisecond)
			m.launcher.doneCh <- launcher.Session{
				SessionID: nextSessionID,
				ProjectID: session.ProjectID,
				Branch:    session.Branch + "-r1",
				Status:    state.WorkerStatusCompleted,
				WorkerID:  session.WorkerID + 100,
			}
		}()
		return &evaluator.EvalResult{
			Action:        "iterate",
			RetryCount:    1,
			NextSessionID: nextSessionID,
		}, nil
	}

	return &evaluator.EvalResult{
		Action:     "accept",
		RetryCount: 1,
	}, nil
}

func (m *mockPlanEvaluator) HandleWorkerCompletionDetailed(ctx context.Context, sessionID string) (*evaluator.CompletionResult, error) {
	_ = ctx
	_ = sessionID
	return &evaluator.CompletionResult{Action: "escalate"}, nil
}

func (m *mockPlanEvaluator) SetTaskChecklists(taskID int64, checklists evaluator.TaskChecklists) {}

func (m *mockPlanEvaluator) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func newMockPlannerLauncher(autoComplete bool) *mockPlannerLauncher {
	return &mockPlannerLauncher{
		autoComplete: autoComplete,
		doneCh:       make(chan launcher.Session, 64),
		launched:     make([]string, 0),
	}
}

func (m *mockPlannerLauncher) LaunchWorker(ctx context.Context, task launcher.Task, agentsMD string, cache string) (*launcher.Session, error) {
	_ = ctx
	_ = agentsMD
	_ = cache

	m.mu.Lock()
	m.seq++
	sessionID := fmt.Sprintf("session-%d", m.seq)
	workerID := int64(m.seq)
	m.launched = append(m.launched, task.ID)
	m.mu.Unlock()

	session := &launcher.Session{
		SessionID: sessionID,
		ProjectID: task.ProjectID,
		Branch:    task.BranchName,
		Status:    state.WorkerStatusRunning,
		WorkerID:  workerID,
	}

	if m.autoComplete {
		go func(s launcher.Session) {
			time.Sleep(20 * time.Millisecond)
			if strings.TrimSpace(m.completionStatus) != "" {
				s.Status = m.completionStatus
			} else {
				s.Status = state.WorkerStatusCompleted
			}
			s.Error = m.completionError
			m.doneCh <- s
		}(*session)
	}

	return session, nil
}

type mockPlannerNotifier struct {
	mu             sync.Mutex
	completedTasks []string
	failedTasks    []string
}

func (m *mockPlannerNotifier) NotifyNeedsInput(ctx context.Context, projectID, question, approvalID string) error {
	_ = ctx
	_ = projectID
	_ = question
	_ = approvalID
	return nil
}

func (m *mockPlannerNotifier) NotifyNeedsInputWithChecks(ctx context.Context, projectID, taskTitle, approvalID string, checks []checklist.CheckResult) error {
	_ = ctx
	_ = projectID
	_ = taskTitle
	_ = approvalID
	_ = checks
	return nil
}

func (m *mockPlannerNotifier) NotifyWorkerFailed(ctx context.Context, projectID, taskTitle, errMsg string) error {
	_ = ctx
	_ = projectID
	_ = errMsg
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failedTasks = append(m.failedTasks, taskTitle)
	return nil
}

func (m *mockPlannerNotifier) NotifyTaskCompleted(ctx context.Context, projectID, taskTitle string) error {
	_ = ctx
	_ = projectID
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completedTasks = append(m.completedTasks, taskTitle)
	return nil
}

func (m *mockPlannerNotifier) NotifyProgress(ctx context.Context, project, taskID, stage, detail string) error {
	_ = ctx
	_ = project
	_ = taskID
	_ = stage
	_ = detail
	return nil
}

func (m *mockPlannerNotifier) completedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.completedTasks)
}

func (m *mockPlannerNotifier) failedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.failedTasks)
}

func (m *mockPlannerLauncher) WorkerDone() <-chan launcher.Session {
	return m.doneCh
}

func (m *mockPlannerLauncher) launchOrder() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.launched...)
}

// mockEscalatingEvaluator always returns "escalate" — simulates all checks failing.
type mockEscalatingEvaluator struct{}

func (m *mockEscalatingEvaluator) EvaluateWorkerOutput(_ context.Context, _ launcher.Session) (*evaluator.EvalResult, error) {
	return &evaluator.EvalResult{
		Action:  "escalate",
		Verdict: "escalate",
	}, nil
}

func (m *mockEscalatingEvaluator) HandleWorkerCompletionDetailed(_ context.Context, _ string) (*evaluator.CompletionResult, error) {
	return &evaluator.CompletionResult{Action: "escalate"}, nil
}

func (m *mockEscalatingEvaluator) SetTaskChecklists(_ int64, _ evaluator.TaskChecklists) {}

func TestExecutePlanEscalatedTasksReturnError(t *testing.T) {
	store, cleanup := setupPlannerTestEnv(t)
	defer cleanup()

	glm := &mockPlannerGLM{
		plans: []*llm.TaskPlan{
			{
				Confidence: 0.9,
				Tasks:      []llm.Task{{ID: "task-1", Title: "Build", Description: "build it", BranchName: "build"}},
			},
		},
	}

	launch := newMockPlannerLauncher(true)
	launch.completionStatus = state.WorkerStatusCompleted

	planner := NewWithDeps(glm, nil, launch, store, "prompts", nil)
	planner.SetEvaluator(&mockEscalatingEvaluator{})

	planResult, err := planner.CreatePlan(context.Background(), "Add a command to build the project with test config support", "flux")
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}

	err = planner.ExecutePlan(context.Background(), planResult.PlanID)
	if err == nil {
		t.Fatal("expected ExecutePlan to return error for escalated/blocked tasks, got nil")
	}
	if !strings.Contains(err.Error(), "held") && !strings.Contains(err.Error(), "escalated") {
		t.Fatalf("expected error about held/escalated tasks, got: %v", err)
	}
}

func setupPlannerTestEnv(t *testing.T) (*state.Store, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	if err := os.MkdirAll(filepath.Join("agents"), 0o755); err != nil {
		t.Fatalf("create agents dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join("sessions", "cache"), 0o755); err != nil {
		t.Fatalf("create sessions/cache dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join("agents", "flux.md"), []byte("# Flux\nRules"), 0o644); err != nil {
		t.Fatalf("write agents file: %v", err)
	}
	if err := os.WriteFile(filepath.Join("sessions", "cache", "flux-previous.md"), []byte("Previous cache"), 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	store, err := state.New(filepath.Join(tmpDir, "state.db"))
	if err != nil {
		t.Fatalf("state.New failed: %v", err)
	}

	if _, err := store.CreateProject(context.Background(), state.Project{Name: "flux", Status: state.ProjectStatusWorking}); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	return store, func() {
		_ = store.Close()
	}
}
