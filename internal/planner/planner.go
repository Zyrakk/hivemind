package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zyrakk/hivemind/internal/evaluator"
	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/state"
)

const (
	plannerNeedsInputStatus = "needs_input"
	plannerReadyStatus      = "ready"
)

type plannerLLM interface {
	Plan(ctx context.Context, directive, agentsMD string) (*llm.TaskPlan, error)
}

type plannerLauncher interface {
	LaunchWorker(ctx context.Context, task launcher.Task, agentsMD string, cache string) (*launcher.Session, error)
	WorkerDone() <-chan launcher.Session
}

type planEvaluator interface {
	EvaluateWorkerOutput(ctx context.Context, session launcher.Session) (*evaluator.EvalResult, error)
	HandleWorkerCompletionDetailed(ctx context.Context, sessionID string) (*evaluator.CompletionResult, error)
}

type PlanResult struct {
	Plan           *llm.TaskPlan `json:"plan"`
	PlanID         string        `json:"plan_id"`
	Status         string        `json:"status"`
	NeedsInput     bool          `json:"needs_input"`
	ConsultantUsed bool          `json:"consultant_used"`
}

type plannedTask struct {
	Task     llm.Task
	DBTaskID int64
}

type runtimeTaskState struct {
	task     plannedTask
	key      string
	status   string
	session  string
	launched bool
}

type storedPlan struct {
	PlanID     string
	ProjectID  int64
	ProjectRef string
	AgentsMD   string
	Cache      string
	Tasks      []plannedTask
}

type Planner struct {
	glm         plannerLLM
	consultants []llm.ConsultantClient
	launcher    plannerLauncher
	evaluator   planEvaluator
	db          *state.Store
	promptsDir  string
	logger      *slog.Logger

	mu       sync.Mutex
	planByID map[string]storedPlan
}

func New(
	glm *llm.GLMClient,
	consultants []llm.ConsultantClient,
	launcher *launcher.Launcher,
	db *state.Store,
	promptsDir string,
	logger *slog.Logger,
) *Planner {
	return NewWithDeps(glm, consultants, launcher, db, promptsDir, logger)
}

func NewWithDeps(
	glm plannerLLM,
	consultants []llm.ConsultantClient,
	launcher plannerLauncher,
	db *state.Store,
	promptsDir string,
	logger *slog.Logger,
) *Planner {
	if strings.TrimSpace(promptsDir) == "" {
		promptsDir = "prompts"
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Planner{
		glm:         glm,
		consultants: consultants,
		launcher:    launcher,
		db:          db,
		promptsDir:  promptsDir,
		logger:      logger,
		planByID:    make(map[string]storedPlan),
	}
}

func (p *Planner) SetEvaluator(eval planEvaluator) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.evaluator = eval
}

func (p *Planner) BuildPlan(ctx context.Context, directive, agentsMD string) (*llm.TaskPlan, error) {
	if p == nil || p.glm == nil {
		return nil, llm.ErrNotImplemented
	}

	return p.glm.Plan(ctx, directive, agentsMD)
}

