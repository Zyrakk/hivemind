package notify

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zyrakk/hivemind/internal/checklist"
	"github.com/zyrakk/hivemind/internal/planner"
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

func codeBlock(content string) string {
	return "```\n" + content + "\n```"
}

func FormatNeedsInputMessage(projectID, question, approvalID string) string {
	var box strings.Builder
	box.WriteString("┌─ INPUT NEEDED ─────────────\n")
	box.WriteString(fmt.Sprintf("│ Project: %s\n", projectID))
	box.WriteString("├────────────────────────────\n")
	box.WriteString(fmt.Sprintf("│ ◐ %s\n", question))
	box.WriteString("└────────────────────────────")

	msg := codeBlock(box.String())
	msg += "\n" + EscapeMarkdownV2(fmt.Sprintf("/approve %s", approvalID))
	msg += "\n" + EscapeMarkdownV2(fmt.Sprintf("/reject %s {reason}", approvalID))
	msg += "\n" + EscapeMarkdownV2("Or reply directly with free text.")

	return TruncateTelegramMessage(msg)
}

func FormatPRReadyMessage(projectID, branch, approvalID string, autoResults []CheckResult, userChecks []UserCheck) string {
	passed := 0
	for _, r := range autoResults {
		if r.Passed {
			passed++
		}
	}

	var box strings.Builder
	box.WriteString("┌─ PR READY ─────────────────\n")
	box.WriteString(fmt.Sprintf("│ Project: %s\n", projectID))
	box.WriteString(fmt.Sprintf("│ Branch:  %s\n", branch))
	if len(autoResults) > 0 {
		box.WriteString("├────────────────────────────\n")
		box.WriteString(fmt.Sprintf("│ ✓ Automated: %d/%d\n", passed, len(autoResults)))
		for _, r := range autoResults {
			icon := "✓"
			if !r.Passed {
				icon = "✗"
			}
			cmd := r.Command
			if cmd == "" {
				cmd = r.Description
			}
			box.WriteString(fmt.Sprintf("│   %s %s\n", icon, cmd))
		}
	}
	if len(userChecks) > 0 {
		box.WriteString("├────────────────────────────\n")
		box.WriteString("│ Manual review required:\n")
		for _, c := range userChecks {
			box.WriteString(fmt.Sprintf("│   ◐ %s\n", c.Description))
		}
	}
	box.WriteString("└────────────────────────────")

	msg := codeBlock(box.String())
	msg += "\n" + EscapeMarkdownV2(fmt.Sprintf("/approve %s", approvalID))

	return TruncateTelegramMessage(msg)
}

func FormatWorkerFailedMessage(projectID, taskTitle, errMsg string) string {
	var box strings.Builder
	box.WriteString("┌─ WORKER FAILED ────────────\n")
	box.WriteString(fmt.Sprintf("│ Project: %s\n", projectID))
	box.WriteString(fmt.Sprintf("│ Task:    %s\n", taskTitle))
	box.WriteString("├────────────────────────────\n")
	box.WriteString(fmt.Sprintf("│ ✗ %s\n", errMsg))
	box.WriteString("└────────────────────────────")

	return TruncateTelegramMessage(codeBlock(box.String()))
}

func FormatTaskCompletedMessage(projectID, taskTitle string) string {
	return TruncateTelegramMessage(codeBlock(fmt.Sprintf(
		"┌─ TASK DONE ────────────────\n│ ✓ %s │ %s\n└────────────────────────────",
		projectID, taskTitle)))
}

func FormatConsultantUsedMessage(consultantName, question, summary string) string {
	var box strings.Builder
	box.WriteString("┌─ CONSULTANT ───────────────\n")
	box.WriteString(fmt.Sprintf("│ → %s\n", consultantName))
	box.WriteString("├────────────────────────────\n")
	box.WriteString(fmt.Sprintf("│ Q: %s\n", question))
	box.WriteString(fmt.Sprintf("│ A: %s\n", summary))
	box.WriteString("└────────────────────────────")

	return TruncateTelegramMessage(codeBlock(box.String()))
}

func FormatBudgetWarningMessage(consultantName string, percentUsed float64) string {
	return TruncateTelegramMessage(codeBlock(fmt.Sprintf(
		"┌─ BUDGET ALERT ─────────────\n│ ● %s: %.1f%% used\n└────────────────────────────",
		consultantName, percentUsed)))
}

