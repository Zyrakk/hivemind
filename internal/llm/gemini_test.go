package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGeminiConsultParsesResponseAndTracksDailyCalls(t *testing.T) {
	promptDir := t.TempDir()
	writeConsultantPrompt(t, promptDir)

	budget, err := NewBudgetTracker(BudgetConfig{
		ConsultantName: "gemini",
		MaxDailyCalls:  1,
		NowFn: func() time.Time {
			return time.Date(2026, 3, 1, 14, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("new budget tracker: %v", err)
	}

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)

		if got := r.URL.Query().Get("key"); got != "google-key" {
			t.Errorf("unexpected api key query param: %q", got)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{
								"text": `{"consultation_type":"plan_validation","agree_with_original":true,"analysis":"Plan is coherent and actionable.","recommendations":["Proceed with implementation"],"risk_flags":[],"confidence":0.81}`,
							},
						},
					},
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     88,
				"candidatesTokenCount": 31,
				"totalTokenCount":      119,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewGeminiClient(GeminiConfig{
		APIKey:    "google-key",
		BaseURL:   server.URL,
		Model:     "gemini-2.5-flash",
		PromptDir: promptDir,
		Budget:    budget,
		Logger:    discardLogger(),
	})

	opinion, err := client.Consult(context.Background(), "plan_validation", "context", "is this plan ok?")
	if err != nil {
		t.Fatalf("consult returned error: %v", err)
	}
	if opinion == nil {
		t.Fatal("expected opinion")
	}
	if opinion.ConsultationType != "plan_validation" {
		t.Fatalf("unexpected consultation type: %s", opinion.ConsultationType)
	}
	if !opinion.AgreeWithOriginal {
		t.Fatal("expected agree_with_original=true")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 API call, got %d", calls)
	}

	if client.IsAvailable() {
		t.Fatal("expected gemini unavailable after max daily calls reached")
	}

	_, err = client.Consult(context.Background(), "plan_validation", "context", "second call")
	if err == nil {
		t.Fatal("expected error when daily calls budget is exhausted")
	}
}

func TestConsultIfNeededFallbackToGeminiWhenClaudeUnavailable(t *testing.T) {
	claude := &fakeConsultantClient{name: "claude", available: false}
	gemini := &fakeConsultantClient{
		name:      "gemini",
		available: true,
		opinion: &Opinion{
			ConsultationType:  "output_evaluation",
			AgreeWithOriginal: true,
			Analysis:          "Looks correct.",
			Recommendations:   []string{"Ship it"},
			RiskFlags:         []string{},
			Confidence:        0.72,
		},
	}

	opinion, err := ConsultIfNeeded(
		context.Background(),
		0.45,
		"output_evaluation",
		"diff context",
		"should we merge?",
		claude,
		gemini,
	)
	if err != nil {
		t.Fatalf("ConsultIfNeeded returned error: %v", err)
	}
	if opinion == nil {
		t.Fatal("expected fallback opinion from gemini")
	}
	if opinion.ConsultationType != "output_evaluation" {
		t.Fatalf("unexpected consultation type: %s", opinion.ConsultationType)
	}

	if claude.calls() != 0 {
		t.Fatalf("expected claude not consulted, calls=%d", claude.calls())
	}
	if gemini.calls() != 1 {
		t.Fatalf("expected gemini consulted once, calls=%d", gemini.calls())
	}
}

type fakeConsultantClient struct {
	name      string
	available bool
	opinion   *Opinion
	err       error

	mu        sync.Mutex
	callCount int
}

func (f *fakeConsultantClient) Consult(ctx context.Context, consultationType string, context string, question string) (*Opinion, error) {
	_ = ctx
	_ = consultationType
	_ = context
	_ = question

	f.mu.Lock()
	f.callCount++
	f.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}
	return f.opinion, nil
}

func (f *fakeConsultantClient) GetName() string {
	return f.name
}

func (f *fakeConsultantClient) GetBudgetRemaining() float64 {
	if f.available {
		return 1
	}
	return 0
}

func (f *fakeConsultantClient) IsAvailable() bool {
	return f.available
}

func (f *fakeConsultantClient) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}
