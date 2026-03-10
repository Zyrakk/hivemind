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

	"github.com/zyrakk/hivemind/internal/checklist"
	"github.com/zyrakk/hivemind/internal/engine"
	"github.com/zyrakk/hivemind/internal/evaluator"
	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/recon"
	"github.com/zyrakk/hivemind/internal/state"
	"gopkg.in/yaml.v3"
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
	SetTaskChecklists(taskID int64, checklists evaluator.TaskChecklists)
}

type plannerEngine interface {
	Think(ctx context.Context, req engine.ThinkRequest) (*engine.ThinkResult, error)
	Propose(ctx context.Context, req engine.ProposeRequest) (*engine.PlanResult, error)
	Rebuild(ctx context.Context, req engine.RebuildRequest) (*engine.PlanResult, error)
	ActiveEngine(ctx context.Context) string
}

type plannerRecon interface {
	RunDefault(ctx context.Context, repoPath string) (*recon.Result, error)
	Run(ctx context.Context, commands []string) (*recon.Result, error)
	RunInDir(ctx context.Context, dir string, commands []string) (*recon.Result, error)
}

type notifier interface {
	NotifyNeedsInput(ctx context.Context, projectID, question, approvalID string) error
	NotifyNeedsInputWithChecks(ctx context.Context, projectID, taskTitle, approvalID string, checks []checklist.CheckResult) error
	NotifyWorkerFailed(ctx context.Context, projectID, taskTitle, errMsg string) error
	NotifyTaskCompleted(ctx context.Context, projectID, taskTitle string) error
	NotifyProgress(ctx context.Context, project, taskID, stage, detail string) error
}

type PlanResult struct {
	Plan           *llm.TaskPlan `json:"plan"`
	PlanID         string        `json:"plan_id"`
	Status         string        `json:"status"`
	NeedsInput     bool          `json:"needs_input"`
	ConsultantUsed bool          `json:"consultant_used"`
	Engine         string        `json:"engine,omitempty"`
}

type plannedTask struct {
	Task     llm.Task `json:"task"`
	DBTaskID int64    `json:"db_task_id"`
}

type runtimeTaskState struct {
	task     plannedTask
	key      string
	status   string
	session  string
	launched bool
}

type storedPlan struct {
	PlanID     string        `json:"plan_id"`
	ProjectID  int64         `json:"project_id"`
	ProjectRef string        `json:"project_ref"`
	Directive  string        `json:"directive"`
	AgentsMD   string        `json:"agents_md"`
	Cache      string        `json:"cache"`
	Plan       *llm.TaskPlan `json:"plan"`
	Engine     string        `json:"engine"`
	Tasks      []plannedTask `json:"tasks"`
}

type Planner struct {
	glm         plannerLLM
	consultants []llm.ConsultantClient
	launcher    plannerLauncher
	evaluator   planEvaluator
	notifier    notifier
	engine      plannerEngine
	recon       plannerRecon
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

func (p *Planner) SetNotifier(n notifier) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.notifier = n
}

func (p *Planner) SetEngine(e plannerEngine) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.engine = e
}

func (p *Planner) SetRecon(r plannerRecon) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.recon = r
}

