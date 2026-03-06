package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type mockEngine struct {
	name      string
	available bool

	thinkResult   *ThinkResult
	thinkErr      error
	thinkCalls    int
	proposeResult *PlanResult
	proposeErr    error
	rebuildResult *PlanResult
	rebuildErr    error
	evalResult    *EvalResult
	evalErr       error
}

func (m *mockEngine) Think(context.Context, ThinkRequest) (*ThinkResult, error) {
	m.thinkCalls++
	if m.thinkErr != nil {
		return nil, m.thinkErr
	}
	if m.thinkResult != nil {
		return m.thinkResult, nil
	}
	return &ThinkResult{Type: "ready"}, nil
}

func (m *mockEngine) Propose(context.Context, ProposeRequest) (*PlanResult, error) {
	if m.proposeErr != nil {
		return nil, m.proposeErr
	}
	if m.proposeResult != nil {
		return m.proposeResult, nil
	}
	return &PlanResult{}, nil
}

func (m *mockEngine) Rebuild(context.Context, RebuildRequest) (*PlanResult, error) {
	if m.rebuildErr != nil {
		return nil, m.rebuildErr
	}
	if m.rebuildResult != nil {
		return m.rebuildResult, nil
	}
	return &PlanResult{}, nil
}

func (m *mockEngine) Evaluate(context.Context, EvalRequest) (*EvalResult, error) {
	if m.evalErr != nil {
		return nil, m.evalErr
	}
	if m.evalResult != nil {
		return m.evalResult, nil
	}
	return &EvalResult{}, nil
}

func (m *mockEngine) Name() string {
	return m.name
}

func (m *mockEngine) Available(context.Context) bool {
	return m.available
}

func TestPrimarySucceeds(t *testing.T) {
	t.Parallel()

	primaryResult := &ThinkResult{Type: "ready", Summary: "primary"}
	primary := &mockEngine{name: "claude-code", available: true, thinkResult: primaryResult}
	fallback := &mockEngine{name: "glm", available: true, thinkResult: &ThinkResult{Type: "ready", Summary: "fallback"}}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "glm"},
		map[string]Engine{
			"claude-code": primary,
			"glm":         fallback,
		},
		testLogger(),
	)

	switchCalled := false
	mgr.SetSwitchCallback(func(from, to, reason string) {
		switchCalled = true
	})

	got, err := mgr.Think(context.Background(), ThinkRequest{})
	if err != nil {
		t.Fatalf("Think() error = %v", err)
	}
	if got != primaryResult {
		t.Fatalf("Think() returned unexpected result")
	}
	if switchCalled {
		t.Fatalf("switch callback should not be called")
	}
	if primary.thinkCalls != 1 {
		t.Fatalf("primary think calls = %d, want 1", primary.thinkCalls)
	}
	if fallback.thinkCalls != 0 {
		t.Fatalf("fallback think calls = %d, want 0", fallback.thinkCalls)
	}
}

func TestPrimaryFailsFallbackSucceeds(t *testing.T) {
	t.Parallel()

	primary := &mockEngine{name: "claude-code", available: true, thinkErr: errors.New("boom")}
	fallbackResult := &ThinkResult{Type: "ready", Summary: "fallback"}
	fallback := &mockEngine{name: "glm", available: true, thinkResult: fallbackResult}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "glm"},
		map[string]Engine{
			"claude-code": primary,
			"glm":         fallback,
		},
		testLogger(),
	)

	var from, to, reason string
	mgr.SetSwitchCallback(func(f, tName, r string) {
		from = f
		to = tName
		reason = r
	})

	got, err := mgr.Think(context.Background(), ThinkRequest{})
	if err != nil {
		t.Fatalf("Think() error = %v", err)
	}
	if got != fallbackResult {
		t.Fatalf("Think() should return fallback result")
	}
	if from != "claude-code" || to != "glm" {
		t.Fatalf("unexpected switch callback values: from=%q to=%q", from, to)
	}
	if !strings.Contains(reason, "Think failed: boom") {
		t.Fatalf("reason = %q, want Think failed message", reason)
	}
}

