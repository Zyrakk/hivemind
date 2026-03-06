package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultClaudeCodeBinary    = "claude"
	defaultClaudeCodeTimeout   = 10 * time.Minute
	defaultClaudeCodePromptDir = "prompts"
)

var shellSegmentSplitPattern = regexp.MustCompile(`\|\||&&|[|;&]`)

type ClaudeCodeConfig struct {
	Binary         string             `yaml:"binary"`
	Model          string             `yaml:"model"`
	TimeoutMinutes int                `yaml:"timeout_minutes"`
	PromptDir      string             `yaml:"prompt_dir"`
	Usage          UsageTrackerConfig `yaml:"usage"`
}

type ClaudeCodeEngine struct {
	binary       string
	model        string
	timeout      time.Duration
	promptDir    string
	logger       *slog.Logger
	usageTracker *UsageTracker
}

type invokeResult struct {
	Result       string `json:"result"`
	Model        string `json:"model"`
	SessionID    string `json:"session_id"`
	InputTokens  int
	OutputTokens int
}

func NewClaudeCodeEngine(cfg ClaudeCodeConfig, logger *slog.Logger) *ClaudeCodeEngine {
	binary := strings.TrimSpace(cfg.Binary)
	if binary == "" {
		binary = defaultClaudeCodeBinary
	}

	timeout := defaultClaudeCodeTimeout
	if cfg.TimeoutMinutes > 0 {
		timeout = time.Duration(cfg.TimeoutMinutes) * time.Minute
	}

	promptDir := strings.TrimSpace(cfg.PromptDir)
	if promptDir == "" {
		promptDir = defaultClaudeCodePromptDir
	}

	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	var usageTracker *UsageTracker
	if !cfg.Usage.isZero() {
		usageTracker = NewUsageTracker(cfg.Usage, logger)
	}

	return &ClaudeCodeEngine{
		binary:       binary,
		model:        strings.TrimSpace(cfg.Model),
		timeout:      timeout,
		promptDir:    promptDir,
		logger:       logger,
		usageTracker: usageTracker,
	}
}

func (e *ClaudeCodeEngine) Think(ctx context.Context, req ThinkRequest) (*ThinkResult, error) {
	if e == nil {
		return nil, errors.New("claude code engine is nil")
	}

	systemPrompt, err := e.loadPrompt("thinker_claude_code.txt")
	if err != nil {
		return nil, err
	}

	invoked, err := e.invoke(ctx, systemPrompt, buildThinkPrompt(req), true)
	if err != nil {
		return nil, err
	}

	return parseThinkResult(invoked.Result)
}

func (e *ClaudeCodeEngine) Propose(ctx context.Context, req ProposeRequest) (*PlanResult, error) {
	if e == nil {
		return nil, errors.New("claude code engine is nil")
	}

	systemPrompt, err := e.loadPrompt("proposer_claude_code.txt")
	if err != nil {
		return nil, err
	}

	invoked, err := e.invoke(ctx, systemPrompt, buildProposePrompt(req), false)
	if err != nil {
		return nil, err
	}

	return parsePlanResult(invoked.Result)
}

func (e *ClaudeCodeEngine) Rebuild(ctx context.Context, req RebuildRequest) (*PlanResult, error) {
	if e == nil {
		return nil, errors.New("claude code engine is nil")
	}

	systemPrompt, err := e.loadPrompt("proposer_claude_code.txt")
	if err != nil {
		return nil, err
	}

	userPrompt, err := buildRebuildPrompt(req)
	if err != nil {
		return nil, err
	}

	invoked, err := e.invoke(ctx, systemPrompt, userPrompt, false)
	if err != nil {
		return nil, err
	}

	return parsePlanResult(invoked.Result)
}

func (e *ClaudeCodeEngine) Evaluate(ctx context.Context, req EvalRequest) (*EvalResult, error) {
	if e == nil {
		return nil, errors.New("claude code engine is nil")
	}

	systemPrompt, err := e.loadPrompt("evaluator_claude_code.txt")
	if err != nil {
		return nil, err
	}

	invoked, err := e.invoke(ctx, systemPrompt, buildEvalPrompt(req), false)
	if err != nil {
		return nil, err
	}

	return parseEvalResult(invoked.Result)
}

func (e *ClaudeCodeEngine) Name() string {
	return "claude-code"
}

func (e *ClaudeCodeEngine) UsageTracker() *UsageTracker {
	if e == nil {
		return nil
	}

	return e.usageTracker
}

func (e *ClaudeCodeEngine) Available(context.Context) bool {
	if e == nil {
		return false
	}

	_, err := exec.LookPath(e.binary)
	return err == nil && (e.usageTracker == nil || e.usageTracker.CanInvoke())
}

