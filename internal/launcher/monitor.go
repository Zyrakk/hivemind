package launcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zyrakk/hivemind/internal/state"
)

func (l *Launcher) MonitorWorker(session Session) {
	ticker := time.NewTicker(l.config.PollInterval)
	defer ticker.Stop()

	timeout := l.effectiveTimeout()
	for {
		worker, ok := l.getWorker(session.SessionID)
		if !ok {
			return
		}

		if timeout > 0 && l.nowFn().UTC().After(worker.StartedAt.Add(timeout)) {
			worker.stopReason = l.timeoutMessage(timeout)
			_ = l.killWorkerProcess(context.Background(), worker)
			l.finalizeWorker(worker)
			return
		}

		alive, err := l.isWorkerAlive(context.Background(), worker)
		if err != nil {
			l.logger.Warn("worker health check failed", slogAnyErr(err), slog.String("session_id", worker.SessionID))
		}
		if !alive {
			l.finalizeWorker(worker)
			return
		}

		<-ticker.C
	}
}

func (l *Launcher) StopWorker(sessionID string) error {
	worker, ok := l.getWorker(sessionID)
	if !ok {
		return ErrWorkerNotFound
	}

	worker.stopReason = "stopped manually"
	if err := l.killWorkerProcess(context.Background(), worker); err != nil {
		return err
	}

	l.updateWorkerStatus(context.Background(), worker, state.WorkerStatusFailed, worker.stopReason)
	return nil
}

func (l *Launcher) PauseWorker(sessionID string) error {
	worker, ok := l.getWorker(sessionID)
	if !ok {
		return ErrWorkerNotFound
	}

	if err := l.signalWorker(worker, syscall.SIGSTOP); err != nil {
		return err
	}
	worker.Status = state.WorkerStatusPaused
	l.updateWorkerStatus(context.Background(), worker, state.WorkerStatusPaused, "")
	return nil
}

func (l *Launcher) ResumeWorker(sessionID string) error {
	worker, ok := l.getWorker(sessionID)
	if !ok {
		return ErrWorkerNotFound
	}

	if err := l.signalWorker(worker, syscall.SIGCONT); err != nil {
		return err
	}
	worker.Status = state.WorkerStatusRunning
	l.updateWorkerStatus(context.Background(), worker, state.WorkerStatusRunning, "")
	return nil
}

func (l *Launcher) getWorker(sessionID string) (*WorkerProcess, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	worker, ok := l.activeWorkers[sessionID]
	return worker, ok
}

func (l *Launcher) removeWorker(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.activeWorkers, sessionID)
}

func (l *Launcher) isWorkerAlive(ctx context.Context, worker *WorkerProcess) (bool, error) {
	if worker == nil {
		return false, nil
	}

	if worker.tmux {
		return l.tmuxSessionExists(ctx, worker.SessionID)
	}

	select {
	case <-worker.waitDone:
		return false, nil
	default:
		return true, nil
	}
}

func (l *Launcher) killWorkerProcess(ctx context.Context, worker *WorkerProcess) error {
	if worker == nil {
		return nil
	}

	if worker.tmux {
		_, err := l.runCommand(ctx, l.config.TmuxBinary, "kill-session", "-t", worker.SessionID)
		if err != nil && !isTmuxSessionMissing(err) {
			return fmt.Errorf("kill tmux session %s: %w", worker.SessionID, err)
		}
		return nil
	}

	if worker.cmd != nil && worker.cmd.Process != nil {
		if err := worker.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill process %d: %w", worker.cmd.Process.Pid, err)
		}
	}

	return nil
}

func (l *Launcher) signalWorker(worker *WorkerProcess, signal syscall.Signal) error {
	if worker == nil {
		return ErrWorkerNotFound
	}

	pid := worker.PID
	if pid <= 0 && worker.tmux {
		pid = l.tmuxPanePID(context.Background(), worker.SessionID)
	}
	if pid <= 0 {
		return fmt.Errorf("worker pid unavailable for session %s", worker.SessionID)
	}

	if err := syscall.Kill(pid, signal); err != nil {
		return fmt.Errorf("signal %s to pid %d: %w", signal.String(), pid, err)
	}

	return nil
}

func (l *Launcher) tmuxSessionExists(ctx context.Context, sessionID string) (bool, error) {
	_, err := l.runCommand(ctx, l.config.TmuxBinary, "has-session", "-t", sessionID)
	if err == nil {
		return true, nil
	}
	if isTmuxSessionMissing(err) {
		return false, nil
	}
	return false, err
}

func (l *Launcher) tmuxPanePID(ctx context.Context, sessionID string) int {
	output, err := l.runCommand(ctx, l.config.TmuxBinary, "list-panes", "-t", sessionID, "-F", "#{pane_pid}")
	if err != nil {
		return 0
	}

	pidRaw := strings.TrimSpace(string(output))
	if pidRaw == "" {
		return 0
	}

	lines := strings.Split(pidRaw, "\n")
	parsed, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return 0
	}

	return parsed
}

