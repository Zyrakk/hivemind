package evaluator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/state"
)

type evaluatorLLM interface {
	Evaluate(ctx context.Context, task, diff, agentsMD string) (*llm.Evaluation, error)
}

type evaluatorLauncher interface {
	LaunchWorker(ctx context.Context, task launcher.Task, agentsMD string, cache string) (*launcher.Session, error)
	GetSession(sessionID string) (launcher.Session, bool)
	GetWorkDir() string
}

type EvalResult struct {
	Verdict        string          `json:"verdict"`
	Evaluation     *llm.Evaluation `json:"evaluation"`
	Action         string          `json:"action"`
	RetryCount     int             `json:"retry_count"`
	NextSessionID  string          `json:"next_session_id,omitempty"`
	ConsultantUsed bool            `json:"consultant_used"`
}

type CompletionResult struct {
	Action        string `json:"action"`
	RetryCount    int    `json:"retry_count"`
	NextSessionID string `json:"next_session_id,omitempty"`
}

type Evaluator struct {
	glm         evaluatorLLM
	consultants []llm.ConsultantClient
	launcher    evaluatorLauncher
	db          *state.Store
	logger      *slog.Logger

	mu      sync.Mutex
	retries map[int64]int
}

func New(
	glm *llm.GLMClient,
	consultants []llm.ConsultantClient,
	launcher *launcher.Launcher,
	db *state.Store,
	logger *slog.Logger,
) *Evaluator {
	return NewWithDeps(glm, consultants, launcher, db, logger)
}

func NewWithDeps(
	glm evaluatorLLM,
	consultants []llm.ConsultantClient,
	launcher evaluatorLauncher,
	db *state.Store,
	logger *slog.Logger,
) *Evaluator {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Evaluator{
		glm:         glm,
		consultants: consultants,
		launcher:    launcher,
		db:          db,
		logger:      logger,
		retries:     make(map[int64]int),
	}
}

func (e *Evaluator) Evaluate(ctx context.Context, diff, criteria string) (*llm.Evaluation, error) {
	if e == nil || e.glm == nil {
		return nil, llm.ErrNotImplemented
	}

	return e.glm.Evaluate(ctx, criteria, diff, "")
}

