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
	projectName := "test-project"
	repoURL := setupRemoteRepository(t, workDir, projectName)
	runLogPath := filepath.Join(workDir, "mock-codex-run.log")
	t.Setenv("MOCK_CODEX_RUN_LOG", runLogPath)
	mockCodex := writeMockCodex(t, workDir)
	store, projectID := newTestStore(t, projectName, repoURL)
	defer func() { _ = store.Close() }()
	workersDir := filepath.Join(workDir, "workers")

	launcher := NewWithStore(store, LauncherConfig{
		CodexBinary:          mockCodex,
		ApprovalMode:         "full-auto",
		MaxConcurrentWorkers: 5,
		WorkDir:              workDir,
		WorkersDir:           workersDir,
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

	runLogRaw, err := os.ReadFile(runLogPath)
	if err != nil {
		t.Fatalf("expected mock codex run log at %s: %v", runLogPath, err)
	}
	runLog := string(runLogRaw)
	if !strings.Contains(runLog, "mode=--full-auto") {
		t.Fatalf("expected --full-auto in mock run log, got: %s", runLog)
	}
	if !strings.Contains(runLog, "cd="+filepath.Join(workersDir, session.SessionID, "repo")) {
		t.Fatalf("expected -C repo path in mock run log, got: %s", runLog)
	}
	if !strings.Contains(runLog, "Contexto de Trabajo") {
		t.Fatalf("expected full context in prompt, got: %s", runLog)
	}

	workerDirPath := filepath.Join(workersDir, session.SessionID)
	if _, err := os.Stat(workerDirPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected worker dir cleanup for successful run, stat err=%v", err)
	}

	active := launcher.GetActiveWorkers()
	if len(active) != 0 {
		t.Fatalf("expected no active workers, got %d", len(active))
	}

	verifyClone := filepath.Join(workDir, "verify")
	runCmd(t, workDir, "git", "clone", repoURL, verifyClone)
	runCmd(t, verifyClone, "git", "checkout", session.Branch)

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
	projectName := "timeout-project"
	repoURL := setupRemoteRepository(t, workDir, projectName)
	mockCodex := writeMockCodex(t, workDir)
	store, projectID := newTestStore(t, projectName, repoURL)
	defer func() { _ = store.Close() }()

	launcher := NewWithStore(store, LauncherConfig{
		CodexBinary:          mockCodex,
		MaxConcurrentWorkers: 1,
		WorkDir:              workDir,
		WorkersDir:           filepath.Join(workDir, "workers"),
		BranchPrefix:         "feature/",
		PollInterval:         20 * time.Millisecond,
		Timeout:              150 * time.Millisecond,
		DisableGitPull:       true,
		DirectExecution:      true,
	})

	session, err := launcher.LaunchWorker(context.Background(), Task{
		ProjectID:   projectID,
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
	projectName := "concurrency-project"
	repoURL := setupRemoteRepository(t, workDir, projectName)
	mockCodex := writeMockCodex(t, workDir)
	store, projectID := newTestStore(t, projectName, repoURL)
	defer func() { _ = store.Close() }()

	launcher := NewWithStore(store, LauncherConfig{
		CodexBinary:          mockCodex,
		MaxConcurrentWorkers: 1,
		WorkDir:              workDir,
		WorkersDir:           filepath.Join(workDir, "workers"),
		BranchPrefix:         "feature/",
		PollInterval:         20 * time.Millisecond,
		Timeout:              2 * time.Second,
		DisableGitPull:       true,
		DirectExecution:      true,
	})

	first, err := launcher.LaunchWorker(context.Background(), Task{
		ProjectID:   projectID,
		Description: "First worker",
		BranchName:  "first-worker",
	}, "# AGENTS", "")
	if err != nil {
		t.Fatalf("launch first worker: %v", err)
	}

	_, err = launcher.LaunchWorker(context.Background(), Task{
		ProjectID:   projectID,
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

mode=""
project_dir=""
prompt=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    exec)
      shift
      ;;
    --full-auto)
      mode="$1"
      shift
      ;;
    --model)
      shift 2
      ;;
    --reasoning-effort)
      shift 2
      ;;
    -C|--cd)
      project_dir="$2"
      shift 2
      ;;
    --)
      shift
      ;;
    *)
      prompt="$1"
      shift
      ;;
  esac
