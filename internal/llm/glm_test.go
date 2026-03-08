package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGLMClientChatAndTokenAccumulation(t *testing.T) {
	t.Parallel()

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		_ = json.NewDecoder(r.Body).Decode(&map[string]any{})

		call := atomic.AddInt32(&calls, 1)
		usage := TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
		if call == 2 {
			usage = TokenUsage{PromptTokens: 6, CompletionTokens: 4, TotalTokens: 10}
		}

		if err := writeCompletionResponse(w, `{"status":"ok"}`, usage); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewGLMClient(GLMConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Logger:  discardLogger(),
	})

	content, usage, err := client.Chat(context.Background(), "system", "message-1")
	if err != nil {
		t.Fatalf("chat call 1 returned error: %v", err)
	}
	if content != `{"status":"ok"}` {
		t.Fatalf("unexpected content from call 1: %s", content)
	}
	if usage.TotalTokens != 15 {
		t.Fatalf("unexpected usage from call 1: %+v", usage)
	}

	_, _, err = client.Chat(context.Background(), "system", "message-2")
	if err != nil {
		t.Fatalf("chat call 2 returned error: %v", err)
	}

	total := client.GetTokensUsed()
	if total.PromptTokens != 16 || total.CompletionTokens != 9 || total.TotalTokens != 25 {
		t.Fatalf("unexpected token accumulation: %+v", total)
	}
}

func TestGLMClientChatRetryOn429(t *testing.T) {
	t.Parallel()

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"too many requests"}}`))
			return
		}

		if err := writeCompletionResponse(w, `{"retry":true}`, TokenUsage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewGLMClient(GLMConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Logger:  discardLogger(),
	})

	content, usage, err := client.Chat(context.Background(), "system", "retry me")
	if err != nil {
		t.Fatalf("chat returned error after retry: %v", err)
	}
	if content != `{"retry":true}` {
		t.Fatalf("unexpected content: %s", content)
	}
	if usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestGLMClientChatTimeout(t *testing.T) {
	t.Parallel()

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(200 * time.Millisecond)
		if err := writeCompletionResponse(w, `{"late":true}`, TokenUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewGLMClient(GLMConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Logger:  discardLogger(),
		Timeout: 50 * time.Millisecond,
	})

	_, _, err := client.Chat(context.Background(), "system", "slow request")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "Client.Timeout") && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout-related error, got: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestGLMClientPlanParsing(t *testing.T) {
	t.Parallel()

	promptDir := t.TempDir()
	writePrompts(t, promptDir)

	planJSON := `{"confidence":0.91,"tasks":[{"id":"task-1","title":"Implement GLM client","description":"Create client methods","acceptance_criteria":["Build passes"],"files_affected":["internal/llm/glm.go"],"depends_on":[],"estimated_complexity":"medium","branch_name":"feature/glm-client"}],"questions":["Need retries?"],"notes":"Looks good"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := writeCompletionResponse(w, planJSON, TokenUsage{PromptTokens: 20, CompletionTokens: 30, TotalTokens: 50}); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewGLMClient(GLMConfig{
		APIKey:    "test-key",
		BaseURL:   server.URL,
		PromptDir: promptDir,
		Logger:    discardLogger(),
	})

	plan, err := client.Plan(context.Background(), "Implement GLM", "AGENTS content")
	if err != nil {
		t.Fatalf("plan returned error: %v", err)
	}
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
	if plan.Confidence != 0.91 {
		t.Fatalf("unexpected confidence: %v", plan.Confidence)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].ID != "task-1" {
		t.Fatalf("unexpected tasks: %+v", plan.Tasks)
	}
	if plan.Tasks[0].Complexity != "medium" {
		t.Fatalf("unexpected task complexity: %s", plan.Tasks[0].Complexity)
	}
}