func (e *Evaluator) EvaluateWorkerOutput(ctx context.Context, session launcher.Session) (*EvalResult, error) {
	if e == nil {
		return nil, fmt.Errorf("evaluator is nil")
	}
	if e.glm == nil {
		return nil, fmt.Errorf("glm client is not configured")
	}

	taskRecord, projectName, err := e.findTaskForSession(ctx, session)
	if err != nil {
		return nil, err
	}

	agentsMD, err := readProjectAgentsForEvaluation(session.ProjectID, projectName)
	if err != nil {
		return nil, err
	}

	diff := strings.TrimSpace(session.Diff)
	if diff == "" {
		diff, err = e.collectDiff(session.Branch)
		if err != nil {
			return nil, err
		}
	}

	evaluation, err := e.glm.Evaluate(ctx, taskRecord.Description, diff, agentsMD)
	if err != nil {
		return nil, fmt.Errorf("glm evaluation failed: %w", err)
	}

	consultantUsed := false
	forceIterate := false
	if evaluation.Confidence < 0.5 {
		consultant := firstAvailableConsultant(e.consultants)
		if consultant != nil {
			consultantUsed = true
			evalJSON, _ := json.Marshal(evaluation)
			opinion, consultErr := consultant.Consult(
				ctx,
				"output_evaluation",
				agentsMD,
				fmt.Sprintf("Valida esta evaluacion: %s\n\nDiff:\n%s", string(evalJSON), diff),
			)
			if consultErr != nil {
				e.logger.Warn("consultant evaluation failed", slog.Any("error", consultErr), slog.String("consultant", consultant.GetName()))
			} else if opinion != nil && !opinion.AgreeWithOriginal {
				forceIterate = true
			}
		}
	}

	taskID := taskRecord.ID
	retryCount := e.getRetryCount(taskID)
	action := decideAction(evaluation, forceIterate, retryCount)
	nextSessionID := ""
	workerRef := optionalWorkerID(session.WorkerID)

	switch action {
	case "accept":
		status := state.TaskStatusCompleted
		if e.db != nil && taskID > 0 {
			if updateErr := e.db.UpdateTask(ctx, taskID, state.TaskUpdate{Status: &status}); updateErr != nil {
				return nil, fmt.Errorf("mark task %d completed: %w", taskID, updateErr)
			}
		}
	case "iterate":
		nextRetry := retryCount + 1
		e.setRetryCount(taskID, nextRetry)

		cache := buildIterationCache(session, evaluation, nextRetry)
		newSession, launchErr := e.launcher.LaunchWorker(ctx, launcher.Task{
			ProjectID:     session.ProjectID,
			ID:            strconv.FormatInt(taskID, 10),
			Title:         taskRecord.Title,
			Description:   taskRecord.Description,
			BranchName:    retryBranchName(session.Branch, nextRetry),
			FilesAffected: nil,
		}, agentsMD, cache)
		if launchErr != nil {
			action = "escalate"
			if escalateErr := e.escalateToUser(ctx, taskRecord.ProjectID, taskID, workerRef, "failed to launch retry worker: "+launchErr.Error()); escalateErr != nil {
				return nil, escalateErr
			}
			break
		}
		nextSessionID = newSession.SessionID

		status := state.TaskStatusInProgress
		update := state.TaskUpdate{Status: &status}
		if newSession.WorkerID > 0 {
			workerID := newSession.WorkerID
			update.AssignedWorkerIDSet = true
			update.AssignedWorkerID = &workerID
		}
		if e.db != nil && taskID > 0 {
			if updateErr := e.db.UpdateTask(ctx, taskID, update); updateErr != nil {
				if update.AssignedWorkerIDSet {
					statusOnly := state.TaskUpdate{Status: &status}
					if retryErr := e.db.UpdateTask(ctx, taskID, statusOnly); retryErr == nil {
						retryCount = nextRetry
						break
					}
				}
				return nil, fmt.Errorf("mark task %d in progress for retry: %w", taskID, updateErr)
			}
		}
		retryCount = nextRetry
	case "escalate":
		if escalateErr := e.escalateToUser(ctx, taskRecord.ProjectID, taskID, workerRef, escalationReason(evaluation)); escalateErr != nil {
			return nil, escalateErr
		}
	}

	return &EvalResult{
		Verdict:        evaluation.Verdict,
		Evaluation:     evaluation,
		Action:         action,
		RetryCount:     retryCount,
		NextSessionID:  nextSessionID,
		ConsultantUsed: consultantUsed,
	}, nil
}

func (e *Evaluator) HandleWorkerCompletion(ctx context.Context, sessionID string) error {
	_, err := e.HandleWorkerCompletionDetailed(ctx, sessionID)
	return err
}

func (e *Evaluator) HandleWorkerCompletionDetailed(ctx context.Context, sessionID string) (*CompletionResult, error) {
	if e == nil || e.launcher == nil {
		return nil, fmt.Errorf("evaluator launcher is not configured")
	}

	session, ok := e.launcher.GetSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	if session.Status == state.WorkerStatusCompleted {
		evalResult, err := e.EvaluateWorkerOutput(ctx, session)
		if err != nil {
			return nil, err
		}
		if evalResult == nil {
			return &CompletionResult{Action: "escalate"}, nil
		}
		return &CompletionResult{
			Action:        evalResult.Action,
			RetryCount:    evalResult.RetryCount,
			NextSessionID: evalResult.NextSessionID,
		}, nil
	}

	taskRecord, projectName, err := e.findTaskForSession(ctx, session)
	if err != nil {
		return nil, err
	}

	retryCount := e.getRetryCount(taskRecord.ID)
	if retryCount >= 2 {
		if escalateErr := e.escalateToUser(ctx, taskRecord.ProjectID, taskRecord.ID, optionalWorkerID(session.WorkerID), "worker failed and retry limit was reached"); escalateErr != nil {
			return nil, escalateErr
		}
		return &CompletionResult{
			Action:     "escalate",
			RetryCount: retryCount,
		}, nil
	}

	agentsMD, err := readProjectAgentsForEvaluation(session.ProjectID, projectName)
	if err != nil {
		return nil, err
	}

	nextRetry := retryCount + 1
	e.setRetryCount(taskRecord.ID, nextRetry)

	cache := fmt.Sprintf("Worker previo fallo. Error: %s\nReintento #%d", strings.TrimSpace(session.Error), nextRetry)
	newSession, launchErr := e.launcher.LaunchWorker(ctx, launcher.Task{
		ProjectID:   session.ProjectID,
		ID:          strconv.FormatInt(taskRecord.ID, 10),
		Title:       taskRecord.Title,
		Description: taskRecord.Description,
		BranchName:  retryBranchName(session.Branch, nextRetry),
	}, agentsMD, cache)
	if launchErr != nil {
		if escalateErr := e.escalateToUser(ctx, taskRecord.ProjectID, taskRecord.ID, optionalWorkerID(session.WorkerID), "failed to relaunch worker: "+launchErr.Error()); escalateErr != nil {
			return nil, escalateErr
		}
		return &CompletionResult{
			Action:     "escalate",
			RetryCount: nextRetry,
		}, nil
	}

	status := state.TaskStatusInProgress
	update := state.TaskUpdate{Status: &status}
	if newSession.WorkerID > 0 {
		workerID := newSession.WorkerID
		update.AssignedWorkerIDSet = true
		update.AssignedWorkerID = &workerID
	}
	if e.db != nil {
		_ = e.db.UpdateTask(ctx, taskRecord.ID, update)
	}

	return &CompletionResult{
		Action:        "iterate",
		RetryCount:    nextRetry,
		NextSessionID: newSession.SessionID,
	}, nil
}

