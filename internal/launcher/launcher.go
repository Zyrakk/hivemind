package launcher

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zyrakk/hivemind/internal/state"
)

var (
	ErrMaxConcurrentWorkers = errors.New("max concurrent workers reached")
	ErrWorkerNotFound       = errors.New("worker not found")
)

type Task struct {
	ProjectID     int64
	ID            string
	Title         string
	Description   string
	BranchName    string
	FilesAffected []string
}

type Session struct {
	SessionID  string
	ProjectID  int64
	Branch     string
	Status     string
	StartedAt  time.Time
	FinishedAt *time.Time
	ExitCode   int
	Error      string
	Diff       string
	WorkerID   int64
}

type WorkerProcess struct {
	SessionID   string
	Branch      string
	Status      string
	PID         int
	StartedAt   time.Time
	ContextFile string
	Task        Task
	WorkerID    int64

	stderrFile string
	exitFile   string
	stdoutFile string
	tmux       bool
	cmd        *exec.Cmd
	waitDone   chan struct{}
	waitErr    error
	stopReason string
}

type LauncherConfig struct {
	CodexBinary          string
	ApprovalMode         string
	TimeoutMinutes       int
	MaxConcurrentWorkers int
	WorkDir              string
	GitRemote            string
	BranchPrefix         string

	TmuxBinary      string
	GitBinary       string
	UseTmux         bool
	DirectExecution bool
	PollInterval    time.Duration
	DisableGitPull  bool
	Timeout         time.Duration
	Logger          *slog.Logger
}

type Launcher struct {
	db            *state.Store
	config        LauncherConfig
	activeWorkers map[string]*WorkerProcess
	completed     map[string]Session
	mu            sync.Mutex
	logger        *slog.Logger
	finishedCh    chan Session
	nowFn         func() time.Time
}

func New(binaryPath string, timeout time.Duration) *Launcher {
	timeoutMinutes := int(timeout / time.Minute)
	if timeoutMinutes <= 0 {
		timeoutMinutes = 30
	}

	return NewWithStore(nil, LauncherConfig{
		CodexBinary:    binaryPath,
		TimeoutMinutes: timeoutMinutes,
	})
}

