package notify

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zyrakk/hivemind/internal/state"
)

func TestEscapeMarkdownV2(t *testing.T) {
	input := `_ * [ ] ( ) ~ ` + "`" + ` > # + - = | { } . !`
	got := EscapeMarkdownV2(input)

	expectedFragments := []string{
		`\_`, `\*`, `\[`, `\]`, `\(`, `\)`, `\~`, "\\`", `\>`, `\#`, `\+`, `\-`, `\=`, `\|`, `\{`, `\}`, `\.`, `\!`,
	}
	for _, fragment := range expectedFragments {
		if !strings.Contains(got, fragment) {
			t.Fatalf("expected escaped fragment %q in %q", fragment, got)
		}
	}
}

func TestTruncateTelegramMessage(t *testing.T) {
	input := strings.Repeat("a", telegramMessageLimit+100)
	got := TruncateTelegramMessage(input)
	if len([]rune(got)) > telegramMessageLimit {
		t.Fatalf("expected <= %d runes, got %d", telegramMessageLimit, len([]rune(got)))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation marker in %q", got)
	}
}

func TestFormatNotificationMessages(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want []string
	}{
		{
			name: "needs input",
			msg:  FormatNeedsInputMessage("Flux", "What should we do?", "abc123"),
			want: []string{"INPUT NEEDED", "Flux", "◐", "/approve abc123"},
		},
		{
			name: "pr ready",
			msg: FormatPRReadyMessage("Flux", "feat/rss", "def456",
				[]CheckResult{
					{Command: "go build ./...", Passed: true},
					{Command: "go test ./...", Passed: false, Output: "FAIL"},
				},
				nil),
			want: []string{"PR READY", "Flux", "feat/rss", "1/2", "✓ go build", "✗ go test", "/approve def456"},
		},
		{
			name: "worker failed",
			msg:  FormatWorkerFailedMessage("Flux", "Parser", "boom"),
			want: []string{"WORKER FAILED", "Flux", "Parser", "✗ boom"},
		},
		{
			name: "task completed",
			msg:  FormatTaskCompletedMessage("Flux", "Parser"),
			want: []string{"TASK DONE", "✓", "Flux", "Parser"},
		},
		{
			name: "consultant",
			msg:  FormatConsultantUsedMessage("claude", "How?", "Like this."),
			want: []string{"CONSULTANT", "claude", "Q:", "A:"},
		},
		{
			name: "budget",
			msg:  FormatBudgetWarningMessage("gemini", 87.5),
			want: []string{"BUDGET ALERT", "gemini", "87.5"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range tc.want {
				if !strings.Contains(tc.msg, w) {
					t.Fatalf("expected %q in message:\n%s", w, tc.msg)
				}
			}
			if len([]rune(tc.msg)) > telegramMessageLimit {
				t.Fatalf("message exceeds telegram limit")
			}
		})
	}
}

func TestFormatStatusAndPendingMessages(t *testing.T) {
	global := state.GlobalState{
		Projects: []state.ProjectSummary{
			{Name: "Flux", Status: state.ProjectStatusWorking, ActiveWorkers: 2},
			{Name: "NHI-Watch", Status: state.ProjectStatusNeedsInput},
			{Name: "ZCloud", Status: state.ProjectStatusPendingReview},
			{Name: "vuln-reporter", Status: state.ProjectStatusPaused},
		},
		Counters: state.Counters{ActiveWorkers: 3, PendingTasks: 7, PendingReview: 1},
	}

	statusMsg := FormatStatusMessage(global)
	for _, want := range []string{"HIVEMIND STATUS", "Flux", "Workers: 3", "┌", "└"} {
		if !strings.Contains(statusMsg, want) {
			t.Fatalf("expected %q in status message:\n%s", want, statusMsg)
		}
	}

	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	pendingMsg := FormatPendingApprovalsMessage([]*PendingApproval{
		{ID: "abc123", Type: "plan", ProjectID: "Flux", Description: "Implementar RSS parser", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(23 * time.Hour)},
		{ID: "def456", Type: "input", ProjectID: "NHI-Watch", Description: "Intervalo de polling", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(11 * time.Hour)},
	}, now)
	for _, want := range []string{"PENDING", "abc123", "def456", "┌", "└"} {
		if !strings.Contains(pendingMsg, want) {
			t.Fatalf("expected %q in pending message:\n%s", want, pendingMsg)
		}
	}
}

func TestFormatHelpMessage(t *testing.T) {
	help := FormatHelpMessage()
	for _, want := range []string{"/status", "/help", "/run", "HIVEMIND COMMANDS", "┌", "└"} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected %q in help message:\n%s", want, help)
		}
	}
}