func (p *Planner) CreatePlan(ctx context.Context, directive string, projectID string) (*PlanResult, error) {
	if p == nil {
		return nil, fmt.Errorf("planner is nil")
	}
	if p.glm == nil {
		return nil, fmt.Errorf("glm client is not configured")
	}
	if strings.TrimSpace(directive) == "" {
		return nil, fmt.Errorf("directive is required")
	}

	agentsMD, err := readProjectAgents(projectID)
	if err != nil {
		return nil, err
	}
	cache := loadRelevantCache(projectID)

	combinedContext := strings.TrimSpace(agentsMD)
	if strings.TrimSpace(cache) != "" {
		combinedContext = combinedContext + "\n\nSession cache:\n" + cache
	}

	plan, err := p.glm.Plan(ctx, directive, combinedContext)
	if err != nil {
		return nil, fmt.Errorf("glm plan call failed: %w", err)
	}

	consultantUsed := false
	currentDirective := directive
	for i := 0; i < 2; i++ {
		if plan == nil || plan.Confidence >= 0.6 {
			break
		}

		consultant := firstAvailableConsultant(p.consultants)
		if consultant == nil {
			break
		}

		consultantUsed = true
		planJSON, marshalErr := json.Marshal(plan)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal plan for consultation: %w", marshalErr)
		}

		opinion, consultErr := consultant.Consult(
			ctx,
			"plan_validation",
			combinedContext,
			"Valida este plan: "+string(planJSON),
		)
		if consultErr != nil {
			p.logger.Warn("consultant validation failed", slog.Any("error", consultErr), slog.String("consultant", consultant.GetName()))
			break
		}
		if opinion == nil || opinion.AgreeWithOriginal {
			break
		}

		feedback := buildPlanFeedback(opinion)
		currentDirective = strings.TrimSpace(currentDirective + "\n\nConsultant feedback:\n" + feedback)

		refinedPlan, refinedErr := p.glm.Plan(ctx, currentDirective, combinedContext)
		if refinedErr != nil {
			return nil, fmt.Errorf("glm plan refinement failed: %w", refinedErr)
		}
		plan = refinedPlan
	}

	resolvedProjectID := int64(0)
	if p.db != nil {
		resolvedProjectID, err = p.db.ResolveProjectID(ctx, projectID)
		if err != nil {
			return nil, fmt.Errorf("resolve project id %q: %w", projectID, err)
		}
	}

	planID := generatePlanID()
	storedTasks, err := p.persistTasks(ctx, resolvedProjectID, plan)
	if err != nil {
		return nil, err
	}

	stored := storedPlan{
		PlanID:     planID,
		ProjectID:  resolvedProjectID,
		ProjectRef: projectID,
		AgentsMD:   agentsMD,
		Cache:      cache,
		Tasks:      storedTasks,
	}

	p.mu.Lock()
	p.planByID[planID] = stored
	p.mu.Unlock()

	needsInput := len(plan.Questions) > 0
	status := plannerReadyStatus
	if needsInput {
		status = plannerNeedsInputStatus
	}

	return &PlanResult{
		Plan:           plan,
		PlanID:         planID,
		Status:         status,
		NeedsInput:     needsInput,
		ConsultantUsed: consultantUsed,
	}, nil
}

