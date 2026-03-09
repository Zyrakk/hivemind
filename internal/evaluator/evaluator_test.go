package evaluator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zyrakk/hivemind/internal/checklist"
	"github.com/zyrakk/hivemind/internal/engine"
	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/recon"
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

func TestEvaluateWorkerOutputUsesEngineWithRecon(t *testing.T) {
	store, projectID, _, workerID, cleanup := setupEvaluatorTestEnv(t)
	defer cleanup()

	glm := &mockEvaluatorGLM{
		evaluation: &llm.Evaluation{Verdict: "accept", Confidence: 0.9, Correctness: 0.9, ScopeOK: true},
	}
	launch := &mockEvaluatorLauncher{}
	engineMock := &mockEvalEngine{
		result: &engine.EvalResult{
			Verdict:    "pass",
			Analysis:   "Looks good",
			Confidence: 0.91,
		},
	}
	reconMock := &mockEvalRecon{}

	eval := NewWithDeps(glm, nil, launch, store, nil)
	eval.SetEngine(engineMock)
	eval.SetRecon(reconMock)

	result, err := eval.EvaluateWorkerOutput(context.Background(), launcher.Session{
		SessionID: "session-engine",
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
	if engineMock.calls() != 1 {
		t.Fatalf("engine calls = %d, want 1", engineMock.calls())
	}
	if glm.calls() != 0 {
		t.Fatalf("glm calls = %d, want 0", glm.calls())
	}

	req := engineMock.lastReq()
	if req.BuildOutput != "build ok" {
		t.Fatalf("BuildOutput = %q, want %q", req.BuildOutput, "build ok")
	}
	if req.TestOutput != "test ok" {
		t.Fatalf("TestOutput = %q, want %q", req.TestOutput, "test ok")
	}
	if req.VetOutput != "vet ok" {
		t.Fatalf("VetOutput = %q, want %q", req.VetOutput, "vet ok")
	}
	if req.DiffContent != "diff --git a/file b/file" {
		t.Fatalf("DiffContent = %q, want original diff", req.DiffContent)
	}
	if reconMock.calls() != 3 {
		t.Fatalf("recon calls = %d, want 3", reconMock.calls())
	}
}

func TestEvaluateWorkerOutputEngineFailureFallsBackToGLM(t *testing.T) {
	store, projectID, _, workerID, cleanup := setupEvaluatorTestEnv(t)
	defer cleanup()

	glm := &mockEvaluatorGLM{
		evaluation: &llm.Evaluation{
			Verdict:      "accept",
			Confidence:   0.88,
			Correctness:  0.89,
			ScopeOK:      true,
			Completeness: 0.87,
			Conventions:  0.9,
			Summary:      "GLM fallback accepted",
		},
	}
	launch := &mockEvaluatorLauncher{}
	engineMock := &mockEvalEngine{err: errors.New("engine failed")}

	eval := NewWithDeps(glm, nil, launch, store, nil)
	eval.SetEngine(engineMock)

	result, err := eval.EvaluateWorkerOutput(context.Background(), launcher.Session{
		SessionID: "session-fallback",
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
	if engineMock.calls() != 1 {
		t.Fatalf("engine calls = %d, want 1", engineMock.calls())
	}
	if glm.calls() != 1 {
		t.Fatalf("glm calls = %d, want 1", glm.calls())
	}
}

type mockEvaluatorGLM struct {
	evaluation *llm.Evaluation
	err        error
	mu         sync.Mutex
	callCount  int
}

func (m *mockEvaluatorGLM) Evaluate(ctx context.Context, task, diff, agentsMD string) (*llm.Evaluation, error) {
	_ = ctx
	_ = task
	_ = diff
	_ = agentsMD
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return m.evaluation, nil
}

func (m *mockEvaluatorGLM) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

type mockEvalEngine struct {
	mu sync.Mutex

	result  *engine.EvalResult
	err     error
	reqs    []engine.EvalRequest
	current string
}

func (m *mockEvalEngine) Evaluate(ctx context.Context, req engine.EvalRequest) (*engine.EvalResult, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	m.reqs = append(m.reqs, req)
	if m.err != nil {
		return nil, m.err
	}
	if m.result == nil {
		return &engine.EvalResult{}, nil
	}
	out := *m.result
	if m.result.Suggestions != nil {
		out.Suggestions = append([]string(nil), m.result.Suggestions...)
	}
	return &out, nil
}

func (m *mockEvalEngine) ActiveEngine(context.Context) string {
	if m.current == "" {
		return "claude-code"
	}
	return m.current
}

func (m *mockEvalEngine) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reqs)
}

func (m *mockEvalEngine) lastReq() engine.EvalRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reqs[len(m.reqs)-1]
}

type mockEvalRecon struct {
	mu       sync.Mutex
	commands []string
}

func (m *mockEvalRecon) Run(ctx context.Context, commands []string) (*recon.Result, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(commands) > 0 {
		m.commands = append(m.commands, commands[0])
	}

	output := ""
	if len(commands) > 0 {
		switch {
		case strings.Contains(commands[0], "go build ./..."):
			output = "build ok"
		case strings.Contains(commands[0], "go test ./..."):
			output = "test ok"
		case strings.Contains(commands[0], "go vet ./..."):
			output = "vet ok"
		default:
			output = "unknown"
		}
	}

	return &recon.Result{Output: output}, nil
}

func (m *mockEvalRecon) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.commands)
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