func (e *ClaudeCodeEngine) invoke(ctx context.Context, systemPrompt, userPrompt string, allowWebSearch bool) (*invokeResult, error) {
	if e == nil {
		return nil, errors.New("claude code engine is nil")
	}
	if e.usageTracker != nil && !e.usageTracker.CanInvoke() {
		return nil, fmt.Errorf("claude code blocked: %s", e.usageTracker.BlockReason())
	}

	combinedPrompt := buildCombinedPrompt(systemPrompt, userPrompt)
	args := buildInvokeArgs(combinedPrompt, e.model, allowWebSearch)

	cmdCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(cmdCtx, e.binary, args...)
	cmd.Env = os.Environ()

	outputBytes, err := cmd.Output()
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("claude code command timed out after %s", e.timeout)
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return nil, fmt.Errorf("claude code command canceled: %w", cmdCtx.Err())
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr == "" {
				stderr = strings.TrimSpace(exitErr.Error())
			}
			return nil, fmt.Errorf("claude code command failed: %s", stderr)
		}

		return nil, fmt.Errorf("run claude code command: %w", err)
	}

	result, err := parseInvokeOutput(outputBytes)
	if err != nil {
		return nil, err
	}

	if e.usageTracker != nil {
		e.usageTracker.Record(result.InputTokens, result.OutputTokens)
	}

	model := strings.TrimSpace(result.Model)
	if model == "" {
		model = e.model
	}

	e.logger.Info(
		"claude code invocation completed",
		slog.String("model", model),
		slog.String("session_id", result.SessionID),
		slog.Int("input_tokens", result.InputTokens),
		slog.Int("output_tokens", result.OutputTokens),
		slog.Duration("latency", time.Since(start)),
		slog.Bool("web_search_enabled", allowWebSearch),
	)

	return result, nil
}

func (e *ClaudeCodeEngine) loadPrompt(filename string) (string, error) {
	if e == nil {
		return "", errors.New("claude code engine is nil")
	}

	promptPath, err := resolvePromptPath(e.promptDir, filename)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("read prompt %q: %w", filename, err)
	}

	return strings.TrimSpace(string(data)), nil
}

func resolvePromptPath(promptDir, filename string) (string, error) {
	if strings.TrimSpace(promptDir) == "" {
		promptDir = defaultClaudeCodePromptDir
	}

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

func parseInvokeOutput(output []byte) (*invokeResult, error) {
	var payload struct {
		Result    string `json:"result"`
		Model     string `json:"model"`
		SessionID string `json:"session_id"`
		Usage     struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode claude code output: %w", err)
	}

	return &invokeResult{
		Result:       payload.Result,
		Model:        payload.Model,
		SessionID:    payload.SessionID,
		InputTokens:  payload.Usage.InputTokens,
		OutputTokens: payload.Usage.OutputTokens,
	}, nil
}

func buildCombinedPrompt(systemPrompt, userPrompt string) string {
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" {
		return userPrompt
	}

	return fmt.Sprintf("<system>\n%s\n</system>\n\n%s", systemPrompt, userPrompt)
}

func buildInvokeArgs(combinedPrompt, model string, allowWebSearch bool) []string {
	model = strings.TrimSpace(model)
	args := []string{"-p", combinedPrompt, "--output-format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if allowWebSearch {
		args = append(args, "--allowedTools", "WebSearch")
	}
	return args
}

func buildThinkPrompt(req ThinkRequest) string {
	if len(req.PreviousThinking) == 0 {
		var builder strings.Builder
		builder.WriteString("PROJECT: ")
		builder.WriteString(req.ProjectName)
		builder.WriteString("\n\nDIRECTIVE: ")
		builder.WriteString(req.Directive)
		builder.WriteString("\n\nAGENTS.MD:\n")
		builder.WriteString(req.AgentsMD)
		builder.WriteString("\n\nREPOSITORY STATE:\n")
		builder.WriteString(req.ReconData)

		if strings.TrimSpace(req.Cache) != "" {
			builder.WriteString("\n\nSESSION CACHE:\n")
			builder.WriteString(req.Cache)
		}

		if len(req.Hints) > 0 {
			builder.WriteString("\n\nOPERATOR HINTS:\n")
			builder.WriteString(strings.Join(req.Hints, "\n"))
		}

		return builder.String()
	}

	var builder strings.Builder
	builder.WriteString("CONVERSATION SO FAR:\n\n")
	builder.WriteString(formatThinkTurns(req.PreviousThinking))
	builder.WriteString("[latest]: ")
	builder.WriteString(req.Response)
	builder.WriteString("\n\n")
	return builder.String()
}