func TestPrimaryUnavailableFallbackSucceeds(t *testing.T) {
	t.Parallel()

	primary := &mockEngine{name: "claude-code", available: false}
	fallbackResult := &ThinkResult{Type: "ready", Summary: "fallback"}
	fallback := &mockEngine{name: "glm", available: true, thinkResult: fallbackResult}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "glm"},
		map[string]Engine{
			"claude-code": primary,
			"glm":         fallback,
		},
		testLogger(),
	)

	var reason string
	mgr.SetSwitchCallback(func(from, to, r string) {
		reason = r
	})

	got, err := mgr.Think(context.Background(), ThinkRequest{})
	if err != nil {
		t.Fatalf("Think() error = %v", err)
	}
	if got != fallbackResult {
		t.Fatalf("Think() should return fallback result")
	}
	if reason != "engine unavailable (quota or auth)" {
		t.Fatalf("reason = %q, want engine unavailable reason", reason)
	}
}

func TestBothFail(t *testing.T) {
	t.Parallel()

	primary := &mockEngine{name: "claude-code", available: true, thinkErr: errors.New("primary failure")}
	fallback := &mockEngine{name: "glm", available: true, thinkErr: errors.New("fallback failure")}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "glm"},
		map[string]Engine{
			"claude-code": primary,
			"glm":         fallback,
		},
		testLogger(),
	)

	switchCalls := 0
	mgr.SetSwitchCallback(func(from, to, reason string) {
		switchCalls++
	})

	got, err := mgr.Think(context.Background(), ThinkRequest{})
	if err == nil {
		t.Fatalf("Think() error = nil, want error")
	}
	if got != nil {
		t.Fatalf("Think() result = %#v, want nil", got)
	}
	if !errors.Is(err, ErrNoEngineAvailable) {
		t.Fatalf("error = %v, want %v", err, ErrNoEngineAvailable)
	}
	if switchCalls != 1 {
		t.Fatalf("switch callback calls = %d, want 1", switchCalls)
	}
}

func TestNoFallback(t *testing.T) {
	t.Parallel()

	primary := &mockEngine{name: "claude-code", available: true, thinkErr: errors.New("primary failure")}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "none"},
		map[string]Engine{
			"claude-code": primary,
		},
		testLogger(),
	)

	got, err := mgr.Think(context.Background(), ThinkRequest{})
	if err == nil {
		t.Fatalf("Think() error = nil, want error")
	}
	if got != nil {
		t.Fatalf("Think() result = %#v, want nil", got)
	}
	if !errors.Is(err, ErrNoEngineAvailable) {
		t.Fatalf("error = %v, want %v", err, ErrNoEngineAvailable)
	}
}

func TestSwitchCallbackReceivesReason(t *testing.T) {
	t.Parallel()

	primaryErr := errors.New("claude code exit 1: auth error")
	primary := &mockEngine{name: "claude-code", available: true, thinkErr: primaryErr}
	fallback := &mockEngine{name: "glm", available: true, thinkResult: &ThinkResult{Type: "ready"}}

	mgr := NewManager(
		ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "glm"},
		map[string]Engine{
			"claude-code": primary,
			"glm":         fallback,
		},
		testLogger(),
	)

	var reason string
	mgr.SetSwitchCallback(func(from, to, r string) {
		reason = r
	})

	_, err := mgr.Think(context.Background(), ThinkRequest{})
	if err != nil {
		t.Fatalf("Think() error = %v", err)
	}
	if !strings.Contains(reason, "auth error") {
		t.Fatalf("reason = %q, want auth error details", reason)
	}
}

func TestActiveEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		primary  bool
		fallback bool
		want     string
	}{
		{
			name:     "primary available",
			primary:  true,
			fallback: true,
			want:     "claude-code",
		},
		{
			name:     "primary unavailable fallback available",
			primary:  false,
			fallback: true,
			want:     "glm",
		},
		{
			name:     "both unavailable",
			primary:  false,
			fallback: false,
			want:     "none",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			primary := &mockEngine{name: "claude-code", available: tc.primary}
			fallback := &mockEngine{name: "glm", available: tc.fallback}

			mgr := NewManager(
				ManagerConfig{PrimaryEngine: "claude-code", FallbackEngine: "glm"},
				map[string]Engine{
					"claude-code": primary,
					"glm":         fallback,
				},
				testLogger(),
			)

			got := mgr.ActiveEngine(context.Background())
			if got != tc.want {
				t.Fatalf("ActiveEngine() = %q, want %q", got, tc.want)
			}
		})
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
