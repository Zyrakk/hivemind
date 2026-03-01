package launcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zyrakk/hivemind/internal/state"
)

func TestBuildWorkerContext(t *testing.T) {
	contextText := BuildWorkerContext("# AGENTS", "Implement feature X", "cached notes", "session-123")
	if !strings.Contains(contextText, "Sesion session-123") {
		t.Fatalf("expected session id in context, got: %s", contextText)
	}
	if !strings.Contains(contextText, "# AGENTS") {
		t.Fatalf("expected AGENTS content in context")
	}
	if !strings.Contains(contextText, "Implement feature X") {
		t.Fatalf("expected task content in context")
	}
	if !strings.Contains(contextText, "cached notes") {
		t.Fatalf("expected cache content in context")
	}

	fallback := BuildWorkerContext("", "", "", "session-456")
	if !strings.Contains(fallback, "AGENTS.md no disponible") {
		t.Fatalf("expected agents fallback text")
	}
	if !strings.Contains(fallback, "No hay cache previo") {
		t.Fatalf("expected cache fallback text")
	}
}

func TestLaunchWorkerAndMonitorCompletion(t *testing.T) {
	t.Setenv("MOCK_CODEX_SLEEP", "0.1")
	t.Setenv("MOCK_CODEX_EXIT", "0")

	workDir := t.TempDir()
	initGitRepo(t, workDir)

	mockCodex := writeMockCodex(t, workDir)
	store, projectID := newTestStore(t)
	defer func() { _ = store.Close() }()

	launcher := NewWithStore(store, LauncherConfig{
		CodexBinary:          mockCodex,
		ApprovalMode:         "full-auto",
		MaxConcurrentWorkers: 5,
		WorkDir:              workDir,
		BranchPrefix:         "feature/",
		PollInterval:         20 * time.Millisecond,
		DisableGitPull:       true,
		DirectExecution:      true,
	})

	task := Task{
		ProjectID:     projectID,
		ID:            "task-1",
		Title:         "Implement launcher",
		Description:   "Create a worker and update files",
		BranchName:    "launcher-worker",
		FilesAffected: []string{"internal/launcher/launcher.go"},
	}

	session, err := launcher.LaunchWorker(context.Background(), task, "# AGENTS\nRules", "prior cache")
	if err != nil {
		t.Fatalf("launch worker: %v", err)
	}
	if session.Status != state.WorkerStatusRunning {
		t.Fatalf("expected running status, got %s", session.Status)
	}

	result := waitForResult(t, launcher.WorkerDone(), session.SessionID, 3*time.Second)
	if result.Status != state.WorkerStatusCompleted {
		t.Fatalf("expected completed worker, got status=%s error=%s", result.Status, result.Error)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if strings.TrimSpace(result.Diff) == "" {
		t.Fatalf("expected non-empty git diff")
	}

	contextPath := filepath.Join(workDir, "sessions", "cache", session.SessionID+"-context.md")
	if _, err := os.Stat(contextPath); err != nil {
		t.Fatalf("expected context file at %s: %v", contextPath, err)
	}

	active := launcher.GetActiveWorkers()
	if len(active) != 0 {
		t.Fatalf("expected no active workers, got %d", len(active))
	}

	detail, err := store.GetProjectDetail(context.Background(), strconv.FormatInt(projectID, 10))
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}
	if len(detail.Workers) != 1 {
		t.Fatalf("expected 1 worker in DB, got %d", len(detail.Workers))
	}
	if detail.Workers[0].Status != state.WorkerStatusCompleted {
		t.Fatalf("expected completed worker in DB, got %s", detail.Workers[0].Status)
	}
}