func buildProposePrompt(req ProposeRequest) string {
	var builder strings.Builder
	builder.WriteString("PROJECT: ")
	builder.WriteString(req.ProjectName)
	builder.WriteString("\n\nDIRECTIVE: ")
	builder.WriteString(req.Directive)
	builder.WriteString("\n\nAGENTS.MD:\n")
	builder.WriteString(req.AgentsMD)
	builder.WriteString("\n\nREPOSITORY STATE:\n")
	builder.WriteString(req.ReconData)
	builder.WriteString("\n\nTHINKING SUMMARY:\n")
	builder.WriteString(req.ThinkingSummary)

	if len(req.ThinkingHistory) > 0 {
		builder.WriteString("\n\nTHINKING HISTORY:\n\n")
		builder.WriteString(formatThinkTurns(req.ThinkingHistory))
	}

	return builder.String()
}

func buildRebuildPrompt(req RebuildRequest) (string, error) {
	previousPlan, err := json.Marshal(req.PreviousPlan)
	if err != nil {
		return "", fmt.Errorf("marshal previous plan: %w", err)
	}

	var builder strings.Builder
	builder.WriteString("PROJECT: ")
	builder.WriteString(req.ProjectName)
	builder.WriteString("\n\nDIRECTIVE: ")
	builder.WriteString(req.Directive)
	builder.WriteString("\n\nAGENTS.MD:\n")
	builder.WriteString(req.AgentsMD)
	builder.WriteString("\n\nREPOSITORY STATE:\n")
	builder.WriteString(req.ReconData)
	builder.WriteString("\n\nPREVIOUS PLAN (REJECTED):\n")
	builder.Write(previousPlan)
	builder.WriteString("\n\nOPERATOR FEEDBACK:\n")
	builder.WriteString(req.Feedback)
	builder.WriteString("\n\nGenerate a revised plan incorporating the feedback.")

	return builder.String(), nil
}

func buildEvalPrompt(req EvalRequest) string {
	var builder strings.Builder
	builder.WriteString("TASK: ")
	builder.WriteString(req.TaskTitle)
	builder.WriteString("\nDESCRIPTION: ")
	builder.WriteString(req.TaskDesc)
	builder.WriteString("\n\nDIFF:\n")
	builder.WriteString(req.DiffContent)
	builder.WriteString("\n\nBUILD OUTPUT:\n")
	builder.WriteString(req.BuildOutput)
	builder.WriteString("\n\nTEST OUTPUT:\n")
	builder.WriteString(req.TestOutput)
	builder.WriteString("\n\nVET OUTPUT:\n")
	builder.WriteString(req.VetOutput)

	if len(req.Criteria) > 0 {
		builder.WriteString("\n\nACCEPTANCE CRITERIA:\n")
		for idx, criterion := range req.Criteria {
			builder.WriteString(fmt.Sprintf("%d. %s\n", idx+1, criterion))
		}
	}

	return builder.String()
}

