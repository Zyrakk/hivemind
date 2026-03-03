package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/zyrakk/hivemind/internal/llm"
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
	if err := n.NotifyPRReady(ctx, "p", "u", "s", "a"); err != nil {
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
	if err := n.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
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
		if !strings.Contains(msg, "Proyecto") {
			t.Fatalf("expected project header in message: %q", msg)
		}
	})

	t.Run("/approve plan", func(t *testing.T) {
		exec := &mockPlanExecutor{}
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
		if !strings.Contains(msg, "Aprobado") {
			t.Fatalf("expected success message, got %q", msg)
		}
		if exec.planID != "plan-1" {
			t.Fatalf("expected ExecutePlan called with plan-1, got %q", exec.planID)
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
		if !strings.Contains(msg, "Aprobado") {
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
		if !strings.Contains(msg, "Rechazado") {
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
		if !strings.Contains(msg, "pausado") {
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
		if !strings.Contains(msg, "reanudado") {
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
		if !strings.Contains(msg, "Consulta") {
			t.Fatalf("expected consultant response, got %q", msg)
		}
		if store.countEventsByType("consultant_used") == 0 {
			t.Fatalf("expected consultant_used event")
		}
	})

	t.Run("/pending", func(t *testing.T) {
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
		if !strings.Contains(msg, "Approvals") {
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
	}
}

type mockPlanExecutor struct {
	planID string
	err    error
}

func (m *mockPlanExecutor) ExecutePlan(ctx context.Context, planID string) error {
	_ = ctx
	m.planID = planID
	return m.err
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
	if !strings.Contains(msg, "No entiendo") {
		t.Fatalf("expected unknown message, got %q", msg)
	}
}

func TestApproveUnknownApprovalID(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "approve", "missing")
	if err != nil {
		t.Fatalf("approve command returned error: %v", err)
	}
	if !strings.Contains(msg, "Approval no encontrado") {
		t.Fatalf("expected not found message, got %q", msg)
	}
}

func TestConsultNoConsultantAvailable(t *testing.T) {
	bot := newTestBot(newMockStore(time.Now().UTC()))
	msg, err := bot.handleCommand(context.Background(), "consult", "pregunta")
	if err != nil {
		t.Fatalf("consult command error = %v", err)
	}
	if !strings.Contains(msg, "No hay consultores") {
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
