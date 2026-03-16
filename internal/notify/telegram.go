package notify

import (
	"context"
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
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	directivepkg "github.com/zyrakk/hivemind/internal/directive"
	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/planner"
	"github.com/zyrakk/hivemind/internal/refiner"
	"github.com/zyrakk/hivemind/internal/state"
)

const (
	defaultApprovalsTTL      = 24 * time.Hour
	defaultPRApprovalTTL     = 48 * time.Hour
	defaultCleanupInterval   = 5 * time.Minute
	defaultOutboxBuffer      = 50
	defaultSendRatePerSecond = 25
)

var (
	ErrUnauthorizedChat   = errors.New("unauthorized chat")
	ErrApprovalNotFound   = errors.New("approval not found")
	ErrBotAlreadyStarted  = errors.New("telegram bot already started")
	ErrBotNotStarted      = errors.New("telegram bot not started")
	ErrNotifierNotRunning = errors.New("notifier not running")
)

type PendingApproval struct {
	ID          string
	Type        string // "plan" | "pr" | "input" | "roadmap"
	ProjectID   string
	Description string
	AcceptsText bool
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// RunHandle tracks a running plan execution, allowing cancellation from outside.
type RunHandle struct {
	Cancel context.CancelFunc
	Done   chan error
	PlanID string
}

type plannerExecutor interface {
	ExecutePlan(ctx context.Context, planID string) error
	ExecuteBatch(ctx context.Context, batchID string) error
}

// quotaTracker allows the TelegramBot to register a callback that fires
// when the usage tracker transitions from quota-blocked to unblocked.
type quotaTracker interface {
	OnResumeFromQuota(cb func())
}

type plannerCreator interface {
	CreatePlan(ctx context.Context, directive, projectID string) (*planner.PlanResult, error)
}

type plannerRebuilder interface {
	RebuildPlan(ctx context.Context, planID, feedback string) (*planner.PlanResult, error)
}

type plannerService interface {
	plannerExecutor
	plannerCreator
	plannerRebuilder
}

type roadmapPlanner interface {
	MetaPlan(ctx context.Context, projectRef, roadmap, feedback, cachedReconData string) (*planner.RoadmapResult, error)
}

type refinerService interface {
	Run(ctx context.Context, document, rubric, improvementPrompt string) (*refiner.RefinementResult, error)
}

type workerController interface {
	GetActiveWorkers() []launcher.WorkerProcess
	PauseWorker(sessionID string) error
	ResumeWorker(sessionID string) error
}

type stateStore interface {
	GetGlobalState(ctx context.Context) (state.GlobalState, error)
	GetProjectDetail(ctx context.Context, projectRef string) (state.ProjectDetail, error)
	ResolveProjectID(ctx context.Context, projectRef string) (int64, error)
	UpdateProjectStatus(ctx context.Context, projectID int64, status string) error
	UpdateProjectStatusByReference(ctx context.Context, projectRef, status string) error
	UpdateTask(ctx context.Context, taskID int64, update state.TaskUpdate) error
	UpdateWorker(ctx context.Context, workerID int64, update state.WorkerUpdate) error
	AppendEvent(ctx context.Context, event state.Event) error
	CreateBatch(ctx context.Context, projectID int64, name string, directives []string) (string, error)
	GetBatch(ctx context.Context, batchID string) (*state.Batch, error)
	GetBatchItems(ctx context.Context, batchID string) ([]state.BatchItem, error)
	UpdateBatchStatus(ctx context.Context, batchID, status string) error
	UpdateBatchItemStatus(ctx context.Context, itemID int64, status, planID, errorMsg string) error
	GetRunningBatches(ctx context.Context) ([]state.Batch, error)
	GetPausedBatches(ctx context.Context) ([]state.Batch, error)
	CreateBatchWithPhases(ctx context.Context, projectID int64, name string, directives, phases, phaseDependsOn []string) (string, error)
}

type TelegramBot struct {
	botToken      string
	allowedChatID int64
	bot           *tgbotapi.BotAPI

	pendingApprovals map[string]*PendingApproval
	approvalsMu      sync.RWMutex
	outbox           chan string
	logger           *slog.Logger

	plannerCreate  plannerCreator
	plannerRebuild plannerRebuilder
	store          stateStore
	plannerExec    plannerExecutor
	roadmapPlanner roadmapPlanner
	workers        workerController
	consultants    []llm.ConsultantClient
	refiner        refinerService
	promptDir      string

	inputResolver func(ctx context.Context, approval *PendingApproval, response string) error
	sendMessageFn func(text string) error

	nowFn            func() time.Time
	approvalsTTL     time.Duration
	prApprovalTTL    time.Duration
	cleanupInterval  time.Duration
	sendRatePerSec   int
	lastProjectRef   string
	lastProjectRefMu sync.RWMutex
	progressMu        sync.Mutex
	progressTimelines map[string]*ProgressTimeline // taskID -> timeline
	progressMsgIDs    map[string]int               // taskID -> Telegram message_id
	planningMsgIDs    map[string]int               // projectRef -> planning Telegram message_id
	editMessageFn     func(messageID int, text string) error
	activeRuns        map[string]*RunHandle // keyed by planID
	activeRunsMu      sync.Mutex

	pendingRoadmaps   map[string]*planner.RoadmapResult // keyed by roadmap ID
	pendingRoadmapsMu sync.RWMutex

	wg           sync.WaitGroup
	cancel       context.CancelFunc
	startStopMu  sync.Mutex
	started      atomic.Bool
	outboxClosed bool
}

func NewTelegramBot(
	botToken string,
	allowedChatID int64,
	svc plannerService,
	db *state.Store,
	logger *slog.Logger,
) *TelegramBot {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &TelegramBot{
		botToken:         botToken,
		allowedChatID:    allowedChatID,
		pendingApprovals: make(map[string]*PendingApproval),
		outbox:           make(chan string, defaultOutboxBuffer),
		logger:           logger,
		plannerCreate:    svc,
		plannerRebuild:   svc,
		store:            db,
		plannerExec:      svc,
		nowFn:            time.Now,
		approvalsTTL:     defaultApprovalsTTL,
		prApprovalTTL:    defaultPRApprovalTTL,
		cleanupInterval:  defaultCleanupInterval,
		sendRatePerSec:   defaultSendRatePerSecond,
		lastProjectRef:   "",
		inputResolver:    nil,
		sendMessageFn:    nil,
		outboxClosed:     false,
		activeRuns:       make(map[string]*RunHandle),
		pendingRoadmaps:  make(map[string]*planner.RoadmapResult),
		lastProjectRefMu: sync.RWMutex{},
		startStopMu:      sync.Mutex{},
		approvalsMu:      sync.RWMutex{},
	}
}

func (t *TelegramBot) SetWorkerController(workers workerController) {
	t.workers = workers
}

func (t *TelegramBot) SetConsultants(consultants []llm.ConsultantClient) {
	t.consultants = consultants
}

func (t *TelegramBot) SetRoadmapPlanner(rp roadmapPlanner) {
	if t == nil {
		return
	}
	t.roadmapPlanner = rp
}

func (t *TelegramBot) SetRefiner(r refinerService) {
	if t == nil {
		return
	}
	t.refiner = r
}

func (t *TelegramBot) SetPromptDir(dir string) {
	if t == nil {
		return
	}
	t.promptDir = dir
}

func (t *TelegramBot) SetInputResolver(resolver func(ctx context.Context, approval *PendingApproval, response string) error) {
	t.inputResolver = resolver
}

// SetUsageTracker registers a callback with the given quota tracker so that
// paused batches are automatically resumed when quota becomes available.
func (t *TelegramBot) SetUsageTracker(tracker quotaTracker) {
	if t == nil || tracker == nil {
		return
	}
	tracker.OnResumeFromQuota(func() {
		t.resumeQuotaPausedBatches()
	})
}

func (t *TelegramBot) resumeQuotaPausedBatches() {
	if t.store == nil {
		return
	}
	ctx := context.Background()
	batches, err := t.store.GetPausedBatches(ctx)
	if err != nil {
		t.logger.Warn("failed to get paused batches for quota resume", "error", err)
		return
	}
	for _, batch := range batches {
		projectRef := fmt.Sprintf("%d", batch.ProjectID)
		if detail, detailErr := t.store.GetProjectDetail(ctx, projectRef); detailErr == nil && detail.ProjectRef != "" {
			projectRef = detail.ProjectRef
		}
		if err := t.store.UpdateBatchStatus(ctx, batch.ID, state.BatchStatusRunning); err != nil {
			t.logger.Warn("failed to resume batch", "batchID", batch.ID, "error", err)
			continue
		}
		t.startBatchExecution(batch.ID, projectRef)
		_ = t.enqueueMessage(ctx, formatEscapedLines(
			fmt.Sprintf("▸ Quota restored — auto-resuming batch %s", batch.ID),
		))
	}
}

func (t *TelegramBot) Start(ctx context.Context) error {
	if t == nil {
		return fmt.Errorf("telegram bot is nil")
	}
	if strings.TrimSpace(t.botToken) == "" {
		return fmt.Errorf("telegram bot token is required")
	}
	if t.allowedChatID == 0 {
		return fmt.Errorf("allowed chat id is required")
	}

	t.startStopMu.Lock()
	defer t.startStopMu.Unlock()
	if t.started.Load() {
		return ErrBotAlreadyStarted
	}

	if t.logger == nil {
		t.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if t.pendingApprovals == nil {
		t.pendingApprovals = make(map[string]*PendingApproval)
	}
	if t.outbox == nil {
		t.outbox = make(chan string, defaultOutboxBuffer)
	}
	if t.plannerExec == nil {
		if svc, ok := t.plannerCreate.(plannerExecutor); ok {
			t.plannerExec = svc
		}
	}
	if t.plannerRebuild == nil {
		if svc, ok := t.plannerCreate.(plannerRebuilder); ok {
			t.plannerRebuild = svc
		}
	}
	if t.nowFn == nil {
		t.nowFn = time.Now
	}
	if t.approvalsTTL <= 0 {
		t.approvalsTTL = defaultApprovalsTTL
	}
	if t.prApprovalTTL <= 0 {
		t.prApprovalTTL = defaultPRApprovalTTL
	}
	if t.cleanupInterval <= 0 {
		t.cleanupInterval = defaultCleanupInterval
	}
	if t.sendRatePerSec <= 0 {
		t.sendRatePerSec = defaultSendRatePerSecond
	}
	if t.bot == nil {
		bot, err := tgbotapi.NewBotAPI(t.botToken)
		if err != nil {
			return fmt.Errorf("create telegram bot api client: %w", err)
		}
		t.bot = bot
	}

	runCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.outboxClosed = false
	t.started.Store(true)

	t.wg.Add(3)
	go t.runUpdateLoop(runCtx)
	go t.runOutboxSender(runCtx)
	go t.runApprovalCleanup(runCtx)

	t.logger.Info("telegram bot started", slog.Int64("allowed_chat_id", t.allowedChatID))
	return nil
}

func (t *TelegramBot) Stop() error {
	if t == nil {
		return nil
	}

	t.startStopMu.Lock()
	if !t.started.Load() {
		t.startStopMu.Unlock()
		return ErrBotNotStarted
	}

	cancel := t.cancel
	bot := t.bot
	if !t.outboxClosed && t.outbox != nil {
		close(t.outbox)
		t.outboxClosed = true
	}
	t.startStopMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if bot != nil {
		bot.StopReceivingUpdates()
	}
	t.wg.Wait()
	t.started.Store(false)
	t.logger.Info("telegram bot stopped")
	return nil
}

func (t *TelegramBot) NotifyNeedsInput(ctx context.Context, projectID, question, approvalID string) error {
	approvalID = normalizeApprovalID(approvalID)
	now := t.nowFn().UTC()

	approval := &PendingApproval{
		ID:          approvalID,
		Type:        "input",
		ProjectID:   strings.TrimSpace(projectID),
		Description: strings.TrimSpace(question),
		AcceptsText: true,
		CreatedAt:   now,
		ExpiresAt:   now.Add(t.approvalsTTL),
	}
	if err := t.upsertApproval(approval); err != nil {
		return err
	}

	return t.enqueueMessage(ctx, FormatNeedsInputMessage(projectID, question, approvalID))
}

func (t *TelegramBot) NotifyNeedsInputWithChecks(ctx context.Context, projectID, taskTitle, approvalID string, checks []CheckResult) error {
	approvalID = normalizeApprovalID(approvalID)
	now := t.nowFn().UTC()

	approval := &PendingApproval{
		ID:          approvalID,
		Type:        "input",
		ProjectID:   strings.TrimSpace(projectID),
		Description: strings.TrimSpace(taskTitle),
		AcceptsText: true,
		CreatedAt:   now,
		ExpiresAt:   now.Add(t.approvalsTTL),
	}
	if err := t.upsertApproval(approval); err != nil {
		return err
	}

	return t.enqueueMessage(ctx, FormatInputNeededWithChecks(projectID, taskTitle, approvalID, checks))
}

func (t *TelegramBot) NotifyPRReady(ctx context.Context, projectID, branch, approvalID string, autoResults []CheckResult, userChecks []UserCheck) error {
	approvalID = normalizeApprovalID(approvalID)
	now := t.nowFn().UTC()

	approval := &PendingApproval{
		ID:          approvalID,
		Type:        "pr",
		ProjectID:   strings.TrimSpace(projectID),
		Description: fmt.Sprintf("PR ready on branch %s", strings.TrimSpace(branch)),
		AcceptsText: false,
		CreatedAt:   now,
		ExpiresAt:   now.Add(t.prApprovalTTL),
	}
	if err := t.upsertApproval(approval); err != nil {
		return err
	}

	return t.enqueueMessage(ctx, FormatPRReadyMessage(projectID, branch, approvalID, autoResults, userChecks))
}

func (t *TelegramBot) NotifyWorkerFailed(ctx context.Context, projectID, taskTitle, errMsg string) error {
	return t.enqueueMessage(ctx, FormatWorkerFailedMessage(projectID, taskTitle, errMsg))
}

func (t *TelegramBot) NotifyTaskCompleted(ctx context.Context, projectID, taskTitle string) error {
	return t.enqueueMessage(ctx, FormatTaskCompletedMessage(projectID, taskTitle))
}

func (t *TelegramBot) NotifyConsultantUsed(ctx context.Context, consultantName, question, summary string) error {
	return t.enqueueMessage(ctx, FormatConsultantUsedMessage(consultantName, question, summary))
}

func (t *TelegramBot) NotifyBudgetWarning(ctx context.Context, consultantName string, percentUsed float64) error {
	return t.enqueueMessage(ctx, FormatBudgetWarningMessage(consultantName, percentUsed))
}

func (t *TelegramBot) NotifyProgress(ctx context.Context, project, taskID, stage, detail string) error {
	_ = ctx
	if t == nil || !t.started.Load() {
		return nil
	}

	taskID = strings.TrimSpace(taskID)

	// Handle planning progress: edit the stored planning message in place.
	if strings.HasPrefix(stage, "planning-") {
		planningStage := strings.TrimPrefix(stage, "planning-")
		if msgID, ok := t.getPlanningMsgID(project); ok && msgID > 0 {
			// detail carries the directive, planningStage carries the phase
			rendered := FormatPlanningProgress(project, detail, nil, planningStage)
			_ = t.editMessage(msgID, rendered)
		}
		return nil
	}

	if taskID == "" {
		// Fallback for callers without a taskID: send as one-off silent message
		return t.sendSilentMessage(FormatProgressMessage(project, stage, detail))
	}

	t.progressMu.Lock()
	if t.progressTimelines == nil {
		t.progressTimelines = make(map[string]*ProgressTimeline)
	}
	if t.progressMsgIDs == nil {
		t.progressMsgIDs = make(map[string]int)
	}

	tl, exists := t.progressTimelines[taskID]
	if !exists {
		tl = &ProgressTimeline{
			Project: strings.TrimSpace(project),
		}
		t.progressTimelines[taskID] = tl
	}

	// Extract title from "launching" detail (format: "task N/M: Title")
	if stage == "launching" {
		if idx := strings.Index(detail, ": "); idx >= 0 {
			tl.Title = detail[idx+2:]
		} else {
			tl.Title = detail
		}
	}

	// Extract branch from "worker-started" detail
	if stage == "worker-started" && strings.HasPrefix(detail, "branch: ") {
		tl.Branch = strings.TrimPrefix(detail, "branch: ")
	}

	// Mark previous active entries as done
	for i := range tl.Entries {
		if tl.Entries[i].Status == ProgressStatusActive {
			tl.Entries[i].Status = ProgressStatusDone
		}
	}

	// Determine status for new entry
	newStatus := ProgressStatusActive
	isTerminal := false
	displayStage := stage
	displayDetail := detail

	switch stage {
	case "worker-started":
		displayStage = "worker started"
	case "codex-executing":
		displayStage = "codex executing"
	case "worker-completed":
		displayStage = "worker completed"
	case "push-successful":
		displayStage = "pushed to origin"
	case "push-failed":
		displayStage = "push failed"
		newStatus = ProgressStatusFailed
	case "evaluation-done":
		displayStage = "evaluation: " + detail
		displayDetail = "" // already included in stage name
		if strings.HasPrefix(detail, "iterate") {
			newStatus = ProgressStatusFailed
		} else {
			newStatus = ProgressStatusDone
		}
		isTerminal = detail == "accept" || detail == "escalate"
	}

	tl.Entries = append(tl.Entries, ProgressEntry{
		Stage:  displayStage,
		Detail: displayDetail,
		Status: newStatus,
		Time:   t.nowFn(),
	})

	rendered := RenderProgressTimeline(tl)
	msgID, hasMsgID := t.progressMsgIDs[taskID]
	t.progressMu.Unlock()

	if !hasMsgID {
		// First message for this task — send new
		newMsgID, err := t.sendAndTrackMessage(rendered)
		if err != nil {
			return err
		}
		t.progressMu.Lock()
		t.progressMsgIDs[taskID] = newMsgID
		t.progressMu.Unlock()
	} else {
		// Edit existing message
		if err := t.editMessage(msgID, rendered); err != nil {
			// Fallback: send new message if edit fails
			t.logger.Warn("edit progress message failed, sending new",
				slog.String("task_id", taskID),
				slog.Any("error", err))
			newMsgID, sendErr := t.sendAndTrackMessage(rendered)
			if sendErr != nil {
				return sendErr
			}
			t.progressMu.Lock()
			t.progressMsgIDs[taskID] = newMsgID
			t.progressMu.Unlock()
		}
	}

	// Clean up on terminal state
	if isTerminal {
		t.progressMu.Lock()
		delete(t.progressTimelines, taskID)
		delete(t.progressMsgIDs, taskID)
		t.progressMu.Unlock()
	}

	return nil
}

func (t *TelegramBot) storePlanningMsgID(project string, msgID int) {
	t.progressMu.Lock()
	defer t.progressMu.Unlock()
	if t.planningMsgIDs == nil {
		t.planningMsgIDs = make(map[string]int)
	}
	if msgID > 0 {
		t.planningMsgIDs[project] = msgID
	}
}

func (t *TelegramBot) getPlanningMsgID(project string) (int, bool) {
	t.progressMu.Lock()
	defer t.progressMu.Unlock()
	id, ok := t.planningMsgIDs[project]
	return id, ok
}

func (t *TelegramBot) clearPlanningMsgID(project string) {
	t.progressMu.Lock()
	defer t.progressMu.Unlock()
	delete(t.planningMsgIDs, project)
}

func (t *TelegramBot) QueueMessage(message string) {
	if t == nil {
		return
	}
	if err := t.enqueueMessage(context.Background(), message); err != nil && !errors.Is(err, ErrNotifierNotRunning) {
		if t.logger != nil {
			t.logger.Warn("telegram queue message failed", slog.Any("error", err))
		}
	}
}

func (t *TelegramBot) NotifyEngineSwitch(from, to, reason string) {
	if t == nil {
		return
	}
	if err := t.enqueueMessage(context.Background(), FormatEngineSwitchMessage(from, to, reason)); err != nil && !errors.Is(err, ErrNotifierNotRunning) {
		if t.logger != nil {
			t.logger.Warn("engine switch notification failed", slog.Any("error", err))
		}
	}
}

func (t *TelegramBot) RegisterPendingApproval(approval PendingApproval) {
	if strings.TrimSpace(approval.ID) == "" {
		approval.ID = normalizeApprovalID("")
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = t.nowFn().UTC()
	}
	if approval.ExpiresAt.IsZero() {
		ttl := t.approvalsTTL
		if strings.EqualFold(approval.Type, "pr") {
			ttl = t.prApprovalTTL
		}
		if ttl <= 0 {
			ttl = defaultApprovalsTTL
		}
		approval.ExpiresAt = approval.CreatedAt.Add(ttl)
	}
	_ = t.upsertApproval(&approval)
}

func (t *TelegramBot) runUpdateLoop(ctx context.Context) {
	defer t.wg.Done()
	if t.bot == nil {
		return
	}

	updateCfg := tgbotapi.NewUpdate(0)
	updateCfg.Timeout = 30
	updates := t.bot.GetUpdatesChan(updateCfg)

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			t.handleUpdate(ctx, update)
		}
	}
}

