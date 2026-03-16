package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/planner"
	"github.com/zyrakk/hivemind/internal/state"
)

func TestTelegramBotImplementsNotifier(t *testing.T) {
	var _ Notifier = (*TelegramBot)(nil)
}

func TestNoopNotifier(t *testing.T) {
	n := NoopNotifier{}
	ctx := context.Background()

	if err := n.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := n.NotifyNeedsInput(ctx, "p", "q", "a"); err != nil {
		t.Fatalf("NotifyNeedsInput() error = %v", err)
	}
	if err := n.NotifyPRReady(ctx, "p", "main", "a1", nil, nil); err != nil {
		t.Fatalf("NotifyPRReady() error = %v", err)
	}
	if err := n.NotifyWorkerFailed(ctx, "p", "t", "e"); err != nil {
		t.Fatalf("NotifyWorkerFailed() error = %v", err)
	}
	if err := n.NotifyTaskCompleted(ctx, "p", "t"); err != nil {
		t.Fatalf("NotifyTaskCompleted() error = %v", err)
	}
	if err := n.NotifyConsultantUsed(ctx, "c", "q", "s"); err != nil {
		t.Fatalf("NotifyConsultantUsed() error = %v", err)
	}
	if err := n.NotifyBudgetWarning(ctx, "c", 80); err != nil {
		t.Fatalf("NotifyBudgetWarning() error = %v", err)
	}
	if err := n.NotifyProgress(ctx, "p", "", "s", "d"); err != nil {
		t.Fatalf("NotifyProgress() error = %v", err)
	}
	if err := n.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestNotifyEngineSwitchQueuesFormattedMessage(t *testing.T) {
	t.Parallel()

	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.NotifyEngineSwitch("claude-code", "glm", "Think failed: rate limit")

	select {
	case msg := <-bot.outbox:
		if !strings.Contains(msg, "ENGINE SWITCH") || !strings.Contains(msg, "claude-code") || !strings.Contains(msg, "glm") {
			t.Fatalf("message = %q, expected engine switch box", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for engine switch message")
	}
}

func TestCommandsWithMockStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	store := newMockStore(now)
	bot := newTestBot(store)

	t.Run("/status", func(t *testing.T) {
		msg, err := bot.handleCommand(ctx, "status", "")
		if err != nil {
			t.Fatalf("status command failed: %v", err)
		}
		if !strings.Contains(msg, "Flux") {
			t.Fatalf("expected project name in status message: %q", msg)
		}
	})

	t.Run("/project", func(t *testing.T) {
		msg, err := bot.handleCommand(ctx, "project", "flux")
		if err != nil {
			t.Fatalf("project command failed: %v", err)
		}
		if !strings.Contains(msg, "PROJECT") {
			t.Fatalf("expected project header in message: %q", msg)
		}
	})

	t.Run("/approve plan", func(t *testing.T) {
		exec := &mockPlanExecutor{called: make(chan string, 1)}
		bot.plannerExec = exec
		bot.pendingApprovals["plan-1"] = &PendingApproval{
			ID:          "plan-1",
			Type:        "plan",
			ProjectID:   "flux",
			Description: "Plan Flux",
			CreatedAt:   now,
			ExpiresAt:   now.Add(24 * time.Hour),
		}

		msg, err := bot.handleCommand(ctx, "approve", "plan-1")
		if err != nil {
			t.Fatalf("approve command failed: %v", err)
		}
		if !strings.Contains(msg, "APPROVED") {
			t.Fatalf("expected success message, got %q", msg)
		}
		select {
		case gotPlanID := <-exec.called:
			if gotPlanID != "plan-1" {
				t.Fatalf("expected ExecutePlan called with plan-1, got %q", gotPlanID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for async ExecutePlan")
		}
		if _, ok := bot.pendingApprovals["plan-1"]; ok {
			t.Fatalf("expected approval removed")
		}
	})

	t.Run("/approve pr", func(t *testing.T) {
		bot.pendingApprovals["pr-1"] = &PendingApproval{
			ID:          "pr-1",
			Type:        "pr",
			ProjectID:   "flux",
			Description: "PR #12",
			CreatedAt:   now,
			ExpiresAt:   now.Add(48 * time.Hour),
		}
		msg, err := bot.handleCommand(ctx, "approve", "pr-1")
		if err != nil {
			t.Fatalf("approve pr command failed: %v", err)
		}
		if !strings.Contains(msg, "Approved") {
			t.Fatalf("expected approved message, got %q", msg)
		}
		if store.countEventsByType("pr_approved") == 0 {
			t.Fatalf("expected pr_approved event")
		}
	})

	t.Run("/reject plan", func(t *testing.T) {
		bot.pendingApprovals["plan-2"] = &PendingApproval{
			ID:          "plan-2",
			Type:        "plan",
			ProjectID:   "flux",
			Description: "Plan para rechazar",
			CreatedAt:   now,
			ExpiresAt:   now.Add(24 * time.Hour),
		}
		msg, err := bot.handleCommand(ctx, "reject", "plan-2 mala idea")
		if err != nil {
			t.Fatalf("reject plan command failed: %v", err)
		}
		if !strings.Contains(msg, "Rejected") {
			t.Fatalf("expected rejected message, got %q", msg)
		}
		if store.taskUpdates == 0 {
			t.Fatalf("expected pending tasks to be marked as blocked")
		}
	})

	t.Run("/pause", func(t *testing.T) {
		msg, err := bot.handleCommand(ctx, "pause", "flux")
		if err != nil {
			t.Fatalf("pause command failed: %v", err)
		}
		if !strings.Contains(msg, "paused") {
			t.Fatalf("expected paused message, got %q", msg)
		}
		if store.projectStatusByRef["flux"] != state.ProjectStatusPaused {
			t.Fatalf("expected project status paused, got %q", store.projectStatusByRef["flux"])
		}
	})

	t.Run("/resume", func(t *testing.T) {
		msg, err := bot.handleCommand(ctx, "resume", "flux")
		if err != nil {
			t.Fatalf("resume command failed: %v", err)
		}
		if !strings.Contains(msg, "resumed") {
			t.Fatalf("expected resumed message, got %q", msg)
		}
		if store.projectStatusByRef["flux"] != state.ProjectStatusWorking {
			t.Fatalf("expected project status working, got %q", store.projectStatusByRef["flux"])
		}
	})

	t.Run("/consult", func(t *testing.T) {
		bot.SetConsultants([]llm.ConsultantClient{&mockConsultant{
			name:      "claude",
			available: true,
			opinion: &llm.Opinion{
				Analysis: "Recomiendo dividir el parser en modulos.",
			},
		}})

		msg, err := bot.handleCommand(ctx, "consult", "Como reducimos riesgo?")
		if err != nil {
			t.Fatalf("consult command failed: %v", err)
		}
		if !strings.Contains(msg, "CONSULTANT") {
			t.Fatalf("expected consultant response, got %q", msg)
		}
		if store.countEventsByType("consultant_used") == 0 {
			t.Fatalf("expected consultant_used event")
		}
	})

	t.Run("/pending", func(t *testing.T) {
		bot.nowFn = func() time.Time { return now }
		bot.pendingApprovals["in-1"] = &PendingApproval{
			ID:          "in-1",
			Type:        "input",
			ProjectID:   "flux",
			Description: "Pregunta",
			AcceptsText: true,
			CreatedAt:   now,
			ExpiresAt:   now.Add(time.Hour),
		}
		msg, err := bot.handleCommand(ctx, "pending", "")
		if err != nil {
			t.Fatalf("pending command failed: %v", err)
		}
		if !strings.Contains(msg, "PENDING") {
			t.Fatalf("expected pending list, got %q", msg)
		}
	})

	t.Run("/help", func(t *testing.T) {
		msg, err := bot.handleCommand(ctx, "help", "")
		if err != nil {
			t.Fatalf("help command failed: %v", err)
		}
		if !strings.Contains(msg, "/status") {
			t.Fatalf("expected help list, got %q", msg)
		}
	})
}

func TestCmdRun_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	creator := &mockPlanCreator{
		result: &planner.PlanResult{
			PlanID: "plan-run-1",
			Plan: &llm.TaskPlan{
				Confidence: 0.92,
				Tasks: []llm.Task{
					{
						ID:          "task-001",
						Title:       "Add health handler",
						Description: "Create /health endpoint returning JSON status",
					},
					{
						ID:          "task-002",
						Title:       "Add health tests",
						Description: "Unit and integration tests for health endpoint",
						DependsOn:   []string{"task-001"},
					},
				},
			},
		},
	}
	bot.plannerCreate = creator

	msg, err := bot.handleCommand(ctx, "run", "flux Implement health endpoint")
	if err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if !strings.Contains(msg, "PLAN") || !strings.Contains(msg, "flux") {
		t.Fatalf("expected plan summary, got %q", msg)
	}

	approval, ok := bot.pendingApprovals["plan-run-1"]
	if !ok {
		t.Fatalf("expected plan approval registration")
	}
	if approval.Type != "plan" {
		t.Fatalf("expected approval type plan, got %q", approval.Type)
	}
	if creator.lastProj != "flux" {
		t.Fatalf("expected project flux, got %q", creator.lastProj)
	}
	if creator.lastArg != "Implement health endpoint" {
		t.Fatalf("expected directive to be parsed, got %q", creator.lastArg)
	}
}

func TestCmdRun_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "run", "")
	if err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if !strings.Contains(msg, "Usage: /run") {
		t.Fatalf("expected usage message, got %q", msg)
	}
}

func TestCmdRun_ProjectNotFound(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.plannerCreate = &mockPlanCreator{
		result: &planner.PlanResult{
			PlanID: "plan-run-404",
			Plan:   &llm.TaskPlan{Confidence: 0.8},
		},
	}

	msg, err := bot.handleCommand(context.Background(), "run", "noexiste Add tests")
	if err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if !strings.Contains(msg, "Project 'noexiste' not found") {
		t.Fatalf("expected project not found message, got %q", msg)
	}
}

func TestCmdRun_PlanNeedsInput(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.plannerCreate = &mockPlanCreator{
		result: &planner.PlanResult{
			PlanID:     "plan-input-1",
			NeedsInput: true,
			Plan: &llm.TaskPlan{
				Confidence: 0.78,
				Tasks:      []llm.Task{{ID: "task-1", Title: "Task 1", Description: "desc"}},
				Questions:  []string{"Which API version should we target?"},
			},
		},
	}

	msg, err := bot.handleCommand(context.Background(), "run", "flux Add health endpoint")
	if err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if !strings.Contains(msg, "Plan requires your input") {
		t.Fatalf("expected needs input message, got %q", msg)
	}

	approval, ok := bot.pendingApprovals["plan-input-1"]
	if !ok {
		t.Fatalf("expected input approval registration")
	}
	if approval.Type != "input" || !approval.AcceptsText {
		t.Fatalf("expected text input approval, got type=%q accepts_text=%t", approval.Type, approval.AcceptsText)
	}
}

func TestCmdApprove_ExecutesPlan(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	exec := &mockPlanExecutor{called: make(chan string, 1)}
	bot.plannerExec = exec
	bot.pendingApprovals["plan-async-1"] = &PendingApproval{
		ID:          "plan-async-1",
		Type:        "plan",
		ProjectID:   "flux",
		Description: "Async plan",
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}

	msg, err := bot.handleCommand(context.Background(), "approve", "plan-async-1")
	if err != nil {
		t.Fatalf("approve command failed: %v", err)
	}
	if !strings.Contains(msg, "APPROVED") {
		t.Fatalf("expected async approval message, got %q", msg)
	}

	select {
	case got := <-exec.called:
		if got != "plan-async-1" {
			t.Fatalf("expected ExecutePlan called with plan-async-1, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async ExecutePlan")
	}
}

func TestSecurityUnauthorizedChatIsIgnored(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	ctx := context.Background()

	update := tgbotapi.Update{Message: &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 9999},
		From: &tgbotapi.User{UserName: "intruder"},
		Text: "hola",
	}}

	bot.handleUpdate(ctx, update)
	if len(bot.outbox) != 0 {
		t.Fatalf("expected no responses for unauthorized chat")
	}
}