func NewWithStore(db *state.Store, config LauncherConfig) *Launcher {
	if strings.TrimSpace(config.CodexBinary) == "" {
		config.CodexBinary = "codex"
	}
	if strings.TrimSpace(config.ApprovalMode) == "" {
		config.ApprovalMode = "full-auto"
	}
	if config.TimeoutMinutes <= 0 && config.Timeout <= 0 {
		config.TimeoutMinutes = 30
	}
	if config.MaxConcurrentWorkers <= 0 {
		config.MaxConcurrentWorkers = 5
	}
	if strings.TrimSpace(config.WorkDir) == "" {
		config.WorkDir = "."
	}
	if strings.TrimSpace(config.GitRemote) == "" {
		config.GitRemote = "origin"
	}
	if strings.TrimSpace(config.BranchPrefix) == "" {
		config.BranchPrefix = "feature/"
	}
	if strings.TrimSpace(config.TmuxBinary) == "" {
		config.TmuxBinary = "tmux"
	}
	if strings.TrimSpace(config.GitBinary) == "" {
		config.GitBinary = "git"
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 30 * time.Second
	}
	if config.DirectExecution {
		config.UseTmux = false
	} else if config.UseTmux {
		config.UseTmux = true
	} else {
		config.UseTmux = true
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Launcher{
		db:            db,
		config:        config,
		activeWorkers: make(map[string]*WorkerProcess),
		completed:     make(map[string]Session),
		logger:        config.Logger,
		finishedCh:    make(chan Session, 128),
		nowFn:         time.Now,
	}
}

func (l *Launcher) LaunchWorker(ctx context.Context, task Task, agentsMD string, cache string) (*Session, error) {
	if strings.TrimSpace(task.Description) == "" {
		return nil, fmt.Errorf("task description is required")
	}

	sessionID := generateSessionID()
	startedAt := l.nowFn().UTC()
	branch := l.buildBranchName(task)

	placeholder := &WorkerProcess{
		SessionID: sessionID,
		Branch:    branch,
		Status:    state.WorkerStatusRunning,
		StartedAt: startedAt,
		Task:      task,
		waitDone:  make(chan struct{}),
	}

	l.mu.Lock()
	if len(l.activeWorkers) >= l.config.MaxConcurrentWorkers {
		l.mu.Unlock()
		return nil, ErrMaxConcurrentWorkers
	}
	l.activeWorkers[sessionID] = placeholder
	l.mu.Unlock()

	cleanupPlaceholder := func() {
		l.mu.Lock()
		delete(l.activeWorkers, sessionID)
		l.mu.Unlock()
	}

	if err := l.prepareBranch(ctx, branch); err != nil {
		cleanupPlaceholder()
		return nil, err
	}

	taskDescription := renderTaskDescription(task)
	contextText := BuildWorkerContext(agentsMD, taskDescription, cache, sessionID)
	contextFile, err := l.writeContextFile(sessionID, contextText)
	if err != nil {
		cleanupPlaceholder()
		return nil, err
	}

	stdoutFile := filepath.Join(l.config.WorkDir, "sessions", "cache", sessionID+"-stdout.log")
	stderrFile := filepath.Join(l.config.WorkDir, "sessions", "cache", sessionID+"-stderr.log")
	exitFile := filepath.Join(l.config.WorkDir, "sessions", "cache", sessionID+"-exit.code")

	worker, err := l.startWorkerProcess(ctx, placeholder, contextFile, task.Description, stdoutFile, stderrFile, exitFile)
	if err != nil {
		cleanupPlaceholder()
		return nil, err
	}

	if l.db != nil {
		workerID, createErr := l.db.CreateWorker(ctx, state.Worker{
			ProjectID:       task.ProjectID,
			SessionID:       sessionID,
			TaskDescription: task.Description,
			Branch:          branch,
			Status:          state.WorkerStatusRunning,
			StartedAt:       startedAt,
		})
		if createErr != nil {
			_ = l.killWorkerProcess(context.Background(), worker)
			cleanupPlaceholder()
			return nil, fmt.Errorf("register worker in state store: %w", createErr)
		}
		worker.WorkerID = workerID
		if task.ProjectID > 0 {
			_ = l.db.AppendEvent(ctx, state.Event{
				ProjectID:   task.ProjectID,
				WorkerID:    &workerID,
				EventType:   "worker_started",
				Description: fmt.Sprintf("Worker %s launched on branch %s", sessionID, branch),
			})
		}
	}

	l.mu.Lock()
	l.activeWorkers[sessionID] = worker
	l.mu.Unlock()

	session := &Session{
		SessionID: sessionID,
		ProjectID: task.ProjectID,
		Branch:    branch,
		Status:    state.WorkerStatusRunning,
		StartedAt: startedAt,
		WorkerID:  worker.WorkerID,
	}

	go l.MonitorWorker(*session)

	return session, nil
}

func (l *Launcher) GetActiveWorkers() []WorkerProcess {
	l.mu.Lock()
	defer l.mu.Unlock()

	workers := make([]WorkerProcess, 0, len(l.activeWorkers))
	for _, worker := range l.activeWorkers {
		if worker == nil {
			continue
		}
		workers = append(workers, WorkerProcess{
			SessionID:   worker.SessionID,
			Branch:      worker.Branch,
			Status:      worker.Status,
			PID:         worker.PID,
			StartedAt:   worker.StartedAt,
			ContextFile: worker.ContextFile,
			Task:        worker.Task,
			WorkerID:    worker.WorkerID,
		})
	}

	return workers
}

func (l *Launcher) WorkerDone() <-chan Session {
	return l.finishedCh
}

func (l *Launcher) GetSession(sessionID string) (Session, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if session, ok := l.completed[sessionID]; ok {
		return session, true
	}

	worker, ok := l.activeWorkers[sessionID]
	if !ok || worker == nil {
		return Session{}, false
	}

	return Session{
		SessionID: worker.SessionID,
		ProjectID: worker.Task.ProjectID,
		Branch:    worker.Branch,
		Status:    worker.Status,
		StartedAt: worker.StartedAt,
		WorkerID:  worker.WorkerID,
	}, true
}

func (l *Launcher) GetWorkDir() string {
	return l.config.WorkDir
}

func (l *Launcher) prepareBranch(ctx context.Context, branch string) error {
	if _, err := l.runCommand(ctx, l.config.GitBinary, "-C", l.config.WorkDir, "checkout", "main"); err != nil {
		return fmt.Errorf("git checkout main: %w", err)
	}

	if !l.config.DisableGitPull {
		if _, err := l.runCommand(ctx, l.config.GitBinary, "-C", l.config.WorkDir, "pull", l.config.GitRemote, "main"); err != nil {
			return fmt.Errorf("git pull %s main: %w", l.config.GitRemote, err)
		}
	}

	if _, err := l.runCommand(ctx, l.config.GitBinary, "-C", l.config.WorkDir, "checkout", "-b", branch); err != nil {
		return fmt.Errorf("git checkout -b %s: %w", branch, err)
	}

	return nil
}

func (l *Launcher) writeContextFile(sessionID, contextText string) (string, error) {
	cacheDir := filepath.Join(l.config.WorkDir, "sessions", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create sessions/cache: %w", err)
	}

	contextFile := filepath.Join(cacheDir, sessionID+"-context.md")
	if err := os.WriteFile(contextFile, []byte(contextText), 0o644); err != nil {
		return "", fmt.Errorf("write context file: %w", err)
	}

	return contextFile, nil
}

func (l *Launcher) startWorkerProcess(
	ctx context.Context,
	baseWorker *WorkerProcess,
	contextFile string,
	taskDescription string,
	stdoutFile string,
	stderrFile string,
	exitFile string,
) (*WorkerProcess, error) {
	worker := *baseWorker
	worker.ContextFile = contextFile
	worker.stdoutFile = stdoutFile
	worker.stderrFile = stderrFile
	worker.exitFile = exitFile

	stdout, err := os.Create(stdoutFile)
	if err != nil {
		return nil, fmt.Errorf("create stdout log: %w", err)
	}
	defer stdout.Close()

	stderr, err := os.Create(stderrFile)
	if err != nil {
		return nil, fmt.Errorf("create stderr log: %w", err)
	}
	defer stderr.Close()

	if l.config.UseTmux {
		cmdLine := l.buildTmuxCommand(taskDescription, contextFile, stdoutFile, stderrFile, exitFile)
		if _, err := l.runCommand(ctx, l.config.TmuxBinary, "new-session", "-d", "-s", worker.SessionID, cmdLine); err != nil {
			return nil, fmt.Errorf("tmux new-session: %w", err)
		}
		worker.tmux = true
		worker.PID = l.tmuxPanePID(ctx, worker.SessionID)
		return &worker, nil
	}

	args := []string{
		"--approval-mode", l.config.ApprovalMode,
		"--context", contextFile,
		taskDescription,
	}
	cmd := exec.CommandContext(ctx, l.config.CodexBinary, args...)
	cmd.Dir = l.config.WorkDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex process: %w", err)
	}

	worker.cmd = cmd
	worker.PID = cmd.Process.Pid
	worker.tmux = false

	go func(w *WorkerProcess) {
		waitErr := cmd.Wait()
		exitCode := 0
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		_ = os.WriteFile(w.exitFile, []byte(fmt.Sprintf("%d\n", exitCode)), 0o644)
		w.waitErr = waitErr
		close(w.waitDone)
	}(&worker)

	return &worker, nil
}

