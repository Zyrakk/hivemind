package engine

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultSoftLimitDaily  = 12
	defaultHardLimitDaily  = 18
	defaultSoftLimitWeekly = 70
	defaultHardLimitWeekly = 100
)

type UsageTrackerConfig struct {
	SoftLimitDaily  int `yaml:"soft_limit_daily"`
	HardLimitDaily  int `yaml:"hard_limit_daily"`
	SoftLimitWeekly int `yaml:"soft_limit_weekly"`
	HardLimitWeekly int `yaml:"hard_limit_weekly"`
}

type UsageStats struct {
	DailyCalls      int
	DailyTokensIn   int
	DailyTokensOut  int
	WeeklyCalls     int
	WeeklyTokensIn  int
	WeeklyTokensOut int
	DailyLimit      int
	WeeklyLimit     int
	AlertLevel      string
}

type UsageTracker struct {
	mu               sync.Mutex
	dailyCalls       int
	dailyTokensIn    int
	dailyTokensOut   int
	dailyResetAt     time.Time
	weeklyCalls      int
	weeklyTokensIn   int
	weeklyTokensOut  int
	weeklyResetAt    time.Time
	config           UsageTrackerConfig
	alertSentDaily70 bool
	alertSentDaily90 bool
	alertSentWeekly  bool
	onAlert          func(message string)
	logger           *slog.Logger
	nowFn            func() time.Time
}

func NewUsageTracker(cfg UsageTrackerConfig, logger *slog.Logger) *UsageTracker {
	if cfg.SoftLimitDaily == 0 {
		cfg.SoftLimitDaily = defaultSoftLimitDaily
	}
	if cfg.HardLimitDaily == 0 {
		cfg.HardLimitDaily = defaultHardLimitDaily
	}
	if cfg.SoftLimitWeekly == 0 {
		cfg.SoftLimitWeekly = defaultSoftLimitWeekly
	}
	if cfg.HardLimitWeekly == 0 {
		cfg.HardLimitWeekly = defaultHardLimitWeekly
	}

	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	now := time.Now()

	return &UsageTracker{
		dailyResetAt:  now,
		weeklyResetAt: now,
		config:        cfg,
		logger:        logger,
		nowFn:         time.Now,
	}
}

func (c UsageTrackerConfig) isZero() bool {
	return c.SoftLimitDaily == 0 &&
		c.HardLimitDaily == 0 &&
		c.SoftLimitWeekly == 0 &&
		c.HardLimitWeekly == 0
}

func (t *UsageTracker) SetAlertCallback(fn func(string)) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.onAlert = fn
}

func (t *UsageTracker) Record(inputTokens, outputTokens int) {
	if t == nil {
		return
	}

	t.mu.Lock()
	t.resetIfNewDay()
	t.resetIfNewWeek()

	t.dailyCalls++
	t.weeklyCalls++
	t.dailyTokensIn += inputTokens
	t.dailyTokensOut += outputTokens
	t.weeklyTokensIn += inputTokens
	t.weeklyTokensOut += outputTokens

	alerts := t.collectAlertsLocked()
	onAlert := t.onAlert
	logger := t.logger
	t.mu.Unlock()

	for _, message := range alerts {
		if logger != nil {
			logger.Warn("claude code usage alert", slog.String("message", message))
		}
		if onAlert != nil {
			onAlert(message)
		}
	}
}

