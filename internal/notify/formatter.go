package notify

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zyrakk/hivemind/internal/state"
)

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

	suffix := EscapeMarkdownV2("... (truncado)")
	suffixRunes := []rune(suffix)
	keep := telegramMessageLimit - len(suffixRunes)
	if keep < 0 {
		keep = 0
	}

	return string(runes[:keep]) + suffix
}

func FormatNeedsInputMessage(projectID, question, approvalID string) string {
	lines := []string{
		fmt.Sprintf("🟡 %s: %s", projectID, question),
		fmt.Sprintf("Responde con /approve %s o /reject %s {razon}", approvalID, approvalID),
		"O responde directamente con texto.",
	}
	return formatEscapedLines(lines...)
}

func FormatPRReadyMessage(projectID, prURL, summary, approvalID string) string {
	lines := []string{
		EscapeMarkdownV2(fmt.Sprintf("🔵 %s: PR listo para review", projectID)),
		EscapeMarkdownV2(summary),
		EscapeMarkdownV2("🔗 ") + MarkdownV2Link("Abrir PR", prURL),
		EscapeMarkdownV2(fmt.Sprintf("Responde con /approve %s o /reject %s {razon}", approvalID, approvalID)),
	}
	return TruncateTelegramMessage(strings.Join(compactLines(lines), "\n"))
}

func FormatWorkerFailedMessage(projectID, taskTitle, errMsg string) string {
	lines := []string{
		fmt.Sprintf("🔴 %s: Worker fallo en '%s'", projectID, taskTitle),
		fmt.Sprintf("Error: %s", errMsg),
		"El orquestador intentara resolver automaticamente.",
	}
	return formatEscapedLines(lines...)
}

func FormatTaskCompletedMessage(projectID, taskTitle string) string {
	return formatEscapedLines(fmt.Sprintf("✅ %s: '%s' completada", projectID, taskTitle))
}

func FormatConsultantUsedMessage(consultantName, question, summary string) string {
	lines := []string{
		fmt.Sprintf("💡 Consulta a %s:", consultantName),
		fmt.Sprintf("Pregunta: %s", question),
		fmt.Sprintf("Respuesta: %s", summary),
	}
	return formatEscapedLines(lines...)
}

func FormatBudgetWarningMessage(consultantName string, percentUsed float64) string {
	return formatEscapedLines(fmt.Sprintf("⚠️ Presupuesto %s: %.1f%% usado este mes", consultantName, percentUsed))
}

func FormatStatusMessage(global state.GlobalState) string {
	lines := []string{"📊 hivemind Status"}
	for _, project := range global.Projects {
		lines = append(lines, formatProjectSummaryLine(project))
	}
	lines = append(lines, "---")
	lines = append(lines, fmt.Sprintf(
		"Workers: %d activos | Tareas: %d pendientes | PRs: %d",
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

	lastEvent := "sin eventos recientes"
	if len(detail.Events) > 0 && strings.TrimSpace(detail.Events[0].Description) != "" {
		lastEvent = strings.TrimSpace(detail.Events[0].Description)
	}

	progress := int(detail.Progress.Overall * 100)
	lines := []string{
		fmt.Sprintf("📊 Proyecto %s", detail.ProjectRef),
		fmt.Sprintf("Estado: %s", detail.Project.Status),
		fmt.Sprintf("Tareas en curso: %d", inProgress),
		fmt.Sprintf("Ultimo evento: %s", lastEvent),
		fmt.Sprintf("Progreso: %d%%", progress),
	}
	return formatEscapedLines(lines...)
}

func FormatPendingApprovalsMessage(approvals []*PendingApproval, now time.Time) string {
	if len(approvals) == 0 {
		return formatEscapedLines("📋 Approvals pendientes: ninguno")
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

	lines := []string{"📋 Approvals pendientes:"}
	for idx, approval := range sorted {
		line := fmt.Sprintf(
			"%d. %s (%s) %s - %s [%s]",
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
		"Comandos disponibles:",
		"/status - estado global",
		"/project {nombre} - detalle de proyecto",
		"/approve {id} - aprobar plan/PR/input",
		"/reject {id} {razon} - rechazar plan/PR/input",
		"/pause {proyecto} - pausar workers del proyecto",
		"/resume {proyecto} - reanudar proyecto",
		"/consult {pregunta} - consultar Claude/Gemini",
		"/pending - listar approvals activos",
		"/help - esta ayuda",
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
		return fmt.Sprintf("🟢 %s: %d workers activos", project.Name, project.ActiveWorkers)
	case state.ProjectStatusNeedsInput:
		return fmt.Sprintf("🟡 %s: esperando input", project.Name)
	case state.ProjectStatusPendingReview:
		return fmt.Sprintf("🔵 %s: pendiente review", project.Name)
	case state.ProjectStatusBlocked:
		return fmt.Sprintf("🔴 %s: bloqueado", project.Name)
	case state.ProjectStatusPaused:
		return fmt.Sprintf("⚪ %s: pausado", project.Name)
	default:
		return fmt.Sprintf("⚪ %s: %s", project.Name, project.Status)
	}
}

func formatRemaining(remaining time.Duration) string {
	if remaining <= 0 {
		return "expirado"
	}
	if remaining >= time.Hour {
		return fmt.Sprintf("%dh restantes", int(remaining.Round(time.Hour)/time.Hour))
	}
	minutes := int(remaining.Round(time.Minute) / time.Minute)
	if minutes <= 0 {
		minutes = 1
	}
	return fmt.Sprintf("%dm restantes", minutes)
}