func (p *Planner) ExecutePlan(ctx context.Context, planID string) error {
	if p == nil {
		return fmt.Errorf("planner is nil")
	}
	if p.launcher == nil {
		return fmt.Errorf("launcher is not configured")
	}

	p.mu.Lock()
	plan, ok := p.planByID[planID]
	planEvaluator := p.evaluator
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("plan %q not found", planID)
	}

	states := make(map[string]*runtimeTaskState, len(plan.Tasks))
	order := make([]string, 0, len(plan.Tasks))
	idToKey := make(map[string]string, len(plan.Tasks))

	for i := range plan.Tasks {
		key := canonicalTaskKey(plan.Tasks[i].Task, i)
		states[key] = &runtimeTaskState{task: plan.Tasks[i], key: key, status: state.TaskStatusPending}
		order = append(order, key)
		if taskID := strings.TrimSpace(plan.Tasks[i].Task.ID); taskID != "" {
			idToKey[taskID] = key
		}
	}

	sessionToTask := make(map[string]string)
	var mapMu sync.Mutex

	for {
		allDone := true
		for _, key := range order {
			s := states[key]
			if s.status != state.TaskStatusCompleted && s.status != state.TaskStatusFailed && s.status != state.TaskStatusBlocked {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}

		ready := make([]string, 0)
		inProgress := 0
		for _, key := range order {
			s := states[key]
			if s.status == state.TaskStatusInProgress {
				inProgress++
			}
			if s.status != state.TaskStatusPending || s.launched {
				continue
			}
			if dependenciesCompleted(s.task.Task.DependsOn, states, idToKey) {
				ready = append(ready, key)
			}
		}

		if len(ready) > 0 {
			errCh := make(chan error, len(ready))
			var wg sync.WaitGroup
			for _, key := range ready {
				key := key
				s := states[key]
				wg.Add(1)
				go func() {
					defer wg.Done()
					session, launchErr := p.launcher.LaunchWorker(ctx, launcher.Task{
						ProjectID:     plan.ProjectID,
						ID:            s.task.Task.ID,
						Title:         s.task.Task.Title,
						Description:   s.task.Task.Description,
						BranchName:    s.task.Task.BranchName,
						FilesAffected: append([]string(nil), s.task.Task.FilesAffected...),
					}, plan.AgentsMD, plan.Cache)
					if launchErr != nil {
						errCh <- fmt.Errorf("launch task %s: %w", key, launchErr)
						return
					}

					s.launched = true
					s.status = state.TaskStatusInProgress
					s.session = session.SessionID
					mapMu.Lock()
					sessionToTask[session.SessionID] = key
					mapMu.Unlock()

					if p.db != nil && s.task.DBTaskID > 0 {
						statusVal := state.TaskStatusInProgress
						update := state.TaskUpdate{Status: &statusVal}
						if session.WorkerID > 0 {
							workerID := session.WorkerID
							update.AssignedWorkerID = &workerID
							update.AssignedWorkerIDSet = true
						}
						if updateErr := p.db.UpdateTask(ctx, s.task.DBTaskID, update); updateErr != nil {
							if update.AssignedWorkerIDSet {
								statusOnly := state.TaskUpdate{Status: &statusVal}
								if retryErr := p.db.UpdateTask(ctx, s.task.DBTaskID, statusOnly); retryErr == nil {
									return
								}
							}
							errCh <- fmt.Errorf("update task %s in progress: %w", key, updateErr)
							return
						}
					}
				}()
			}
			wg.Wait()
			close(errCh)
			for launchErr := range errCh {
				if launchErr != nil {
					return launchErr
				}
			}
		}

		inProgress = 0
		for _, key := range order {
			if states[key].status == state.TaskStatusInProgress {
				inProgress++
			}
		}
		if inProgress == 0 {
			blocked := markBlockedByDependencies(states, order)
			if blocked > 0 {
				for _, key := range order {
					s := states[key]
					if s.status != state.TaskStatusBlocked || p.db == nil || s.task.DBTaskID <= 0 {
						continue
					}
					statusVal := state.TaskStatusBlocked
					_ = p.db.UpdateTask(ctx, s.task.DBTaskID, state.TaskUpdate{Status: &statusVal})
				}
				return fmt.Errorf("execution blocked by failed dependencies")
			}
		}

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case session := <-p.launcher.WorkerDone():
				mapMu.Lock()
				taskKey, ok := sessionToTask[session.SessionID]
				mapMu.Unlock()
				if !ok {
					continue
				}

				s := states[taskKey]
				if s == nil || s.status != state.TaskStatusInProgress {
					continue
				}

				if planEvaluator != nil {
					var persistStatus *string
					if session.Status == state.WorkerStatusCompleted {
						evalResult, evalErr := planEvaluator.EvaluateWorkerOutput(ctx, session)
						if evalErr != nil {
							return fmt.Errorf("evaluate worker output for task %s: %w", taskKey, evalErr)
						}
						if evalResult == nil {
							return fmt.Errorf("evaluate worker output for task %s returned nil result", taskKey)
						}

						action := strings.ToLower(strings.TrimSpace(evalResult.Action))
						switch action {
						case "accept":
							status := state.TaskStatusCompleted
							s.status = status
							persistStatus = &status
						case "iterate":
							nextSessionID := strings.TrimSpace(evalResult.NextSessionID)
							if nextSessionID == "" {
								return fmt.Errorf("evaluator iterate action for task %s missing next session id", taskKey)
							}
							s.status = state.TaskStatusInProgress
							s.session = nextSessionID
							mapMu.Lock()
							sessionToTask[nextSessionID] = taskKey
							mapMu.Unlock()
							goto continueOuter
						case "escalate":
							status := state.TaskStatusBlocked
							s.status = status
							persistStatus = &status
						default:
							return fmt.Errorf("unsupported evaluator action %q for task %s", evalResult.Action, taskKey)
						}
					} else {
						completionResult, completionErr := planEvaluator.HandleWorkerCompletionDetailed(ctx, session.SessionID)
						if completionErr != nil {
							return fmt.Errorf("handle worker completion for task %s: %w", taskKey, completionErr)
						}
						if completionResult == nil {
							status := state.TaskStatusBlocked
							s.status = status
							persistStatus = &status
							goto continueOuter
						}

						action := strings.ToLower(strings.TrimSpace(completionResult.Action))
						switch action {
						case "iterate":
							nextSessionID := strings.TrimSpace(completionResult.NextSessionID)
							if nextSessionID == "" {
								return fmt.Errorf("completion iterate action for task %s missing next session id", taskKey)
							}
							s.status = state.TaskStatusInProgress
							s.session = nextSessionID
							mapMu.Lock()
							sessionToTask[nextSessionID] = taskKey
							mapMu.Unlock()
							goto continueOuter
						case "accept":
							status := state.TaskStatusCompleted
							s.status = status
							persistStatus = &status
						case "escalate":
							status := state.TaskStatusBlocked
							s.status = status
							persistStatus = &status
						default:
							status := state.TaskStatusBlocked
							s.status = status
							persistStatus = &status
						}
					}
					if persistStatus != nil && p.db != nil && s.task.DBTaskID > 0 {
						update := state.TaskUpdate{Status: persistStatus}
						if updateErr := p.db.UpdateTask(ctx, s.task.DBTaskID, update); updateErr != nil {
							return fmt.Errorf("update task %s completion: %w", taskKey, updateErr)
						}
					}
					goto continueOuter
				}

				statusVal := state.TaskStatusCompleted
				if session.Status != state.WorkerStatusCompleted {
					statusVal = state.TaskStatusFailed
				}
				s.status = statusVal

				if p.db != nil && s.task.DBTaskID > 0 {
					update := state.TaskUpdate{Status: &statusVal}
					if updateErr := p.db.UpdateTask(ctx, s.task.DBTaskID, update); updateErr != nil {
						return fmt.Errorf("update task %s completion: %w", taskKey, updateErr)
					}
				}

				goto continueOuter
			}
		}

	continueOuter:
		continue
	}

	failed := make([]string, 0)
	for _, key := range order {
		if states[key].status == state.TaskStatusFailed {
			failed = append(failed, key)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("plan execution completed with failed tasks: %s", strings.Join(failed, ", "))
	}

	return nil
}

