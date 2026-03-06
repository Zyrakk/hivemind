package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

var ErrNoEngineAvailable = errors.New("no engine available")

type Manager struct {
	primary  Engine
	fallback Engine
	logger   *slog.Logger
	mu       sync.RWMutex
	onSwitch func(from, to, reason string)
	lastUsed string
}

type ManagerConfig struct {
	PrimaryEngine  string `json:"primary" yaml:"primary"`
	FallbackEngine string `json:"fallback" yaml:"fallback"`
}

func NewManager(cfg ManagerConfig, engines map[string]Engine, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	mgr := &Manager{logger: logger}
	if len(engines) == 0 {
		logger.Error("no engines configured")
		return mgr
	}

	primaryName := strings.TrimSpace(cfg.PrimaryEngine)
	primary := engines[primaryName]
	if primary == nil {
		if primaryName != "" {
			logger.Error("primary engine not found; using first available",
				slog.String("requested", primaryName))
		} else {
			logger.Error("primary engine not configured; using first available")
		}
		primary = firstAvailableEngine(engines)
	}
	mgr.primary = primary

	fallbackName := strings.TrimSpace(cfg.FallbackEngine)
	if fallbackName == "" || strings.EqualFold(fallbackName, "none") {
		return mgr
	}

	fallback := engines[fallbackName]
	if fallback == nil {
		logger.Error("fallback engine not found",
			slog.String("requested", fallbackName))
		return mgr
	}

	if mgr.primary != nil && fallback.Name() == mgr.primary.Name() {
		logger.Warn("fallback matches primary; fallback disabled",
			slog.String("engine", fallback.Name()))
		return mgr
	}

	mgr.fallback = fallback
	return mgr
}

func (m *Manager) SetSwitchCallback(fn func(from, to, reason string)) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSwitch = fn
}

func (m *Manager) LastUsedEngine() string {
	if m == nil {
		return ""
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastUsed
}

func (m *Manager) Think(ctx context.Context, req ThinkRequest) (*ThinkResult, error) {
	return runWithFallback(ctx, m, "Think", func(eng Engine) (*ThinkResult, error) {
		return eng.Think(ctx, req)
	})
}

func (m *Manager) Propose(ctx context.Context, req ProposeRequest) (*PlanResult, error) {
	return runWithFallback(ctx, m, "Propose", func(eng Engine) (*PlanResult, error) {
		return eng.Propose(ctx, req)
	})
}

func (m *Manager) Rebuild(ctx context.Context, req RebuildRequest) (*PlanResult, error) {
	return runWithFallback(ctx, m, "Rebuild", func(eng Engine) (*PlanResult, error) {
		return eng.Rebuild(ctx, req)
	})
}

func (m *Manager) Evaluate(ctx context.Context, req EvalRequest) (*EvalResult, error) {
	return runWithFallback(ctx, m, "Evaluate", func(eng Engine) (*EvalResult, error) {
		return eng.Evaluate(ctx, req)
	})
}

func (m *Manager) ActiveEngine(ctx context.Context) string {
	if m == nil {
		return "none"
	}
	if m.primary != nil && m.primary.Available(ctx) {
		return m.primary.Name()
	}
	if m.fallback != nil && m.fallback.Available(ctx) {
		return m.fallback.Name()
	}
	return "none"
}

func (m *Manager) notifySwitch(from, to, reason string) {
	if m == nil {
		return
	}

	m.mu.RLock()
	callback := m.onSwitch
	m.mu.RUnlock()

	if callback != nil {
		callback(from, to, reason)
	}
	if m.logger != nil {
		m.logger.Warn("engine switch",
			slog.String("from", from),
			slog.String("to", to),
			slog.String("reason", reason))
	}
}

func (m *Manager) recordLastUsed(name string) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastUsed = strings.TrimSpace(name)
}

func runWithFallback[T any](
	ctx context.Context,
	m *Manager,
	operation string,
	call func(eng Engine) (*T, error),
) (*T, error) {
	if m == nil || m.primary == nil {
		return nil, ErrNoEngineAvailable
	}

	primary := m.primary
	reason := ""

	if primary.Available(ctx) {
		result, err := call(primary)
		if err == nil {
			m.recordLastUsed(primary.Name())
			return result, nil
		}
		reason = fmt.Sprintf("%s failed: %s", operation, err.Error())
		if m.logger != nil {
			m.logger.Warn("primary engine call failed",
				slog.String("engine", primary.Name()),
				slog.String("operation", operation),
				slog.Any("error", err))
		}
	} else {
		reason = "engine unavailable (quota or auth)"
		if m.logger != nil {
			m.logger.Warn("primary engine unavailable",
				slog.String("engine", primary.Name()),
				slog.String("operation", operation))
		}
	}

	if m.fallback != nil && m.fallback.Available(ctx) {
		fallback := m.fallback
		m.notifySwitch(primary.Name(), fallback.Name(), reason)
		result, err := call(fallback)
		if err == nil {
			m.recordLastUsed(fallback.Name())
			return result, nil
		}
		if m.logger != nil {
			m.logger.Warn("fallback engine call failed",
				slog.String("engine", fallback.Name()),
				slog.String("operation", operation),
				slog.Any("error", err))
		}
	}

	return nil, ErrNoEngineAvailable
}

func firstAvailableEngine(engines map[string]Engine) Engine {
	if len(engines) == 0 {
		return nil
	}

	keys := make([]string, 0, len(engines))
	for name := range engines {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if eng := engines[key]; eng != nil {
			return eng
		}
	}
	return nil
}