func TestChannelPostFreeTextHandled(t *testing.T) {
	now := time.Now().UTC()
	store := newMockStore(now)
	bot := newTestBot(store)
	bot.pendingApprovals["input-channel"] = &PendingApproval{
		ID:          "input-channel",
		Type:        "input",
		ProjectID:   "flux",
		Description: "resolver por channel post",
		AcceptsText: true,
		CreatedAt:   now,
		ExpiresAt:   now.Add(24 * time.Hour),
	}

	update := tgbotapi.Update{
		ChannelPost: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 1234},
			Text: "respuesta desde canal",
		},
	}

	bot.handleUpdate(context.Background(), update)

	if _, ok := bot.pendingApprovals["input-channel"]; ok {
		t.Fatalf("expected input approval resolved from channel post")
	}
	if store.countEventsByType("input_response") == 0 {
		t.Fatalf("expected input_response event from channel post")
	}
}

func TestPendingApprovalsConcurrentAccess(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	ctx := context.Background()

	const total = 200
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("a-%d", i)
			_ = bot.NotifyNeedsInput(ctx, "flux", fmt.Sprintf("question %d", i), id)
		}(i)
	}
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = bot.handleCommand(ctx, "pending", "")
			_ = bot.findLatestTextApproval()
		}()
	}
	wg.Wait()

	if len(bot.pendingApprovals) == 0 {
		t.Fatalf("expected approvals to be present")
	}
}