done

echo "mode=$mode" > .mock-codex-run.log
echo "cd=$project_dir" >> .mock-codex-run.log
echo "prompt=$prompt" >> .mock-codex-run.log
if [[ -n "${MOCK_CODEX_RUN_LOG:-}" ]]; then
  echo "mode=$mode" > "${MOCK_CODEX_RUN_LOG}"
  echo "cd=$project_dir" >> "${MOCK_CODEX_RUN_LOG}"
  echo "prompt=$prompt" >> "${MOCK_CODEX_RUN_LOG}"
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

func TestBuildTmuxCommandUsesCurrentCodexSyntax(t *testing.T) {
	launcher := NewWithStore(nil, LauncherConfig{
		CodexBinary:     "codex",
		Model:           "gpt-5.4",
		ReasoningEffort: "medium",
		WorkDir:         t.TempDir(),
		WorkersDir:      t.TempDir(),
	})

	cmdLine := launcher.buildTmuxCommand(
		"/home/stefan/Github_Repos/flux",
		"/tmp/example-prompt.txt",
		"/tmp/example-stdout.log",
		"/tmp/example-stderr.log",
		"/tmp/example-exit.code",
	)

	if !strings.Contains(cmdLine, "--full-auto") {
		t.Fatalf("expected --full-auto in tmux command, got: %s", cmdLine)
	}
	if !strings.Contains(cmdLine, "--model") {
		t.Fatalf("expected --model in tmux command, got: %s", cmdLine)
	}
	if !strings.Contains(cmdLine, "--reasoning-effort") || !strings.Contains(cmdLine, "'medium'") {
		t.Fatalf("expected reasoning effort flag in tmux command, got: %s", cmdLine)
	}
	if !strings.Contains(cmdLine, " -C ") {
		t.Fatalf("expected -C in tmux command, got: %s", cmdLine)
	}
	modelIndex := strings.Index(cmdLine, "--model")
	effortIndex := strings.Index(cmdLine, "--reasoning-effort")
	repoIndex := strings.Index(cmdLine, " -C ")
	if modelIndex == -1 || effortIndex == -1 || repoIndex == -1 {
		t.Fatalf("expected model, reasoning, and -C positions in tmux command, got: %s", cmdLine)
	}
	if modelIndex > repoIndex {
		t.Fatalf("expected --model before -C, got: %s", cmdLine)
	}
	if effortIndex > repoIndex {
		t.Fatalf("expected --reasoning-effort before -C, got: %s", cmdLine)
	}
	if !strings.Contains(cmdLine, "$(cat ") {
		t.Fatalf("expected prompt file expansion in tmux command, got: %s", cmdLine)
	}
	if strings.Contains(cmdLine, "--approval-mode") {
		t.Fatalf("unexpected legacy --approval-mode flag in tmux command: %s", cmdLine)
	}
	if strings.Contains(cmdLine, "--context") {
		t.Fatalf("unexpected legacy --context flag in tmux command: %s", cmdLine)
	}
}