func (p *Planner) notifyProgress(ctx context.Context, project, taskID, stage, detail string) {
	p.mu.Lock()
	n := p.notifier
	p.mu.Unlock()
	if n == nil {
		return
	}
	_ = n.NotifyProgress(ctx, project, taskID, stage, detail)
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
	if strings.TrimSpace(directive) == "" {
		return nil, fmt.Errorf("directive is required")
	}

	cleanedDirective, valErr := ValidateDirective(directive)
	if valErr != nil {
		return nil, valErr
	}
	directive = cleanedDirective

	agentsMD, err := readProjectAgents(projectID)
	if err != nil {
		return nil, err
	}
	cache := loadRelevantCache(projectID)

	p.mu.Lock()
	eng := p.engine
	rc := p.recon
	p.mu.Unlock()

	var (
		plan       *llm.TaskPlan
		engineName = "glm"
	)

	if eng != nil {
		enginePlan, engineErr := p.createPlanViaEngine(ctx, eng, rc, directive, projectID, agentsMD, cache)
		if engineErr == nil {
			plan = convertEnginePlanToLLMPlan(enginePlan)
			engineName = resolvePlanEngineName(ctx, eng)
		} else {
			if isEngineNeedsInputError(engineErr) {
				return nil, engineErr
			}
			p.logger.Warn("engine planning failed, falling back to GLM",
				slog.String("error", engineErr.Error()))
		}
	}

	if plan == nil {
		if p.glm == nil {
			return nil, fmt.Errorf("glm client is not configured")
		}

		combinedContext := strings.TrimSpace(agentsMD)
		if strings.TrimSpace(cache) != "" {
			combinedContext = combinedContext + "\n\nSession cache:\n" + cache
		}

		plan, err = p.glm.Plan(ctx, directive, combinedContext)
		if err != nil {
			return nil, fmt.Errorf("glm plan call failed: %w", err)
		}
		engineName = "glm"
	}

	return p.finalizePlan(ctx, directive, projectID, agentsMD, cache, plan, engineName)
}