func (l *Launcher) finalizeWorker(worker *WorkerProcess) {
	if worker == nil {
		return
	}

	exitCode := l.resolveWorkerExitCode(worker)
	status := state.WorkerStatusCompleted
	errorMessage := strings.TrimSpace(worker.stopReason)
	if exitCode != 0 {
		status = state.WorkerStatusFailed
		if errorMessage == "" {
			errorMessage = strings.TrimSpace(readTextFile(worker.stderrFile))
		}
		if errorMessage == "" {
			errorMessage = fmt.Sprintf("worker exited with code %d", exitCode)
		}
	}

	diff := strings.TrimSpace(l.collectWorkerDiff(worker.Branch))
	finishedAt := l.nowFn().UTC()

	session := Session{
		SessionID:  worker.SessionID,
		ProjectID:  worker.Task.ProjectID,
		Branch:     worker.Branch,
		Status:     status,
		StartedAt:  worker.StartedAt,
		FinishedAt: &finishedAt,
		ExitCode:   exitCode,
		Error:      errorMessage,
		Diff:       diff,
		WorkerID:   worker.WorkerID,
	}

	worker.Status = status
	l.updateWorkerStatus(context.Background(), worker, status, errorMessage)
	l.removeWorker(worker.SessionID)
	l.mu.Lock()
	l.completed[worker.SessionID] = session
	l.mu.Unlock()

	select {
	case l.finishedCh <- session:
	default:
		l.logger.Warn("worker result channel full", slog.String("session_id", worker.SessionID))
	}
}

func (l *Launcher) resolveWorkerExitCode(worker *WorkerProcess) int {
	if worker == nil {
		return -1
	}

	exitCode, err := readExitCodeFile(worker.exitFile)
	if err == nil {
		return exitCode
	}

	if worker.waitErr != nil {
		return parseExitCode(worker.waitErr)
	}

	if worker.tmux {
		return -1
	}

	select {
	case <-worker.waitDone:
		if worker.waitErr != nil {
			return parseExitCode(worker.waitErr)
		}
		return 0
	default:
		return -1
	}
}

func (l *Launcher) collectWorkerDiff(branch string) string {
	if strings.TrimSpace(branch) == "" {
		return ""
	}

	output, err := l.runCommand(context.Background(), l.config.GitBinary, "-C", l.config.WorkDir, "diff", "main..."+branch)
	if err == nil && strings.TrimSpace(string(output)) != "" {
		return string(output)
	}

	fallback, fallbackErr := l.runCommand(context.Background(), l.config.GitBinary, "-C", l.config.WorkDir, "diff")
	if fallbackErr == nil && strings.TrimSpace(string(fallback)) != "" {
		return string(fallback)
	}

	if err != nil || fallbackErr != nil {
		l.logger.Warn("collect worker diff failed", slog.String("branch", branch), slogAnyErr(err), slogAnyErr(fallbackErr))
	}
	return ""
}

func (l *Launcher) updateWorkerStatus(ctx context.Context, worker *WorkerProcess, status string, errorMessage string) {
	if l.db == nil || worker == nil || worker.WorkerID <= 0 {
		return
	}

	update := state.WorkerUpdate{Status: &status, ErrorMessageSet: true}
	if strings.TrimSpace(errorMessage) != "" {
		msg := strings.TrimSpace(errorMessage)
		update.ErrorMessage = &msg
	}

	if err := l.db.UpdateWorker(ctx, worker.WorkerID, update); err != nil {
		l.logger.Warn("update worker status failed", slog.String("session_id", worker.SessionID), slogAnyErr(err))
	}

	if worker.Task.ProjectID > 0 {
		description := fmt.Sprintf("Worker %s status=%s", worker.SessionID, status)
		if strings.TrimSpace(errorMessage) != "" {
			description = description + ": " + strings.TrimSpace(errorMessage)
		}
		_ = l.db.AppendEvent(ctx, state.Event{
			ProjectID:   worker.Task.ProjectID,
			WorkerID:    &worker.WorkerID,
			EventType:   "worker_status",
			Description: description,
		})
	}
}

func (l *Launcher) effectiveTimeout() time.Duration {
	if l.config.Timeout > 0 {
		return l.config.Timeout
	}
	if l.config.TimeoutMinutes <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(l.config.TimeoutMinutes) * time.Minute
}

func (l *Launcher) timeoutMessage(timeout time.Duration) string {
	if l.config.TimeoutMinutes > 0 && l.config.Timeout <= 0 {
		return fmt.Sprintf("timeout after %d minutes", l.config.TimeoutMinutes)
	}
	return fmt.Sprintf("timeout after %s", timeout.Round(time.Second))
}

func readExitCodeFile(path string) (int, error) {
	if strings.TrimSpace(path) == "" {
		return 0, fmt.Errorf("empty exit file path")
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	code, err := strconv.Atoi(strings.TrimSpace(string(payload)))
	if err != nil {
		return 0, err
	}

	return code, nil
}

func readTextFile(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(payload)
}

func isTmuxSessionMissing(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "can't find session") ||
		strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "failed to connect to server")
}

func slogAnyErr(err error) slog.Attr {
	if err == nil {
		return slog.Any("error", nil)
	}
	return slog.Any("error", err)
}
