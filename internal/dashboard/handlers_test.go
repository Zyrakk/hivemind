package dashboard

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/zyrakk/hivemind/internal/state"
)

func TestHealthEndpoint(t *testing.T) {
	_, server := setupDashboardTestServer(t)

	resp, body := mustRequestJSON(t, server, http.MethodGet, "/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d. body=%s", http.StatusOK, resp.StatusCode, string(body))
	}

	var payload map[string]string
	mustDecodeJSON(t, body, &payload)
	if payload["status"] != "ok" {
		t.Fatalf("expected status=ok, got %q", payload["status"])
	}
	if payload["uptime"] == "" {
		t.Fatal("expected non-empty uptime")
	}

	resp, _ = mustRequestJSON(t, server, http.MethodOptions, "/api/state", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected OPTIONS status %d, got %d", http.StatusNoContent, resp.StatusCode)
	}
	if origin := resp.Header.Get("Access-Control-Allow-Origin"); origin == "" {
		t.Fatal("expected Access-Control-Allow-Origin header")
	}
}

func TestDashboardEndpointsEndToEnd(t *testing.T) {
	_, server := setupDashboardTestServer(t)

	resp, body := mustRequestJSON(t, server, http.MethodPost, "/api/projects", map[string]any{
		"id":             "flux",
		"description":    "Flux",
		"status":         state.ProjectStatusWorking,
		"repo_url":       "https://example.com/flux",
		"agents_md_path": "/workspace/AGENTS.md",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/projects expected %d, got %d. body=%s", http.StatusCreated, resp.StatusCode, string(body))
	}

	resp, body = mustRequestJSON(t, server, http.MethodPost, "/api/tasks", map[string]any{
		"project_id":  "flux",
		"title":       "Implementar seccion economia",
		"description": "crear endpoints",
		"priority":    3,
		"depends_on":  []string{},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/tasks expected %d, got %d. body=%s", http.StatusCreated, resp.StatusCode, string(body))
	}

	var createdTask idResponse
	mustDecodeJSON(t, body, &createdTask)
	if createdTask.ID <= 0 {
		t.Fatalf("expected task id > 0, got %d", createdTask.ID)
	}

	resp, body = mustRequestJSON(t, server, http.MethodPost, "/api/workers", map[string]any{
		"project_id":       "flux",
		"session_id":       "flux-economy-001",
		"task_description": "Implementar seccion economia",
		"branch":           "feature/flux-economy",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/workers expected %d, got %d. body=%s", http.StatusCreated, resp.StatusCode, string(body))
	}

	var createdWorker idResponse
	mustDecodeJSON(t, body, &createdWorker)
	if createdWorker.ID <= 0 {
		t.Fatalf("expected worker id > 0, got %d", createdWorker.ID)
	}

	resp, body = mustRequestJSON(t, server, http.MethodPut, "/api/tasks/"+strconv.FormatInt(createdTask.ID, 10), map[string]any{
		"assigned_worker_id": createdWorker.ID,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/tasks/{id} expected %d, got %d. body=%s", http.StatusOK, resp.StatusCode, string(body))
	}

	resp, body = mustRequestJSON(t, server, http.MethodPost, "/api/events", map[string]any{
		"project_id":  "flux",
		"worker_id":   createdWorker.ID,
		"event_type":  "worker_started",
		"description": "worker lanzado",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/events expected %d, got %d. body=%s", http.StatusCreated, resp.StatusCode, string(body))
	}

	resp, body = mustRequestJSON(t, server, http.MethodPost, "/api/state/flux", map[string]any{
		"status": state.ProjectStatusPendingReview,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/state/{project} expected %d, got %d. body=%s", http.StatusOK, resp.StatusCode, string(body))
	}

	resp, body = mustRequestJSON(t, server, http.MethodGet, "/api/projects", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/projects expected %d, got %d. body=%s", http.StatusOK, resp.StatusCode, string(body))
	}

	var projects []projectSummaryResponse
	mustDecodeJSON(t, body, &projects)
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].ID != "flux" {
		t.Fatalf("expected project id flux, got %q", projects[0].ID)
	}
	if projects[0].Status != state.ProjectStatusPendingReview {
		t.Fatalf("expected project status %q, got %q", state.ProjectStatusPendingReview, projects[0].Status)
	}

	resp, body = mustRequestJSON(t, server, http.MethodGet, "/api/project/flux", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/project/{id} expected %d, got %d. body=%s", http.StatusOK, resp.StatusCode, string(body))
	}

	var detail projectDetailResponse
	mustDecodeJSON(t, body, &detail)
	if detail.Project.ID != "flux" {
		t.Fatalf("expected detail project id flux, got %q", detail.Project.ID)
	}
	if detail.Project.Status != state.ProjectStatusPendingReview {
		t.Fatalf("expected detail project status %q, got %q", state.ProjectStatusPendingReview, detail.Project.Status)
	}
	if len(detail.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(detail.Tasks))
	}
	if detail.Tasks[0].AssignedWorkerID == nil || *detail.Tasks[0].AssignedWorkerID != createdWorker.ID {
		t.Fatalf("expected task assigned_worker_id=%d", createdWorker.ID)
	}
	if len(detail.Workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(detail.Workers))
	}
	if len(detail.RecentEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(detail.RecentEvents))
	}
	if strings.TrimSpace(detail.Context.Summary) == "" {
		t.Fatal("expected non-empty context summary")
	}
	if len(detail.Context.ArchitectureDecisions) == 0 {
		t.Fatal("expected context architecture decisions")
	}
	if detail.Context.QuickLinks.Repository == "" {
		t.Fatal("expected context quick_links.repository")
	}
	if len(detail.Context.ContributeNow) == 0 {
		t.Fatal("expected context contribute_now items")
	}

	resp, body = mustRequestJSON(t, server, http.MethodGet, "/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/state expected %d, got %d. body=%s", http.StatusOK, resp.StatusCode, string(body))
	}

	var global globalStateResponse
	mustDecodeJSON(t, body, &global)
	if global.Counters.ActiveWorkers != 1 {
		t.Fatalf("expected active_workers=1, got %d", global.Counters.ActiveWorkers)
	}
	if global.Counters.PendingTasks != 1 {
		t.Fatalf("expected pending_tasks=1, got %d", global.Counters.PendingTasks)
	}
	if global.Counters.PendingReview != 1 {
		t.Fatalf("expected pending_reviews=1, got %d", global.Counters.PendingReview)
	}

	resp, body = mustRequestJSON(t, server, http.MethodPut, "/api/workers/"+strconv.FormatInt(createdWorker.ID, 10), map[string]any{
		"status":        state.WorkerStatusCompleted,
		"error_message": nil,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/workers/{id} expected %d, got %d. body=%s", http.StatusOK, resp.StatusCode, string(body))
	}

	resp, body = mustRequestJSON(t, server, http.MethodGet, "/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/state expected %d after worker update, got %d. body=%s", http.StatusOK, resp.StatusCode, string(body))
	}

	mustDecodeJSON(t, body, &global)
	if global.Counters.ActiveWorkers != 0 {
		t.Fatalf("expected active_workers=0 after completion, got %d", global.Counters.ActiveWorkers)
	}
}

func setupDashboardTestServer(t *testing.T) (*state.Store, *httptest.Server) {
	t.Helper()

	store, err := state.New(":memory:")
	if err != nil {
		t.Fatalf("state.New() failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := DefaultConfig()
	cfg.Logger = logger
	cfg.Host = "127.0.0.1"
	cfg.Port = 0

	srv := NewServer(store, cfg)
	testServer := httptest.NewServer(srv.Handler)

	t.Cleanup(func() {
		testServer.Close()
		_ = store.Close()
	})

	return store, testServer
}

func mustRequestJSON(t *testing.T, server *httptest.Server, method, path string, body any) (*http.Response, []byte) {
	t.Helper()

	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() failed: %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, server.URL+path, requestBody)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("http request failed: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll() failed: %v", err)
	}

	return resp, responseBody
}

func mustDecodeJSON(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("json.Unmarshal() failed: %v body=%s", err, string(body))
	}
}