func FormatStatusMessage(global state.GlobalState) string {
	var box strings.Builder
	box.WriteString("┌─ HIVEMIND STATUS ──────────\n")
	box.WriteString(fmt.Sprintf("│ Workers: %d │ Tasks: %d │ PRs: %d\n",
		global.Counters.ActiveWorkers,
		global.Counters.PendingTasks,
		global.Counters.PendingReview))
	box.WriteString("├────────────────────────────\n")
	for _, project := range global.Projects {
		box.WriteString(formatProjectSummaryLine(project))
		box.WriteString("\n")
	}
	box.WriteString("└────────────────────────────")

	return TruncateTelegramMessage(codeBlock(box.String()))
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

	var box strings.Builder
	box.WriteString("┌─ PROJECT ──────────────────\n")
	box.WriteString(fmt.Sprintf("│ Name:     %s\n", detail.ProjectRef))
	box.WriteString(fmt.Sprintf("│ Status:   %s\n", detail.Project.Status))
	box.WriteString(fmt.Sprintf("│ Tasks:    %d in progress\n", inProgress))
	box.WriteString(fmt.Sprintf("│ Progress: %d%%\n", progress))
	box.WriteString("├────────────────────────────\n")
	box.WriteString(fmt.Sprintf("│ Last: %s\n", lastEvent))
	box.WriteString("└────────────────────────────")

	return TruncateTelegramMessage(codeBlock(box.String()))
}