func (e *Evaluator) findTaskForSession(ctx context.Context, session launcher.Session) (state.Task, string, error) {
	if e.db == nil {
		return state.Task{}, "", fmt.Errorf("state store is not configured")
	}

	searchProjects := make([]state.ProjectSummary, 0)
	if session.ProjectID > 0 {
		project, err := e.db.GetProjectByID(ctx, session.ProjectID)
		if err == nil {
			searchProjects = append(searchProjects, state.ProjectSummary{InternalID: project.ID, Name: project.Name})
		}
	}
	if len(searchProjects) == 0 {
		summaries, err := e.db.ListProjectSummaries(ctx)
		if err != nil {
			return state.Task{}, "", err
		}
		searchProjects = append(searchProjects, summaries...)
	}

	for _, summary := range searchProjects {
		detail, err := e.db.GetProjectDetail(ctx, strconv.FormatInt(summary.InternalID, 10))
		if err != nil {
			continue
		}

		for _, task := range detail.Tasks {
			if task.AssignedWorkerID != nil && session.WorkerID > 0 && *task.AssignedWorkerID == session.WorkerID {
				if session.ProjectID == 0 {
					session.ProjectID = summary.InternalID
				}
				return task, detail.Project.Name, nil
			}
		}
	}

	return state.Task{}, "", fmt.Errorf("task not found for session %s", session.SessionID)
}

func (e *Evaluator) collectDiff(branch string) (string, error) {
	if strings.TrimSpace(branch) == "" {
		return "", fmt.Errorf("branch is required to collect diff")
	}

	workDir := "."
	if e.launcher != nil && strings.TrimSpace(e.launcher.GetWorkDir()) != "" {
		workDir = e.launcher.GetWorkDir()
	}

	output, err := runGitDiff(workDir, "main..."+branch)
	if err != nil {
		fallback, fallbackErr := runGitDiff(workDir, "")
		if fallbackErr != nil {
			return "", fmt.Errorf("collect diff for branch %s: %w", branch, err)
		}
		return fallback, nil
	}

	return output, nil
}

func runGitDiff(workDir, revision string) (string, error) {
	args := []string{"-C", workDir, "diff"}
	if strings.TrimSpace(revision) != "" {
		args = append(args, revision)
	}

	cmd := exec.Command("git", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git diff %s: %w (%s)", revision, err, strings.TrimSpace(buf.String()))
	}

	return strings.TrimSpace(buf.String()), nil
}

func readProjectAgentsForEvaluation(projectID int64, projectName string) (string, error) {
	candidates := make([]string, 0, 3)
	if projectID > 0 {
		candidates = append(candidates, filepath.Join("agents", fmt.Sprintf("%d.md", projectID)))
	}
	if strings.TrimSpace(projectName) != "" {
		name := strings.TrimSpace(projectName)
		candidates = append(candidates,
			filepath.Join("agents", name+".md"),
			filepath.Join("agents", strings.ToLower(name)+".md"),
		)
	}

	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err == nil {
			return strings.TrimSpace(string(data)), nil
		}
	}

	return "", fmt.Errorf("agents file not found for project %d (%s)", projectID, projectName)
}