func (t *TelegramBot) runOutboxSender(ctx context.Context) {
	defer t.wg.Done()

	interval := time.Second / time.Duration(t.sendRatePerSec)
	if interval <= 0 {
		interval = time.Second / defaultSendRatePerSecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-t.outbox:
			if !ok {
				return
			}
			if strings.TrimSpace(message) == "" {
				continue
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			if err := t.sendMessage(message); err != nil {
				t.logger.Warn("telegram send failed", slog.Any("error", err))
			}
		}
	}
}

func (t *TelegramBot) runApprovalCleanup(ctx context.Context) {
	defer t.wg.Done()

	ticker := time.NewTicker(t.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed := t.cleanupExpiredApprovals(t.nowFn().UTC())
			if removed > 0 {
				t.logger.Info("expired approvals removed", slog.Int("count", removed))
			}
		}
	}
}

func (t *TelegramBot) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	msg := inboundMessage(update)
	if msg == nil {
		return
	}

	if msg.Chat == nil || msg.Chat.ID != t.allowedChatID {
		username := ""
		if msg.From != nil {
			username = msg.From.UserName
		}
		chatID := int64(0)
		if msg.Chat != nil {
			chatID = msg.Chat.ID
		}
		t.logger.Warn("telegram unauthorized access",
			slog.Int64("chat_id", chatID),
			slog.String("username", username),
		)
		return
	}

	if msg.IsCommand() {
		response, err := t.handleCommand(ctx, msg.Command(), strings.TrimSpace(msg.CommandArguments()))
		if err != nil {
			t.logger.Warn("telegram command failed", slog.String("command", msg.Command()), slog.Any("error", err))
			response = EscapeMarkdownV2("Error: " + err.Error())
		}
		if strings.TrimSpace(response) != "" {
			_ = t.enqueueMessage(ctx, response)
		}
		return
	}

	response, err := t.handleFreeText(ctx, strings.TrimSpace(msg.Text))
	if err != nil {
		t.logger.Warn("telegram free text handling failed", slog.Any("error", err))
		response = EscapeMarkdownV2("Error: " + err.Error())
	}
	if strings.TrimSpace(response) != "" {
		_ = t.enqueueMessage(ctx, response)
	}
}

