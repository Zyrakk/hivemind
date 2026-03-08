package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
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
	Type        string // "plan" | "pr" | "input"
	ProjectID   string
	Description string
	AcceptsText bool
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type plannerExecutor interface {
	ExecutePlan(ctx context.Context, planID string) error
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
	workers        workerController
	consultants    []llm.ConsultantClient

	inputResolver func(ctx context.Context, approval *PendingApproval, response string) error
	sendMessageFn func(text string) error

	nowFn            func() time.Time
	approvalsTTL     time.Duration
	prApprovalTTL    time.Duration
	cleanupInterval  time.Duration
	sendRatePerSec   int
	lastProjectRef   string
	lastProjectRefMu sync.RWMutex

	wg           sync.WaitGroup
	cancel       context.CancelFunc
	startStopMu  sync.Mutex
	started      atomic.Bool
	outboxClosed bool
}

func NewTelegramBot(
	botToken string,
	allowedChatID int64,
	planner plannerService,
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
		plannerCreate:    planner,
		plannerRebuild:   planner,
		store:            db,
		plannerExec:      planner,
		nowFn:            time.Now,
		approvalsTTL:     defaultApprovalsTTL,
		prApprovalTTL:    defaultPRApprovalTTL,
		cleanupInterval:  defaultCleanupInterval,
		sendRatePerSec:   defaultSendRatePerSecond,
		lastProjectRef:   "",
		inputResolver:    nil,
		sendMessageFn:    nil,
		outboxClosed:     false,
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

func (t *TelegramBot) SetInputResolver(resolver func(ctx context.Context, approval *PendingApproval, response string) error) {
	t.inputResolver = resolver
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

func (t *TelegramBot) NotifyPRReady(ctx context.Context, projectID, prURL, summary, approvalID string) error {
	approvalID = normalizeApprovalID(approvalID)
	now := t.nowFn().UTC()

	approval := &PendingApproval{
		ID:          approvalID,
		Type:        "pr",
		ProjectID:   strings.TrimSpace(projectID),
		Description: strings.TrimSpace(summary),
		AcceptsText: false,
		CreatedAt:   now,
		ExpiresAt:   now.Add(t.prApprovalTTL),
	}
	if err := t.upsertApproval(approval); err != nil {
		return err
	}

	return t.enqueueMessage(ctx, FormatPRReadyMessage(projectID, prURL, summary, approvalID))
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
	if err := t.enqueueMessage(context.Background(), formatEscapedLines(
		fmt.Sprintf(
			"▸ Engine switch: %s → %s. Reason: %s",
			strings.TrimSpace(from),
			strings.TrimSpace(to),
			strings.TrimSpace(reason),
		),
	)); err != nil && !errors.Is(err, ErrNotifierNotRunning) {
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

	_ = t.enqueueMessage(ctx, formatEscapedLines(
		fmt.Sprintf("… Planning for %s", projectRef),
		fmt.Sprintf("Directive: %s", directive),
	))

	planResult, err := t.plannerCreate.CreatePlan(ctx, directive, projectRef)
	if err != nil {
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

	summary := formatPlanSummary(projectRef, planResult)
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
			execCtx := context.Background()
			_ = t.enqueueMessage(execCtx, formatEscapedLines(
				fmt.Sprintf(">> Executing plan %s for %s", approval.ID, approval.ProjectID),
			))

			if t.plannerExec == nil {
				_ = t.enqueueMessage(execCtx, formatEscapedLines(
					fmt.Sprintf("✗ Plan %s failed: planner is not configured", approval.ID),
				))
				return
			}

			if err := t.plannerExec.ExecutePlan(execCtx, approval.ID); err != nil {
				_ = t.enqueueMessage(execCtx, formatEscapedLines(
					fmt.Sprintf("✗ Plan %s failed: %s", approval.ID, err.Error()),
				))
				return
			}

			_ = t.enqueueMessage(execCtx, formatEscapedLines(
				fmt.Sprintf("✓ Plan %s completed.", approval.ID),
			))
		}(copyApproval)

		return formatEscapedLines(fmt.Sprintf("✓ Plan %s approved. Executing.", copyApproval.ID)), nil
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

func formatPlanSummary(projectRef string, result *planner.PlanResult) string {
	if result == nil || result.Plan == nil {
		return formatEscapedLines("✗ Empty plan result.")
	}

	engineName := strings.TrimSpace(result.Engine)
	if engineName == "" {
		engineName = "glm"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("▸ Plan for %s [engine: %s] (%d tasks):\n", projectRef, engineName, len(result.Plan.Tasks)))
	sb.WriteString(fmt.Sprintf("Confidence: %.0f%%\n", result.Plan.Confidence*100))
	if result.ConsultantUsed {
		sb.WriteString("→ Validated by consultant\n")
	}
	sb.WriteString("\n")

	for i, task := range result.Plan.Tasks {
		title := strings.TrimSpace(task.Title)
		if title == "" {
			title = fmt.Sprintf("Task %d", i+1)
		}
		description := strings.TrimSpace(task.Description)
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, title, description))
		if len(task.DependsOn) > 0 {
			sb.WriteString(fmt.Sprintf("   Depends on: %s\n", strings.Join(task.DependsOn, ", ")))
		}
	}

	sb.WriteString(fmt.Sprintf("\n/approve %s — execute this plan", result.PlanID))
	sb.WriteString(fmt.Sprintf("\n/reject %s {reason} — discard", result.PlanID))

	return formatEscapedLines(sb.String())
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

	return t.enqueueMessage(ctx, formatPlanSummary(approval.ProjectID, rebuilt))
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
	defer t.approvalsMu.Unlock()

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
