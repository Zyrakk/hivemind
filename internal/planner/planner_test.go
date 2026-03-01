package planner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zyrakk/hivemind/internal/evaluator"
	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
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
	result, err := planner.CreatePlan(context.Background(), "Implement planner module", "flux")
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
	result, err := planner.CreatePlan(context.Background(), "Implement risky feature", "flux")
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
	result, err := planner.CreatePlan(context.Background(), "Plan with ambiguity", "flux")
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
	planResult, err := planner.CreatePlan(context.Background(), "Run dependent tasks", "flux")
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
	if order[0] != "task-a" || order[1] != "task-b" {
		t.Fatalf("expected launch order [task-a task-b], got %v", order)
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

	planResult, err := planner.CreatePlan(context.Background(), "Run full flow", "flux")
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

type mockPlannerGLM struct {
	mu    sync.Mutex
	plans []*llm.TaskPlan
	idx   int
	count int
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
	autoComplete bool
	doneCh       chan launcher.Session

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
			s.Status = state.WorkerStatusCompleted
			m.doneCh <- s
		}(*session)
	}

	return session, nil
}

func (m *mockPlannerLauncher) WorkerDone() <-chan launcher.Session {
	return m.doneCh
}

func (m *mockPlannerLauncher) launchOrder() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.launched...)
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