func inboundMessage(update tgbotapi.Update) *tgbotapi.Message {
	if update.Message != nil {
		return update.Message
	}
	if update.ChannelPost != nil {
		return update.ChannelPost
	}
	return nil
}

func parseBatchArgs(args string) (projectRef string, directives []string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", nil
	}

	lines := strings.Split(args, "\n")
	firstLine := strings.TrimSpace(lines[0])
	if firstLine == "" {
		return "", nil
	}

	parts := strings.SplitN(firstLine, " ", 2)
	projectRef = strings.TrimSpace(parts[0])

	if len(parts) > 1 {
		remaining := strings.TrimSpace(parts[1])
		if strings.Contains(remaining, "|") {
			for _, d := range strings.Split(remaining, "|") {
				d = strings.TrimSpace(d)
				if d != "" {
					directives = append(directives, d)
				}
			}
		} else if remaining != "" {
			directives = append(directives, remaining)
		}
	}

	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line != "" {
			directives = append(directives, line)
		}
	}

	return projectRef, directives
}

// parseRoadmapArgs extracts project and roadmap text from the args string.
// Format: /roadmap {project} {roadmap text...} (possibly multiline)
func parseRoadmapArgs(args string) (projectRef, roadmap string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", ""
	}

	lines := strings.Split(args, "\n")
	firstLine := strings.TrimSpace(lines[0])
	if firstLine == "" {
		return "", ""
	}

	parts := strings.SplitN(firstLine, " ", 2)
	projectRef = strings.TrimSpace(parts[0])

	var roadmapParts []string
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		roadmapParts = append(roadmapParts, strings.TrimSpace(parts[1]))
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line != "" {
			roadmapParts = append(roadmapParts, line)
		}
	}

	return projectRef, strings.Join(roadmapParts, "\n")
}

func (t *TelegramBot) cmdBatch(ctx context.Context, args string) (string, error) {
	projectRef, directives := parseBatchArgs(args)
	if projectRef == "" || len(directives) == 0 {
		return formatEscapedLines(
			"Usage: /batch {project} {directive 1} | {directive 2}",
			"Or multiline:",
			"  /batch {project}",
			"  directive 1",
			"  directive 2",
		), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	projectID, err := t.store.ResolveProjectID(ctx, projectRef)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Project '%s' not found.", projectRef)), nil
		}
		return "", err
	}

	var validationErrors []string
	cleaned := make([]string, 0, len(directives))
	for i, d := range directives {
		c, valErr := planner.ValidateDirective(d)
		if valErr != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("%d: %s", i+1, valErr.Error()))
		} else {
			cleaned = append(cleaned, c)
		}
	}

	if len(validationErrors) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("✗ %d of %d directives invalid:\n", len(validationErrors), len(directives)))
		for _, e := range validationErrors {
			sb.WriteString(fmt.Sprintf("  %s\n", e))
		}
		return formatEscapedLines(sb.String()), nil
	}

	batchID, err := t.store.CreateBatch(ctx, projectID, "", cleaned)
	if err != nil {
		return "", err
	}

	t.setLastProjectRef(projectRef)
	return FormatBatchCreatedMessage(projectRef, batchID, cleaned), nil
}

func (t *TelegramBot) cmdStartBatch(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /start_batch {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}

	if batch.Status != state.BatchStatusPending && batch.Status != state.BatchStatusPaused {
		return formatEscapedLines(fmt.Sprintf("✗ Batch is %s, cannot start.", batch.Status)), nil
	}

	if err := t.store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning); err != nil {
		return "", err
	}

	items, err := t.store.GetBatchItems(ctx, batchID)
	if err != nil {
		return "", err
	}

	// Resolve project ref for display.
	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	if detail, err := t.store.GetProjectDetail(ctx, projectRef); err == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	t.startBatchExecution(batchID, projectRef)

	return formatEscapedLines(fmt.Sprintf("▸ Batch started. Executing directive 1/%d...", len(items))), nil
}

