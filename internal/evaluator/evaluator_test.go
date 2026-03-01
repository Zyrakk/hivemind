package evaluator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/state"
)

func TestEvaluateWorkerOutputAccept(t *testing.T) {
	store, projectID, taskID, workerID, cleanup := setupEvaluatorTestEnv(t)
	defer cleanup()

	glm := &mockEvaluatorGLM{
		evaluation: &llm.Evaluation{
			Verdict:      "accept",
			Confidence:   0.91,
			Correctness:  0.92,
			ScopeOK:      true,
			Completeness: 0.89,
			Conventions:  0.9,
			Summary:      "Looks good",
		},
	}

	launch := &mockEvaluatorLauncher{}
	eval := NewWithDeps(glm, nil, launch, store, nil)

	result, err := eval.EvaluateWorkerOutput(context.Background(), launcher.Session{
		SessionID: "session-1",
		ProjectID: projectID,
		Branch:    "feature/task",
		Status:    state.WorkerStatusCompleted,
		WorkerID:  workerID,
		Diff:      "diff --git a/file b/file",
	})
	if err != nil {
		t.Fatalf("EvaluateWorkerOutput returned error: %v", err)
	}
	if result.Action != "accept" {
		t.Fatalf("expected action accept, got %s", result.Action)
	}

	detail, err := store.GetProjectDetail(context.Background(), "flux")
	if err != nil {
		t.Fatalf("GetProjectDetail returned error: %v", err)
	}

	status := ""
	for _, task := range detail.Tasks {
		if task.ID == taskID {
			status = task.Status
			break
		}
	}
	if status != state.TaskStatusCompleted {
		t.Fatalf("expected task %d completed, got %s", taskID, status)
	}
}

func TestEvaluateWorkerOutputIterateRelaunchesWorker(t *testing.T) {
	store, projectID, taskID, workerID, cleanup := setupEvaluatorTestEnv(t)
	defer cleanup()

	glm := &mockEvaluatorGLM{
		evaluation: &llm.Evaluation{
			Verdict:      "iterate",
			Confidence:   0.88,
			Correctness:  0.4,
			ScopeOK:      false,
			Completeness: 0.6,
			Conventions:  0.8,
			Summary:      "Needs another pass",
			Issues: []llm.Issue{
				{Severity: "high", Description: "missing checks", Suggestion: "add validation"},
			},
		},
	}

	launch := &mockEvaluatorLauncher{}
	eval := NewWithDeps(glm, nil, launch, store, nil)

	result, err := eval.EvaluateWorkerOutput(context.Background(), launcher.Session{
		SessionID: "session-2",
		ProjectID: projectID,
		Branch:    "feature/task",
		Status:    state.WorkerStatusCompleted,
		WorkerID:  workerID,
		Diff:      "diff --git a/file b/file",
	})
	if err != nil {
		t.Fatalf("EvaluateWorkerOutput returned error: %v", err)
	}
	if result.Action != "iterate" {
		t.Fatalf("expected action iterate, got %s", result.Action)
	}
	if result.RetryCount != 1 {
		t.Fatalf("expected retry count 1, got %d", result.RetryCount)
	}
	if result.NextSessionID == "" {
		t.Fatal("expected next session id for iterate action")
	}
	if launch.launchCalls() != 1 {
		t.Fatalf("expected one relaunch, got %d", launch.launchCalls())
	}

	detail, err := store.GetProjectDetail(context.Background(), "flux")
	if err != nil {
		t.Fatalf("GetProjectDetail returned error: %v", err)
	}

	found := false
	for _, task := range detail.Tasks {
		if task.ID != taskID {
			continue
		}
		found = true
		if task.Status != state.TaskStatusInProgress {
			t.Fatalf("expected task in_progress after iterate, got %s", task.Status)
		}
		if task.AssignedWorkerID == nil {
			t.Fatalf("expected assigned worker id to remain set after iterate")
		}
	}
	if !found {
		t.Fatalf("task %d not found", taskID)
	}
}