func TestGLMClientEvaluateParsing(t *testing.T) {
	t.Parallel()

	promptDir := t.TempDir()
	writePrompts(t, promptDir)

	evalJSON := `{"verdict":"approved","confidence":0.88,"completeness":0.9,"correctness":0.85,"conventions":0.95,"scope_ok":true,"issues":[{"severity":"low","description":"minor naming","suggestion":"align naming"}],"summary":"Ready"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := writeCompletionResponse(w, evalJSON, TokenUsage{PromptTokens: 15, CompletionTokens: 18, TotalTokens: 33}); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewGLMClient(GLMConfig{
		APIKey:    "test-key",
		BaseURL:   server.URL,
		PromptDir: promptDir,
		Logger:    discardLogger(),
	})

	evaluation, err := client.Evaluate(context.Background(), "Task details", "diff --git", "AGENTS content")
	if err != nil {
		t.Fatalf("evaluate returned error: %v", err)
	}
	if evaluation == nil {
		t.Fatal("expected non-nil evaluation")
	}
	if evaluation.Verdict != "approved" || !evaluation.ScopeOK {
		t.Fatalf("unexpected evaluation verdict/scope: %+v", evaluation)
	}
	if len(evaluation.Issues) != 1 || evaluation.Issues[0].Severity != "low" {
		t.Fatalf("unexpected issues: %+v", evaluation.Issues)
	}
}

func writeCompletionResponse(w http.ResponseWriter, content string, usage TokenUsage) error {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"content": content,
				},
			},
		},
		"usage": usage,
	}

	return json.NewEncoder(w).Encode(resp)
}

func writePrompts(t *testing.T, dir string) {
	t.Helper()

	files := map[string]string{
		"planner.txt":   "planner-system-prompt",
		"evaluator.txt": "evaluator-system-prompt",
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write prompt file %s: %v", name, err)
		}
	}
}

func TestGLMResolvePromptFallback(t *testing.T) {
	t.Parallel()

	// Create a fallback directory with the prompt file.
	fallbackDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fallbackDir, "planner.txt"), []byte("fallback-prompt"), 0o644); err != nil {
		t.Fatalf("write fallback prompt: %v", err)
	}

	// Override the fallback dirs for this test.
	origFallback := glmPromptFallbackDirs
	glmPromptFallbackDirs = []string{fallbackDir}
	t.Cleanup(func() { glmPromptFallbackDirs = origFallback })

	// Use a non-existent promptDir so the normal search fails.
	client := NewGLMClient(GLMConfig{
		APIKey:    "test-key",
		PromptDir: "/nonexistent/prompts",
		Logger:    discardLogger(),
	})

	got, err := client.loadPrompt("planner.txt")
	if err != nil {
		t.Fatalf("loadPrompt() error = %v", err)
	}
	if got != "fallback-prompt" {
		t.Fatalf("loadPrompt() = %q, want %q", got, "fallback-prompt")
	}
}

func TestGLMResolvePromptFallbackRelative(t *testing.T) {
	t.Parallel()

	// Create a fallback directory with the prompt file.
	fallbackDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fallbackDir, "evaluator.txt"), []byte("fallback-eval"), 0o644); err != nil {
		t.Fatalf("write fallback prompt: %v", err)
	}

	origFallback := glmPromptFallbackDirs
	glmPromptFallbackDirs = []string{fallbackDir}
	t.Cleanup(func() { glmPromptFallbackDirs = origFallback })

	// Use relative promptDir "prompts" — won't find the file via cwd walk.
	client := NewGLMClient(GLMConfig{
		APIKey:    "test-key",
		PromptDir: "nonexistent-prompts-dir",
		Logger:    discardLogger(),
	})

	got, err := client.loadPrompt("evaluator.txt")
	if err != nil {
		t.Fatalf("loadPrompt() error = %v", err)
	}
	if got != "fallback-eval" {
		t.Fatalf("loadPrompt() = %q, want %q", got, "fallback-eval")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
