package refiner

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/zyrakk/hivemind/internal/llm"
)

type mockLLM struct {
	responses []string
	callIndex int
}

func (m *mockLLM) call(ctx context.Context, system, user string) (string, llm.TokenUsage, error) {
	if m.callIndex >= len(m.responses) {
		return "{}", llm.TokenUsage{}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, llm.TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}, nil
}

func (m *mockLLM) Chat(ctx context.Context, system, user string) (string, llm.TokenUsage, error) {
	return m.call(ctx, system, user)
}

func (m *mockLLM) ChatText(ctx context.Context, system, user string) (string, llm.TokenUsage, error) {
	return m.call(ctx, system, user)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNew_SetsFields(t *testing.T) {
	improver := &mockLLM{}
	evaluator := &mockLLM{}
	r := New(improver, evaluator, testLogger())
	if r == nil {
		t.Fatal("expected non-nil refiner")
	}
	if r.improver != improver {
		t.Fatal("expected improver to be set")
	}
	if r.evaluator != evaluator {
		t.Fatal("expected evaluator to be set")
	}
}

func TestExtractDocument_StripsFences(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "Hello world", "Hello world"},
		{"markdown fences", "```markdown\nHello world\n```", "Hello world"},
		{"bare fences", "```\nHello world\n```", "Hello world"},
		{"no fences with whitespace", "  Hello world  ", "Hello world"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDocument(tc.input)
			if got != tc.want {
				t.Errorf("extractDocument(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseRubricScore_Valid(t *testing.T) {
	input := `{"overall_score": 0.85, "verdict": "accept", "summary": "Good.", "deficiencies": [{"criterion": "testability", "section": "rules", "description": "vague", "suggestion": "be specific"}]}`
	score, err := parseRubricScore(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.OverallScore != 0.85 {
		t.Errorf("expected score 0.85, got %f", score.OverallScore)
	}
	if len(score.Deficiencies) != 1 {
		t.Errorf("expected 1 deficiency, got %d", len(score.Deficiencies))
	}
}

func TestParseRubricScore_Malformed(t *testing.T) {
	_, err := parseRubricScore("not json at all")
	if err == nil {
		t.Fatal("expected error for malformed input")
	}
}

func TestParseRubricScore_WrappedInFences(t *testing.T) {
	input := "```json\n{\"overall_score\": 0.9, \"verdict\": \"accept\", \"summary\": \"Fine.\", \"deficiencies\": []}\n```"
	score, err := parseRubricScore(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.OverallScore != 0.9 {
		t.Errorf("expected score 0.9, got %f", score.OverallScore)
	}
}

func TestBuildImproveMessage_FirstIteration(t *testing.T) {
	msg := buildImproveMessage("doc content", nil)
	if !strings.Contains(msg, "doc content") {
		t.Error("expected document in message")
	}
	if strings.Contains(msg, "deficiencies") {
		t.Error("first iteration should not mention deficiencies")
	}
}

func TestBuildImproveMessage_WithDeficiencies(t *testing.T) {
	defs := []Deficiency{
		{Criterion: "testability", Section: "rules", Description: "too vague", Suggestion: "add commands"},
	}
	msg := buildImproveMessage("doc content", defs)
	if !strings.Contains(msg, "doc content") {
		t.Error("expected document in message")
	}
	if !strings.Contains(msg, "testability") {
		t.Error("expected deficiency criterion in message")
	}
	if !strings.Contains(msg, "too vague") {
		t.Error("expected deficiency description in message")
	}
}

func TestRefine_ConvergesFirstPass(t *testing.T) {
	improver := &mockLLM{responses: []string{"improved doc"}}
	evaluator := &mockLLM{responses: []string{
		`{"overall_score": 0.9, "verdict": "accept", "summary": "Good.", "deficiencies": []}`,
	}}
	r := New(improver, evaluator, testLogger())
	result, err := r.Run(context.Background(), "original", "rubric", "improve prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if !result.Converged {
		t.Error("expected converged=true")
	}
	if result.FinalScore != 0.9 {
		t.Errorf("expected score 0.9, got %f", result.FinalScore)
	}
	if result.FinalDocument != "improved doc" {
		t.Errorf("expected 'improved doc', got %q", result.FinalDocument)
	}
	if result.OriginalDocument != "original" {
		t.Errorf("expected original preserved, got %q", result.OriginalDocument)
	}
}

func TestRefine_IteratesAndConverges(t *testing.T) {
	improver := &mockLLM{responses: []string{"v1", "v2", "v3"}}
	evaluator := &mockLLM{responses: []string{
		`{"overall_score": 0.5, "verdict": "iterate", "summary": "Needs work.", "deficiencies": [{"criterion":"a","section":"s","description":"d1","suggestion":"s1"},{"criterion":"b","section":"s","description":"d2","suggestion":"s2"},{"criterion":"c","section":"s","description":"d3","suggestion":"s3"}]}`,
		`{"overall_score": 0.6, "verdict": "iterate", "summary": "Better.", "deficiencies": [{"criterion":"a","section":"s","description":"d1","suggestion":"s1"},{"criterion":"b","section":"s","description":"d2","suggestion":"s2"}]}`,
		`{"overall_score": 0.9, "verdict": "accept", "summary": "Done.", "deficiencies": []}`,
	}}
	r := New(improver, evaluator, testLogger())
	result, err := r.Run(context.Background(), "original", "rubric", "improve prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", result.Iterations)
	}
	if !result.Converged {
		t.Error("expected converged=true")
	}
	if result.FinalDocument != "v3" {
		t.Errorf("expected 'v3', got %q", result.FinalDocument)
	}
}

func TestRefine_MaxIterationCap(t *testing.T) {
	improver := &mockLLM{responses: []string{"v1", "v2", "v3", "v4"}}
	evaluator := &mockLLM{responses: []string{
		`{"overall_score": 0.3, "verdict": "iterate", "summary": "Bad.", "deficiencies": [{"criterion":"a","section":"s","description":"d1","suggestion":"s"},{"criterion":"b","section":"s","description":"d2","suggestion":"s"},{"criterion":"c","section":"s","description":"d3","suggestion":"s"},{"criterion":"d","section":"s","description":"d4","suggestion":"s"}]}`,
		`{"overall_score": 0.4, "verdict": "iterate", "summary": "Bad.", "deficiencies": [{"criterion":"a","section":"s","description":"d1","suggestion":"s"},{"criterion":"b","section":"s","description":"d2","suggestion":"s"},{"criterion":"c","section":"s","description":"d3","suggestion":"s"}]}`,
		`{"overall_score": 0.5, "verdict": "iterate", "summary": "Bad.", "deficiencies": [{"criterion":"a","section":"s","description":"d1","suggestion":"s"},{"criterion":"b","section":"s","description":"d2","suggestion":"s"}]}`,
		`{"overall_score": 0.6, "verdict": "iterate", "summary": "Bad.", "deficiencies": [{"criterion":"a","section":"s","description":"d1","suggestion":"s"}]}`,
	}}
	r := New(improver, evaluator, testLogger())
	result, err := r.Run(context.Background(), "original", "rubric", "improve prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Iterations != 4 {
		t.Errorf("expected 4 iterations, got %d", result.Iterations)
	}
	if result.Converged {
		t.Error("expected converged=false (hit max iterations)")
	}
}

func TestRefine_StallDetection(t *testing.T) {
	improver := &mockLLM{responses: []string{"v1", "v2"}}
	evaluator := &mockLLM{responses: []string{
		`{"overall_score": 0.5, "verdict": "iterate", "summary": "Mid.", "deficiencies": [{"criterion":"a","section":"s","description":"d1","suggestion":"s"},{"criterion":"b","section":"s","description":"d2","suggestion":"s"},{"criterion":"c","section":"s","description":"d3","suggestion":"s"}]}`,
		`{"overall_score": 0.5, "verdict": "iterate", "summary": "Still mid.", "deficiencies": [{"criterion":"a","section":"s","description":"d1","suggestion":"s"},{"criterion":"b","section":"s","description":"d2","suggestion":"s"},{"criterion":"c","section":"s","description":"d3","suggestion":"s"}]}`,
	}}
	r := New(improver, evaluator, testLogger())
	result, err := r.Run(context.Background(), "original", "rubric", "improve prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
	if !result.Converged {
		t.Error("expected converged=true (stall detected)")
	}
}

func TestRefine_MalformedEvalJSON(t *testing.T) {
	improver := &mockLLM{responses: []string{"v1"}}
	evaluator := &mockLLM{responses: []string{"this is not json"}}
	r := New(improver, evaluator, testLogger())
	result, err := r.Run(context.Background(), "original", "rubric", "improve prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Malformed JSON -> score=0, empty deficiencies -> stall on i>0 check doesn't trigger for i=0
	// but len(deficiencies)=0 means next iteration i=1 would have len(score.Deficiencies)=0 >= len(lastDeficiencies)=0 -> stall
	// Actually i=0 special case: no stall check. lastDeficiencies set to empty.
	// Since score < threshold and i=0, loop continues. But improver has no more responses.
	// The mock returns "{}" for exhausted responses -> evaluator also returns "{}" -> score=0, deficiencies=[] >= [] -> stall.
	// So 2 iterations.
	if result.Iterations < 1 {
		t.Errorf("expected at least 1 iteration, got %d", result.Iterations)
	}
	// Should not panic
}

func TestRefine_SameClientForBothRoles(t *testing.T) {
	// Interleaved: improve (ChatText), eval (Chat), ...
	mock := &mockLLM{responses: []string{
		"improved doc",
		`{"overall_score": 0.9, "verdict": "accept", "summary": "Good.", "deficiencies": []}`,
	}}
	r := New(mock, mock, testLogger())
	result, err := r.Run(context.Background(), "original", "rubric", "improve prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if !result.Converged {
		t.Error("expected converged=true")
	}
}
