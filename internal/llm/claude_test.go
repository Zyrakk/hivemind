package llm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaudeConsultParsesResponseAndTracksBudget(t *testing.T) {
	promptDir := t.TempDir()
	writeConsultantPrompt(t, promptDir)

	budget, err := NewBudgetTracker(BudgetConfig{
		ConsultantName: "claude",
		MaxMonthlyUSD:  0.05,
		MaxDailyCalls:  10,
		NowFn: func() time.Time {
			return time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("new budget tracker: %v", err)
	}

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)

		if got := r.Header.Get("x-api-key"); got != "anthropic-key" {
			t.Errorf("unexpected x-api-key: %q", got)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("unexpected anthropic-version: %q", got)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if got, _ := reqBody["model"].(string); got != "claude-sonnet-4-5-20250929" {
			t.Errorf("unexpected model: %q", got)
		}

		resp := map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": `{"consultation_type":"output_evaluation","agree_with_original":false,"analysis":"Needs stronger validation and tests.","recommendations":["Add retry tests","Validate JSON fields"],"risk_flags":["missing edge-case coverage"],"confidence":0.78}`,
				},
			},
			"usage": map[string]any{
				"input_tokens":  1000,
				"output_tokens": 500,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClaudeClient(ClaudeConfig{
		APIKey:    "anthropic-key",
		BaseURL:   server.URL,
		PromptDir: promptDir,
		Budget:    budget,
		Logger:    discardLogger(),
	})

	opinion, err := client.Consult(context.Background(), "output_evaluation", "diff context", "is this safe?")
	if err != nil {
		t.Fatalf("consult returned error: %v", err)
	}
	if opinion == nil {
		t.Fatal("expected opinion")
	}
	if opinion.ConsultationType != "output_evaluation" {
		t.Fatalf("unexpected consultation type: %s", opinion.ConsultationType)
	}
	if opinion.AgreeWithOriginal {
		t.Fatalf("expected disagree_with_original=false")
	}
	if len(opinion.Recommendations) != 2 {
		t.Fatalf("unexpected recommendations: %+v", opinion.Recommendations)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 API call, got %d", calls)
	}

	snapshot := budget.Snapshot()
	if snapshot.CurrentDayCalls != 1 {
		t.Fatalf("expected 1 daily call recorded, got %d", snapshot.CurrentDayCalls)
	}
	if snapshot.CurrentMonthUSD <= 0 {
		t.Fatalf("expected monthly cost to be recorded, got %f", snapshot.CurrentMonthUSD)
	}
}

func TestClaudeIsUnavailableWhenMonthlyBudgetReached(t *testing.T) {
	promptDir := t.TempDir()
	writeConsultantPrompt(t, promptDir)

	budget, err := NewBudgetTracker(BudgetConfig{
		ConsultantName: "claude",
		MaxMonthlyUSD:  0.001,
		NowFn: func() time.Time {
			return time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("new budget tracker: %v", err)
	}

	budget.Record(0.001)

	client := NewClaudeClient(ClaudeConfig{
		APIKey:    "anthropic-key",
		BaseURL:   "http://127.0.0.1:0",
		PromptDir: promptDir,
		Budget:    budget,
		Logger:    discardLogger(),
	})

	if client.IsAvailable() {
		t.Fatal("expected claude to be unavailable after reaching monthly limit")
	}

	_, err = client.Consult(context.Background(), "output_evaluation", "ctx", "question")
	if err == nil {
		t.Fatal("expected consult error when budget is exhausted")
	}
}

func TestBudgetTrackerLimitsPersistenceAndResets(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "budget.db")
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	tracker, err := NewBudgetTracker(BudgetConfig{
		ConsultantName: "claude",
		MaxMonthlyUSD:  1.0,
		MaxDailyCalls:  2,
		DBPath:         dbPath,
		NowFn: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("new budget tracker: %v", err)
	}

	if !tracker.CanAfford(0.4) {
		t.Fatal("expected budget to afford first call")
	}
	tracker.Record(0.4)
	if !tracker.CanAfford(0.5) {
		t.Fatal("expected budget to afford second call")
	}
	tracker.Record(0.5)

	if tracker.CanAfford(0.01) {
		t.Fatal("expected daily call limit to block third call")
	}

	now = now.Add(24 * time.Hour)
	if !tracker.CanAfford(0.09) {
		t.Fatal("expected daily reset to allow new call")
	}
	tracker.Record(0.09)

	if tracker.CanAfford(0.2) {
		t.Fatal("expected monthly USD limit to block over-budget call")
	}

	if err := tracker.Close(); err != nil {
		t.Fatalf("close tracker: %v", err)
	}

	trackerReloaded, err := NewBudgetTracker(BudgetConfig{
		ConsultantName: "claude",
		DBPath:         dbPath,
		NowFn: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("reload budget tracker: %v", err)
	}
	defer func() { _ = trackerReloaded.Close() }()

	snapshot := trackerReloaded.Snapshot()
	if math.Abs(snapshot.CurrentMonthUSD-0.99) > 1e-9 {
		t.Fatalf("unexpected persisted monthly usd: %f", snapshot.CurrentMonthUSD)
	}
	if snapshot.CurrentDayCalls != 1 {
		t.Fatalf("unexpected persisted daily calls: %d", snapshot.CurrentDayCalls)
	}

	now = time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)
	if !trackerReloaded.CanAfford(0.8) {
		t.Fatal("expected monthly reset to allow call in new month")
	}
	snapshot = trackerReloaded.Snapshot()
	if snapshot.CurrentMonthUSD != 0 {
		t.Fatalf("expected monthly usd reset, got %f", snapshot.CurrentMonthUSD)
	}
}

func writeConsultantPrompt(t *testing.T, dir string) {
	t.Helper()

	path := filepath.Join(dir, "consultant.txt")
	content := "You are a strict consultant. Return JSON only."
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write consultant prompt: %v", err)
	}
}