func (p *Planner) persistTasks(ctx context.Context, projectID int64, plan *llm.TaskPlan) ([]plannedTask, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan is nil")
	}

	result := make([]plannedTask, 0, len(plan.Tasks))
	for i, task := range plan.Tasks {
		entry := plannedTask{Task: task}
		if p.db != nil {
			dependsPayload, err := json.Marshal(task.DependsOn)
			if err != nil {
				return nil, fmt.Errorf("marshal task dependencies for task %q: %w", task.ID, err)
			}

			taskRecord := state.Task{
				ProjectID:   projectID,
				Title:       fallbackTaskTitle(task, i),
				Description: task.Description,
				Status:      state.TaskStatusPending,
				Priority:    complexityToPriority(task.Complexity),
				DependsOn:   string(dependsPayload),
			}

			taskID, createErr := p.db.CreateTask(ctx, taskRecord)
			if createErr != nil {
				return nil, fmt.Errorf("create db task %q: %w", task.ID, createErr)
			}
			entry.DBTaskID = taskID
		}

		result = append(result, entry)
	}

	return result, nil
}

func fallbackTaskTitle(task llm.Task, idx int) string {
	title := strings.TrimSpace(task.Title)
	if title != "" {
		return title
	}
	if strings.TrimSpace(task.ID) != "" {
		return "Task " + strings.TrimSpace(task.ID)
	}
	return fmt.Sprintf("Task %d", idx+1)
}

func complexityToPriority(complexity string) int {
	switch strings.ToLower(strings.TrimSpace(complexity)) {
	case "high", "hard", "complex":
		return 5
	case "medium", "normal":
		return 3
	case "low", "easy", "simple":
		return 2
	default:
		return 3
	}
}

func dependenciesCompleted(dependsOn []string, states map[string]*runtimeTaskState, idToKey map[string]string) bool {
	for _, dep := range dependsOn {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}

		key := dep
		if mapped, ok := idToKey[dep]; ok {
			key = mapped
		}

		stateEntry, ok := states[key]
		if !ok || stateEntry.status != state.TaskStatusCompleted {
			return false
		}
	}

	return true
}