func TestApprovalTTLCleanup(t *testing.T) {
	base := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	bot := newTestBot(newMockStore(base))
	bot.nowFn = func() time.Time { return base }

	bot.RegisterPendingApproval(PendingApproval{
		ID:          "ttl-1",
		Type:        "input",
		ProjectID:   "flux",
		Description: "caduca",
		AcceptsText: true,
		CreatedAt:   base,
		ExpiresAt:   base.Add(30 * time.Minute),
	})

	removed := bot.cleanupExpiredApprovals(base.Add(time.Hour))
	if removed != 1 {
		t.Fatalf("expected one expired approval removed, got %d", removed)
	}
	if len(bot.pendingApprovals) != 0 {
		t.Fatalf("expected no pending approvals")
	}
}

func TestEndToEndNeedsInputThenFreeText(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	if err := bot.NotifyNeedsInput(ctx, "flux", "Que intervalo de polling usamos?", "input-1"); err != nil {
		t.Fatalf("NotifyNeedsInput failed: %v", err)
	}
	if _, ok := bot.pendingApprovals["input-1"]; !ok {
		t.Fatalf("expected pending approval to be created")
	}

	msg, err := bot.handleFreeText(ctx, "Cada 5 minutos")
	if err != nil {
		t.Fatalf("handleFreeText failed: %v", err)
	}
	if !strings.Contains(msg, "Input") {
		t.Fatalf("expected input ack message, got %q", msg)
	}
	if _, ok := bot.pendingApprovals["input-1"]; ok {
		t.Fatalf("expected pending approval removed after free text")
	}
	if store.countEventsByType("input_response") == 0 {
		t.Fatalf("expected input_response event")
	}
}

