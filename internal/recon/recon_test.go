package recon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSimpleCommands(t *testing.T) {
	t.Parallel()

	runner := NewRunner(testLogger())
	result, err := runner.Run(context.Background(), []string{"echo hello", "echo world"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := result.Detail["echo hello"]; got != "hello" {
		t.Fatalf("detail for echo hello = %q, want %q", got, "hello")
	}
	if got := result.Detail["echo world"]; got != "world" {
		t.Fatalf("detail for echo world = %q, want %q", got, "world")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %v, want no errors", result.Errors)
	}
	if !strings.Contains(result.Output, "$ echo hello\nhello\n\n") {
		t.Fatalf("output missing first command content: %q", result.Output)
	}
	if !strings.Contains(result.Output, "$ echo world\nworld\n\n") {
		t.Fatalf("output missing second command content: %q", result.Output)
	}
}

func TestRunWithError(t *testing.T) {
	t.Parallel()

	runner := NewRunner(testLogger())
	result, err := runner.Run(context.Background(), []string{"false"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(result.Errors) != 1 || result.Errors[0] != "false" {
		t.Fatalf("errors = %v, want [false]", result.Errors)
	}
	if !strings.Contains(result.Detail["false"], "[error:") {
		t.Fatalf("detail should include error prefix: %q", result.Detail["false"])
	}
	if !strings.Contains(result.Output, "$ false\n") {
		t.Fatalf("output missing command entry: %q", result.Output)
	}
}

func TestTruncation(t *testing.T) {
	t.Parallel()

	runner := NewRunner(testLogger())
	runner.maxOutputPerCommand = 64

	result, err := runner.Run(context.Background(), []string{"seq 1 100000"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	out := result.Detail["seq 1 100000"]
	if !strings.Contains(out, "... [truncated]") {
		t.Fatalf("expected per-command truncation, got: %q", out)
	}
}

func TestTotalTruncation(t *testing.T) {
	t.Parallel()

	runner := NewRunner(testLogger())
	runner.maxOutputPerCommand = 5000
	runner.maxTotalOutput = 240

	commands := []string{
		"seq 1 1000",
		"seq 1 1000",
		"seq 1 1000",
		"seq 1 1000",
		"seq 1 1000",
		"seq 1 1000",
		"seq 1 1000",
		"seq 1 1000",
		"seq 1 1000",
		"seq 1 1000",
	}

	result, err := runner.Run(context.Background(), commands)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !strings.Contains(result.Output, "... [truncated]") {
		t.Fatalf("expected total output truncation, got: %q", result.Output)
	}
}

func TestDefaultQueries(t *testing.T) {
	t.Parallel()

	repoPath := "/tmp/example-repo"
	queries := DefaultQueries(repoPath)
	if len(queries) != 4 {
		t.Fatalf("len(DefaultQueries) = %d, want 4", len(queries))
	}

	for i, query := range queries {
		if !strings.Contains(query, repoPath) {
			t.Fatalf("query[%d] = %q does not include repo path %q", i, query, repoPath)
		}
	}
}

func TestRunDefault(t *testing.T) {
	t.Parallel()

	repoPath := t.TempDir()
	goModContent := "module example.com/recon\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(repoPath, "go.mod"), []byte(goModContent), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}

	runner := NewRunner(testLogger())
	result, err := runner.RunDefault(context.Background(), repoPath)
	if err != nil {
		t.Fatalf("RunDefault() error = %v", err)
	}

	if result == nil {
		t.Fatalf("RunDefault() returned nil result")
	}

	goModCmd := DefaultQueries(repoPath)[1]
	if !strings.Contains(result.Detail[goModCmd], "module example.com/recon") {
		t.Fatalf("go.mod command output missing module declaration: %q", result.Detail[goModCmd])
	}
}

func TestCommandTimeout(t *testing.T) {
	t.Parallel()

	runner := NewRunner(testLogger())
	runner.commandTimeout = 100 * time.Millisecond

	start := time.Now()
	result, err := runner.Run(context.Background(), []string{"sleep 30"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("Run() took too long: %s", elapsed)
	}
	if len(result.Errors) != 1 || result.Errors[0] != "sleep 30" {
		t.Fatalf("errors = %v, want [sleep 30]", result.Errors)
	}
	if !strings.Contains(result.Detail["sleep 30"], "[error:") {
		t.Fatalf("timeout output should include error prefix: %q", result.Detail["sleep 30"])
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