func TestBuildCodexArgsWithModel(t *testing.T) {
	launcher := NewWithStore(nil, LauncherConfig{
		CodexBinary:     "codex",
		Model:           "gpt-5.4",
		ReasoningEffort: "high",
		WorkDir:         t.TempDir(),
		WorkersDir:      t.TempDir(),
	})

	args := launcher.buildCodexArgs("/repo", "do something")

	expect := []string{"exec", "--full-auto", "-C", "/repo", "--model", "gpt-5.4", "--reasoning-effort", "high", "--", "do something"}
	if len(args) != len(expect) {
		t.Fatalf("unexpected arg count: got %v want %v", args, expect)
	}
	for i := range expect {
		if args[i] != expect[i] {
			t.Fatalf("unexpected args[%d]: got %q want %q (full args: %v)", i, args[i], expect[i], args)
		}
	}
	if indexOfArg(args, "--model") > indexOfArg(args, "--") {
		t.Fatalf("expected --model before --, got %v", args)
	}
	if indexOfArg(args, "--reasoning-effort") > indexOfArg(args, "--") {
		t.Fatalf("expected --reasoning-effort before --, got %v", args)
	}
}

func TestBuildCodexArgsWithoutModel(t *testing.T) {
	launcher := NewWithStore(nil, LauncherConfig{
		CodexBinary: "codex",
		WorkDir:     t.TempDir(),
		WorkersDir:  t.TempDir(),
	})

	args := launcher.buildCodexArgs("/repo", "do something")
	expect := []string{"exec", "--full-auto", "-C", "/repo", "--", "do something"}
	if len(args) != len(expect) {
		t.Fatalf("unexpected arg count: got %v want %v", args, expect)
	}
	for i := range expect {
		if args[i] != expect[i] {
			t.Fatalf("unexpected args[%d]: got %q want %q (full args: %v)", i, args[i], expect[i], args)
		}
	}
	if indexOfArg(args, "--model") != -1 {
		t.Fatalf("did not expect --model in args: %v", args)
	}
	if indexOfArg(args, "--reasoning-effort") != -1 {
		t.Fatalf("did not expect --reasoning-effort in args: %v", args)
	}
}

func TestBuildCodexArgsXhighEffort(t *testing.T) {
	launcher := NewWithStore(nil, LauncherConfig{
		CodexBinary:     "codex",
		ReasoningEffort: "xhigh",
		WorkDir:         t.TempDir(),
		WorkersDir:      t.TempDir(),
	})

	args := launcher.buildCodexArgs("/repo", "do something")
	effortIndex := indexOfArg(args, "--reasoning-effort")
	if effortIndex == -1 {
		t.Fatalf("expected --reasoning-effort in args: %v", args)
	}
	if effortIndex+1 >= len(args) || args[effortIndex+1] != "xhigh" {
		t.Fatalf("expected xhigh effort in args: %v", args)
	}
}

func TestInvalidReasoningEffortIgnored(t *testing.T) {
	launcher := NewWithStore(nil, LauncherConfig{
		CodexBinary:     "codex",
		ReasoningEffort: "ultra",
		WorkDir:         t.TempDir(),
		WorkersDir:      t.TempDir(),
	})

	if launcher.config.ReasoningEffort != "" {
		t.Fatalf("expected invalid reasoning effort to be ignored, got %q", launcher.config.ReasoningEffort)
	}
}

func indexOfArg(args []string, target string) int {
	for i, arg := range args {
		if arg == target {
			return i
		}
	}
	return -1
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

func setupRemoteRepository(t *testing.T, baseDir, projectName string) string {
	t.Helper()

	originPath := filepath.Join(baseDir, projectName+".git")
	seedDir := filepath.Join(baseDir, projectName+"-seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatalf("create seed dir: %v", err)
	}

	runCmd(t, baseDir, "git", "init", "--bare", originPath)
	initGitRepo(t, seedDir)
	runCmd(t, seedDir, "git", "remote", "add", "origin", originPath)
	runCmd(t, seedDir, "git", "push", "-u", "origin", "main")
	runCmd(t, baseDir, "git", "--git-dir", originPath, "symbolic-ref", "HEAD", "refs/heads/main")

	return originPath
}

func newTestStore(t *testing.T, projectName, repoURL string) (*state.Store, int64) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.New(dbPath)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	projectID, err := store.CreateProject(context.Background(), state.Project{
		Name:    projectName,
		Status:  state.ProjectStatusWorking,
		RepoURL: repoURL,
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