func (t *TelegramBot) cmdCancelBatch(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /cancel_batch {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}

	if batch.Status == state.BatchStatusCompleted {
		return formatEscapedLines("✗ Batch already completed, cannot cancel."), nil
	}

	// Cancel active execution if running.
	t.activeRunsMu.Lock()
	handle, running := t.activeRuns[batchID]
	t.activeRunsMu.Unlock()
	if running {
		handle.Cancel()
		select {
		case <-handle.Done:
		case <-time.After(30 * time.Second):
			t.logger.Warn("batch cancellation timed out", "batchID", batchID)
		}
	}

	if err := t.store.UpdateBatchStatus(ctx, batchID, state.BatchStatusPaused); err != nil {
		return "", err
	}

	return formatEscapedLines(fmt.Sprintf("✓ Batch %s cancelled.", batchID)), nil
}

func (t *TelegramBot) cmdBatchStatus(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /batch_status {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}

	items, err := t.store.GetBatchItems(ctx, batchID)
	if err != nil {
		return "", err
	}

	// Resolve project ref from project ID for display.
	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	detail, detailErr := t.store.GetProjectDetail(ctx, projectRef)
	if detailErr == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	return FormatBatchStatusMessage(projectRef, batchID, batch.Status, batch.CompletedItems, batch.TotalItems, items), nil
}

func (t *TelegramBot) cmdRetry(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /retry {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}
	if batch.Status != state.BatchStatusPaused {
		return formatEscapedLines(fmt.Sprintf("✗ Batch is %s, not paused.", batch.Status)), nil
	}

	// Find last failed item.
	items, err := t.store.GetBatchItems(ctx, batchID)
	if err != nil {
		return "", err
	}
	var failedItem *state.BatchItem
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Status == state.BatchItemStatusFailed {
			failedItem = &items[i]
			break
		}
	}
	if failedItem == nil {
		return formatEscapedLines("✗ No failed item found to retry."), nil
	}

	// Reset failed item to pending.
	if err := t.store.UpdateBatchItemStatus(ctx, failedItem.ID, state.BatchItemStatusPending, "", ""); err != nil {
		return "", err
	}

	// Set batch to running.
	if err := t.store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning); err != nil {
		return "", err
	}

	// Resolve project ref.
	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	if detail, detailErr := t.store.GetProjectDetail(ctx, projectRef); detailErr == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	t.startBatchExecution(batchID, projectRef)

	return formatEscapedLines(fmt.Sprintf("▸ Retrying item %d...", failedItem.Sequence)), nil
}

func (t *TelegramBot) cmdSkip(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /skip {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}
	if batch.Status != state.BatchStatusPaused {
		return formatEscapedLines(fmt.Sprintf("✗ Batch is %s, not paused.", batch.Status)), nil
	}

	items, err := t.store.GetBatchItems(ctx, batchID)
	if err != nil {
		return "", err
	}
	var failedItem *state.BatchItem
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Status == state.BatchItemStatusFailed {
			failedItem = &items[i]
			break
		}
	}
	if failedItem == nil {
		return formatEscapedLines("✗ No failed item found to skip."), nil
	}

	if err := t.store.UpdateBatchItemStatus(ctx, failedItem.ID, state.BatchItemStatusSkipped, "", ""); err != nil {
		return "", err
	}

	if err := t.store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning); err != nil {
		return "", err
	}

	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	if detail, detailErr := t.store.GetProjectDetail(ctx, projectRef); detailErr == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	t.startBatchExecution(batchID, projectRef)

	return formatEscapedLines(fmt.Sprintf("⊘ Skipped item %d. Continuing...", failedItem.Sequence)), nil
}

func (t *TelegramBot) cmdResumeBatch(ctx context.Context, args string) (string, error) {
	batchID := strings.TrimSpace(args)
	if batchID == "" {
		return formatEscapedLines("Usage: /resume_batch {batch-id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	batch, err := t.store.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Batch '%s' not found.", batchID)), nil
		}
		return "", err
	}
	if batch.Status != state.BatchStatusPaused {
		return formatEscapedLines(fmt.Sprintf("✗ Batch is %s, not paused.", batch.Status)), nil
	}

	if err := t.store.UpdateBatchStatus(ctx, batchID, state.BatchStatusRunning); err != nil {
		return "", err
	}

	projectRef := fmt.Sprintf("%d", batch.ProjectID)
	if detail, detailErr := t.store.GetProjectDetail(ctx, projectRef); detailErr == nil && detail.ProjectRef != "" {
		projectRef = detail.ProjectRef
	}

	t.startBatchExecution(batchID, projectRef)

	return formatEscapedLines(fmt.Sprintf("▸ Resuming batch %s...", batchID)), nil
}

func (t *TelegramBot) cmdRoadmap(ctx context.Context, args string) (string, error) {
	projectRef, roadmapText := parseRoadmapArgs(args)
	if projectRef == "" || roadmapText == "" {
		return formatEscapedLines(
			"Usage: /roadmap {project} {roadmap text}",
			"Example: /roadmap flux Build a REST API with auth and metrics",
		), nil
	}
	if t.roadmapPlanner == nil {
		return formatEscapedLines("✗ Meta-planner is not configured. Requires claude-code engine."), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	if _, err := t.store.ResolveProjectID(ctx, projectRef); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return formatEscapedLines(fmt.Sprintf("✗ Project '%s' not found.", projectRef)), nil
		}
		return "", err
	}

	t.setLastProjectRef(projectRef)

	// Send initial progress message.
	_ = t.enqueueMessage(ctx, formatEscapedLines(fmt.Sprintf("▸ Analyzing roadmap for %s...", projectRef)))

	result, err := t.roadmapPlanner.MetaPlan(ctx, projectRef, roadmapText, "", "")
	if err != nil {
		return formatEscapedLines(fmt.Sprintf("✗ Roadmap analysis failed: %s", err.Error())), nil
	}

	// Store for approve/reject.
	t.pendingRoadmapsMu.Lock()
	t.pendingRoadmaps[result.ID] = result
	t.pendingRoadmapsMu.Unlock()

	// Register as pending approval so /pending shows it.
	t.RegisterPendingApproval(PendingApproval{
		ID:          result.ID,
		Type:        "roadmap",
		ProjectID:   projectRef,
		Description: roadmapText,
		AcceptsText: false,
	})

	return FormatRoadmapMessage(projectRef, result.ID, result.Phases, result.TotalDirectives, result.ValidDirectives), nil
}