func TestRateLimitingOutboxSender(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.sendRatePerSec = 25

	var mu sync.Mutex
	timestamps := make([]time.Time, 0, 100)
	bot.sendMessageFn = func(text string) error {
		mu.Lock()
		timestamps = append(timestamps, time.Now())
		mu.Unlock()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	bot.wg.Add(1)
	go bot.runOutboxSender(ctx)

	for i := 0; i < 100; i++ {
		_ = bot.enqueueMessage(context.Background(), fmt.Sprintf("msg-%d", i))
	}

	deadline := time.After(4 * time.Second)
	for {
		mu.Lock()
		count := len(timestamps)
		mu.Unlock()
		if count >= 5 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for rate-limited sends")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	cancel()
	bot.startStopMu.Lock()
	if !bot.outboxClosed {
		close(bot.outbox)
		bot.outboxClosed = true
	}
	bot.startStopMu.Unlock()
	bot.wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(timestamps) < 5 {
		t.Fatalf("expected at least 5 sent messages, got %d", len(timestamps))
	}

	elapsed := timestamps[4].Sub(timestamps[0])
	if elapsed < 120*time.Millisecond {
		t.Fatalf("expected sends to be rate-limited, elapsed=%s", elapsed)
	}
}

func newTestBot(store stateStore) *TelegramBot {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &TelegramBot{
		allowedChatID:    1234,
		pendingApprovals: make(map[string]*PendingApproval),
		outbox:           make(chan string, 2048),
		logger:           logger,
		store:            store,
		nowFn:            time.Now,
		approvalsTTL:     defaultApprovalsTTL,
		prApprovalTTL:    defaultPRApprovalTTL,
		cleanupInterval:  defaultCleanupInterval,
		sendRatePerSec:   defaultSendRatePerSecond,
		activeRuns:       make(map[string]*RunHandle),
	}
}

type mockPlanExecutor struct {
	planID string
	err    error
	called chan string
}

func (m *mockPlanExecutor) ExecutePlan(ctx context.Context, planID string) error {
	_ = ctx
	m.planID = planID
	if m.called != nil {
		select {
		case m.called <- planID:
		default:
		}
	}
	return m.err
}

func (m *mockPlanExecutor) ExecuteBatch(ctx context.Context, batchID string) error {
	_ = ctx
	return nil
}

type mockPlanCreator struct {
	result    *planner.PlanResult
	err       error
	lastArg   string
	lastProj  string
	callCount int
}

func (m *mockPlanCreator) CreatePlan(ctx context.Context, directive, projectID string) (*planner.PlanResult, error) {
	_ = ctx
	m.callCount++
	m.lastArg = directive
	m.lastProj = projectID
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

type mockConsultant struct {
	name      string
	available bool
	opinion   *llm.Opinion
	err       error
}

func (m *mockConsultant) Consult(ctx context.Context, consultationType string, context string, question string) (*llm.Opinion, error) {
	_ = ctx
	_ = consultationType
	_ = context
	_ = question
	if m.err != nil {
		return nil, m.err
	}
	return m.opinion, nil
}

func (m *mockConsultant) GetName() string {
	return m.name
}

func (m *mockConsultant) GetBudgetRemaining() float64 {
	return 100
}

func (m *mockConsultant) IsAvailable() bool {
	return m.available
}

type mockStore struct {
	mu sync.Mutex

	global             state.GlobalState
	detailByRef        map[string]state.ProjectDetail
	projectIDByRef     map[string]int64
	projectStatusByRef map[string]string
	projectStatusByID  map[int64]string
	events             []state.Event
	taskUpdates        int
	workerUpdates      int
	batches            map[string]*state.Batch
	batchItems         map[string][]state.BatchItem
}

func newMockStore(now time.Time) *mockStore {
	fluxDetail := state.ProjectDetail{
		ProjectRef: "flux",
		Project: state.Project{
			ID:     1,
			Name:   "Flux",
			Status: state.ProjectStatusWorking,
		},
		Tasks: []state.Task{
			{ID: 10, ProjectID: 1, Title: "T1", Status: state.TaskStatusPending},
			{ID: 11, ProjectID: 1, Title: "T2", Status: state.TaskStatusInProgress},
		},
		Workers: []state.Worker{
			{ID: 20, ProjectID: 1, SessionID: "s1", Status: state.WorkerStatusRunning},
			{ID: 21, ProjectID: 1, SessionID: "s2", Status: state.WorkerStatusPaused},
		},
		Events: []state.Event{{
			ID:          1,
			ProjectID:   1,
			EventType:   "worker_started",
			Description: "Worker en progreso",
			Timestamp:   now,
		}},
		Progress: state.Progress{Overall: 0.5},
	}

	return &mockStore{
		global: state.GlobalState{
			Projects: []state.ProjectSummary{{
				ID:            "flux",
				InternalID:    1,
				Name:          "Flux",
				Status:        state.ProjectStatusWorking,
				ActiveWorkers: 2,
				PendingTasks:  3,
				LastActivity:  ptrTime(now),
			}},
			Counters: state.Counters{ActiveWorkers: 2, PendingTasks: 3, PendingReview: 1},
		},
		detailByRef: map[string]state.ProjectDetail{
			"flux": fluxDetail,
			"1":    fluxDetail,
		},
		projectIDByRef: map[string]int64{
			"flux": 1,
			"1":    1,
		},
		projectStatusByRef: make(map[string]string),
		projectStatusByID:  make(map[int64]string),
		events:             make([]state.Event, 0),
		batches:            make(map[string]*state.Batch),
		batchItems:         make(map[string][]state.BatchItem),
	}
}

func (m *mockStore) GetGlobalState(ctx context.Context) (state.GlobalState, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.global, nil
}

func (m *mockStore) GetProjectDetail(ctx context.Context, projectRef string) (state.ProjectDetail, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if detail, ok := m.detailByRef[strings.ToLower(strings.TrimSpace(projectRef))]; ok {
		return detail, nil
	}
	if detail, ok := m.detailByRef[strings.TrimSpace(projectRef)]; ok {
		return detail, nil
	}
	return state.ProjectDetail{}, state.ErrNotFound
}

func (m *mockStore) ResolveProjectID(ctx context.Context, projectRef string) (int64, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(projectRef))
	if id, ok := m.projectIDByRef[key]; ok {
		return id, nil
	}
	if id, ok := m.projectIDByRef[strings.TrimSpace(projectRef)]; ok {
		return id, nil
	}
	return 0, state.ErrNotFound
}

func (m *mockStore) UpdateProjectStatus(ctx context.Context, projectID int64, status string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.projectStatusByID[projectID] = status
	return nil
}

func (m *mockStore) UpdateProjectStatusByReference(ctx context.Context, projectRef, status string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.projectStatusByRef[strings.TrimSpace(projectRef)] = status
	return nil
}

func (m *mockStore) UpdateTask(ctx context.Context, taskID int64, update state.TaskUpdate) error {
	_ = ctx
	_ = taskID
	_ = update
	m.mu.Lock()
	defer m.mu.Unlock()
	m.taskUpdates++
	return nil
}

func (m *mockStore) UpdateWorker(ctx context.Context, workerID int64, update state.WorkerUpdate) error {
	_ = ctx
	_ = workerID
	_ = update
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workerUpdates++
	return nil
}

func (m *mockStore) AppendEvent(ctx context.Context, event state.Event) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *mockStore) CreateBatch(ctx context.Context, projectID int64, name string, directives []string) (string, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	batchID := fmt.Sprintf("batch-test-%d", len(m.batches)+1)
	m.batches[batchID] = &state.Batch{
		ID:         batchID,
		ProjectID:  projectID,
		Name:       name,
		Status:     state.BatchStatusPending,
		TotalItems: len(directives),
	}
	items := make([]state.BatchItem, len(directives))
	for i, d := range directives {
		items[i] = state.BatchItem{
			ID:        int64(i + 1),
			BatchID:   batchID,
			Sequence:  i + 1,
			Directive: d,
			Status:    state.BatchItemStatusPending,
		}
	}
	m.batchItems[batchID] = items
	return batchID, nil
}

func (m *mockStore) GetBatch(ctx context.Context, batchID string) (*state.Batch, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.batches[batchID]
	if !ok {
		return nil, state.ErrNotFound
	}
	return b, nil
}

func (m *mockStore) GetBatchItems(ctx context.Context, batchID string) ([]state.BatchItem, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	items, ok := m.batchItems[batchID]
	if !ok {
		return nil, state.ErrNotFound
	}
	return items, nil
}

func (m *mockStore) UpdateBatchStatus(ctx context.Context, batchID, status string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.batches[batchID]
	if !ok {
		return state.ErrNotFound
	}
	b.Status = status
	return nil
}

func (m *mockStore) UpdateBatchItemStatus(ctx context.Context, itemID int64, status, planID, errorMsg string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	for bID, items := range m.batchItems {
		for i, item := range items {
			if item.ID == itemID {
				m.batchItems[bID][i].Status = status
				if planID != "" {
					m.batchItems[bID][i].PlanID = &planID
				}
				if errorMsg != "" {
					m.batchItems[bID][i].Error = &errorMsg
				}
				return nil
			}
		}
	}
	return state.ErrNotFound
}

func (m *mockStore) GetRunningBatches(ctx context.Context) ([]state.Batch, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []state.Batch
	for _, b := range m.batches {
		if b.Status == state.BatchStatusRunning {
			result = append(result, *b)
		}
	}
	return result, nil
}

func (m *mockStore) GetPausedBatches(ctx context.Context) ([]state.Batch, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []state.Batch
	for _, b := range m.batches {
		if b.Status == state.BatchStatusPaused {
			result = append(result, *b)
		}
	}
	return result, nil
}

func (m *mockStore) CreateBatchWithPhases(ctx context.Context, projectID int64, name string, directives, phases, phaseDependsOn []string) (string, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	batchID := fmt.Sprintf("batch-test-%d", len(m.batches)+1)
	m.batches[batchID] = &state.Batch{
		ID:         batchID,
		ProjectID:  projectID,
		Name:       name,
		Status:     state.BatchStatusPending,
		TotalItems: len(directives),
	}
	items := make([]state.BatchItem, len(directives))
	for i, d := range directives {
		items[i] = state.BatchItem{
			ID:        int64(i + 1),
			BatchID:   batchID,
			Sequence:  i + 1,
			Directive: d,
			Status:    state.BatchItemStatusPending,
		}
	}
	m.batchItems[batchID] = items
	return batchID, nil
}

func (m *mockStore) countEventsByType(eventType string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, event := range m.events {
		if event.EventType == eventType {
			count++
		}
	}
	return count
}

func ptrTime(ts time.Time) *time.Time {
	copyTs := ts
	return &copyTs
}

func TestHandleFreeTextWithoutPendingApproval(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleFreeText(context.Background(), "hola")
	if err != nil {
		t.Fatalf("handleFreeText() error = %v", err)
	}
	if !strings.Contains(msg, "Unknown command") {
		t.Fatalf("expected unknown message, got %q", msg)
	}
}

func TestApproveUnknownApprovalID(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "approve", "missing")
	if err != nil {
		t.Fatalf("approve command returned error: %v", err)
	}
	if !strings.Contains(msg, "Approval not found") {
		t.Fatalf("expected not found message, got %q", msg)
	}
}

