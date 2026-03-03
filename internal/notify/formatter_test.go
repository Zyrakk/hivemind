package notify

import (
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
	if !strings.Contains(got, "truncado") {
		t.Fatalf("expected truncation marker in %q", got)
	}
}

func TestFormatNotificationMessages(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "needs input",
			msg:  FormatNeedsInputMessage("Flux", "Que hacemos?", "abc123"),
			want: "🟡",
		},
		{
			name: "pr ready",
			msg:  FormatPRReadyMessage("Flux", "https://example.com/pr/12", "Cambios listos", "def456"),
			want: "🔵",
		},
		{
			name: "worker failed",
			msg:  FormatWorkerFailedMessage("Flux", "Parser", "boom"),
			want: "🔴",
		},
		{
			name: "task completed",
			msg:  FormatTaskCompletedMessage("Flux", "Parser"),
			want: "✅",
		},
		{
			name: "consultant",
			msg:  FormatConsultantUsedMessage("claude", "Q", "A"),
			want: "💡",
		},
		{
			name: "budget",
			msg:  FormatBudgetWarningMessage("gemini", 87.5),
			want: "⚠️",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.msg, tc.want) {
				t.Fatalf("expected %q in message %q", tc.want, tc.msg)
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
	if !strings.Contains(statusMsg, "📊") {
		t.Fatalf("expected status emoji in %q", statusMsg)
	}
	if !strings.Contains(statusMsg, "Flux") {
		t.Fatalf("expected project name in %q", statusMsg)
	}

	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	pendingMsg := FormatPendingApprovalsMessage([]*PendingApproval{
		{ID: "abc123", Type: "plan", ProjectID: "Flux", Description: "Implementar RSS parser", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(23 * time.Hour)},
		{ID: "def456", Type: "input", ProjectID: "NHI-Watch", Description: "Intervalo de polling", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(11 * time.Hour)},
	}, now)
	if !strings.Contains(pendingMsg, "📋") {
		t.Fatalf("expected pending emoji in %q", pendingMsg)
	}
	if !strings.Contains(pendingMsg, "abc123") || !strings.Contains(pendingMsg, "def456") {
		t.Fatalf("expected approval ids in %q", pendingMsg)
	}
}

func TestFormatHelpMessage(t *testing.T) {
	help := FormatHelpMessage()
	if !strings.Contains(help, "/status") || !strings.Contains(help, "/help") {
		t.Fatalf("expected command list in %q", help)
	}
}