func (t *TelegramBot) cmdApproveRoadmap(ctx context.Context, args string) (string, error) {
	roadmapID := strings.TrimSpace(args)
	if roadmapID == "" {
		return formatEscapedLines("Usage: /approve_roadmap {id}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	t.pendingRoadmapsMu.RLock()
	result, ok := t.pendingRoadmaps[roadmapID]
	t.pendingRoadmapsMu.RUnlock()

	if !ok {
		return formatEscapedLines(fmt.Sprintf("✗ Roadmap '%s' not found.", roadmapID)), nil
	}

	// Flatten phases, dropping invalid directives.
	directives, phaseNames, phaseDeps, dropped := planner.FlattenValidatedPhases(result.Phases)

	if len(directives) == 0 {
		return formatEscapedLines("✗ No valid directives to execute. All were flagged by L1 validation."), nil
	}

	projectID, err := t.store.ResolveProjectID(ctx, result.ProjectRef)
	if err != nil {
		return "", err
	}

	batchID, err := t.store.CreateBatchWithPhases(ctx, projectID, "roadmap", directives, phaseNames, phaseDeps)
	if err != nil {
		return "", err
	}

	// Clean up pending state.
	t.pendingRoadmapsMu.Lock()
	delete(t.pendingRoadmaps, roadmapID)
	t.pendingRoadmapsMu.Unlock()
	t.approvalsMu.Lock()
	delete(t.pendingApprovals, roadmapID)
	t.approvalsMu.Unlock()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✓ Roadmap approved. Batch created with %d directives", len(directives)))
	if dropped > 0 {
		sb.WriteString(fmt.Sprintf(" (%d flagged directives dropped)", dropped))
	}
	sb.WriteString(fmt.Sprintf(".\n\n/start_batch %s", batchID))

	return formatEscapedLines(sb.String()), nil
}

func (t *TelegramBot) cmdRejectRoadmap(ctx context.Context, args string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return formatEscapedLines("Usage: /reject_roadmap {id} {feedback}"), nil
	}

	roadmapID := strings.TrimSpace(parts[0])
	feedback := ""
	if len(parts) > 1 {
		feedback = strings.TrimSpace(parts[1])
	}
	if feedback == "" {
		return formatEscapedLines("Please provide feedback. Usage: /reject_roadmap {id} {feedback}"), nil
	}

	if t.roadmapPlanner == nil {
		return formatEscapedLines("✗ Meta-planner is not configured."), nil
	}

	t.pendingRoadmapsMu.RLock()
	prev, ok := t.pendingRoadmaps[roadmapID]
	t.pendingRoadmapsMu.RUnlock()

	if !ok {
		return formatEscapedLines(fmt.Sprintf("✗ Roadmap '%s' not found.", roadmapID)), nil
	}

	_ = t.enqueueMessage(ctx, formatEscapedLines(fmt.Sprintf("▸ Revising roadmap for %s...", prev.ProjectRef)))

	// Re-call MetaPlan with feedback + cached recon data (no re-running recon).
	revised, err := t.roadmapPlanner.MetaPlan(ctx, prev.ProjectRef, prev.Roadmap, feedback, prev.ReconData)
	if err != nil {
		return formatEscapedLines(fmt.Sprintf("✗ Roadmap revision failed: %s", err.Error())), nil
	}

	// Replace old roadmap with revised.
	t.pendingRoadmapsMu.Lock()
	delete(t.pendingRoadmaps, roadmapID)
	t.pendingRoadmaps[revised.ID] = revised
	t.pendingRoadmapsMu.Unlock()

	// Replace old approval.
	t.approvalsMu.Lock()
	delete(t.pendingApprovals, roadmapID)
	t.approvalsMu.Unlock()
	t.RegisterPendingApproval(PendingApproval{
		ID:          revised.ID,
		Type:        "roadmap",
		ProjectID:   prev.ProjectRef,
		Description: prev.Roadmap,
		AcceptsText: false,
	})

	return FormatRoadmapMessage(prev.ProjectRef, revised.ID, revised.Phases, revised.TotalDirectives, revised.ValidDirectives), nil
}

func (t *TelegramBot) startBatchExecution(batchID, projectRef string) {
	go func() {
		if t.plannerExec == nil {
			_ = t.enqueueMessage(context.Background(), formatEscapedLines(
				fmt.Sprintf("✗ Batch %s failed: planner is not configured", batchID),
			))
			return
		}

		runCtx, runCancel := context.WithCancel(context.Background())
		handle := &RunHandle{
			Cancel: runCancel,
			Done:   make(chan error, 1),
			PlanID: batchID, // reuse PlanID field for batch ID
		}

		t.activeRunsMu.Lock()
		t.activeRuns[batchID] = handle
		t.activeRunsMu.Unlock()

		defer func() {
			t.activeRunsMu.Lock()
			delete(t.activeRuns, batchID)
			t.activeRunsMu.Unlock()
			runCancel()
		}()

		err := t.plannerExec.ExecuteBatch(runCtx, batchID)
		handle.Done <- err

		t.handleBatchResult(batchID, projectRef, err)
	}()
}

func (t *TelegramBot) handleBatchResult(batchID, projectRef string, err error) {
	ctx := context.Background()

	switch {
	case err == nil:
		batch, _ := t.store.GetBatch(ctx, batchID)
		total := 0
		if batch != nil {
			total = batch.TotalItems
		}
		_ = t.enqueueMessage(ctx, FormatBatchCompletedMessage(projectRef, batchID, total))

	case errors.Is(err, context.Canceled):
		// Silent — user already sent /cancel_batch.

	default:
		var quotaErr *planner.ErrBatchPausedQuota
		var checkErr *planner.ErrBatchPausedChecklist
		var itemErr *planner.ErrBatchItemFailed
		var phaseErr *planner.ErrBatchPhaseDependency

		switch {
		case errors.As(err, &quotaErr):
			_ = t.enqueueMessage(ctx, formatEscapedLines(
				fmt.Sprintf("⏸ Batch %s paused: %s. Will auto-resume when quota resets.", batchID, quotaErr.Reason),
			))

		case errors.As(err, &checkErr):
			t.RegisterPendingApproval(PendingApproval{
				ID:          checkErr.PlanID,
				Type:        "plan",
				ProjectID:   projectRef,
				Description: fmt.Sprintf("batch %s item %d checklist", checkErr.BatchID, checkErr.ItemID),
				AcceptsText: false,
			})
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("⏸ Batch %s paused — item needs review:\n", batchID))
			for _, c := range checkErr.Checks {
				sb.WriteString(fmt.Sprintf("  • %s\n", c))
			}
			sb.WriteString(fmt.Sprintf("\n/approve %s", checkErr.PlanID))
			_ = t.enqueueMessage(ctx, formatEscapedLines(sb.String()))

		case errors.As(err, &itemErr):
			items, _ := t.store.GetBatchItems(ctx, batchID)
			seq := 0
			errMsg := "unknown error"
			for _, item := range items {
				if item.ID == itemErr.ItemID {
					seq = item.Sequence
					if item.Error != nil {
						errMsg = *item.Error
					}
					break
				}
			}
			_ = t.enqueueMessage(ctx, FormatBatchFailedMessage(projectRef, batchID, seq, errMsg))

		case errors.As(err, &phaseErr):
			_ = t.enqueueMessage(ctx, formatEscapedLines(
				fmt.Sprintf("⏸ Batch %s paused: phase %q has failed items %v.\n/skip %s or fix and /retry %s",
					batchID, phaseErr.Phase, phaseErr.FailedItems, batchID, batchID),
			))

		default:
			_ = t.enqueueMessage(ctx, formatEscapedLines(
				fmt.Sprintf("✗ Batch %s failed: %v", batchID, err),
			))
		}
	}
}

func (t *TelegramBot) handleCommand(ctx context.Context, command, args string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "status":
		return t.cmdStatus(ctx)
	case "project":
		return t.cmdProject(ctx, args)
	case "run":
		return t.cmdRun(ctx, args)
	case "approve":
		return t.cmdApprove(ctx, args)
	case "reject":
		return t.cmdReject(ctx, args)
	case "pause":
		return t.cmdPause(ctx, args)
	case "resume":
		return t.cmdResume(ctx, args)
	case "consult":
		return t.cmdConsult(ctx, args)
	case "batch":
		return t.cmdBatch(ctx, args)
	case "start_batch":
		return t.cmdStartBatch(ctx, args)
	case "cancel_batch":
		return t.cmdCancelBatch(ctx, args)
	case "batch_status":
		return t.cmdBatchStatus(ctx, args)
	case "resume_batch":
		return t.cmdResumeBatch(ctx, args)
	case "retry":
		return t.cmdRetry(ctx, args)
	case "skip":
		return t.cmdSkip(ctx, args)
	case "roadmap":
		return t.cmdRoadmap(ctx, args)
	case "approve_roadmap":
		return t.cmdApproveRoadmap(ctx, args)
	case "reject_roadmap":
		return t.cmdRejectRoadmap(ctx, args)
	case "refine":
		return formatEscapedLines("Send a .md file as a Telegram document with caption /refine to start refinement."), nil
	case "pending":
		return t.cmdPending(ctx), nil
	case "help":
		return FormatHelpMessage(), nil
	default:
		return formatEscapedLines("Unknown command. Use /help to see available commands."), nil
	}
}

func (t *TelegramBot) handleFreeText(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}

	// Fallback: Telegram doesn't create command entities for hyphenated
	// commands like /start-batch. Normalize hyphens to underscores and
	// re-dispatch as a regular command.
	if strings.HasPrefix(text, "/") {
		first := strings.SplitN(text, " ", 2)
		cmd := strings.TrimPrefix(first[0], "/")
		if strings.Contains(cmd, "-") {
			cmd = strings.ReplaceAll(cmd, "-", "_")
			args := ""
			if len(first) > 1 {
				args = strings.TrimSpace(first[1])
			}
			return t.handleCommand(ctx, cmd, args)
		}
	}

	approval := t.findLatestTextApproval()
	if approval == nil {
		return formatEscapedLines("Unknown command. Use /help to see available commands."), nil
	}

	if err := t.resolveInputApproval(ctx, approval, text); err != nil {
		return "", err
	}

	return formatEscapedLines(fmt.Sprintf("✓ Input registered for %s", approval.ProjectID)), nil
}

func (t *TelegramBot) cmdStatus(ctx context.Context) (string, error) {
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	global, err := t.store.GetGlobalState(ctx)
	if err != nil {
		return "", err
	}

	return FormatStatusMessage(global), nil
}