func TestConsultNoConsultantAvailable(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "consult", "pregunta")
	if err != nil {
		t.Fatalf("consult command error = %v", err)
	}
	if !strings.Contains(msg, "No consultants available") {
		t.Fatalf("expected no consultant message, got %q", msg)
	}
}

func TestEnqueueMessageClosedOutbox(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.startStopMu.Lock()
	close(bot.outbox)
	bot.outboxClosed = true
	bot.startStopMu.Unlock()

	err := bot.enqueueMessage(context.Background(), "hola")
	if !errors.Is(err, ErrNotifierNotRunning) {
		t.Fatalf("expected ErrNotifierNotRunning, got %v", err)
	}
}

func TestNotifyProgressSendsFormattedMessage(t *testing.T) {
	t.Parallel()
	var sent []string
	var mu sync.Mutex
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.started.Store(true)
	bot.sendMessageFn = func(text string) error {
		mu.Lock()
		sent = append(sent, text)
		mu.Unlock()
		return nil
	}

	if err := bot.NotifyProgress(context.Background(), "flux", "", "worker-started", "branch: feature/foo"); err != nil {
		t.Fatalf("NotifyProgress() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	if !strings.Contains(sent[0], "flux") || !strings.Contains(sent[0], "worker\\-started") {
		t.Fatalf("unexpected message: %q", sent[0])
	}
}

func TestNotifyProgressEditInPlace_FirstMessageSent(t *testing.T) {
	t.Parallel()
	var sent []string
	var mu sync.Mutex
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.started.Store(true)
	bot.sendMessageFn = func(text string) error {
		mu.Lock()
		sent = append(sent, text)
		mu.Unlock()
		return nil
	}

	_ = bot.NotifyProgress(context.Background(), "flux", "t1", "launching", "task 1/1: Fix bug")
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	if !strings.Contains(sent[0], "flux") || !strings.Contains(sent[0], "launching") {
		t.Fatalf("expected timeline content, got %q", sent[0])
	}
}

func TestNotifyProgressEditInPlace_SubsequentEdits(t *testing.T) {
	t.Parallel()
	var sent []string
	var edited []string
	var mu sync.Mutex
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.started.Store(true)
	bot.sendMessageFn = func(text string) error {
		mu.Lock()
		sent = append(sent, text)
		mu.Unlock()
		return nil
	}
	bot.editMessageFn = func(msgID int, text string) error {
		mu.Lock()
		edited = append(edited, text)
		mu.Unlock()
		return nil
	}

	ctx := context.Background()
	_ = bot.NotifyProgress(ctx, "flux", "t1", "launching", "task 1/1: Fix bug")
	// sendMessageFn returns msgID 0, which is stored
	_ = bot.NotifyProgress(ctx, "flux", "t1", "worker-started", "branch: feature/fix")
	_ = bot.NotifyProgress(ctx, "flux", "t1", "codex-executing", "~3min est.")

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	if len(edited) != 2 {
		t.Fatalf("expected 2 edits, got %d", len(edited))
	}
	// Last edit should have all three stages
	last := edited[len(edited)-1]
	if !strings.Contains(last, "✓ launching") {
		t.Fatalf("expected done launching, got %q", last)
	}
	if !strings.Contains(last, "codex executing") {
		t.Fatalf("expected codex executing, got %q", last)
	}
}

func TestNotifyProgressEditInPlace_CleansUpOnTerminal(t *testing.T) {
	t.Parallel()
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.started.Store(true)
	bot.sendMessageFn = func(text string) error { return nil }
	bot.editMessageFn = func(msgID int, text string) error { return nil }

	ctx := context.Background()
	_ = bot.NotifyProgress(ctx, "flux", "t1", "launching", "task 1/1")
	_ = bot.NotifyProgress(ctx, "flux", "t1", "evaluation-done", "accept")

	bot.progressMu.Lock()
	_, hasTimeline := bot.progressTimelines["t1"]
	_, hasMsgID := bot.progressMsgIDs["t1"]
	bot.progressMu.Unlock()

	if hasTimeline || hasMsgID {
		t.Fatal("expected progress state cleanup after terminal event")
	}
}

func TestNotifyProgressEditInPlace_EditFailureFallback(t *testing.T) {
	t.Parallel()
	var sendCount int
	var mu sync.Mutex
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.started.Store(true)
	bot.sendMessageFn = func(text string) error {
		mu.Lock()
		sendCount++
		mu.Unlock()
		return nil
	}
	bot.editMessageFn = func(msgID int, text string) error {
		return fmt.Errorf("message not found")
	}

	ctx := context.Background()
	_ = bot.NotifyProgress(ctx, "flux", "t1", "launching", "task 1/1")
	_ = bot.NotifyProgress(ctx, "flux", "t1", "worker-started", "branch: x")

	mu.Lock()
	defer mu.Unlock()
	// First send + fallback send after edit failure = 2
	if sendCount != 2 {
		t.Fatalf("expected 2 sends (initial + fallback), got %d", sendCount)
	}
}

func TestNotifyProgressEmptyTaskIDFallback(t *testing.T) {
	t.Parallel()
	var sent int
	bot := newTestBot(newMockStore(time.Now().UTC()))
	bot.started.Store(true)
	bot.sendMessageFn = func(text string) error {
		sent++
		return nil
	}

	_ = bot.NotifyProgress(context.Background(), "flux", "", "worker-started", "branch: x")
	if sent != 1 {
		t.Fatalf("expected 1 message for empty taskID, got %d", sent)
	}
}

func TestNotifyProgressNilBot(t *testing.T) {
	t.Parallel()
	var bot *TelegramBot
	if err := bot.NotifyProgress(context.Background(), "flux", "", "stage", "detail"); err != nil {
		t.Fatalf("expected nil error for nil bot, got %v", err)
	}
}

func TestNotifyProgressNotStarted(t *testing.T) {
	t.Parallel()
	bot := newTestBot(newMockStore(time.Now().UTC()))
	// bot.started is false by default
	if err := bot.NotifyProgress(context.Background(), "flux", "", "stage", "detail"); err != nil {
		t.Fatalf("expected nil error for not-started bot, got %v", err)
	}
}

func TestParseBatchArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      string
		wantProj  string
		wantCount int
		wantFirst string
	}{
		{"pipe separated", "nhi-watch Add YAML parser for scoring rules | Add dry-run flag to the audit cmd", "nhi-watch", 2, "Add YAML parser for scoring rules"},
		{"multiline", "nhi-watch\nAdd YAML parser for scoring rules\nAdd dry-run flag to the audit cmd", "nhi-watch", 2, "Add YAML parser for scoring rules"},
		{"single directive", "nhi-watch Add YAML config parser for scoring rules", "nhi-watch", 1, "Add YAML config parser for scoring rules"},
		{"empty", "", "", 0, ""},
		{"project only", "nhi-watch", "nhi-watch", 0, ""},
		{"mixed inline and multiline", "nhi-watch Add config parser\nAdd dry-run flag to audit command", "nhi-watch", 2, "Add config parser"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj, dirs := parseBatchArgs(tc.args)
			if proj != tc.wantProj {
				t.Fatalf("project = %q, want %q", proj, tc.wantProj)
			}
			if len(dirs) != tc.wantCount {
				t.Fatalf("directives count = %d, want %d (got %v)", len(dirs), tc.wantCount, dirs)
			}
			if tc.wantCount > 0 && dirs[0] != tc.wantFirst {
				t.Fatalf("first directive = %q, want %q", dirs[0], tc.wantFirst)
			}
		})
	}
}

