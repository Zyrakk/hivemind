package notify

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zyrakk/hivemind/internal/checklist"
	"github.com/zyrakk/hivemind/internal/state"
)

// CheckResult is an alias for checklist.CheckResult.
type CheckResult = checklist.CheckResult

// UserCheck is an alias for checklist.UserCheck.
type UserCheck = checklist.UserCheck

const telegramMessageLimit = 4096

var markdownV2SpecialChars = map[rune]struct{}{
	'\\': {},
	'_':  {},
	'*':  {},
	'[':  {},
	']':  {},
	'(':  {},
	')':  {},
	'~':  {},
	'`':  {},
	'>':  {},
	'#':  {},
	'+':  {},
	'-':  {},
	'=':  {},
	'|':  {},
	'{':  {},
	'}':  {},
	'.':  {},
	'!':  {},
}

func EscapeMarkdownV2(text string) string {
	if text == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(text) * 2)
	for _, r := range text {
		if _, ok := markdownV2SpecialChars[r]; ok {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}

	return b.String()
}

func TruncateTelegramMessage(text string) string {
	runes := []rune(text)
	if len(runes) <= telegramMessageLimit {
		return text
	}

	suffix := EscapeMarkdownV2("... (truncated)")
	suffixRunes := []rune(suffix)
	keep := telegramMessageLimit - len(suffixRunes)
	if keep < 0 {
		keep = 0
	}

	return string(runes[:keep]) + suffix
}

func FormatNeedsInputMessage(projectID, question, approvalID string) string {
	lines := []string{
		fmt.Sprintf("◐ %s: %s", projectID, question),
		fmt.Sprintf("Reply with /approve %s or /reject %s {reason}", approvalID, approvalID),
		"Or reply directly with free text.",
	}
	return formatEscapedLines(lines...)
}

func FormatPRReadyMessage(projectID, branch, approvalID string, autoResults []CheckResult, userChecks []UserCheck) string {
	lines := []string{
		EscapeMarkdownV2(fmt.Sprintf("◎ %s: PR ready for review", projectID)),
		EscapeMarkdownV2(fmt.Sprintf("Branch: %s", branch)),
		EscapeMarkdownV2(fmt.Sprintf("Reply with /approve %s or /reject %s {reason}", approvalID, approvalID)),
	}
	return TruncateTelegramMessage(strings.Join(compactLines(lines), "\n"))
}

func FormatWorkerFailedMessage(projectID, taskTitle, errMsg string) string {
	lines := []string{
		fmt.Sprintf("! %s: worker failed on '%s'", projectID, taskTitle),
		fmt.Sprintf("Error: %s", errMsg),
		"The orchestrator will attempt to resolve automatically.",
	}
	return formatEscapedLines(lines...)
}

func FormatTaskCompletedMessage(projectID, taskTitle string) string {
	return formatEscapedLines(fmt.Sprintf("✓ %s: '%s' completed", projectID, taskTitle))
}

func FormatConsultantUsedMessage(consultantName, question, summary string) string {
	lines := []string{
		fmt.Sprintf("→ Consulted %s:", consultantName),
		fmt.Sprintf("Question: %s", question),
		fmt.Sprintf("Response: %s", summary),
	}
	return formatEscapedLines(lines...)
}

func FormatBudgetWarningMessage(consultantName string, percentUsed float64) string {
	return formatEscapedLines(fmt.Sprintf("‼ Budget %s: %.1f%% used this month", consultantName, percentUsed))
}

func FormatStatusMessage(global state.GlobalState) string {
	lines := []string{"▸ Hivemind Status"}
	for _, project := range global.Projects {
		lines = append(lines, formatProjectSummaryLine(project))
	}
	lines = append(lines, "---")
	lines = append(lines, fmt.Sprintf(
		"Workers: %d active | Tasks: %d pending | PRs: %d",
		global.Counters.ActiveWorkers,
		global.Counters.PendingTasks,
		global.Counters.PendingReview,
	))
	return formatEscapedLines(lines...)
}