func TestMonitorTimeoutKillsWorker(t *testing.T) {
	t.Setenv("MOCK_CODEX_SLEEP", "2")
	t.Setenv("MOCK_CODEX_EXIT", "0")

	workDir := t.TempDir()
	initGitRepo(t, workDir)
	mockCodex := writeMockCodex(t, workDir)

	launcher := NewWithStore(nil, LauncherConfig{
		CodexBinary:          mockCodex,
		MaxConcurrentWorkers: 1,
		WorkDir:              workDir,
		BranchPrefix:         "feature/",
		PollInterval:         20 * time.Millisecond,
		Timeout:              150 * time.Millisecond,
		DisableGitPull:       true,
		DirectExecution:      true,
	})

	session, err := launcher.LaunchWorker(context.Background(), Task{
		Description: "Long running task",
		BranchName:  "timeout-worker",
	}, "# AGENTS", "")
	if err != nil {
		t.Fatalf("launch worker: %v", err)
	}

	result := waitForResult(t, launcher.WorkerDone(), session.SessionID, 3*time.Second)
	if result.Status != state.WorkerStatusFailed {
		t.Fatalf("expected failed status on timeout, got %s", result.Status)
	}
	if !strings.Contains(strings.ToLower(result.Error), "timeout") {
		t.Fatalf("expected timeout message, got %q", result.Error)
	}
}

func TestLaunchWorkerRespectsMaxConcurrentWorkers(t *testing.T) {
	t.Setenv("MOCK_CODEX_SLEEP", "0.8")
	t.Setenv("MOCK_CODEX_EXIT", "0")

	workDir := t.TempDir()
	initGitRepo(t, workDir)
	mockCodex := writeMockCodex(t, workDir)

	launcher := NewWithStore(nil, LauncherConfig{
		CodexBinary:          mockCodex,
		MaxConcurrentWorkers: 1,
		WorkDir:              workDir,
		BranchPrefix:         "feature/",
		PollInterval:         20 * time.Millisecond,
		Timeout:              2 * time.Second,
		DisableGitPull:       true,
		DirectExecution:      true,
	})

	first, err := launcher.LaunchWorker(context.Background(), Task{
		Description: "First worker",
		BranchName:  "first-worker",
	}, "# AGENTS", "")
	if err != nil {
		t.Fatalf("launch first worker: %v", err)
	}

	_, err = launcher.LaunchWorker(context.Background(), Task{
		Description: "Second worker",
		BranchName:  "second-worker",
	}, "# AGENTS", "")
	if !errors.Is(err, ErrMaxConcurrentWorkers) {
		t.Fatalf("expected ErrMaxConcurrentWorkers, got %v", err)
	}

	_ = waitForResult(t, launcher.WorkerDone(), first.SessionID, 4*time.Second)
}

func writeMockCodex(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "mock-codex.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail

context_file=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --approval-mode)
      shift 2
      ;;
    --context)
      context_file="$2"
      shift 2
      ;;
    *)
      task_desc="$1"
      shift
      ;;
  esac
done

if [[ -n "$context_file" ]]; then
  echo "context=$context_file" > .mock-codex-run.log
fi
if [[ -n "${task_desc:-}" ]]; then
  echo "task=$task_desc" >> .mock-codex-run.log
fi

echo "worker-change" >> README.md
sleep "${MOCK_CODEX_SLEEP:-0.1}"
exit "${MOCK_CODEX_EXIT:-0}"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock codex script: %v", err)
	}

	return path
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "checkout", "-b", "main")
	runCmd(t, dir, "git", "config", "user.email", "test@example.com")
	runCmd(t, dir, "git", "config", "user.name", "Test User")

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	runCmd(t, dir, "git", "add", "README.md")
	runCmd(t, dir, "git", "commit", "-m", "chore: init test repo")
}

func newTestStore(t *testing.T) (*state.Store, int64) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.New(dbPath)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	projectID, err := store.CreateProject(context.Background(), state.Project{
		Name:   "test-project",
		Status: state.ProjectStatusWorking,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	return store, projectID
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed: %v\noutput: %s", name, args, err, string(output))
	}
}

func waitForResult(t *testing.T, results <-chan Session, sessionID string, timeout time.Duration) Session {
	t.Helper()

	deadline := time.After(timeout)
	for {
		select {
		case result := <-results:
			if result.SessionID == sessionID {
				return result
			}
		case <-deadline:
			t.Fatalf("timed out waiting for worker result for session %s", sessionID)
		}
	}
}