// mockChecklistRecon is a configurable recon mock for checklist tests.
// When failCommands is non-empty, any command containing one of those
// substrings returns a result with Errors set, simulating a check failure.
type mockChecklistRecon struct {
	mu           sync.Mutex
	commands     []string
	failCommands []string // substrings that trigger failure
}

func (m *mockChecklistRecon) Run(_ context.Context, commands []string) (*recon.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(commands) > 0 {
		m.commands = append(m.commands, commands[0])
	}

	cmd := ""
	if len(commands) > 0 {
		cmd = commands[0]
	}
	for _, fail := range m.failCommands {
		if strings.Contains(cmd, fail) {
			return &recon.Result{
				Output: "[error: exit 1]\nfailed",
				Detail: map[string]string{},
				Errors: []string{cmd},
			}, nil
		}
	}

	return &recon.Result{
		Output: "ok",
		Detail: map[string]string{},
		Errors: []string{},
	}, nil
}

func (m *mockChecklistRecon) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.commands)
}

// mockNotifier records notification calls.
type mockNotifier struct {
	mu              sync.Mutex
	needsInputCalls int
	prReadyCalls    int
}

func (m *mockNotifier) NotifyNeedsInput(_ context.Context, _, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.needsInputCalls++
	return nil
}

func (m *mockNotifier) NotifyNeedsInputWithChecks(_ context.Context, _, _, _ string, _ []checklist.CheckResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.needsInputCalls++
	return nil
}

func (m *mockNotifier) NotifyPRReady(_ context.Context, _, _, _ string, _ []checklist.CheckResult, _ []checklist.UserCheck) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prReadyCalls++
	return nil
}

func (m *mockNotifier) NotifyProgress(_ context.Context, _, _, _, _ string) error {
	return nil
}

