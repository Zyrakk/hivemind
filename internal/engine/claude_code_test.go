package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestBuildThinkPromptFirstTurn(t *testing.T) {
	t.Parallel()

	req := ThinkRequest{
		ProjectName: "hivemind",
		Directive:   "Implement Claude Code engine",
		AgentsMD:    "No secrets.",
		ReconData:   "internal/engine exists",
		Cache:       "decision=use cli",
		Hints:       []string{"prefer tests", "keep repo read-only"},
	}

	got := buildThinkPrompt(req)

	assertContains(t, got, "PROJECT: hivemind")
	assertContains(t, got, "DIRECTIVE: Implement Claude Code engine")
	assertContains(t, got, "AGENTS.MD:\nNo secrets.")
	assertContains(t, got, "REPOSITORY STATE:\ninternal/engine exists")
	assertContains(t, got, "SESSION CACHE:\ndecision=use cli")
	assertContains(t, got, "OPERATOR HINTS:\nprefer tests\nkeep repo read-only")
}

func TestBuildThinkPromptContinuation(t *testing.T) {
	t.Parallel()

	req := ThinkRequest{
		PreviousThinking: []ThinkTurn{
			{Role: "engine", Content: "Need more repository context."},
			{Role: "operator", Content: "Focus on the CLI implementation."},
		},
		Response: "Here is the latest recon data.",
	}

	got := buildThinkPrompt(req)

	assertContains(t, got, "CONVERSATION SO FAR:")
	assertContains(t, got, "[engine]: Need more repository context.")
	assertContains(t, got, "[operator]: Focus on the CLI implementation.")
	assertContains(t, got, "[latest]: Here is the latest recon data.")
}

func TestBuildProposePrompt(t *testing.T) {
	t.Parallel()

	req := ProposeRequest{
		ProjectName:     "hivemind",
		Directive:       "Plan the Claude engine work",
		AgentsMD:        "Use conventional commits.",
		ReconData:       "engine.go defines interface",
		ThinkingSummary: "The engine should wrap the claude CLI.",
		ThinkingHistory: []ThinkTurn{
			{Role: "engine", Content: "WebSearch is only needed for Think."},
		},
	}

	got := buildProposePrompt(req)

	assertContains(t, got, "PROJECT: hivemind")
	assertContains(t, got, "THINKING SUMMARY:\nThe engine should wrap the claude CLI.")
	assertContains(t, got, "THINKING HISTORY:")
	assertContains(t, got, "[engine]: WebSearch is only needed for Think.")
}

func TestBuildRebuildPrompt(t *testing.T) {
	t.Parallel()

	req := RebuildRequest{
		PreviousPlan: &PlanResult{
			Tasks: []PlanTask{
				{ID: "task-1", Title: "Implement engine", Prompt: "Do the work"},
			},
			Summary: "Initial plan",
		},
		Feedback:    "Task prompts need more detail.",
		Directive:   "Rebuild the plan",
		ProjectName: "hivemind",
		AgentsMD:    "Stay within scope.",
		ReconData:   "manager.go already exists",
	}

	got, err := buildRebuildPrompt(req)
	if err != nil {
		t.Fatalf("buildRebuildPrompt() error = %v", err)
	}

	assertContains(t, got, "PROJECT: hivemind")
	assertContains(t, got, "PREVIOUS PLAN (REJECTED):")
	assertContains(t, got, `"id":"task-1"`)
	assertContains(t, got, "OPERATOR FEEDBACK:\nTask prompts need more detail.")
	assertContains(t, got, "Generate a revised plan incorporating the feedback.")
}

func TestBuildEvalPrompt(t *testing.T) {
	t.Parallel()

	req := EvalRequest{
		TaskTitle:   "Implement Claude engine",
		TaskDesc:    "Add a CLI-backed engine implementation.",
		DiffContent: "diff --git a/internal/engine/claude_code.go b/internal/engine/claude_code.go",
		BuildOutput: "go build ./... ok",
		TestOutput:  "go test ./... ok",
		VetOutput:   "go vet ./... ok",
		Criteria:    []string{"Build passes", "Tests cover parsing"},
	}

	got := buildEvalPrompt(req)

	assertContains(t, got, "TASK: Implement Claude engine")
	assertContains(t, got, "DESCRIPTION: Add a CLI-backed engine implementation.")
	assertContains(t, got, "DIFF:\ndiff --git a/internal/engine/claude_code.go b/internal/engine/claude_code.go")
	assertContains(t, got, "BUILD OUTPUT:\ngo build ./... ok")
	assertContains(t, got, "TEST OUTPUT:\ngo test ./... ok")
	assertContains(t, got, "VET OUTPUT:\ngo vet ./... ok")
	assertContains(t, got, "ACCEPTANCE CRITERIA:\n1. Build passes\n2. Tests cover parsing")
}

func TestParseThinkResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want ThinkResult
	}{
		{
			name: "question",
			raw:  `{"type":"question","question":"Need operator confirmation?"}`,
			want: ThinkResult{Type: "question", Question: "Need operator confirmation?"},
		},
		{
			name: "info request",
			raw:  `{"type":"info_request","commands":["rg -n Engine internal/engine","git -C . status --short"]}`,
			want: ThinkResult{Type: "info_request", Commands: []string{"rg -n Engine internal/engine", "git -C . status --short"}},
		},
		{
			name: "ready",
			raw:  `{"type":"ready","summary":"Enough context collected."}`,
			want: ThinkResult{Type: "ready", Summary: "Enough context collected."},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseThinkResult(tc.raw)
			if err != nil {
				t.Fatalf("parseThinkResult() error = %v", err)
			}
			if !reflect.DeepEqual(*got, tc.want) {
				t.Fatalf("parseThinkResult() = %#v, want %#v", *got, tc.want)
			}
		})
	}
}

func TestParsePlanResult(t *testing.T) {
	t.Parallel()

	raw := `{"tasks":[{"id":"T1","title":"Implement engine","description":"Add claude code engine","branch_name":"feature/claude-engine","dependencies":[],"priority":1,"type":"coding","prompt":"Implement internal/engine/claude_code.go"}],"summary":"Single task plan","confidence":0.91}`

	got, err := parsePlanResult(raw)
	if err != nil {
		t.Fatalf("parsePlanResult() error = %v", err)
	}

	if len(got.Tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(got.Tasks))
	}
	if got.Tasks[0].ID != "T1" || got.Tasks[0].Title != "Implement engine" || got.Tasks[0].Prompt == "" {
		t.Fatalf("unexpected task: %#v", got.Tasks[0])
	}
	if got.Summary != "Single task plan" {
		t.Fatalf("summary = %q, want %q", got.Summary, "Single task plan")
	}
}

func TestParseEvalResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		raw         string
		wantVerdict string
	}{
		{
			name:        "pass",
			raw:         `{"task_id":"T1","verdict":"pass","analysis":"Looks correct.","suggestions":["Ship it"],"confidence":0.95}`,
			wantVerdict: "pass",
		},
		{
			name:        "retry",
			raw:         `{"task_id":"T1","verdict":"retry","analysis":"Needs more tests.","suggestions":["Add unit tests"],"retry_prompt":"Add edge case coverage","confidence":0.74}`,
			wantVerdict: "retry",
		},
		{
			name:        "escalate",
			raw:         `{"task_id":"T1","verdict":"escalate","analysis":"Risk is unclear.","suggestions":["Need human review"],"confidence":0.42}`,
			wantVerdict: "escalate",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseEvalResult(tc.raw)
			if err != nil {
				t.Fatalf("parseEvalResult() error = %v", err)
			}
			if got.Verdict != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q", got.Verdict, tc.wantVerdict)
			}
		})
	}
}

func TestParseDoubleEncodedJSON(t *testing.T) {
	t.Parallel()

	raw := `"{\"type\":\"ready\",\"summary\":\"Double encoded JSON works.\"}"`

	got, err := parseThinkResult(raw)
	if err != nil {
		t.Fatalf("parseThinkResult() error = %v", err)
	}

	if got.Type != "ready" || got.Summary != "Double encoded JSON works." {
		t.Fatalf("unexpected think result: %#v", *got)
	}
}

func TestParseMarkdownFencedJSON(t *testing.T) {
	t.Parallel()

	raw := "```json\n{\"type\":\"ready\",\"summary\":\"Fenced JSON.\"}\n```"

	got, err := parseThinkResult(raw)
	if err != nil {
		t.Fatalf("parseThinkResult() error = %v", err)
	}

	if got.Type != "ready" || got.Summary != "Fenced JSON." {
		t.Fatalf("unexpected think result: %#v", *got)
	}
}

func TestParseMarkdownFencedNoLangJSON(t *testing.T) {
	t.Parallel()

	raw := "```\n{\"type\":\"ready\",\"summary\":\"Fenced no lang.\"}\n```"

	got, err := parseThinkResult(raw)
	if err != nil {
		t.Fatalf("parseThinkResult() error = %v", err)
	}

	if got.Type != "ready" || got.Summary != "Fenced no lang." {
		t.Fatalf("unexpected think result: %#v", *got)
	}
}