func (t *TelegramBot) cmdProject(ctx context.Context, args string) (string, error) {
	projectRef := strings.TrimSpace(args)
	if projectRef == "" {
		return formatEscapedLines("Usage: /project {name}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	detail, err := t.store.GetProjectDetail(ctx, projectRef)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(detail.ProjectRef) != "" {
		t.setLastProjectRef(detail.ProjectRef)
	} else {
		t.setLastProjectRef(projectRef)
	}

	return FormatProjectDetailMessage(detail), nil
}

func (t *TelegramBot) cmdRun(ctx context.Context, args string) (string, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return formatEscapedLines(
			"Usage: /run {project} {directive}",
			"Example: /run flux Implement health check endpoint",
		), nil
	}
	if t.plannerCreate == nil {
		return formatEscapedLines("✗ Planner is not configured."), nil
	}

	directive, projectRef, hasRouting := parseDirectiveRoutingFromArgs(args)
	if !hasRouting {
		parts := strings.SplitN(args, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			return formatEscapedLines("Need project and directive. Example: /run flux Add integration tests"), nil
		}
		projectRef = strings.TrimSpace(parts[0])
		directive = strings.TrimSpace(parts[1])
	}
	projectRef = strings.TrimSpace(projectRef)
	directive = strings.TrimSpace(directive)
	if projectRef == "" || directive == "" {
		return formatEscapedLines("Need project and directive. Example: /run flux Add integration tests"), nil
	}

	if t.store != nil {
		if _, err := t.store.GetProjectDetail(ctx, projectRef); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return formatEscapedLines(fmt.Sprintf("✗ Project '%s' not found.", projectRef)), nil
			}
			return "", err
		}
	}

	t.setLastProjectRef(projectRef)

	// Send box-drawing planning message and store its ID for edit-in-place.
	planningMsg := FormatPlanningMessage(projectRef, directive, "analyzing repository...")
	planningMsgID, _ := t.sendAndTrackMessage(planningMsg)
	t.storePlanningMsgID(projectRef, planningMsgID)

	planResult, err := t.plannerCreate.CreatePlan(ctx, directive, projectRef)
	t.clearPlanningMsgID(projectRef)
	if err != nil {
		var valErr *planner.ErrDirectiveInvalid
		if errors.As(err, &valErr) {
			return FormatInvalidDirectiveMessage(valErr.Reason), nil
		}
		return formatEscapedLines(fmt.Sprintf("✗ Plan creation failed: %s", err.Error())), nil
	}
	if planResult == nil || planResult.Plan == nil {
		return formatEscapedLines("✗ Plan creation failed: planner returned empty result."), nil
	}

	if planResult.NeedsInput {
		var sb strings.Builder
		sb.WriteString("? Plan requires your input:\n")
		for i, q := range planResult.Plan.Questions {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, strings.TrimSpace(q)))
		}
		sb.WriteString("\nReply with free text to provide answers.")

		t.RegisterPendingApproval(PendingApproval{
			ID:          planResult.PlanID,
			Type:        "input",
			ProjectID:   projectRef,
			Description: directive,
			AcceptsText: true,
		})

		return formatEscapedLines(sb.String()), nil
	}

	summary := FormatPlanMessage(projectRef, planResult)
	t.RegisterPendingApproval(PendingApproval{
		ID:          planResult.PlanID,
		Type:        "plan",
		ProjectID:   projectRef,
		Description: directive,
		AcceptsText: false,
	})

	return summary, nil
}

func (t *TelegramBot) cmdApprove(ctx context.Context, args string) (string, error) {
	approvalID := strings.TrimSpace(args)
	if approvalID == "" {
		return formatEscapedLines("Usage: /approve {id}"), nil
	}

	t.approvalsMu.RLock()
	approval, ok := t.pendingApprovals[approvalID]
	if !ok {
		t.approvalsMu.RUnlock()
		return formatEscapedLines(fmt.Sprintf("✗ Approval not found: %s", approvalID)), nil
	}
	copyApproval := *approval
	t.approvalsMu.RUnlock()

	switch strings.ToLower(strings.TrimSpace(copyApproval.Type)) {
	case "plan":
		t.approvalsMu.Lock()
		delete(t.pendingApprovals, approvalID)
		t.approvalsMu.Unlock()

		go func(approval PendingApproval) {
			if t.plannerExec == nil {
				_ = t.enqueueMessage(context.Background(), formatEscapedLines(
					fmt.Sprintf("✗ Plan %s failed: planner is not configured", approval.ID),
				))
				return
			}

			runCtx, runCancel := context.WithCancel(context.Background())
			handle := &RunHandle{
				Cancel: runCancel,
				Done:   make(chan error, 1),
				PlanID: approval.ID,
			}

			t.activeRunsMu.Lock()
			t.activeRuns[approval.ID] = handle
			t.activeRunsMu.Unlock()

			defer func() {
				t.activeRunsMu.Lock()
				delete(t.activeRuns, approval.ID)
				t.activeRunsMu.Unlock()
				runCancel()
			}()

			err := t.plannerExec.ExecutePlan(runCtx, approval.ID)
			handle.Done <- err

			if err != nil {
				errMsg := err.Error()
				// If the plan is held/escalated, send a HELD message instead of "failed"
				if strings.Contains(errMsg, "escalated and awaiting input") || strings.Contains(errMsg, "blocked by failed dependencies") {
					// HELD message is already sent by the planner/evaluator via NotifyNeedsInput.
					// Don't send a redundant "completed" or "failed" message.
					return
				}
				if runCtx.Err() != nil {
					// Plan was cancelled externally — don't send failure message.
					return
				}
				_ = t.enqueueMessage(context.Background(), formatEscapedLines(
					fmt.Sprintf("✗ Plan %s failed: %s", approval.ID, errMsg),
				))
				return
			}

			_ = t.enqueueMessage(context.Background(), FormatPlanCompletedMessage(approval.ProjectID))
		}(copyApproval)

		return FormatApprovedMessage(copyApproval.ProjectID), nil
	case "pr":
		if err := t.markPRApproval(ctx, &copyApproval, true, ""); err != nil {
			return "", err
		}
	case "input":
		if err := t.resolveInputWithoutText(ctx, &copyApproval); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("approval type %q is not supported", copyApproval.Type)
	}

	t.approvalsMu.Lock()
	delete(t.pendingApprovals, approvalID)
	t.approvalsMu.Unlock()

	return formatEscapedLines(fmt.Sprintf("✓ Approved: %s", copyApproval.Description)), nil
}

func (t *TelegramBot) cmdReject(ctx context.Context, args string) (string, error) {
	parts := strings.Fields(strings.TrimSpace(args))
	if len(parts) == 0 {
		return formatEscapedLines("Usage: /reject {id} {reason}"), nil
	}

	approvalID := parts[0]
	reason := "no reason provided"
	if len(parts) > 1 {
		reason = strings.TrimSpace(strings.Join(parts[1:], " "))
	}

	t.approvalsMu.RLock()
	approval, ok := t.pendingApprovals[approvalID]
	if !ok {
		t.approvalsMu.RUnlock()
		return formatEscapedLines(fmt.Sprintf("✗ Approval not found: %s", approvalID)), nil
	}
	copyApproval := *approval
	t.approvalsMu.RUnlock()

	var err error
	switch strings.ToLower(strings.TrimSpace(copyApproval.Type)) {
	case "plan":
		err = t.rejectPlan(ctx, &copyApproval, reason)
	case "pr":
		err = t.markPRApproval(ctx, &copyApproval, false, reason)
	case "input":
		err = t.rejectInput(ctx, &copyApproval, reason)
	default:
		err = fmt.Errorf("approval type %q is not supported", copyApproval.Type)
	}

	if err == nil {
		t.approvalsMu.Lock()
		delete(t.pendingApprovals, approvalID)
		t.approvalsMu.Unlock()
	}

	if err != nil {
		return "", err
	}

	return formatEscapedLines(fmt.Sprintf("✓ Rejected: %s", copyApproval.Description)), nil
}