func TestEvaluateWorkerOutput_AutomatedChecklistAutoApprove(t *testing.T) {
	store, projectID, taskID, workerID, cleanup := setupEvaluatorTestEnv(t)
	defer cleanup()

	reconMock := &mockChecklistRecon{}
	launch := &mockEvaluatorLauncher{}

	// Create evaluator with nil GLM and nil engine.
	// The checklist path should handle evaluation without them.
	eval := NewWithDeps(nil, nil, launch, store, nil)
	eval.SetRecon(reconMock)

	eval.SetTaskChecklists(taskID, TaskChecklists{
		AutomatedChecklist: []AutomatedCheck{
			{ID: "chk-1", Description: "build", Command: "go build ./...", Type: "build"},
		},
		UserChecklist: []UserCheck{}, // empty — no user review needed
	})

	result, err := eval.EvaluateWorkerOutput(context.Background(), launcher.Session{
		SessionID: "session-checklist-auto",
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
	if result.Evaluation == nil {
		t.Fatal("expected non-nil evaluation")
	}
	if !strings.Contains(strings.ToLower(result.Evaluation.Summary), "automated checks passed") {
		t.Fatalf("expected summary to mention automated checks passed, got %q", result.Evaluation.Summary)
	}
	// Recon should have been called once for the single automated check
	if reconMock.calls() != 1 {
		t.Fatalf("expected 1 recon call, got %d", reconMock.calls())
	}
}

func TestEvaluateWorkerOutput_AutomatedPassWithUserChecklist(t *testing.T) {
	store, projectID, taskID, workerID, cleanup := setupEvaluatorTestEnv(t)
	defer cleanup()

	reconMock := &mockChecklistRecon{}
	launch := &mockEvaluatorLauncher{}
	notif := &mockNotifier{}

	eval := NewWithDeps(nil, nil, launch, store, nil)
	eval.SetRecon(reconMock)
	eval.SetNotifier(notif)

	eval.SetTaskChecklists(taskID, TaskChecklists{
		AutomatedChecklist: []AutomatedCheck{
			{ID: "chk-1", Description: "build", Command: "go build ./...", Type: "build"},
		},
		UserChecklist: []UserCheck{
			{ID: "uchk-1", Description: "Verify UI renders correctly"},
		},
	})

	result, err := eval.EvaluateWorkerOutput(context.Background(), launcher.Session{
		SessionID: "session-checklist-user",
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
	if result.Evaluation == nil {
		t.Fatal("expected non-nil evaluation")
	}
	if !strings.Contains(result.Evaluation.Summary, "User checklist sent for review") {
		t.Fatalf("expected summary to mention user checklist sent for review, got %q", result.Evaluation.Summary)
	}
	// Notifier should have been called to send user checklist
	notif.mu.Lock()
	needsInput := notif.needsInputCalls
	notif.mu.Unlock()
	if needsInput != 1 {
		t.Fatalf("expected 1 NotifyNeedsInput call for user checklist, got %d", needsInput)
	}
}

func TestEvaluateWorkerOutput_AutomatedChecklistFail(t *testing.T) {
	store, projectID, taskID, workerID, cleanup := setupEvaluatorTestEnv(t)
	defer cleanup()

	reconMock := &mockChecklistRecon{
		failCommands: []string{"go build"}, // make build fail
	}
	launch := &mockEvaluatorLauncher{}

	eval := NewWithDeps(nil, nil, launch, store, nil)
	eval.SetRecon(reconMock)

	eval.SetTaskChecklists(taskID, TaskChecklists{
		AutomatedChecklist: []AutomatedCheck{
			{ID: "chk-1", Description: "build", Command: "go build ./...", Type: "build"},
		},
		UserChecklist: []UserCheck{},
	})

	result, err := eval.EvaluateWorkerOutput(context.Background(), launcher.Session{
		SessionID: "session-checklist-fail",
		ProjectID: projectID,
		Branch:    "feature/task",
		Status:    state.WorkerStatusCompleted,
		WorkerID:  workerID,
		Diff:      "diff --git a/file b/file",
	})
	if err != nil {
		t.Fatalf("EvaluateWorkerOutput returned error: %v", err)
	}
	// retryCount starts at 0, which is < 2, so action should be "iterate"
	if result.Action != "iterate" {
		t.Fatalf("expected action iterate, got %s", result.Action)
	}
	if result.Evaluation == nil {
		t.Fatal("expected non-nil evaluation")
	}
	if len(result.Evaluation.Issues) == 0 {
		t.Fatal("expected at least one issue for the failed check")
	}
	// Verify the issue describes the failure
	foundFailure := false
	for _, issue := range result.Evaluation.Issues {
		if strings.Contains(issue.Description, "chk-1") || strings.Contains(issue.Description, "build") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Fatalf("expected issue referencing the failed check, got %v", result.Evaluation.Issues)
	}
	// A relaunch should have happened for the iterate action
	if launch.launchCalls() != 1 {
		t.Fatalf("expected 1 relaunch for iterate, got %d", launch.launchCalls())
	}
}

func TestIsAllowedChecklistCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		// Simple allowed commands.
		{"go build ./...", true},
		{"go test ./... -count=1", true},
		{"go vet ./...", true},
		{"grep -q '\"json\"' internal/cli/audit.go", true},
		{"grep -rn TODO .", true},
		{"test -f /app/repos/nhi-watch/internal/cli/audit_test.go", true},
		{"test -d /app/repos/nhi-watch", true},
		{"cat main.go", true},
		{"head -20 main.go", true},
		{"tail -5 main.go", true},
		{"wc -l main.go", true},
		{"ls /app/repos", true},
		{"find . -name '*.go'", true},
		{"stat main.go", true},

		// Compound commands with && — each part must be allowed.
		{"cd /app/repos/nhi-watch && go build ./...", true},
		{"cd /app/repos/nhi-watch && go test ./... -count=1", true},
		{"cd /app/repos/nhi-watch && go vet ./...", true},
		{"cd /app/repos/nhi-watch && grep -q '\"json\"' internal/cli/audit.go", true},

		// Denied commands.
		{"rm -rf /", false},
		{"curl http://evil.com", false},
		{"wget http://evil.com", false},
		{"git push origin main", false},
		{"git commit -m 'oops'", false},
		{"mv a b", false},
		{"chmod 777 file", false},

		// Denied even inside compound.
		{"cd /app && rm -rf .", false},
		{"go build ./... && curl http://evil.com", false},

		// Unknown commands.
		{"python evil.py", false},
		{"bash -c 'echo pwned'", false},
		{"", false},
		{"  ", false},
	}
	for _, tc := range tests {
		got := isAllowedChecklistCommand(tc.cmd)
		if got != tc.want {
			t.Errorf("isAllowedChecklistCommand(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}