func TestCmdBatch_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	msg, err := bot.handleCommand(ctx, "batch",
		"flux Add YAML config parser for the scoring rules system | Add a dry-run flag to the audit command")
	if err != nil {
		t.Fatalf("batch command failed: %v", err)
	}
	if !strings.Contains(msg, "BATCH CREATED") {
		t.Fatalf("expected batch created message, got %q", msg)
	}
	if !strings.Contains(msg, "flux") {
		t.Fatalf("expected project name in message, got %q", msg)
	}
	if !strings.Contains(msg, "Items:   2") {
		t.Fatalf("expected 2 items, got %q", msg)
	}
	if !strings.Contains(msg, "/start_batch") {
		t.Fatalf("expected start hint, got %q", msg)
	}

	store.mu.Lock()
	if len(store.batches) != 1 {
		t.Fatalf("expected 1 batch in store, got %d", len(store.batches))
	}
	store.mu.Unlock()
}

func TestCmdBatch_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "batch", "")
	if err != nil {
		t.Fatalf("batch command failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage message, got %q", msg)
	}
}

func TestCmdBatch_ProjectNotFound(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "batch",
		"noexiste Add YAML config parser for the scoring rules system | Add a dry-run flag to the audit command")
	if err != nil {
		t.Fatalf("batch command failed: %v", err)
	}
	if !strings.Contains(msg, "not found") {
		t.Fatalf("expected project not found message, got %q", msg)
	}
}