func (t *UsageTracker) CanInvoke() bool {
	if t == nil {
		return true
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.resetIfNewDay()
	t.resetIfNewWeek()

	return t.dailyCalls < t.config.HardLimitDaily && t.weeklyCalls < t.config.HardLimitWeekly
}

func (t *UsageTracker) BlockReason() string {
	if t == nil {
		return ""
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.resetIfNewDay()
	t.resetIfNewWeek()

	if t.dailyCalls >= t.config.HardLimitDaily {
		return fmt.Sprintf("daily limit reached (%d/%d)", t.dailyCalls, t.config.HardLimitDaily)
	}
	if t.weeklyCalls >= t.config.HardLimitWeekly {
		return fmt.Sprintf("weekly limit reached (%d/%d)", t.weeklyCalls, t.config.HardLimitWeekly)
	}

	return ""
}

func (t *UsageTracker) GetUsage() UsageStats {
	if t == nil {
		return UsageStats{AlertLevel: "normal"}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.resetIfNewDay()
	t.resetIfNewWeek()

	return UsageStats{
		DailyCalls:      t.dailyCalls,
		DailyTokensIn:   t.dailyTokensIn,
		DailyTokensOut:  t.dailyTokensOut,
		WeeklyCalls:     t.weeklyCalls,
		WeeklyTokensIn:  t.weeklyTokensIn,
		WeeklyTokensOut: t.weeklyTokensOut,
		DailyLimit:      t.config.HardLimitDaily,
		WeeklyLimit:     t.config.HardLimitWeekly,
		AlertLevel:      t.alertLevelLocked(),
	}
}

func (t *UsageTracker) collectAlertsLocked() []string {
	alerts := make([]string, 0, 3)

	if t.dailyCalls >= t.config.SoftLimitDaily && !t.alertSentDaily70 {
		alerts = append(alerts, fmt.Sprintf(
			"▸ Claude Code: %d/%d calls today. Weekly: %d/%d.",
			t.dailyCalls,
			t.config.HardLimitDaily,
			t.weeklyCalls,
			t.config.HardLimitWeekly,
		))
		t.alertSentDaily70 = true
	}

	if t.dailyCalls >= t.dailyCriticalThresholdLocked() && !t.alertSentDaily90 {
		alerts = append(alerts, fmt.Sprintf(
			"● Claude Code: %d/%d daily calls. GLM fallback at %d. Weekly: %d/%d.",
			t.dailyCalls,
			t.config.HardLimitDaily,
			t.config.HardLimitDaily,
			t.weeklyCalls,
			t.config.HardLimitWeekly,
		))
		t.alertSentDaily90 = true
	}

	if t.weeklyCalls >= t.config.SoftLimitWeekly && !t.alertSentWeekly {
		alerts = append(alerts, fmt.Sprintf(
			"▸ Claude Code weekly: %d/%d. Consider reducing usage or using GLM.",
			t.weeklyCalls,
			t.config.HardLimitWeekly,
		))
		t.alertSentWeekly = true
	}

	return alerts
}

func (t *UsageTracker) alertLevelLocked() string {
	if t.dailyCalls >= t.dailyCriticalThresholdLocked() || t.weeklyCalls >= t.config.HardLimitWeekly {
		return "critical"
	}
	if t.dailyCalls >= t.config.SoftLimitDaily || t.weeklyCalls >= t.config.SoftLimitWeekly {
		return "warning"
	}

	return "normal"
}

func (t *UsageTracker) resetIfNewDay() {
	now := t.now()
	if t.dailyResetAt.IsZero() {
		t.dailyResetAt = now
		return
	}

	nowUTC := now.UTC()
	resetUTC := t.dailyResetAt.UTC()
	if nowUTC.Year() != resetUTC.Year() || nowUTC.YearDay() != resetUTC.YearDay() {
		t.dailyCalls = 0
		t.dailyTokensIn = 0
		t.dailyTokensOut = 0
		t.alertSentDaily70 = false
		t.alertSentDaily90 = false
		t.dailyResetAt = now
	}
}

func (t *UsageTracker) resetIfNewWeek() {
	now := t.now()
	if t.weeklyResetAt.IsZero() {
		t.weeklyResetAt = now
		return
	}

	if now.Sub(t.weeklyResetAt) >= 7*24*time.Hour {
		t.weeklyCalls = 0
		t.weeklyTokensIn = 0
		t.weeklyTokensOut = 0
		t.alertSentWeekly = false
		t.weeklyResetAt = now
	}
}

func (t *UsageTracker) now() time.Time {
	if t != nil && t.nowFn != nil {
		return t.nowFn()
	}
	return time.Now()
}

func (t *UsageTracker) dailyCriticalThresholdLocked() int {
	threshold := t.config.HardLimitDaily - 2
	if threshold < 1 {
		return 1
	}
	return threshold
}