func (l *Launcher) buildTmuxCommand(taskDescription, contextFile, stdoutFile, stderrFile, exitFile string) string {
	args := []string{
		l.config.CodexBinary,
		"--approval-mode", l.config.ApprovalMode,
		"--context", contextFile,
		taskDescription,
	}

	quotedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		quotedArgs = append(quotedArgs, shellQuote(arg))
	}

	return fmt.Sprintf(
		"%s > %s 2> %s; CODE=$?; echo $CODE > %s",
		strings.Join(quotedArgs, " "),
		shellQuote(stdoutFile),
		shellQuote(stderrFile),
		shellQuote(exitFile),
	)
}

func (l *Launcher) runCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = l.config.WorkDir

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	err := cmd.Run()
	if err != nil {
		return combined.Bytes(), fmt.Errorf("%s %s: %w (%s)", command, strings.Join(args, " "), err, strings.TrimSpace(combined.String()))
	}

	return combined.Bytes(), nil
}

func (l *Launcher) buildBranchName(task Task) string {
	base := strings.TrimSpace(task.BranchName)
	if base == "" {
		base = strings.TrimSpace(task.ID)
	}
	if base == "" {
		base = strings.TrimSpace(task.Title)
	}
	if base == "" {
		base = "worker-" + generateSessionID()
	}

	base = sanitizeBranch(base)
	if strings.HasPrefix(base, l.config.BranchPrefix) {
		return base
	}
	return l.config.BranchPrefix + base
}

func renderTaskDescription(task Task) string {
	parts := []string{strings.TrimSpace(task.Description)}
	if len(task.FilesAffected) > 0 {
		parts = append(parts, "Archivos permitidos: "+strings.Join(task.FilesAffected, ", "))
	}
	if strings.TrimSpace(task.Title) != "" {
		parts = append([]string{"Titulo: " + strings.TrimSpace(task.Title)}, parts...)
	}
	if strings.TrimSpace(task.ID) != "" {
		parts = append([]string{"Task ID: " + strings.TrimSpace(task.ID)}, parts...)
	}

	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, part)
		}
	}

	return strings.Join(filtered, "\n")
}

func generateSessionID() string {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("hivemind-%d", time.Now().UnixNano())
	}

	return fmt.Sprintf("hivemind-%d-%s", time.Now().Unix(), hex.EncodeToString(random))
}

func sanitizeBranch(input string) string {
	clean := strings.ToLower(strings.TrimSpace(input))
	if clean == "" {
		return "worker"
	}

	builder := strings.Builder{}
	prevDash := false
	for _, r := range clean {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '/' || r == '-' || r == '_'
		if !allowed {
			if !prevDash {
				builder.WriteByte('-')
				prevDash = true
			}
			continue
		}
		builder.WriteRune(r)
		prevDash = r == '-'
	}

	out := strings.Trim(builder.String(), "-/")
	if out == "" {
		return "worker"
	}

	return out
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func parseExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if errors.Is(err, syscall.ESRCH) {
		return 0
	}
	return -1
}