func TestCmdBatch_ValidationFailure(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "batch",
		"flux fix it | Add YAML config parser for the scoring rules system")
	if err != nil {
		t.Fatalf("batch command failed: %v", err)
	}
	if !strings.Contains(msg, "invalid") {
		t.Fatalf("expected validation error, got %q", msg)
	}
}

func TestCmdStartBatch_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	// Pre-create a batch
	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
		"Add dry-run flag to the audit command",
	})

	msg, err := bot.handleCommand(ctx, "start_batch", batchID)
	if err != nil {
		t.Fatalf("start_batch command failed: %v", err)
	}
	if !strings.Contains(msg, "Batch started") {
		t.Fatalf("expected started message, got %q", msg)
	}
	if !strings.Contains(msg, "1/2") {
		t.Fatalf("expected directive count, got %q", msg)
	}

	store.mu.Lock()
	if store.batches[batchID].Status != state.BatchStatusRunning {
		t.Fatalf("expected batch status running, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdStartBatch_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "start_batch", "")
	if err != nil {
		t.Fatalf("start_batch command failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage message, got %q", msg)
	}
}

func TestCmdStartBatch_NotFound(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "start_batch", "batch-nope")
	if err != nil {
		t.Fatalf("start_batch command failed: %v", err)
	}
	if !strings.Contains(msg, "not found") {
		t.Fatalf("expected not found message, got %q", msg)
	}
}

func TestCmdCancelBatch_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{"Add config parser for scoring rules here"})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusRunning
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "cancel_batch", batchID)
	if err != nil {
		t.Fatalf("cancel_batch command failed: %v", err)
	}
	if !strings.Contains(msg, "cancelled") {
		t.Fatalf("expected cancelled message, got %q", msg)
	}

	store.mu.Lock()
	if store.batches[batchID].Status != state.BatchStatusPaused {
		t.Fatalf("expected batch status paused, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdCancelBatch_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "cancel_batch", "")
	if err != nil {
		t.Fatalf("cancel_batch command failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage message, got %q", msg)
	}
}

func TestCmdCancelBatch_NotFound(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "cancel_batch", "batch-nope")
	if err != nil {
		t.Fatalf("cancel_batch command failed: %v", err)
	}
	if !strings.Contains(msg, "not found") {
		t.Fatalf("expected not found message, got %q", msg)
	}
}

func TestCmdCancelBatch_AlreadyCompleted(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{"Add config parser for scoring rules here"})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusCompleted
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "cancel_batch", batchID)
	if err != nil {
		t.Fatalf("cancel_batch command failed: %v", err)
	}
	if !strings.Contains(msg, "already completed") {
		t.Fatalf("expected already completed message, got %q", msg)
	}
}

func TestCmdBatchStatus_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
		"Add dry-run flag to the audit command",
	})

	msg, err := bot.handleCommand(ctx, "batch_status", batchID)
	if err != nil {
		t.Fatalf("batch_status command failed: %v", err)
	}
	if !strings.Contains(msg, "BATCH STATUS") {
		t.Fatalf("expected batch status message, got %q", msg)
	}
	if !strings.Contains(msg, "flux") {
		t.Fatalf("expected project ref in message, got %q", msg)
	}
	if !strings.Contains(msg, "◻ Add YAML config") {
		t.Fatalf("expected item listing, got %q", msg)
	}
}

func TestCmdBatchStatus_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "batch_status", "")
	if err != nil {
		t.Fatalf("batch_status command failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage message, got %q", msg)
	}
}

func TestCmdBatchStatus_NotFound(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "batch_status", "batch-nope")
	if err != nil {
		t.Fatalf("batch_status command failed: %v", err)
	}
	if !strings.Contains(msg, "not found") {
		t.Fatalf("expected not found message, got %q", msg)
	}
}