func (p *Planner) RebuildPlan(ctx context.Context, planID, feedback string) (*PlanResult, error) {
	if p == nil {
		return nil, fmt.Errorf("planner is nil")
	}

	p.mu.Lock()
	eng := p.engine
	rc := p.recon
	stored, ok := p.planByID[planID]
	p.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("plan %s not found", planID)
	}
	if eng == nil {
		return nil, fmt.Errorf("no engine configured for rebuild")
	}

	var reconData string
	if rc != nil {
		repoPath := resolveRepoPath(stored.ProjectRef)
		if repoPath != "" {
			reconResult, _ := rc.RunDefault(ctx, repoPath)
			if reconResult != nil {
				reconData = reconResult.Output
			}
		}
	}

	rebuildReq := engine.RebuildRequest{
		PreviousPlan: convertStoredPlanToEnginePlan(stored),
		Feedback:     strings.TrimSpace(feedback),
		Directive:    stored.Directive,
		ProjectName:  stored.ProjectRef,
		AgentsMD:     stored.AgentsMD,
		ReconData:    reconData,
	}

	newPlan, err := eng.Rebuild(ctx, rebuildReq)
	if err != nil {
		return nil, fmt.Errorf("rebuild: %w", err)
	}

	return p.finalizePlan(
		ctx,
		stored.Directive,
		stored.ProjectRef,
		stored.AgentsMD,
		stored.Cache,
		convertEnginePlanToLLMPlan(newPlan),
		resolvePlanEngineName(ctx, eng),
	)
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
	planNotifier := p.notifier
	p.mu.Unlock()

	if !ok && p.db != nil {
		dbPlan, dbErr := p.db.GetPlan(ctx, planID)
		if dbErr != nil {
			return fmt.Errorf("plan %q not found in memory or DB: %w", planID, dbErr)
		}
		restored, deserErr := deserializeStoredPlan(dbPlan.PlanData)
		if deserErr != nil {
			return fmt.Errorf("deserialize plan %q: %w", planID, deserErr)
		}
		plan = restored
		// Cache it for subsequent lookups.
		p.mu.Lock()
		p.planByID[planID] = plan
		p.mu.Unlock()
		ok = true
	}
	if !ok {
		return fmt.Errorf("plan %q not found", planID)
	}

	// Mark plan as executing in DB.
	if p.db != nil {
		_ = p.db.UpdatePlanStatus(ctx, planID, state.PlanStatusExecuting)
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
		// Check for cancellation before evaluating next batch of tasks.
		select {
		case <-ctx.Done():
			if p.db != nil {
				_ = p.db.UpdatePlanStatus(ctx, planID, state.PlanStatusCancelled)
			}
			for _, key := range order {
				s := states[key]
				if s.status == state.TaskStatusPending {
					s.status = state.TaskStatusFailed
					if p.db != nil && s.task.DBTaskID > 0 {
						failedStatus := state.TaskStatusFailed
						_ = p.db.UpdateTask(ctx, s.task.DBTaskID, state.TaskUpdate{Status: &failedStatus})
					}
				}
			}
			return ctx.Err()
		default:
		}

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
					taskTitle := strings.TrimSpace(s.task.Task.Title)
					if taskTitle == "" {
						taskTitle = key
					}
					progressTaskID := s.task.Task.ID
					if s.task.DBTaskID > 0 {
						progressTaskID = strconv.FormatInt(s.task.DBTaskID, 10)
					}
					p.notifyProgress(ctx, plan.ProjectRef, progressTaskID, "launching", fmt.Sprintf("task %d/%d: %s", indexOf(order, key)+1, len(order), taskTitle))
					session, launchErr := p.launcher.LaunchWorker(ctx, launcher.Task{
						ProjectID:     plan.ProjectID,
						ProjectRef:    plan.ProjectRef,
						ID:            progressTaskID,
						Title:         s.task.Task.Title,
						Description:   effectiveWorkerPrompt(s.task.Task),
						BranchName:    s.task.Task.BranchName,
						FilesAffected: append([]string(nil), s.task.Task.FilesAffected...),
					}, plan.AgentsMD, plan.Cache)
					if launchErr != nil {
						if planNotifier != nil {
							taskTitle := strings.TrimSpace(s.task.Task.Title)
							if taskTitle == "" {
								taskTitle = key
							}
							_ = planNotifier.NotifyWorkerFailed(ctx, plan.ProjectRef, taskTitle, launchErr.Error())
						}
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
				if planNotifier != nil {
					_ = planNotifier.NotifyNeedsInput(ctx, plan.ProjectRef, "Plan requires a decision", planID)
				}
				return fmt.Errorf("execution blocked by failed dependencies")
			}
		}

		for {
			select {
			case <-ctx.Done():
				if p.db != nil {
					_ = p.db.UpdatePlanStatus(ctx, planID, state.PlanStatusCancelled)
				}
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
							if planNotifier != nil {
								_ = planNotifier.NotifyTaskCompleted(ctx, plan.ProjectRef, fallbackTaskTitle(s.task.Task, 0))
							}
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
							if planNotifier != nil {
								_ = planNotifier.NotifyNeedsInput(ctx, plan.ProjectRef, "Plan requires a decision", planID)
							}
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
							if planNotifier != nil {
								_ = planNotifier.NotifyNeedsInput(ctx, plan.ProjectRef, "Plan requires a decision", planID)
							}
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
							if planNotifier != nil {
								_ = planNotifier.NotifyTaskCompleted(ctx, plan.ProjectRef, fallbackTaskTitle(s.task.Task, 0))
							}
						case "escalate":
							status := state.TaskStatusBlocked
							s.status = status
							persistStatus = &status
							if planNotifier != nil {
								_ = planNotifier.NotifyNeedsInput(ctx, plan.ProjectRef, "Plan requires a decision", planID)
							}
						default:
							status := state.TaskStatusBlocked
							s.status = status
							persistStatus = &status
							if planNotifier != nil {
								_ = planNotifier.NotifyNeedsInput(ctx, plan.ProjectRef, "Plan requires a decision", planID)
							}
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
				if planNotifier != nil {
					taskTitle := fallbackTaskTitle(s.task.Task, 0)
					if statusVal == state.TaskStatusCompleted {
						_ = planNotifier.NotifyTaskCompleted(ctx, plan.ProjectRef, taskTitle)
					} else {
						errMsg := strings.TrimSpace(session.Error)
						if errMsg == "" {
							errMsg = "worker did not complete successfully"
						}
						_ = planNotifier.NotifyWorkerFailed(ctx, plan.ProjectRef, taskTitle, errMsg)
					}
				}

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
	blocked := make([]string, 0)
	for _, key := range order {
		switch states[key].status {
		case state.TaskStatusFailed:
			failed = append(failed, key)
		case state.TaskStatusBlocked:
			blocked = append(blocked, key)
		}
	}
	// Update plan status in DB based on outcome.
	if p.db != nil {
		if len(failed) > 0 || len(blocked) > 0 {
			_ = p.db.UpdatePlanStatus(ctx, planID, state.PlanStatusFailed)
		} else {
			_ = p.db.UpdatePlanStatus(ctx, planID, state.PlanStatusCompleted)
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("plan execution completed with failed tasks: %s", strings.Join(failed, ", "))
	}
	if len(blocked) > 0 {
		return fmt.Errorf("plan execution held: %d task(s) escalated and awaiting input: %s", len(blocked), strings.Join(blocked, ", "))
	}

	return nil
}

func (p *Planner) createPlanViaEngine(
	ctx context.Context,
	eng plannerEngine,
	rc plannerRecon,
	directive, projectID, agentsMD, cache string,
) (*engine.PlanResult, error) {
	repoPath := resolveRepoPath(projectID)
	var reconData string
        if rc != nil {
		if repoPath != "" {
			reconResult, err := rc.RunDefault(ctx, repoPath)
			if err != nil {
				p.logger.Warn("recon failed, proceeding without", slog.Any("error", err))
			} else {
				reconData = reconResult.Output
			}
		}
	}

	var thinkingHistory []engine.ThinkTurn
	thinkReq := engine.ThinkRequest{
		Directive:   directive,
		ProjectName: projectID,
		AgentsMD:    agentsMD,
		ReconData:   reconData,
		Cache:       cache,
	}

	var thinkingSummary string
	for i := 0; i < 5; i++ {
		thinkResult, err := eng.Think(ctx, thinkReq)
		if err != nil {
			return nil, fmt.Errorf("think turn %d: %w", i+1, err)
		}
		if thinkResult == nil {
			return nil, fmt.Errorf("think turn %d returned nil result", i+1)
		}

		thinkType := strings.ToLower(strings.TrimSpace(thinkResult.Type))
		// Notify planning progress for edit-in-place updates.
		p.notifyProgress(ctx, projectID, "", "planning-"+thinkType, directive)

		switch thinkType {
		case "ready":
			thinkingSummary = strings.TrimSpace(thinkResult.Summary)
			p.notifyProgress(ctx, projectID, "", "planning-generating plan...", directive)
			goto propose
		case "question":
			question := strings.TrimSpace(thinkResult.Question)
			thinkingHistory = append(thinkingHistory, engine.ThinkTurn{
				Role:    "engine",
				Content: question,
			})
			return nil, fmt.Errorf("engine needs input: %s", question)
		case "info_request":
			commands := append([]string(nil), thinkResult.Commands...)
			thinkingHistory = append(thinkingHistory, engine.ThinkTurn{
				Role:    "engine",
				Content: "Requested: " + strings.Join(commands, ", "),
			})
			if rc == nil {
				return nil, fmt.Errorf("engine requested recon commands but recon is not configured")
			}
			infoResult, runErr := rc.RunInDir(ctx, repoPath, commands)
			if runErr != nil {
				return nil, fmt.Errorf("recon run: %w", runErr)
			}
			response := ""
			if infoResult != nil {
				response = infoResult.Output
			}
			thinkingHistory = append(thinkingHistory, engine.ThinkTurn{
				Role:    "recon",
				Content: response,
			})
					thinkReq = engine.ThinkRequest{
						Directive:        directive,
						ProjectName:      projectID,
						AgentsMD:         agentsMD,
						ReconData:        reconData,
						Cache:            cache,
						PreviousThinking: append([]engine.ThinkTurn(nil), thinkingHistory...),
						Response:         response,
					}
		default:
			return nil, fmt.Errorf("unknown think result type: %s", thinkResult.Type)
		}
	}

	thinkingSummary = "Max thinking iterations reached. Proceeding with available context."

propose:
	planResult, err := eng.Propose(ctx, engine.ProposeRequest{
		Directive:       directive,
		ProjectName:     projectID,
		AgentsMD:        agentsMD,
		ReconData:       reconData,
		ThinkingSummary: thinkingSummary,
		ThinkingHistory: thinkingHistory,
	})
	if err != nil {
		return nil, fmt.Errorf("propose: %w", err)
	}

	return planResult, nil
}

func (p *Planner) finalizePlan(
	ctx context.Context,
	directive, projectID, agentsMD, cache string,
	plan *llm.TaskPlan,
	engineName string,
) (*PlanResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan is nil")
	}

	combinedContext := strings.TrimSpace(agentsMD)
	if strings.TrimSpace(cache) != "" {
		combinedContext = combinedContext + "\n\nSession cache:\n" + cache
	}

	consultantUsed := false
	currentDirective := directive
	engineName = normalizePlanEngineName(engineName)
	for i := 0; i < 2; i++ {
		if plan.Confidence >= 0.6 {
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
		if p.glm == nil {
			p.logger.Warn("consultant requested plan refinement but glm is not configured")
			break
		}

		feedback := buildPlanFeedback(opinion)
		currentDirective = strings.TrimSpace(currentDirective + "\n\nConsultant feedback:\n" + feedback)

		refinedPlan, refinedErr := p.glm.Plan(ctx, currentDirective, combinedContext)
		if refinedErr != nil {
			return nil, fmt.Errorf("glm plan refinement failed: %w", refinedErr)
		}
		plan = refinedPlan
		engineName = "glm"
	}

	resolvedProjectID := int64(0)
	var err error
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
		Directive:  directive,
		AgentsMD:   agentsMD,
		Cache:      cache,
		Plan:       cloneTaskPlan(plan),
		Engine:     engineName,
		Tasks:      storedTasks,
	}

	// Persist to DB (source of truth).
	if p.db != nil {
		planJSON, marshalErr := serializeStoredPlan(stored)
		if marshalErr != nil {
			return nil, fmt.Errorf("serialize plan: %w", marshalErr)
		}
		if createErr := p.db.CreatePlan(ctx, resolvedProjectID, planID, directive, engineName, planJSON); createErr != nil {
			return nil, fmt.Errorf("persist plan: %w", createErr)
		}
	}

	// Keep in-memory cache for fast lookup within the same process lifetime.
	p.mu.Lock()
	p.planByID[planID] = stored
	p.mu.Unlock()

	needsInput := len(plan.Questions) > 0
	status := plannerReadyStatus
	if needsInput {
		status = plannerNeedsInputStatus
	}

	return &PlanResult{
		Plan:           cloneTaskPlan(plan),
		PlanID:         planID,
		Status:         status,
		NeedsInput:     needsInput,
		ConsultantUsed: consultantUsed,
		Engine:         engineName,
	}, nil
}

func (p *Planner) persistTasks(ctx context.Context, projectID int64, plan *llm.TaskPlan) ([]plannedTask, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan is nil")
	}

	p.mu.Lock()
	eval := p.evaluator
	p.mu.Unlock()

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

			// Register checklists with evaluator
			if eval != nil && (len(task.AutomatedChecklist) > 0 || len(task.UserChecklist) > 0) {
				cl := evaluator.TaskChecklists{}
				for _, ac := range task.AutomatedChecklist {
					cl.AutomatedChecklist = append(cl.AutomatedChecklist, evaluator.AutomatedCheck{
						ID:          ac.ID,
						Description: ac.Description,
						Command:     ac.Command,
						Type:        ac.Type,
					})
				}
				for _, uc := range task.UserChecklist {
					cl.UserChecklist = append(cl.UserChecklist, evaluator.UserCheck{
						ID:          uc.ID,
						Description: uc.Description,
					})
				}
				eval.SetTaskChecklists(taskID, cl)
			}
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

func effectiveWorkerPrompt(task llm.Task) string {
	if ep := strings.TrimSpace(task.ExecutionPrompt); ep != "" {
		return ep
	}
	return strings.TrimSpace(task.Description)
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
		filepath.Join("/app/agents", projectID+".md"),
		filepath.Join("/app/agents", strings.ToLower(projectID)+".md"),
		filepath.Join("agents", projectID+".md"),
		filepath.Join("agents", strings.ToLower(projectID)+".md"),
	}
	if numeric, err := strconv.ParseInt(projectID, 10, 64); err == nil && numeric > 0 {
		candidates = append(candidates, filepath.Join("agents", fmt.Sprintf("%d.md", numeric)))
		candidates = append(candidates, filepath.Join("/app/agents", fmt.Sprintf("%d.md", numeric)))
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

func convertEnginePlanToLLMPlan(result *engine.PlanResult) *llm.TaskPlan {
	if result == nil {
		return nil
	}

	tasks := make([]llm.Task, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		description := strings.TrimSpace(task.Description)
		if description == "" {
			description = strings.TrimSpace(task.Prompt)
		}

		executionPrompt := strings.TrimSpace(task.ExecutionPrompt)
		if executionPrompt == "" {
			executionPrompt = strings.TrimSpace(task.Prompt)
		}

		tasks = append(tasks, llm.Task{
			ID:                 strings.TrimSpace(task.ID),
			Title:              strings.TrimSpace(task.Title),
			Description:        description,
			DependsOn:          append([]string(nil), task.Dependencies...),
			Complexity:         complexityFromPriority(task.Priority),
			BranchName:         strings.TrimSpace(task.BranchName),
			Briefing:           strings.TrimSpace(task.Briefing),
			ExecutionPrompt:    executionPrompt,
			AutomatedChecklist: convertEngineChecks(task.AutomatedChecklist),
			UserChecklist:      convertEngineChecks(task.UserChecklist),
		})
	}

	return &llm.TaskPlan{
		Confidence: result.Confidence,
		Tasks:      tasks,
		Notes:      strings.TrimSpace(result.Summary),
	}
}

func convertEngineChecks(checks []engine.Check) []llm.CheckItem {
	if len(checks) == 0 {
		return nil
	}
	items := make([]llm.CheckItem, 0, len(checks))
	for _, c := range checks {
		items = append(items, llm.CheckItem{
			ID:          c.ID,
			Description: c.Description,
			Command:     c.Command,
			Type:        c.Type,
		})
	}
	return items
}

func convertStoredPlanToEnginePlan(plan storedPlan) *engine.PlanResult {
	result := &engine.PlanResult{}
	source := plan.Plan
	if source != nil {
		result.Confidence = source.Confidence
		result.Summary = strings.TrimSpace(source.Notes)
		for _, task := range source.Tasks {
			pt := engine.PlanTask{
				ID:           strings.TrimSpace(task.ID),
				Title:        strings.TrimSpace(task.Title),
				Description:  strings.TrimSpace(task.Description),
				BranchName:   strings.TrimSpace(task.BranchName),
				Dependencies: append([]string(nil), task.DependsOn...),
			}
			if strings.TrimSpace(task.Briefing) != "" {
				pt.Briefing = strings.TrimSpace(task.Briefing)
			}
			if strings.TrimSpace(task.ExecutionPrompt) != "" {
				pt.ExecutionPrompt = strings.TrimSpace(task.ExecutionPrompt)
			}
			pt.AutomatedChecklist = convertLLMChecksToEngine(task.AutomatedChecklist)
			pt.UserChecklist = convertLLMChecksToEngine(task.UserChecklist)
			result.Tasks = append(result.Tasks, pt)
		}
		return result
	}

	for _, task := range plan.Tasks {
		pt := engine.PlanTask{
			ID:           strings.TrimSpace(task.Task.ID),
			Title:        strings.TrimSpace(task.Task.Title),
			Description:  strings.TrimSpace(task.Task.Description),
			BranchName:   strings.TrimSpace(task.Task.BranchName),
			Dependencies: append([]string(nil), task.Task.DependsOn...),
		}
		if strings.TrimSpace(task.Task.Briefing) != "" {
			pt.Briefing = strings.TrimSpace(task.Task.Briefing)
		}
		if strings.TrimSpace(task.Task.ExecutionPrompt) != "" {
			pt.ExecutionPrompt = strings.TrimSpace(task.Task.ExecutionPrompt)
		}
		pt.AutomatedChecklist = convertLLMChecksToEngine(task.Task.AutomatedChecklist)
		pt.UserChecklist = convertLLMChecksToEngine(task.Task.UserChecklist)
		result.Tasks = append(result.Tasks, pt)
	}
	return result
}

func convertLLMChecksToEngine(items []llm.CheckItem) []engine.Check {
	if len(items) == 0 {
		return nil
	}
	checks := make([]engine.Check, 0, len(items))
	for _, item := range items {
		checks = append(checks, engine.Check{
			ID:          item.ID,
			Description: item.Description,
			Command:     item.Command,
			Type:        item.Type,
		})
	}
	return checks
}

func cloneTaskPlan(plan *llm.TaskPlan) *llm.TaskPlan {
	if plan == nil {
		return nil
	}

	cloned := &llm.TaskPlan{
		Confidence: plan.Confidence,
		Questions:  append([]string(nil), plan.Questions...),
		Notes:      plan.Notes,
		Tasks:      make([]llm.Task, 0, len(plan.Tasks)),
	}
	for _, task := range plan.Tasks {
		cloned.Tasks = append(cloned.Tasks, llm.Task{
			ID:                 task.ID,
			Title:              task.Title,
			Description:        task.Description,
			AcceptanceCriteria: append([]string(nil), task.AcceptanceCriteria...),
			FilesAffected:      append([]string(nil), task.FilesAffected...),
			DependsOn:          append([]string(nil), task.DependsOn...),
			Complexity:         task.Complexity,
			BranchName:         task.BranchName,
			Briefing:           task.Briefing,
			ExecutionPrompt:    task.ExecutionPrompt,
			AutomatedChecklist: cloneCheckItems(task.AutomatedChecklist),
			UserChecklist:      cloneCheckItems(task.UserChecklist),
		})
	}
	return cloned
}

func cloneCheckItems(items []llm.CheckItem) []llm.CheckItem {
	if items == nil {
		return nil
	}
	cloned := make([]llm.CheckItem, len(items))
	copy(cloned, items)
	return cloned
}

func resolveRepoPath(projectID string) string {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ""
	}

	candidates := make([]string, 0, 6)
	if filepath.IsAbs(projectID) {
		candidates = append(candidates, projectID)
	}
	candidates = append(candidates, projectID)

	if cwd, err := os.Getwd(); err == nil {
		base := filepath.Base(cwd)
		if strings.EqualFold(base, projectID) {
			candidates = append(candidates, cwd)
		}
	}

	if reposDir := configuredReposDir(); reposDir != "" {
		candidates = append(candidates,
			filepath.Join(reposDir, projectID),
			filepath.Join(reposDir, strings.ToLower(projectID)),
		)
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}

	return ""
}

func configuredReposDir() string {
	for _, envName := range []string{"HIVEMIND_REPOS_DIR", "CODEX_REPOS_DIR", "REPOS_DIR"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			return value
		}
	}

	configPath := strings.TrimSpace(os.Getenv("CONFIG_PATH"))
	if configPath == "" {
		configPath = "config.yaml"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}

	var cfg struct {
		Codex struct {
			ReposDir string `yaml:"repos_dir"`
		} `yaml:"codex"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ""
	}

	return strings.TrimSpace(cfg.Codex.ReposDir)
}

func complexityFromPriority(priority int) string {
	switch {
	case priority >= 4:
		return "high"
	case priority <= 1:
		return "low"
	default:
		return "medium"
	}
}

func isEngineNeedsInputError(err error) bool {
	return err != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(err.Error())), "engine needs input:")
}

func resolvePlanEngineName(ctx context.Context, eng plannerEngine) string {
	if eng == nil {
		return "glm"
	}

	type lastUsedEngineReporter interface {
		LastUsedEngine() string
	}

	if reporter, ok := eng.(lastUsedEngineReporter); ok {
		if name := strings.TrimSpace(reporter.LastUsedEngine()); name != "" {
			return normalizePlanEngineName(name)
		}
	}

	return normalizePlanEngineName(eng.ActiveEngine(ctx))
}

func normalizePlanEngineName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "none") {
		return "glm"
	}
	return name
}

func generatePlanID() string {
	return fmt.Sprintf("plan-%d", time.Now().UnixNano())
}

func serializeStoredPlan(sp storedPlan) ([]byte, error) {
	return json.Marshal(sp)
}

func deserializeStoredPlan(data []byte) (storedPlan, error) {
	var sp storedPlan
	err := json.Unmarshal(data, &sp)
	return sp, err
}

func indexOf(slice []string, item string) int {
	for i, v := range slice {
		if v == item {
			return i
		}
	}
	return 0
}