func markBlockedByDependencies(states map[string]*runtimeTaskState, order []string) int {
	blocked := 0
	statusByID := make(map[string]string)
	for _, key := range order {
		statusByID[key] = states[key].status
		if taskID := strings.TrimSpace(states[key].task.Task.ID); taskID != "" {
			statusByID[taskID] = states[key].status
		}
	}

	for _, key := range order {
		entry := states[key]
		if entry.status != state.TaskStatusPending {
			continue
		}
		for _, dep := range entry.task.Task.DependsOn {
			depStatus := statusByID[strings.TrimSpace(dep)]
			if depStatus == state.TaskStatusFailed || depStatus == state.TaskStatusBlocked {
				entry.status = state.TaskStatusBlocked
				blocked++
				break
			}
		}
	}

	return blocked
}

func canonicalTaskKey(task llm.Task, idx int) string {
	if id := strings.TrimSpace(task.ID); id != "" {
		return id
	}
	if title := strings.TrimSpace(task.Title); title != "" {
		return strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	}
	return fmt.Sprintf("task-%d", idx+1)
}

func firstAvailableConsultant(consultants []llm.ConsultantClient) llm.ConsultantClient {
	for _, consultant := range consultants {
		if consultant != nil && consultant.IsAvailable() {
			return consultant
		}
	}

	return nil
}

func buildPlanFeedback(opinion *llm.Opinion) string {
	if opinion == nil {
		return ""
	}

	lines := []string{}
	if strings.TrimSpace(opinion.Analysis) != "" {
		lines = append(lines, "Analysis: "+strings.TrimSpace(opinion.Analysis))
	}
	if len(opinion.Recommendations) > 0 {
		lines = append(lines, "Recommendations: "+strings.Join(opinion.Recommendations, "; "))
	}
	if len(opinion.RiskFlags) > 0 {
		lines = append(lines, "RiskFlags: "+strings.Join(opinion.RiskFlags, "; "))
	}

	return strings.Join(lines, "\n")
}

func readProjectAgents(projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", fmt.Errorf("projectID is required")
	}

	candidates := []string{
		filepath.Join("agents", projectID+".md"),
		filepath.Join("agents", strings.ToLower(projectID)+".md"),
	}
	if numeric, err := strconv.ParseInt(projectID, 10, 64); err == nil && numeric > 0 {
		candidates = append(candidates, filepath.Join("agents", fmt.Sprintf("%d.md", numeric)))
	}

	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err == nil {
			return strings.TrimSpace(string(data)), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read AGENTS file %s: %w", candidate, err)
		}
	}

	return "", fmt.Errorf("agents file not found for project %q", projectID)
}

func loadRelevantCache(projectID string) string {
	cacheDir := filepath.Join("sessions", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return ""
	}

	type cacheFile struct {
		path    string
		modTime time.Time
	}

	projectID = strings.ToLower(strings.TrimSpace(projectID))
	files := make([]cacheFile, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".md") || strings.Contains(name, "-context") {
			continue
		}
		if projectID != "" && !strings.Contains(name, projectID) {
			continue
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		files = append(files, cacheFile{path: filepath.Join(cacheDir, entry.Name()), modTime: info.ModTime()})
	}

	if len(files) == 0 {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(entry.Name())
			if !strings.HasSuffix(name, ".md") || strings.Contains(name, "-context") {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			files = append(files, cacheFile{path: filepath.Join(cacheDir, entry.Name()), modTime: info.ModTime()})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})
	if len(files) > 3 {
		files = files[:3]
	}

	chunks := make([]string, 0, len(files))
	for _, file := range files {
		data, readErr := os.ReadFile(file.path)
		if readErr != nil {
			continue
		}
		chunks = append(chunks, fmt.Sprintf("### %s\n%s", filepath.Base(file.path), strings.TrimSpace(string(data))))
	}

	return strings.TrimSpace(strings.Join(chunks, "\n\n"))
}

func generatePlanID() string {
	return fmt.Sprintf("plan-%d", time.Now().UnixNano())
}