func FormatProjectDetailMessage(detail state.ProjectDetail) string {
	inProgress := 0
	for _, task := range detail.Tasks {
		if task.Status == state.TaskStatusInProgress {
			inProgress++
		}
	}

	lastEvent := "no recent events"
	if len(detail.Events) > 0 && strings.TrimSpace(detail.Events[0].Description) != "" {
		lastEvent = strings.TrimSpace(detail.Events[0].Description)
	}

	progress := int(detail.Progress.Overall * 100)
	lines := []string{
		fmt.Sprintf("▸ Project %s", detail.ProjectRef),
		fmt.Sprintf("Status: %s", detail.Project.Status),
		fmt.Sprintf("Tasks in progress: %d", inProgress),
		fmt.Sprintf("Last event: %s", lastEvent),
		fmt.Sprintf("Progress: %d%%", progress),
	}
	return formatEscapedLines(lines...)
}

func FormatPendingApprovalsMessage(approvals []*PendingApproval, now time.Time) string {
	if len(approvals) == 0 {
		return formatEscapedLines("▸ Pending approvals: none")
	}

	sorted := make([]*PendingApproval, 0, len(approvals))
	for _, approval := range approvals {
		if approval != nil {
			sorted = append(sorted, approval)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})

	lines := []string{"▸ Pending approvals:"}
	for idx, approval := range sorted {
		line := fmt.Sprintf(
			"%d. %s (%s) %s — %s [%s]",
			idx+1,
			approval.ID,
			approval.Type,
			approval.ProjectID,
			approval.Description,
			formatRemaining(approval.ExpiresAt.Sub(now)),
		)
		lines = append(lines, line)
	}

	return formatEscapedLines(lines...)
}

func FormatHelpMessage() string {
	lines := []string{
		"▸ Hivemind Commands",
		"/status — global overview",
		"/project {name} — project detail",
		"/run {project} {directive} — submit new work",
		"/approve {id} — approve plan/PR/input",
		"/reject {id} {reason} — reject with feedback",
		"/pause {project} — pause project workers",
		"/resume {project} — resume project",
		"/consult {question} — query Claude/Gemini",
		"/pending — list active approvals",
		"/help — this message",
	}
	return formatEscapedLines(lines...)
}

func MarkdownV2Link(label, rawURL string) string {
	label = EscapeMarkdownV2(label)
	url := strings.TrimSpace(rawURL)
	url = strings.ReplaceAll(url, `\`, `\\`)
	url = strings.ReplaceAll(url, ")", `\)`)
	url = strings.ReplaceAll(url, "(", `\(`)
	if url == "" {
		return label
	}
	return "[" + label + "](" + url + ")"
}

func formatEscapedLines(lines ...string) string {
	escaped := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		escaped = append(escaped, EscapeMarkdownV2(line))
	}
	return TruncateTelegramMessage(strings.Join(escaped, "\n"))
}

func compactLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		result = append(result, line)
	}
	return result
}

func formatProjectSummaryLine(project state.ProjectSummary) string {
	switch project.Status {
	case state.ProjectStatusWorking:
		return fmt.Sprintf("● %s: %d workers active", project.Name, project.ActiveWorkers)
	case state.ProjectStatusNeedsInput:
		return fmt.Sprintf("◐ %s: awaiting input", project.Name)
	case state.ProjectStatusPendingReview:
		return fmt.Sprintf("◎ %s: pending review", project.Name)
	case state.ProjectStatusBlocked:
		return fmt.Sprintf("■ %s: blocked", project.Name)
	case state.ProjectStatusPaused:
		return fmt.Sprintf("‖ %s: paused", project.Name)
	default:
		return fmt.Sprintf("  %s: %s", project.Name, project.Status)
	}
}

func formatRemaining(remaining time.Duration) string {
	if remaining <= 0 {
		return "expired"
	}
	if remaining >= time.Hour {
		return fmt.Sprintf("%dh remaining", int(remaining.Round(time.Hour)/time.Hour))
	}
	minutes := int(remaining.Round(time.Minute) / time.Minute)
	if minutes <= 0 {
		minutes = 1
	}
	return fmt.Sprintf("%dm remaining", minutes)
}
