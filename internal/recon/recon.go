package recon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultCommandTimeout      = 10 * time.Second
	defaultMaxOutputPerCommand = 8000
	defaultMaxTotalOutput      = 30000
	truncatedMarker            = "\n... [truncated]"
)

// Runner executes local read-only shell commands to gather repository context.
type Runner struct {
	logger              *slog.Logger
	commandTimeout      time.Duration
	maxOutputPerCommand int
	maxTotalOutput      int
}

type Result struct {
	Output string            `json:"output"`
	Detail map[string]string `json:"detail"`
	Errors []string          `json:"errors"`
}

func NewRunner(logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Runner{
		logger:              logger,
		commandTimeout:      defaultCommandTimeout,
		maxOutputPerCommand: defaultMaxOutputPerCommand,
		maxTotalOutput:      defaultMaxTotalOutput,
	}
}

func DefaultQueries(repoPath string) []string {
	return []string{
		fmt.Sprintf(
			"tree %s -L 3 --gitignore -I 'node_modules|vendor|.git' 2>/dev/null || find %s -maxdepth 3 -type f | head -100",
			repoPath,
			repoPath,
		),
		fmt.Sprintf("cat %s/go.mod 2>/dev/null || echo 'no go.mod'", repoPath),
		fmt.Sprintf("git -C %s log --oneline -15 2>/dev/null || echo 'no git history'", repoPath),
		fmt.Sprintf("git -C %s diff --stat HEAD~5 2>/dev/null || echo 'no recent changes'", repoPath),
	}
}

func (r *Runner) Run(ctx context.Context, commands []string) (*Result, error) {
	if r == nil {
		return nil, errors.New("recon runner is nil")
	}

	result := &Result{
		Detail: make(map[string]string, len(commands)),
		Errors: make([]string, 0),
	}

	var combined strings.Builder
	for _, rawCmd := range commands {
		cmd := strings.TrimSpace(rawCmd)
		if cmd == "" {
			continue
		}

		cmdCtx, cancel := context.WithTimeout(ctx, r.commandTimeout)
		execCmd := exec.CommandContext(cmdCtx, "sh", "-c", cmd)
		// Bound wait time after context cancellation so child shell pipelines cannot
		// hold open pipes indefinitely.
		execCmd.WaitDelay = r.commandTimeout
		out, err := execCmd.CombinedOutput()
		cancel()

		output := strings.TrimSpace(string(out))
		if err != nil {
			output = fmt.Sprintf("[error: %v]\n%s", err, output)
			result.Errors = append(result.Errors, cmd)
			r.logger.Warn("recon command failed",
				slog.String("command", cmd),
				slog.Any("error", err))
		}

		output = truncate(output, r.maxOutputPerCommand)
		result.Detail[cmd] = output
		combined.WriteString(fmt.Sprintf("$ %s\n%s\n\n", cmd, output))
	}

	result.Output = truncate(combined.String(), r.maxTotalOutput)
	return result, nil
}

func (r *Runner) RunDefault(ctx context.Context, repoPath string) (*Result, error) {
	return r.Run(ctx, DefaultQueries(repoPath))
}

func truncate(text string, maxChars int) string {
	if maxChars <= 0 {
		if text == "" {
			return text
		}
		return truncatedMarker
	}

	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}

	return string(runes[:maxChars]) + truncatedMarker
}