func decideAction(evaluation *llm.Evaluation, forceIterate bool, retryCount int) string {
	if evaluation == nil {
		if retryCount < 2 {
			return "iterate"
		}
		return "escalate"
	}

	verdict := strings.ToLower(strings.TrimSpace(evaluation.Verdict))
	if isAcceptVerdict(verdict) && evaluation.ScopeOK && evaluation.Correctness >= 0.6 && !forceIterate {
		return "accept"
	}

	iteratePreferred := forceIterate || isIterateVerdict(verdict) || !evaluation.ScopeOK || evaluation.Correctness < 0.6
	if iteratePreferred && retryCount < 2 {
		return "iterate"
	}

	return "escalate"
}

func isAcceptVerdict(verdict string) bool {
	switch verdict {
	case "accept", "accepted", "approved", "pass", "passed", "ok":
		return true
	default:
		return false
	}
}

func isIterateVerdict(verdict string) bool {
	switch verdict {
	case "iterate", "retry", "changes_requested", "rework", "revise", "fix":
		return true
	default:
		return false
	}
}

func retryBranchName(base string, retry int) string {
	clean := strings.TrimSpace(base)
	if clean == "" {
		clean = fmt.Sprintf("retry-%d", time.Now().Unix())
	}
	return fmt.Sprintf("%s-r%d", clean, retry)
}

func buildIterationCache(session launcher.Session, evaluation *llm.Evaluation, retry int) string {
	issues := make([]string, 0)
	if evaluation != nil {
		for _, issue := range evaluation.Issues {
			issues = append(issues, fmt.Sprintf("[%s] %s -> %s", issue.Severity, issue.Description, issue.Suggestion))
		}
	}

	return strings.TrimSpace(fmt.Sprintf(
		"Reintento #%d solicitado por evaluator para session %s.\nResumen: %s\nIssues:\n- %s",
		retry,
		session.SessionID,
		safeEvalSummary(evaluation),
		strings.Join(issues, "\n- "),
	))
}

func safeEvalSummary(evaluation *llm.Evaluation) string {
	if evaluation == nil {
		return "sin evaluacion"
	}
	if strings.TrimSpace(evaluation.Summary) != "" {
		return strings.TrimSpace(evaluation.Summary)
	}
	return strings.TrimSpace(evaluation.Verdict)
}

func firstAvailableConsultant(consultants []llm.ConsultantClient) llm.ConsultantClient {
	for _, consultant := range consultants {
		if consultant != nil && consultant.IsAvailable() {
			return consultant
		}
	}
	return nil
}

func (e *Evaluator) escalateToUser(ctx context.Context, projectID int64, taskID int64, workerID *int64, reason string) error {
	if e.db == nil {
		return nil
	}

	status := state.TaskStatusBlocked
	if taskID > 0 {
		if err := e.db.UpdateTask(ctx, taskID, state.TaskUpdate{Status: &status}); err != nil {
			return fmt.Errorf("mark task %d blocked: %w", taskID, err)
		}
	}

	if projectID > 0 {
		projectStatus := state.ProjectStatusNeedsInput
		if err := e.db.UpdateProjectStatus(ctx, projectID, projectStatus); err != nil && !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("mark project %d needs_input: %w", projectID, err)
		}
	}

	if projectID > 0 {
		description := fmt.Sprintf("Evaluator escalated task %d for user input: %s", taskID, strings.TrimSpace(reason))
		if err := e.db.AppendEvent(ctx, state.Event{
			ProjectID:   projectID,
			WorkerID:    workerID,
			EventType:   "input_needed",
			Description: description,
		}); err != nil {
			return fmt.Errorf("append escalation event for project %d: %w", projectID, err)
		}
	}

	return nil
}

func optionalWorkerID(workerID int64) *int64 {
	if workerID <= 0 {
		return nil
	}
	ref := workerID
	return &ref
}

func escalationReason(evaluation *llm.Evaluation) string {
	if evaluation == nil {
		return "manual review required"
	}

	parts := make([]string, 0, 3)
	if verdict := strings.TrimSpace(evaluation.Verdict); verdict != "" {
		parts = append(parts, "verdict="+verdict)
	}
	if summary := strings.TrimSpace(evaluation.Summary); summary != "" {
		parts = append(parts, summary)
	}
	if len(evaluation.Issues) > 0 {
		parts = append(parts, fmt.Sprintf("%d issue(s) detected", len(evaluation.Issues)))
	}
	if len(parts) == 0 {
		return "manual review required"
	}

	return strings.Join(parts, "; ")
}

func (e *Evaluator) getRetryCount(taskID int64) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.retries[taskID]
}

func (e *Evaluator) setRetryCount(taskID int64, retry int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.retries[taskID] = retry
}