func TestEvaluateWorkerOutputEscalateMarksNeedsInputAndCreatesEvent(t *testing.T) {
	store, projectID, taskID, workerID, cleanup := setupEvaluatorTestEnv(t)
	defer cleanup()

	glm := &mockEvaluatorGLM{
		evaluation: &llm.Evaluation{
			Verdict:      "escalate",
			Confidence:   0.91,
			Correctness:  0.93,
			ScopeOK:      true,
			Completeness: 0.8,
			Conventions:  0.88,
			Summary:      "Manual product decision required",
		},
	}

	launch := &mockEvaluatorLauncher{}
	eval := NewWithDeps(glm, nil, launch, store, nil)

	result, err := eval.EvaluateWorkerOutput(context.Background(), launcher.Session{
		SessionID: "session-3",
		ProjectID: projectID,
		Branch:    "feature/task",
		Status:    state.WorkerStatusCompleted,
		WorkerID:  workerID,
		Diff:      "diff --git a/file b/file",
	})
	if err != nil {
		t.Fatalf("EvaluateWorkerOutput returned error: %v", err)
	}
	if result.Action != "escalate" {
		t.Fatalf("expected action escalate, got %s", result.Action)
	}

	detail, err := store.GetProjectDetail(context.Background(), "flux")
	if err != nil {
		t.Fatalf("GetProjectDetail returned error: %v", err)
	}
	if detail.Project.Status != state.ProjectStatusNeedsInput {
		t.Fatalf("expected project status needs_input, got %s", detail.Project.Status)
	}

	var taskStatus string
	for _, task := range detail.Tasks {
		if task.ID == taskID {
			taskStatus = task.Status
			break
		}
	}
	if taskStatus != state.TaskStatusBlocked {
		t.Fatalf("expected task %d blocked, got %s", taskID, taskStatus)
	}

	foundInputNeeded := false
	for _, event := range detail.Events {
		if event.EventType == "input_needed" {
			foundInputNeeded = true
			break
		}
	}
	if !foundInputNeeded {
		t.Fatal("expected input_needed event after escalation")
	}
}

type mockEvaluatorGLM struct {
	evaluation *llm.Evaluation
	err        error
}

func (m *mockEvaluatorGLM) Evaluate(ctx context.Context, task, diff, agentsMD string) (*llm.Evaluation, error) {
	_ = ctx
	_ = task
	_ = diff
	_ = agentsMD
	if m.err != nil {
		return nil, m.err
	}
	return m.evaluation, nil
}

type mockEvaluatorLauncher struct {
	mu       sync.Mutex
	calls    int
	sessions map[string]launcher.Session
}

func (m *mockEvaluatorLauncher) LaunchWorker(ctx context.Context, task launcher.Task, agentsMD string, cache string) (*launcher.Session, error) {
	_ = ctx
	_ = task
	_ = agentsMD
	_ = cache

	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.sessions == nil {
		m.sessions = make(map[string]launcher.Session)
	}

	session := launcher.Session{
		SessionID: fmt.Sprintf("retry-%d", m.calls),
		ProjectID: task.ProjectID,
		Branch:    task.BranchName,
		Status:    state.WorkerStatusRunning,
		WorkerID:  999,
		StartedAt: time.Now().UTC(),
	}
	m.sessions[session.SessionID] = session
	return &session, nil
}

func (m *mockEvaluatorLauncher) GetSession(sessionID string) (launcher.Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[sessionID]
	return session, ok
}

func (m *mockEvaluatorLauncher) GetWorkDir() string {
	return "."
}

func (m *mockEvaluatorLauncher) launchCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func setupEvaluatorTestEnv(t *testing.T) (*state.Store, int64, int64, int64, func()) {
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
	if err := os.WriteFile(filepath.Join("agents", "flux.md"), []byte("# Flux\nConventions"), 0o644); err != nil {
		t.Fatalf("write agents file: %v", err)
	}

	store, err := state.New(filepath.Join(tmpDir, "state.db"))
	if err != nil {
		t.Fatalf("state.New failed: %v", err)
	}

	projectID, err := store.CreateProject(context.Background(), state.Project{Name: "flux", Status: state.ProjectStatusWorking})
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	workerID, err := store.CreateWorker(context.Background(), state.Worker{
		ProjectID:       projectID,
		SessionID:       "session-1",
		TaskDescription: "Implement task",
		Branch:          "feature/task",
		Status:          state.WorkerStatusCompleted,
	})
	if err != nil {
		t.Fatalf("CreateWorker failed: %v", err)
	}

	taskID, err := store.CreateTask(context.Background(), state.Task{
		ProjectID:   projectID,
		Title:       "Implement task",
		Description: "Do work",
		Status:      state.TaskStatusInProgress,
		Priority:    3,
		DependsOn:   "[]",
	})
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	workerRef := workerID
	inProgress := state.TaskStatusInProgress
	if err := store.UpdateTask(context.Background(), taskID, state.TaskUpdate{
		Status:              &inProgress,
		AssignedWorkerID:    &workerRef,
		AssignedWorkerIDSet: true,
	}); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	return store, projectID, taskID, workerID, func() {
		_ = store.Close()
	}
}