func TestParseJSONWithPreamble(t *testing.T) {
	t.Parallel()

	raw := "Based on the analysis, here's the plan:\n{\"type\":\"ready\",\"summary\":\"Preamble stripped.\"}"

	got, err := parseThinkResult(raw)
	if err != nil {
		t.Fatalf("parseThinkResult() error = %v", err)
	}

	if got.Type != "ready" || got.Summary != "Preamble stripped." {
		t.Fatalf("unexpected think result: %#v", *got)
	}
}

func TestParseJSONWithTrailingText(t *testing.T) {
	t.Parallel()

	raw := "{\"type\":\"ready\",\"summary\":\"Has trailing.\"}\n\nLet me know if you need anything else!"

	got, err := parseThinkResult(raw)
	if err != nil {
		t.Fatalf("parseThinkResult() error = %v", err)
	}

	if got.Type != "ready" || got.Summary != "Has trailing." {
		t.Fatalf("unexpected think result: %#v", *got)
	}
}

func TestParsePlanResultMarkdownFenced(t *testing.T) {
	t.Parallel()

	raw := "```json\n{\"tasks\":[{\"id\":\"T1\",\"title\":\"Do thing\",\"description\":\"desc\",\"branch_name\":\"feature/thing\",\"dependencies\":[],\"priority\":1,\"type\":\"coding\",\"prompt\":\"do it\"}],\"summary\":\"plan\",\"confidence\":0.9}\n```"

	got, err := parsePlanResult(raw)
	if err != nil {
		t.Fatalf("parsePlanResult() error = %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != "T1" {
		t.Fatalf("unexpected plan result: %#v", got)
	}
}

func TestParseEvalResultWithPreamble(t *testing.T) {
	t.Parallel()

	raw := "Based on my review:\n{\"task_id\":\"T1\",\"verdict\":\"pass\",\"analysis\":\"Good.\",\"suggestions\":[],\"confidence\":0.95}"

	got, err := parseEvalResult(raw)
	if err != nil {
		t.Fatalf("parseEvalResult() error = %v", err)
	}
	if got.Verdict != "pass" {
		t.Fatalf("verdict = %q, want %q", got.Verdict, "pass")
	}
}

func TestParseResultJSONNestedBraces(t *testing.T) {
	t.Parallel()

	raw := "Here is the result:\n{\"type\":\"ready\",\"summary\":\"Has {nested} braces.\"}\nDone."

	got, err := parseThinkResult(raw)
	if err != nil {
		t.Fatalf("parseThinkResult() error = %v", err)
	}

	if got.Type != "ready" || got.Summary != "Has {nested} braces." {
		t.Fatalf("unexpected think result: %#v", *got)
	}
}

func TestValidateThinkResultBadType(t *testing.T) {
	t.Parallel()

	err := validateThinkResult(&ThinkResult{Type: "unknown"})
	if err == nil {
		t.Fatal("validateThinkResult() error = nil, want error")
	}
}

func TestValidateInfoRequestDangerousCommand(t *testing.T) {
	t.Parallel()

	err := validateInfoRequestCommands([]string{"rm -rf /tmp/bad"})
	if err == nil {
		t.Fatal("validateInfoRequestCommands() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not read-only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildInvokeArgs(t *testing.T) {
	t.Parallel()

	t.Run("no model no tools", func(t *testing.T) {
		t.Parallel()

		got := buildInvokeArgs("prompt", "", false)
		want := []string{"-p", "prompt", "--output-format", "json"}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("buildInvokeArgs() = %#v, want %#v", got, want)
		}
		assertNoRepoFlag(t, got)
	})

	t.Run("model and websearch", func(t *testing.T) {
		t.Parallel()

		got := buildInvokeArgs("prompt", "claude-sonnet-4-5-20250929", true)
		want := []string{
			"-p", "prompt",
			"--output-format", "json",
			"--model", "claude-sonnet-4-5-20250929",
			"--allowedTools", "WebSearch",
		}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("buildInvokeArgs() = %#v, want %#v", got, want)
		}
		assertNoRepoFlag(t, got)
	})
}

func TestAvailableBinaryNotFound(t *testing.T) {
	t.Parallel()

	engine := NewClaudeCodeEngine(ClaudeCodeConfig{
		Binary: "definitely-not-a-real-claude-binary-12345",
	}, nil)

	if engine.Available(context.Background()) {
		t.Fatal("Available() = true, want false")
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(got, want) {
		t.Fatalf("expected prompt to contain %q\nfull prompt:\n%s", want, got)
	}
}

func assertNoRepoFlag(t *testing.T, args []string) {
	t.Helper()

	for _, arg := range args {
		if arg == "-C" {
			t.Fatalf("buildInvokeArgs() unexpectedly included repo flag: %#v", args)
		}
	}
}