func TestHyphenatedCommandFallback(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{"Add config parser for scoring rules here"})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusRunning
	store.mu.Unlock()

	// Simulate Telegram not recognizing /cancel-batch as a command,
	// so it arrives as free text.
	msg, err := bot.handleFreeText(ctx, fmt.Sprintf("/cancel-batch %s", batchID))
	if err != nil {
		t.Fatalf("hyphenated command failed: %v", err)
	}
	if !strings.Contains(msg, "cancelled") {
		t.Fatalf("expected cancelled message via hyphen fallback, got %q", msg)
	}
}

func TestCmdRetry_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
		"Add dry-run flag to the audit command",
	})
	// Simulate: batch paused, first item failed.
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.batchItems[batchID][0].Status = state.BatchItemStatusFailed
	errMsg := "LLM unavailable"
	store.batchItems[batchID][0].Error = &errMsg
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "retry", batchID)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !strings.Contains(msg, "Retrying") {
		t.Fatalf("expected retry message, got %q", msg)
	}

	store.mu.Lock()
	if store.batchItems[batchID][0].Status != state.BatchItemStatusPending {
		t.Fatalf("expected item reset to pending, got %q", store.batchItems[batchID][0].Status)
	}
	if store.batches[batchID].Status != state.BatchStatusRunning {
		t.Fatalf("expected batch running, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdRetry_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "retry", "")
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage, got %q", msg)
	}
}

func TestCmdRetry_NoFailedItem(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "retry", batchID)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !strings.Contains(msg, "no failed item") && !strings.Contains(msg, "No failed item") {
		t.Fatalf("expected no failed item message, got %q", msg)
	}
}

func TestCmdSkip_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
		"Add dry-run flag to the audit command",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.batchItems[batchID][0].Status = state.BatchItemStatusFailed
	errMsg := "LLM unavailable"
	store.batchItems[batchID][0].Error = &errMsg
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "skip", batchID)
	if err != nil {
		t.Fatalf("skip failed: %v", err)
	}
	if !strings.Contains(msg, "Skipped") {
		t.Fatalf("expected skip message, got %q", msg)
	}

	store.mu.Lock()
	if store.batchItems[batchID][0].Status != state.BatchItemStatusSkipped {
		t.Fatalf("expected item skipped, got %q", store.batchItems[batchID][0].Status)
	}
	if store.batches[batchID].Status != state.BatchStatusRunning {
		t.Fatalf("expected batch running, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdSkip_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "skip", "")
	if err != nil {
		t.Fatalf("skip failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage, got %q", msg)
	}
}

func TestCmdStartBatch_AlreadyRunning(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{"Add config parser for scoring rules here"})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusRunning
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "start_batch", batchID)
	if err != nil {
		t.Fatalf("start_batch command failed: %v", err)
	}
	if !strings.Contains(msg, "cannot start") {
		t.Fatalf("expected cannot start message, got %q", msg)
	}
}

func TestCmdResumeBatch_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.mu.Unlock()

	msg, err := bot.handleCommand(ctx, "resume_batch", batchID)
	if err != nil {
		t.Fatalf("resume_batch failed: %v", err)
	}
	if !strings.Contains(msg, "Resuming") {
		t.Fatalf("expected resume message, got %q", msg)
	}

	store.mu.Lock()
	if store.batches[batchID].Status != state.BatchStatusRunning {
		t.Fatalf("expected running, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdResumeBatch_NotPaused(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})

	msg, err := bot.handleCommand(ctx, "resume_batch", batchID)
	if err != nil {
		t.Fatalf("resume_batch failed: %v", err)
	}
	if !strings.Contains(msg, "not paused") {
		t.Fatalf("expected not paused message, got %q", msg)
	}
}

func TestHyphenatedResumeBatchFallback(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.mu.Unlock()

	// Simulate Telegram not recognizing /resume-batch as a command,
	// so it arrives as free text.
	msg, err := bot.handleFreeText(ctx, fmt.Sprintf("/resume-batch %s", batchID))
	if err != nil {
		t.Fatalf("hyphenated command failed: %v", err)
	}
	if !strings.Contains(msg, "Resuming") {
		t.Fatalf("expected resume message via hyphen fallback, got %q", msg)
	}
}

func TestCmdResumeBatch_MissingArgs(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "resume_batch", "")
	if err != nil {
		t.Fatalf("resume_batch failed: %v", err)
	}
	if !strings.Contains(msg, "Usage") {
		t.Fatalf("expected usage, got %q", msg)
	}
}

func TestResumeQuotaPausedBatches(t *testing.T) {
	ctx := context.Background()
	store := newMockStore(time.Now().UTC())
	bot := newTestBot(store)

	batchID, _ := store.CreateBatch(ctx, 1, "", []string{
		"Add YAML config parser for scoring rules",
	})
	store.mu.Lock()
	store.batches[batchID].Status = state.BatchStatusPaused
	store.mu.Unlock()

	bot.resumeQuotaPausedBatches()

	// Verify batch was set to running.
	store.mu.Lock()
	if store.batches[batchID].Status != state.BatchStatusRunning {
		t.Fatalf("expected running, got %q", store.batches[batchID].Status)
	}
	store.mu.Unlock()
}

func TestCmdRefine_NoFile(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "refine", "")
	if err != nil {
		t.Fatalf("refine command failed: %v", err)
	}
	if !strings.Contains(msg, "/refine") {
		t.Fatalf("expected usage instructions mentioning /refine, got %q", msg)
	}
}

func TestResolveRefinerPromptPath_AbsoluteDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveRefinerPromptPath(dir, "test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Join(dir, "test.txt") {
		t.Errorf("expected %q, got %q", filepath.Join(dir, "test.txt"), got)
	}
}

func TestResolveRefinerPromptPath_AbsoluteDir_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveRefinerPromptPath(dir, "nonexistent.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