func (t *TelegramBot) cmdPause(ctx context.Context, args string) (string, error) {
	projectRef := strings.TrimSpace(args)
	if projectRef == "" {
		return formatEscapedLines("Usage: /pause {project}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	detail, err := t.store.GetProjectDetail(ctx, projectRef)
	if err != nil {
		return "", err
	}

	stopped := 0
	for _, worker := range detail.Workers {
		if worker.Status != state.WorkerStatusRunning {
			continue
		}
		if t.workers != nil {
			if pauseErr := t.workers.PauseWorker(worker.SessionID); pauseErr == nil {
				stopped++
				continue
			}
		}

		status := state.WorkerStatusPaused
		if updateErr := t.store.UpdateWorker(ctx, worker.ID, state.WorkerUpdate{Status: &status}); updateErr != nil {
			t.logger.Warn("pause worker state update failed", slog.Int64("worker_id", worker.ID), slog.Any("error", updateErr))
			continue
		}
		stopped++
	}

	if err := t.store.UpdateProjectStatusByReference(ctx, projectRef, state.ProjectStatusPaused); err != nil {
		return "", err
	}
	t.setLastProjectRef(projectRef)

	return formatEscapedLines(fmt.Sprintf("‖ %s paused. %d workers stopped.", projectRef, stopped)), nil
}

func (t *TelegramBot) cmdResume(ctx context.Context, args string) (string, error) {
	projectRef := strings.TrimSpace(args)
	if projectRef == "" {
		return formatEscapedLines("Usage: /resume {project}"), nil
	}
	if t.store == nil {
		return "", fmt.Errorf("state store is not configured")
	}

	detail, err := t.store.GetProjectDetail(ctx, projectRef)
	if err != nil {
		return "", err
	}

	for _, worker := range detail.Workers {
		if worker.Status != state.WorkerStatusPaused {
			continue
		}
		if t.workers != nil {
			if resumeErr := t.workers.ResumeWorker(worker.SessionID); resumeErr == nil {
				continue
			}
		}
		status := state.WorkerStatusRunning
		if updateErr := t.store.UpdateWorker(ctx, worker.ID, state.WorkerUpdate{Status: &status}); updateErr != nil {
			t.logger.Warn("resume worker state update failed", slog.Int64("worker_id", worker.ID), slog.Any("error", updateErr))
		}
	}

	if err := t.store.UpdateProjectStatusByReference(ctx, projectRef, state.ProjectStatusWorking); err != nil {
		return "", err
	}
	t.setLastProjectRef(projectRef)

	return formatEscapedLines(fmt.Sprintf("▶ %s resumed.", projectRef)), nil
}

func (t *TelegramBot) cmdConsult(ctx context.Context, args string) (string, error) {
	question := strings.TrimSpace(args)
	if question == "" {
		return formatEscapedLines("Usage: /consult {question}"), nil
	}

	consultant := t.selectConsultant()
	if consultant == nil {
		return formatEscapedLines("‼ No consultants available."), nil
	}

	consultCtx, projectRef, err := t.buildConsultationContext(ctx)
	if err != nil {
		return "", err
	}

	opinion, err := consultant.Consult(ctx, "telegram_consult", consultCtx, question)
	if err != nil {
		return "", err
	}

	summary := "no response"
	if opinion != nil {
		summary = strings.TrimSpace(opinion.Analysis)
		if summary == "" {
			summary = strings.TrimSpace(strings.Join(opinion.Recommendations, "; "))
		}
	}

	if projectRef != "" {
		_ = t.recordConsultation(ctx, projectRef, consultant.GetName(), question, summary)
	}

	response := FormatConsultantUsedMessage(consultant.GetName(), question, summary)
	return response, nil
}

func (t *TelegramBot) cmdPending(ctx context.Context) string {
	_ = ctx
	now := t.nowFn().UTC()
	t.cleanupExpiredApprovals(now)
	return FormatPendingApprovalsMessage(t.listApprovals(), now)
}

func parseDirectiveRoutingFromArgs(args string) (directive, projectRef string, hasRouting bool) {
	return directivepkg.ParseRouting(args)
}

func (t *TelegramBot) executePlanApproval(ctx context.Context, approval *PendingApproval) error {
	if approval == nil {
		return ErrApprovalNotFound
	}
	if t.plannerExec == nil {
		return fmt.Errorf("planner is not configured")
	}
	return t.plannerExec.ExecutePlan(ctx, approval.ID)
}

func (t *TelegramBot) markPRApproval(ctx context.Context, approval *PendingApproval, approved bool, reason string) error {
	if approval == nil {
		return ErrApprovalNotFound
	}
	if t.store == nil {
		return fmt.Errorf("state store is not configured")
	}

	projectID, err := t.store.ResolveProjectID(ctx, approval.ProjectID)
	if err != nil {
		return err
	}

	eventType := "pr_approved"
	description := fmt.Sprintf("PR approved: %s", approval.Description)
	if !approved {
		eventType = "pr_rejected"
		description = fmt.Sprintf("PR rejected: %s. Reason: %s", approval.Description, strings.TrimSpace(reason))
	}

	if err := t.store.AppendEvent(ctx, state.Event{
		ProjectID:   projectID,
		EventType:   eventType,
		Description: description,
	}); err != nil {
		return err
	}

	status := state.ProjectStatusWorking
	if !approved {
		status = state.ProjectStatusBlocked
	}

	return t.store.UpdateProjectStatus(ctx, projectID, status)
}

func (t *TelegramBot) rejectPlan(ctx context.Context, approval *PendingApproval, reason string) error {
	if approval == nil {
		return ErrApprovalNotFound
	}
	if t.store == nil {
		return fmt.Errorf("state store is not configured")
	}

	projectID, err := t.store.ResolveProjectID(ctx, approval.ProjectID)
	if err != nil {
		return err
	}

	status := state.ProjectStatusBlocked
	if err := t.store.UpdateProjectStatus(ctx, projectID, status); err != nil {
		return err
	}

	detail, err := t.store.GetProjectDetail(ctx, approval.ProjectID)
	if err == nil {
		rejected := state.TaskStatusRejected
		for _, task := range detail.Tasks {
			if task.Status != state.TaskStatusPending && task.Status != state.TaskStatusInProgress && task.Status != state.TaskStatusBlocked {
				continue
			}
			if updateErr := t.store.UpdateTask(ctx, task.ID, state.TaskUpdate{Status: &rejected}); updateErr != nil {
				t.logger.Warn("failed to mark task as rejected",
					slog.Int64("task_id", task.ID),
					slog.Any("error", updateErr))
			}
		}

		cancelled := state.WorkerStatusCancelled
		for _, worker := range detail.Workers {
			if worker.Status != state.WorkerStatusRunning && worker.Status != state.WorkerStatusPaused && worker.Status != state.WorkerStatusBlocked {
				continue
			}
			if updateErr := t.store.UpdateWorker(ctx, worker.ID, state.WorkerUpdate{Status: &cancelled}); updateErr != nil {
				t.logger.Warn("failed to cancel worker on plan rejection",
					slog.Int64("worker_id", worker.ID),
					slog.Any("error", updateErr))
			}
		}
	}

	if err := t.store.AppendEvent(ctx, state.Event{
		ProjectID:   projectID,
		EventType:   "plan_rejected",
		Description: fmt.Sprintf("Plan rejected: %s. Reason: %s", approval.Description, strings.TrimSpace(reason)),
	}); err != nil {
		return err
	}

	if t.plannerRebuild == nil {
		return nil
	}

	rebuilt, rebuildErr := t.plannerRebuild.RebuildPlan(ctx, approval.ID, reason)
	if rebuildErr != nil {
		t.logger.Warn("plan rebuild failed after rejection",
			slog.String("plan_id", approval.ID),
			slog.Any("error", rebuildErr))
		return nil
	}
	if rebuilt == nil || rebuilt.Plan == nil {
		return nil
	}

	if err := t.store.UpdateProjectStatus(ctx, projectID, state.ProjectStatusWorking); err != nil {
		return err
	}

	t.RegisterPendingApproval(PendingApproval{
		ID:          rebuilt.PlanID,
		Type:        "plan",
		ProjectID:   approval.ProjectID,
		Description: approval.Description,
		AcceptsText: false,
	})

	return t.enqueueMessage(ctx, FormatPlanMessage(approval.ProjectID, rebuilt))
}

func (t *TelegramBot) resolveInputWithoutText(ctx context.Context, approval *PendingApproval) error {
	if approval == nil {
		return ErrApprovalNotFound
	}

	if t.store == nil {
		return nil
	}
	projectID, err := t.store.ResolveProjectID(ctx, approval.ProjectID)
	if err != nil {
		return err
	}

	if err := t.store.AppendEvent(ctx, state.Event{
		ProjectID:   projectID,
		EventType:   "input_approved",
		Description: fmt.Sprintf("Input approved: %s", approval.Description),
	}); err != nil {
		return err
	}

	return t.store.UpdateProjectStatus(ctx, projectID, state.ProjectStatusWorking)
}

func (t *TelegramBot) rejectInput(ctx context.Context, approval *PendingApproval, reason string) error {
	if approval == nil {
		return ErrApprovalNotFound
	}
	if t.store == nil {
		return nil
	}

	projectID, err := t.store.ResolveProjectID(ctx, approval.ProjectID)
	if err != nil {
		return err
	}

	if err := t.store.AppendEvent(ctx, state.Event{
		ProjectID:   projectID,
		EventType:   "input_rejected",
		Description: fmt.Sprintf("Input rejected: %s. Reason: %s", approval.Description, strings.TrimSpace(reason)),
	}); err != nil {
		return err
	}

	return t.store.UpdateProjectStatus(ctx, projectID, state.ProjectStatusBlocked)
}

func (t *TelegramBot) resolveInputApproval(ctx context.Context, approval *PendingApproval, response string) error {
	if approval == nil {
		return ErrApprovalNotFound
	}

	if t.inputResolver != nil {
		if err := t.inputResolver(ctx, approval, response); err != nil {
			return err
		}
	} else if t.store != nil {
		projectID, err := t.store.ResolveProjectID(ctx, approval.ProjectID)
		if err != nil {
			return err
		}
		if err := t.store.AppendEvent(ctx, state.Event{
			ProjectID:   projectID,
			EventType:   "input_response",
			Description: fmt.Sprintf("%s | response: %s", approval.Description, strings.TrimSpace(response)),
		}); err != nil {
			return err
		}
		if err := t.store.UpdateProjectStatus(ctx, projectID, state.ProjectStatusWorking); err != nil {
			return err
		}
	}

	t.approvalsMu.Lock()
	delete(t.pendingApprovals, approval.ID)
	t.approvalsMu.Unlock()
	return nil
}

func (t *TelegramBot) buildConsultationContext(ctx context.Context) (string, string, error) {
	if t.store == nil {
		agentsSummary, _ := t.loadAgentsSummary()
		return agentsSummary, "", nil
	}

	projectRef := t.getLastProjectRef()
	if strings.TrimSpace(projectRef) == "" {
		global, err := t.store.GetGlobalState(ctx)
		if err == nil {
			projectRef = pickProjectRef(global)
		}
	}

	var parts []string
	if projectRef != "" {
		detail, err := t.store.GetProjectDetail(ctx, projectRef)
		if err == nil {
			parts = append(parts, projectContextSummary(detail))
			if strings.TrimSpace(detail.ProjectRef) != "" {
				projectRef = detail.ProjectRef
			}
		}
	}

	agentsSummary, err := t.loadAgentsSummary()
	if err == nil && strings.TrimSpace(agentsSummary) != "" {
		parts = append(parts, agentsSummary)
	}

	if pending := t.findLatestTextApproval(); pending != nil {
		parts = append(parts, "Pending input original: "+strings.TrimSpace(pending.Description))
	}

	if len(parts) == 0 {
		parts = append(parts, "No additional context available")
	}

	return strings.TrimSpace(strings.Join(parts, "\n\n")), projectRef, nil
}

func (t *TelegramBot) loadAgentsSummary() (string, error) {
	payload, err := os.ReadFile("AGENTS.md")
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(payload), "\n")
	selected := make([]string, 0, 20)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		selected = append(selected, line)
		if len(selected) >= 20 {
			break
		}
	}

	text := strings.Join(selected, "\n")
	if len(text) > 1200 {
		text = text[:1200]
	}

	return text, nil
}