func TestFormatterEnglish(t *testing.T) {
	help := FormatHelpMessage()
	if !strings.Contains(help, "HIVEMIND COMMANDS") || !strings.Contains(help, "global overview") {
		t.Fatalf("expected english help text, got %q", help)
	}

	detail := FormatProjectDetailMessage(state.ProjectDetail{
		ProjectRef: "flux",
		Project: state.Project{
			Name:   "Flux",
			Status: state.ProjectStatusWorking,
		},
		Tasks:    []state.Task{{Status: state.TaskStatusInProgress}},
		Events:   []state.Event{{Description: "Worker started"}},
		Progress: state.Progress{Overall: 0.5},
	})
	if !strings.Contains(detail, "PROJECT") || !strings.Contains(detail, "in progress") {
		t.Fatalf("expected english project detail text, got %q", detail)
	}
}

func TestFormatterNoOldEmojis(t *testing.T) {
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)

	global := state.GlobalState{
		Projects: []state.ProjectSummary{
			{Name: "Flux", Status: state.ProjectStatusWorking, ActiveWorkers: 2},
		},
		Counters: state.Counters{ActiveWorkers: 2, PendingTasks: 1, PendingReview: 1},
	}
	detail := state.ProjectDetail{
		ProjectRef: "flux",
		Project: state.Project{
			Name:   "Flux",
			Status: state.ProjectStatusWorking,
		},
		Tasks: []state.Task{{Status: state.TaskStatusInProgress}},
		Events: []state.Event{{
			Description: "Worker completed health check implementation",
		}},
		Progress: state.Progress{Overall: 0.5},
	}

	outputs := []string{
		FormatHelpMessage(),
		FormatStatusMessage(global),
		FormatProjectDetailMessage(detail),
		FormatNeedsInputMessage("flux", "Need input", "a-1"),
		FormatPRReadyMessage("flux", "main", "a-2", []CheckResult{{Command: "go test ./...", Passed: true}}, nil),
		FormatWorkerFailedMessage("flux", "Task", "boom"),
		FormatTaskCompletedMessage("flux", "Task"),
		FormatConsultantUsedMessage("claude", "Question", "Answer"),
		FormatBudgetWarningMessage("gemini", 88.1),
		FormatPendingApprovalsMessage([]*PendingApproval{
			{ID: "a-3", Type: "plan", ProjectID: "flux", Description: "desc", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		}, now),
	}

	oldIcons := []string{"📊", "🟢", "🟡", "🔵", "🔴", "⚪", "⏸", "▶️", "✅", "❌", "⚠️", "💡", "📋", "🧠", "🚀", "❓"}
	for _, out := range outputs {
		for _, oldIcon := range oldIcons {
			if strings.Contains(out, oldIcon) {
				t.Fatalf("found old icon %q in output %q", oldIcon, out)
			}
		}
	}
}

func TestFormatBoxDrawingPresent(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"status", FormatStatusMessage(state.GlobalState{Counters: state.Counters{ActiveWorkers: 1}})},
		{"help", FormatHelpMessage()},
		{"project", FormatProjectDetailMessage(state.ProjectDetail{ProjectRef: "x", Project: state.Project{Status: "working"}})},
		{"pr ready", FormatPRReadyMessage("p", "b", "a", nil, nil)},
		{"needs input", FormatNeedsInputMessage("p", "q", "a")},
		{"worker failed", FormatWorkerFailedMessage("p", "t", "e")},
		{"consultant", FormatConsultantUsedMessage("c", "q", "a")},
		{"budget", FormatBudgetWarningMessage("g", 50.0)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.msg, "┌") || !strings.Contains(tc.msg, "└") {
				t.Fatalf("expected box-drawing characters in %s message:\n%s", tc.name, tc.msg)
			}
			if !strings.Contains(tc.msg, "```") {
				t.Fatalf("expected code block in %s message:\n%s", tc.name, tc.msg)
			}
		})
	}
}

func TestFormatProgressMessage(t *testing.T) {
	msg := FormatProgressMessage("Flux", "launching", "task 1 of 3")
	if !strings.Contains(msg, "Flux") || !strings.Contains(msg, "launching") {
		t.Fatalf("expected project and stage in progress message: %q", msg)
	}
}

func TestFormatEngineSwitchMessage(t *testing.T) {
	msg := FormatEngineSwitchMessage("claude-code", "glm", "rate limit")
	for _, want := range []string{"ENGINE SWITCH", "claude-code", "glm", "rate limit"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected %q in engine switch message:\n%s", want, msg)
		}
	}
}

func TestFormatQuotaAlertMessage(t *testing.T) {
	msg := FormatQuotaAlertMessage(45, 100, 200, 500)
	for _, want := range []string{"QUOTA ALERT", "45/100", "200/500"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected %q in quota message:\n%s", want, msg)
		}
	}
}