func formatThinkTurns(turns []ThinkTurn) string {
	var builder strings.Builder
	for _, turn := range turns {
		builder.WriteString("[")
		builder.WriteString(turn.Role)
		builder.WriteString("]: ")
		builder.WriteString(turn.Content)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func parseThinkResult(raw string) (*ThinkResult, error) {
	parsed, err := parseResultJSON[ThinkResult](raw)
	if err != nil {
		return nil, err
	}
	if err := validateThinkResult(&parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parsePlanResult(raw string) (*PlanResult, error) {
	parsed, err := parseResultJSON[PlanResult](raw)
	if err != nil {
		return nil, err
	}
	if err := validatePlanResult(&parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseEvalResult(raw string) (*EvalResult, error) {
	parsed, err := parseResultJSON[EvalResult](raw)
	if err != nil {
		return nil, err
	}
	if err := validateEvalResult(&parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseResultJSON[T any](raw string) (T, error) {
	var result T

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return result, errors.New("empty result")
	}

	directErr := json.Unmarshal([]byte(trimmed), &result)
	if directErr == nil {
		return result, nil
	}

	if !strings.HasPrefix(trimmed, "\"") {
		return result, fmt.Errorf("decode result json: %w", directErr)
	}

	var inner string
	if err := json.Unmarshal([]byte(trimmed), &inner); err != nil {
		return result, fmt.Errorf("decode result json: %w", directErr)
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(inner)), &result); err != nil {
		return result, fmt.Errorf("decode double-encoded result json: %w", err)
	}

	return result, nil
}

func validateThinkResult(result *ThinkResult) error {
	if result == nil {
		return errors.New("think result is nil")
	}

	switch result.Type {
	case "question", "info_request", "ready":
	default:
		return fmt.Errorf("invalid think result type %q", result.Type)
	}

	if result.Type == "info_request" {
		if err := validateInfoRequestCommands(result.Commands); err != nil {
			return err
		}
	}

	return nil
}

func validatePlanResult(result *PlanResult) error {
	if result == nil {
		return errors.New("plan result is nil")
	}
	if len(result.Tasks) == 0 {
		return errors.New("plan result has no tasks")
	}

	for idx, task := range result.Tasks {
		if strings.TrimSpace(task.ID) == "" {
			return fmt.Errorf("plan task[%d] missing id", idx)
		}
		if strings.TrimSpace(task.Title) == "" {
			return fmt.Errorf("plan task[%d] missing title", idx)
		}
		if strings.TrimSpace(task.Prompt) == "" {
			return fmt.Errorf("plan task[%d] missing prompt", idx)
		}
	}

	return nil
}

func validateEvalResult(result *EvalResult) error {
	if result == nil {
		return errors.New("eval result is nil")
	}

	switch result.Verdict {
	case "pass", "retry", "escalate":
		return nil
	default:
		return fmt.Errorf("invalid eval verdict %q", result.Verdict)
	}
}

func validateInfoRequestCommands(commands []string) error {
	for _, command := range commands {
		if err := validateInfoRequestCommand(command); err != nil {
			return err
		}
	}
	return nil
}

func validateInfoRequestCommand(command string) error {
	for _, segment := range splitShellSegments(command) {
		tokens := strings.Fields(segment)
		if len(tokens) == 0 {
			continue
		}

		cmdIndex := firstCommandToken(tokens)
		if cmdIndex == -1 {
			continue
		}

		commandName := strings.ToLower(tokens[cmdIndex])
		switch commandName {
		case "rm", "mv", "cp", "chmod", "chown", "chgrp", "touch", "mkdir", "rmdir", "install", "ln", "dd", "truncate", "tee", "patch":
			return fmt.Errorf("info_request command %q is not read-only: %s", command, commandName)
		case "git":
			subcommand := gitSubcommand(tokens[cmdIndex+1:])
			switch subcommand {
			case "add", "am", "apply", "branch", "checkout", "cherry-pick", "clean", "clone", "commit", "init", "merge", "rebase", "reset", "restore", "stash", "switch", "tag", "worktree":
				return fmt.Errorf("info_request command %q is not read-only: git %s", command, subcommand)
			}
		case "sed":
			if hasSedInPlaceFlag(tokens[cmdIndex+1:]) {
				return fmt.Errorf("info_request command %q is not read-only: sed -i", command)
			}
		case "perl":
			if hasPerlInPlaceFlag(tokens[cmdIndex+1:]) {
				return fmt.Errorf("info_request command %q is not read-only: perl in-place edit", command)
			}
		case "find":
			if containsExactToken(tokens[cmdIndex+1:], "-delete") {
				return fmt.Errorf("info_request command %q is not read-only: find -delete", command)
			}
		}
	}

	return nil
}

func splitShellSegments(command string) []string {
	return shellSegmentSplitPattern.Split(command, -1)
}

func firstCommandToken(tokens []string) int {
	for idx, token := range tokens {
		if !isEnvAssignment(token) {
			return idx
		}
	}
	return -1
}

func isEnvAssignment(token string) bool {
	if token == "" {
		return false
	}

	eq := strings.IndexByte(token, '=')
	if eq <= 0 {
		return false
	}

	for idx, r := range token[:eq] {
		if idx == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') {
				return false
			}
			continue
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}

	return true
}

func gitSubcommand(tokens []string) string {
	for idx := 0; idx < len(tokens); idx++ {
		token := strings.ToLower(tokens[idx])
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "-") {
			if gitOptionTakesValue(token) && idx+1 < len(tokens) {
				idx++
			}
			continue
		}
		return token
	}
	return ""
}

func gitOptionTakesValue(token string) bool {
	switch token {
	case "-c", "-C", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--super-prefix", "--config-env":
		return true
	default:
		return false
	}
}

func hasSedInPlaceFlag(tokens []string) bool {
	for _, token := range tokens {
		if token == "-i" || strings.HasPrefix(token, "-i") {
			return true
		}
	}
	return false
}

func hasPerlInPlaceFlag(tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(token, "-pi") || token == "-i" || strings.HasPrefix(token, "-i") {
			return true
		}
	}
	return false
}

func containsExactToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}