func (t *TelegramBot) selectConsultant() llm.ConsultantClient {
	for _, consultant := range t.consultants {
		if consultant != nil && consultant.IsAvailable() {
			return consultant
		}
	}
	return nil
}

func (t *TelegramBot) recordConsultation(ctx context.Context, projectRef, consultant, question, summary string) error {
	if t.store == nil {
		return nil
	}
	projectID, err := t.store.ResolveProjectID(ctx, projectRef)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("Consultant %s used. Question: %s. Summary: %s", consultant, question, summary)
	return t.store.AppendEvent(ctx, state.Event{
		ProjectID:   projectID,
		EventType:   "consultant_used",
		Description: description,
	})
}

func (t *TelegramBot) findLatestTextApproval() *PendingApproval {
	now := t.nowFn().UTC()

	t.approvalsMu.RLock()
	defer t.approvalsMu.RUnlock()

	var latest *PendingApproval
	for _, approval := range t.pendingApprovals {
		if approval == nil {
			continue
		}
		if !approval.AcceptsText {
			continue
		}
		if !approval.ExpiresAt.IsZero() && approval.ExpiresAt.Before(now) {
			continue
		}
		if latest == nil || approval.CreatedAt.After(latest.CreatedAt) {
			copyApproval := *approval
			latest = &copyApproval
		}
	}
	return latest
}

func (t *TelegramBot) listApprovals() []*PendingApproval {
	t.approvalsMu.RLock()
	defer t.approvalsMu.RUnlock()

	approvals := make([]*PendingApproval, 0, len(t.pendingApprovals))
	for _, approval := range t.pendingApprovals {
		if approval == nil {
			continue
		}
		copyApproval := *approval
		approvals = append(approvals, &copyApproval)
	}
	return approvals
}

func (t *TelegramBot) cleanupExpiredApprovals(now time.Time) int {
	t.approvalsMu.Lock()

	removed := 0
	for id, approval := range t.pendingApprovals {
		if approval == nil {
			delete(t.pendingApprovals, id)
			removed++
			continue
		}
		if !approval.ExpiresAt.IsZero() && approval.ExpiresAt.Before(now) {
			delete(t.pendingApprovals, id)
			removed++
		}
	}

	// Snapshot surviving approval IDs before releasing approvalsMu,
	// so we can clean up orphaned roadmaps without holding both locks.
	surviving := make(map[string]struct{}, len(t.pendingApprovals))
	for id := range t.pendingApprovals {
		surviving[id] = struct{}{}
	}
	t.approvalsMu.Unlock()

	// Clean up orphaned roadmaps whose approval has expired.
	t.pendingRoadmapsMu.Lock()
	for id := range t.pendingRoadmaps {
		if _, exists := surviving[id]; !exists {
			delete(t.pendingRoadmaps, id)
		}
	}
	t.pendingRoadmapsMu.Unlock()

	return removed
}

func (t *TelegramBot) upsertApproval(approval *PendingApproval) error {
	if approval == nil {
		return fmt.Errorf("approval is nil")
	}
	if strings.TrimSpace(approval.ID) == "" {
		return fmt.Errorf("approval id is required")
	}

	t.approvalsMu.Lock()
	defer t.approvalsMu.Unlock()
	if t.pendingApprovals == nil {
		t.pendingApprovals = make(map[string]*PendingApproval)
	}
	copyApproval := *approval
	t.pendingApprovals[copyApproval.ID] = &copyApproval
	return nil
}

func (t *TelegramBot) enqueueMessage(ctx context.Context, message string) error {
	_ = ctx
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	message = TruncateTelegramMessage(message)

	t.startStopMu.Lock()
	defer t.startStopMu.Unlock()
	if t.outbox == nil || t.outboxClosed {
		return ErrNotifierNotRunning
	}

	select {
	case t.outbox <- message:
		return nil
	default:
	}

	select {
	case <-t.outbox:
		t.logger.Warn("telegram outbox full; dropped oldest message")
	default:
	}

	select {
	case t.outbox <- message:
		return nil
	default:
		t.logger.Warn("telegram outbox still full; dropped newest message")
		return nil
	}
}

func (t *TelegramBot) editMessage(messageID int, text string) error {
	if t.editMessageFn != nil {
		return t.editMessageFn(messageID, text)
	}
	if t.bot == nil {
		return ErrNotifierNotRunning
	}

	edit := tgbotapi.NewEditMessageText(t.allowedChatID, messageID, text)
	edit.ParseMode = tgbotapi.ModeMarkdownV2

	_, err := t.bot.Send(edit)
	if err != nil {
		return fmt.Errorf("telegram edit: %w", err)
	}
	return nil
}

func (t *TelegramBot) sendAndTrackMessage(text string) (int, error) {
	if t.sendMessageFn != nil {
		return 0, t.sendMessageFn(text)
	}
	if t.bot == nil {
		return 0, ErrNotifierNotRunning
	}

	msg := tgbotapi.NewMessage(t.allowedChatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.DisableNotification = true

	sent, err := t.bot.Send(msg)
	if err != nil {
		return 0, fmt.Errorf("telegram send: %w", err)
	}
	return sent.MessageID, nil
}

func (t *TelegramBot) sendMessage(message string) error {
	if t.sendMessageFn != nil {
		return t.sendMessageFn(message)
	}
	if t.bot == nil {
		return ErrNotifierNotRunning
	}

	msg := tgbotapi.NewMessage(t.allowedChatID, message)
	msg.ParseMode = tgbotapi.ModeMarkdownV2

	_, err := t.bot.Send(msg)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	return nil
}

func (t *TelegramBot) sendSilentMessage(message string) error {
	if t.sendMessageFn != nil {
		return t.sendMessageFn(message)
	}
	if t.bot == nil {
		return ErrNotifierNotRunning
	}

	msg := tgbotapi.NewMessage(t.allowedChatID, message)
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.DisableNotification = true

	_, err := t.bot.Send(msg)
	if err != nil {
		return fmt.Errorf("telegram send silent: %w", err)
	}
	return nil
}

func (t *TelegramBot) setLastProjectRef(projectRef string) {
	projectRef = strings.TrimSpace(projectRef)
	if projectRef == "" {
		return
	}
	t.lastProjectRefMu.Lock()
	defer t.lastProjectRefMu.Unlock()
	t.lastProjectRef = projectRef
}

func (t *TelegramBot) getLastProjectRef() string {
	t.lastProjectRefMu.RLock()
	defer t.lastProjectRefMu.RUnlock()
	return t.lastProjectRef
}

func pickProjectRef(global state.GlobalState) string {
	if len(global.Projects) == 0 {
		return ""
	}

	projects := append([]state.ProjectSummary(nil), global.Projects...)
	sort.SliceStable(projects, func(i, j int) bool {
		if projects[i].ActiveWorkers == projects[j].ActiveWorkers {
			return projects[i].InternalID < projects[j].InternalID
		}
		return projects[i].ActiveWorkers > projects[j].ActiveWorkers
	})

	for _, project := range projects {
		if strings.TrimSpace(project.ID) != "" {
			return project.ID
		}
	}

	return strconv.FormatInt(projects[0].InternalID, 10)
}

func projectContextSummary(detail state.ProjectDetail) string {
	lastEvent := "no events"
	if len(detail.Events) > 0 {
		lastEvent = strings.TrimSpace(detail.Events[0].Description)
		if lastEvent == "" {
			lastEvent = detail.Events[0].EventType
		}
	}

	inProgress := 0
	for _, task := range detail.Tasks {
		if task.Status == state.TaskStatusInProgress {
			inProgress++
		}
	}

	return strings.TrimSpace(fmt.Sprintf(
		"Project: %s\nStatus: %s\nTasks in progress: %d\nProgress: %.0f%%\nLast event: %s",
		detail.ProjectRef,
		detail.Project.Status,
		inProgress,
		detail.Progress.Overall*100,
		lastEvent,
	))
}

func normalizeApprovalID(approvalID string) string {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID != "" {
		return approvalID
	}

	return fmt.Sprintf("a-%d", time.Now().UnixNano())
}

func resolveRefinerPromptPath(promptDir, filename string) (string, error) {
	if filepath.IsAbs(promptDir) {
		candidate := filepath.Join(promptDir, filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		return "", fmt.Errorf("prompt %q not found in %s", filename, promptDir)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	searchDir := workingDir
	for {
		candidate := filepath.Join(searchDir, promptDir, filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			break
		}
		searchDir = parent
	}
	return "", fmt.Errorf("prompt %q not found (searched from %s)", filename, workingDir)
}