func FormatPendingApprovalsMessage(approvals []*PendingApproval, now time.Time) string {
	if len(approvals) == 0 {
		return codeBlock("┌─ PENDING ──────────────────\n│ No pending approvals\n└────────────────────────────")
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

	var box strings.Builder
	box.WriteString("┌─ PENDING ──────────────────\n")
	for idx, approval := range sorted {
		box.WriteString(fmt.Sprintf("│ %d. %s (%s) %s\n│    %s [%s]\n",
			idx+1,
			approval.ID,
			approval.Type,
			approval.ProjectID,
			approval.Description,
			formatRemaining(approval.ExpiresAt.Sub(now)),
		))
	}
	box.WriteString("└────────────────────────────")

	return TruncateTelegramMessage(codeBlock(box.String()))
}

func FormatHelpMessage() string {
	var box strings.Builder
	box.WriteString("┌─ HIVEMIND COMMANDS ────────\n")
	box.WriteString("│ /status      global overview\n")
	box.WriteString("│ /project {n} project detail\n")
	box.WriteString("│ /run {p} {d} submit new work\n")
	box.WriteString("│ /approve {id} approve item\n")
	box.WriteString("│ /reject {id}  reject + reason\n")
	box.WriteString("│ /pause {p}   pause project\n")
	box.WriteString("│ /resume {p}  resume project\n")
	box.WriteString("│ /consult {q} query LLM\n")
	box.WriteString("│ /pending     active approvals\n")
	box.WriteString("│ /help        this message\n")
	box.WriteString("└────────────────────────────")

	return codeBlock(box.String())
}

func FormatPlanMessage(projectRef string, result *planner.PlanResult) string {
	if result == nil || result.Plan == nil {
		return codeBlock("┌─ PLAN ─────────────────────\n│ ✗ Empty plan result\n└────────────────────────────")
	}

	engineName := strings.TrimSpace(result.Engine)
	if engineName == "" {
		engineName = "glm"
	}

	var box strings.Builder
	box.WriteString("┌─ PLAN ─────────────────────\n")
	box.WriteString(fmt.Sprintf("│ Project:    %s\n", projectRef))
	box.WriteString(fmt.Sprintf("│ Engine:     %s\n", engineName))
	box.WriteString(fmt.Sprintf("│ Tasks:      %d\n", len(result.Plan.Tasks)))
	box.WriteString(fmt.Sprintf("│ Confidence: %.0f%%\n", result.Plan.Confidence*100))
	if result.ConsultantUsed {
		box.WriteString("│ ✓ Validated by consultant\n")
	}
	box.WriteString("├────────────────────────────\n")
	for i, task := range result.Plan.Tasks {
		title := strings.TrimSpace(task.Title)
		if title == "" {
			title = fmt.Sprintf("Task %d", i+1)
		}
		box.WriteString(fmt.Sprintf("│ %d ▸ %s\n", i+1, title))
		briefing := strings.TrimSpace(task.Briefing)
		if briefing == "" {
			briefing = strings.TrimSpace(task.Description)
		}
		if briefing != "" {
			box.WriteString(fmt.Sprintf("│     %s\n", briefing))
		}
		if len(task.DependsOn) > 0 {
			box.WriteString(fmt.Sprintf("│     Depends on: %s\n", strings.Join(task.DependsOn, ", ")))
		}
	}
	box.WriteString("└────────────────────────────")

	msg := codeBlock(box.String())
	msg += "\n" + EscapeMarkdownV2(fmt.Sprintf("/approve %s", result.PlanID))
	msg += "\n" + EscapeMarkdownV2(fmt.Sprintf("/reject %s {reason}", result.PlanID))

	return TruncateTelegramMessage(msg)
}

func FormatProgressMessage(project, stage, detail string) string {
	return EscapeMarkdownV2(fmt.Sprintf("▸ %s │ %s │ %s", project, stage, detail))
}

func FormatEngineSwitchMessage(from, to, reason string) string {
	var box strings.Builder
	box.WriteString("┌─ ENGINE SWITCH ────────────\n")
	box.WriteString(fmt.Sprintf("│ ● %s → %s\n", strings.TrimSpace(from), strings.TrimSpace(to)))
	box.WriteString(fmt.Sprintf("│   Reason: %s\n", strings.TrimSpace(reason)))
	box.WriteString("└────────────────────────────")

	return codeBlock(box.String())
}

func FormatQuotaAlertMessage(dailyUsed, dailyLimit, weeklyUsed, weeklyLimit int) string {
	var box strings.Builder
	box.WriteString("┌─ QUOTA ALERT ──────────────\n")
	box.WriteString(fmt.Sprintf("│ ● Claude Code usage\n"))
	box.WriteString(fmt.Sprintf("│   Daily:  %d/%d\n", dailyUsed, dailyLimit))
	box.WriteString(fmt.Sprintf("│   Weekly: %d/%d\n", weeklyUsed, weeklyLimit))
	box.WriteString("└────────────────────────────")

	return codeBlock(box.String())
}

const (
	ProgressStatusDone   = "done"
	ProgressStatusActive = "active"
	ProgressStatusFailed = "failed"
)

// ProgressEntry represents a single stage in the progress timeline.
type ProgressEntry struct {
	Stage  string
	Detail string
	Status string // ProgressStatusDone, ProgressStatusActive, ProgressStatusFailed
	Time   time.Time // recorded for elapsed-time display in future iterations
}

// ProgressTimeline tracks the full progress state for a single task.
type ProgressTimeline struct {
	Project string
	Title   string
	Branch  string
	Entries []ProgressEntry
}

// RenderProgressTimeline renders a box-drawing timeline as a code block.
func RenderProgressTimeline(tl *ProgressTimeline) string {
	if tl == nil {
		return ""
	}

	var box strings.Builder
	box.WriteString(fmt.Sprintf("┌─ %s ────────────────────\n", tl.Project))
	box.WriteString(fmt.Sprintf("│ %s\n", tl.Title))
	if tl.Branch != "" {
		box.WriteString(fmt.Sprintf("│ branch: %s\n", tl.Branch))
	}
	box.WriteString("├────────────────────────────\n")
	for _, entry := range tl.Entries {
		icon := "▸"
		switch entry.Status {
		case ProgressStatusDone:
			icon = "✓"
		case ProgressStatusFailed:
			icon = "✗"
		}
		line := fmt.Sprintf("│ %s %s", icon, entry.Stage)
		if entry.Detail != "" {
			line += fmt.Sprintf(" (%s)", entry.Detail)
		}
		box.WriteString(line + "\n")
	}
	box.WriteString("└────────────────────────────")

	return TruncateTelegramMessage(codeBlock(box.String()))
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

func formatProjectSummaryLine(project state.ProjectSummary) string {
	icon := " "
	status := project.Status
	switch project.Status {
	case state.ProjectStatusWorking:
		icon = "▸"
		status = fmt.Sprintf("%d workers", project.ActiveWorkers)
	case state.ProjectStatusNeedsInput:
		icon = "◐"
		status = "awaiting input"
	case state.ProjectStatusPendingReview:
		icon = "◐"
		status = "pending review"
	case state.ProjectStatusBlocked:
		icon = "■"
		status = "blocked"
	case state.ProjectStatusPaused:
		icon = "‖"
		status = "paused"
	}
	return fmt.Sprintf("│ %s %-14s │ %s", icon, project.Name, status)
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