func TestRenderProgressTimeline_SingleActive(t *testing.T) {
	tl := &ProgressTimeline{
		Project: "nhi-watch",
		Title:   "Add --dry-run flag to audit",
		Entries: []ProgressEntry{
			{Stage: "launching", Detail: "task 1/1", Status: ProgressStatusActive, Time: time.Now()},
		},
	}
	got := RenderProgressTimeline(tl)
	if !strings.Contains(got, "nhi-watch") {
		t.Fatalf("expected project name, got %q", got)
	}
	if !strings.Contains(got, "▸ launching") {
		t.Fatalf("expected active marker, got %q", got)
	}
	if !strings.Contains(got, "Add --dry-run flag") {
		t.Fatalf("expected title, got %q", got)
	}
}

func TestRenderProgressTimeline_MixedStatuses(t *testing.T) {
	tl := &ProgressTimeline{
		Project: "nhi-watch",
		Title:   "Add dry-run flag",
		Branch:  "feature/audit-dry-run",
		Entries: []ProgressEntry{
			{Stage: "launched", Status: ProgressStatusDone},
			{Stage: "worker started", Status: ProgressStatusDone},
			{Stage: "codex completed", Detail: "2m 31s", Status: ProgressStatusDone},
			{Stage: "pushed to origin", Status: ProgressStatusDone},
			{Stage: "evaluating", Detail: "7 checks", Status: ProgressStatusActive},
		},
	}
	got := RenderProgressTimeline(tl)
	if count := strings.Count(got, "✓"); count != 4 {
		t.Fatalf("expected 4 done markers, got %d in %q", count, got)
	}
	if !strings.Contains(got, "▸ evaluating") {
		t.Fatalf("expected active evaluating, got %q", got)
	}
	if !strings.Contains(got, "feature/audit-dry-run") {
		t.Fatalf("expected branch, got %q", got)
	}
}

func TestRenderProgressTimeline_FailedEntry(t *testing.T) {
	tl := &ProgressTimeline{
		Project: "nhi-watch",
		Title:   "Add dry-run flag",
		Entries: []ProgressEntry{
			{Stage: "launched", Status: ProgressStatusDone},
			{Stage: "evaluation", Detail: "5/7 checks passed", Status: ProgressStatusFailed},
			{Stage: "retry 2/3 launching", Status: ProgressStatusActive},
		},
	}
	got := RenderProgressTimeline(tl)
	if !strings.Contains(got, "✗ evaluation") {
		t.Fatalf("expected failed marker, got %q", got)
	}
}

func TestRenderProgressTimeline_CodeBlock(t *testing.T) {
	tl := &ProgressTimeline{
		Project: "flux",
		Title:   "test",
		Entries: []ProgressEntry{{Stage: "launching", Status: ProgressStatusActive}},
	}
	got := RenderProgressTimeline(tl)
	if !strings.HasPrefix(got, "```\n") || !strings.HasSuffix(got, "\n```") {
		t.Fatalf("expected code block wrapping, got %q", got)
	}
}

func TestRenderProgressTimeline_Nil(t *testing.T) {
	got := RenderProgressTimeline(nil)
	if got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}

func TestRenderProgressTimeline_TruncatesLongTimeline(t *testing.T) {
	tl := &ProgressTimeline{
		Project: "nhi-watch",
		Title:   "Task title",
	}
	for i := 0; i < 200; i++ {
		tl.Entries = append(tl.Entries, ProgressEntry{
			Stage:  fmt.Sprintf("stage-%d-with-some-longer-text-padding", i),
			Detail: "some detail text here",
			Status: ProgressStatusDone,
		})
	}
	got := RenderProgressTimeline(tl)
	if len([]rune(got)) > 4096 {
		t.Fatalf("rendered timeline exceeds 4096 chars: %d", len([]rune(got)))
	}
}

func TestFormatBatchCreatedMessage(t *testing.T) {
	msg := FormatBatchCreatedMessage("nhi-watch", "batch-123", []string{
		"Add YAML config parser for scoring rules",
		"Add --dry-run flag to the audit command",
		"Add --json output flag to the audit command",
		"Add CSV export to the reporter module",
	})
	for _, want := range []string{
		"BATCH CREATED",
		"nhi-watch",
		"Items:   4",
		"ready",
		"1 ◻ Add YAML config",
		"4 ◻ Add CSV export",
		"/start_batch batch-123",
		"/cancel_batch batch-123",
		"┌", "└", "```",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected %q in message:\n%s", want, msg)
		}
	}
	if len([]rune(msg)) > telegramMessageLimit {
		t.Fatalf("message exceeds telegram limit")
	}
}

func TestFormatPRReadyWithUserChecks(t *testing.T) {
	msg := FormatPRReadyMessage("Flux", "feat/x", "pr-1",
		[]CheckResult{
			{Command: "go build ./...", Passed: true},
		},
		[]UserCheck{
			{Description: "Verify UI renders correctly"},
		})
	if !strings.Contains(msg, "Manual review") {
		t.Fatalf("expected manual review section in PR message:\n%s", msg)
	}
	if !strings.Contains(msg, "Verify UI") {
		t.Fatalf("expected user check description in PR message:\n%s", msg)
	}
}
