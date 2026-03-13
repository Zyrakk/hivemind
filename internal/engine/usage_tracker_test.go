package engine

import (
	"testing"
	"time"
)

func TestRecordAndCanInvokeDaily(t *testing.T) {
	t.Parallel()

	tracker, _ := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  2,
		HardLimitDaily:  3,
		SoftLimitWeekly: 20,
		HardLimitWeekly: 30,
	})

	for range 3 {
		tracker.Record(10, 20)
	}

	if tracker.CanInvoke() {
		t.Fatal("CanInvoke() = true, want false")
	}
}

func TestRecordAndCanInvokeWeekly(t *testing.T) {
	t.Parallel()

	tracker, _ := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  10,
		HardLimitDaily:  20,
		SoftLimitWeekly: 2,
		HardLimitWeekly: 3,
	})

	for range 3 {
		tracker.Record(10, 20)
	}

	if tracker.CanInvoke() {
		t.Fatal("CanInvoke() = true, want false")
	}
}

func TestDailyReset(t *testing.T) {
	t.Parallel()

	tracker, current := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  2,
		HardLimitDaily:  5,
		SoftLimitWeekly: 20,
		HardLimitWeekly: 30,
	})

	tracker.Record(11, 13)
	tracker.Record(7, 5)

	*current = current.Add(24 * time.Hour)

	usage := tracker.GetUsage()
	if usage.DailyCalls != 0 {
		t.Fatalf("DailyCalls = %d, want 0", usage.DailyCalls)
	}
	if usage.DailyTokensIn != 0 || usage.DailyTokensOut != 0 {
		t.Fatalf("daily tokens = (%d, %d), want (0, 0)", usage.DailyTokensIn, usage.DailyTokensOut)
	}
	if usage.WeeklyCalls != 2 {
		t.Fatalf("WeeklyCalls = %d, want 2", usage.WeeklyCalls)
	}
	if usage.WeeklyTokensIn != 18 || usage.WeeklyTokensOut != 18 {
		t.Fatalf("weekly tokens = (%d, %d), want (18, 18)", usage.WeeklyTokensIn, usage.WeeklyTokensOut)
	}
}

func TestWeeklyReset(t *testing.T) {
	t.Parallel()

	tracker, current := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  2,
		HardLimitDaily:  5,
		SoftLimitWeekly: 3,
		HardLimitWeekly: 4,
	})

	tracker.Record(11, 13)
	tracker.Record(7, 5)

	*current = current.Add(8 * 24 * time.Hour)

	usage := tracker.GetUsage()
	if usage.WeeklyCalls != 0 {
		t.Fatalf("WeeklyCalls = %d, want 0", usage.WeeklyCalls)
	}
	if usage.WeeklyTokensIn != 0 || usage.WeeklyTokensOut != 0 {
		t.Fatalf("weekly tokens = (%d, %d), want (0, 0)", usage.WeeklyTokensIn, usage.WeeklyTokensOut)
	}
}

func TestDailyAlertAt70Percent(t *testing.T) {
	t.Parallel()

	tracker, _ := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  7,
		HardLimitDaily:  10,
		SoftLimitWeekly: 50,
		HardLimitWeekly: 20,
	})

	var alerts []string
	tracker.SetAlertCallback(func(message string) {
		alerts = append(alerts, message)
	})

	for range 7 {
		tracker.Record(1, 1)
	}

	if len(alerts) != 1 {
		t.Fatalf("len(alerts) = %d, want 1", len(alerts))
	}
	if alerts[0] != "▸ Claude Code: 7/10 calls today. Weekly: 7/20." {
		t.Fatalf("alert = %q, want %q", alerts[0], "▸ Claude Code: 7/10 calls today. Weekly: 7/20.")
	}
}

func TestDailyAlertAt90Percent(t *testing.T) {
	t.Parallel()

	tracker, _ := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  7,
		HardLimitDaily:  10,
		SoftLimitWeekly: 50,
		HardLimitWeekly: 100,
	})

	var alerts []string
	tracker.SetAlertCallback(func(message string) {
		alerts = append(alerts, message)
	})

	for range 8 {
		tracker.Record(1, 1)
	}

	if len(alerts) != 2 {
		t.Fatalf("len(alerts) = %d, want 2", len(alerts))
	}
	if alerts[1] != "● Claude Code: 8/10 daily calls. GLM fallback at 10. Weekly: 8/100." {
		t.Fatalf("alert = %q, want %q", alerts[1], "● Claude Code: 8/10 daily calls. GLM fallback at 10. Weekly: 8/100.")
	}
}

func TestWeeklyAlert(t *testing.T) {
	t.Parallel()

	tracker, _ := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  50,
		HardLimitDaily:  60,
		SoftLimitWeekly: 3,
		HardLimitWeekly: 5,
	})

	var alerts []string
	tracker.SetAlertCallback(func(message string) {
		alerts = append(alerts, message)
	})

	for range 3 {
		tracker.Record(1, 1)
	}

	if len(alerts) != 1 {
		t.Fatalf("len(alerts) = %d, want 1", len(alerts))
	}
	if alerts[0] != "▸ Claude Code weekly: 3/5. Consider reducing usage or using GLM." {
		t.Fatalf("alert = %q, want %q", alerts[0], "▸ Claude Code weekly: 3/5. Consider reducing usage or using GLM.")
	}
}

func TestAlertOnlyFiresOnce(t *testing.T) {
	t.Parallel()

	tracker, _ := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  2,
		HardLimitDaily:  10,
		SoftLimitWeekly: 50,
		HardLimitWeekly: 100,
	})

	var alerts []string
	tracker.SetAlertCallback(func(message string) {
		alerts = append(alerts, message)
	})

	for range 5 {
		tracker.Record(1, 1)
	}

	if len(alerts) != 1 {
		t.Fatalf("len(alerts) = %d, want 1", len(alerts))
	}
}

func TestAlertResetsDaily(t *testing.T) {
	t.Parallel()

	tracker, current := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  2,
		HardLimitDaily:  5,
		SoftLimitWeekly: 50,
		HardLimitWeekly: 100,
	})

	var alerts []string
	tracker.SetAlertCallback(func(message string) {
		alerts = append(alerts, message)
	})

	tracker.Record(1, 1)
	tracker.Record(1, 1)

	*current = current.Add(24 * time.Hour)

	tracker.Record(1, 1)
	tracker.Record(1, 1)

	if len(alerts) != 2 {
		t.Fatalf("len(alerts) = %d, want 2", len(alerts))
	}
	if alerts[0] != "▸ Claude Code: 2/5 calls today. Weekly: 2/100." {
		t.Fatalf("first alert = %q, want %q", alerts[0], "▸ Claude Code: 2/5 calls today. Weekly: 2/100.")
	}
	if alerts[1] != "▸ Claude Code: 2/5 calls today. Weekly: 4/100." {
		t.Fatalf("second alert = %q, want %q", alerts[1], "▸ Claude Code: 2/5 calls today. Weekly: 4/100.")
	}
}

func TestBlockReason(t *testing.T) {
	t.Parallel()

	t.Run("daily", func(t *testing.T) {
		t.Parallel()

		tracker, _ := newTestUsageTracker(UsageTrackerConfig{
			SoftLimitDaily:  1,
			HardLimitDaily:  2,
			SoftLimitWeekly: 10,
			HardLimitWeekly: 20,
		})

		for range 2 {
			tracker.Record(1, 1)
		}

		if got := tracker.BlockReason(); got != "daily limit reached (2/2)" {
			t.Fatalf("BlockReason() = %q, want %q", got, "daily limit reached (2/2)")
		}
	})

	t.Run("weekly", func(t *testing.T) {
		t.Parallel()

		tracker, _ := newTestUsageTracker(UsageTrackerConfig{
			SoftLimitDaily:  10,
			HardLimitDaily:  20,
			SoftLimitWeekly: 1,
			HardLimitWeekly: 2,
		})

		for range 2 {
			tracker.Record(1, 1)
		}

		if got := tracker.BlockReason(); got != "weekly limit reached (2/2)" {
			t.Fatalf("BlockReason() = %q, want %q", got, "weekly limit reached (2/2)")
		}
	})
}

func TestGetUsage(t *testing.T) {
	t.Parallel()

	tracker, _ := newTestUsageTracker(UsageTrackerConfig{
		SoftLimitDaily:  3,
		HardLimitDaily:  6,
		SoftLimitWeekly: 8,
		HardLimitWeekly: 10,
	})

	tracker.Record(10, 20)
	tracker.Record(5, 7)

	usage := tracker.GetUsage()
	if usage.DailyCalls != 2 || usage.WeeklyCalls != 2 {
		t.Fatalf("calls = (%d, %d), want (2, 2)", usage.DailyCalls, usage.WeeklyCalls)
	}
	if usage.DailyTokensIn != 15 || usage.DailyTokensOut != 27 {
		t.Fatalf("daily tokens = (%d, %d), want (15, 27)", usage.DailyTokensIn, usage.DailyTokensOut)
	}
	if usage.WeeklyTokensIn != 15 || usage.WeeklyTokensOut != 27 {
		t.Fatalf("weekly tokens = (%d, %d), want (15, 27)", usage.WeeklyTokensIn, usage.WeeklyTokensOut)
	}
	if usage.DailyLimit != 6 || usage.WeeklyLimit != 10 {
		t.Fatalf("limits = (%d, %d), want (6, 10)", usage.DailyLimit, usage.WeeklyLimit)
	}
	if usage.AlertLevel != "normal" {
		t.Fatalf("AlertLevel = %q, want %q", usage.AlertLevel, "normal")
	}
}

func TestDefaultLimits(t *testing.T) {
	t.Parallel()

	tracker := NewUsageTracker(UsageTrackerConfig{}, nil)

	if tracker.config.SoftLimitDaily != 12 {
		t.Fatalf("SoftLimitDaily = %d, want 12", tracker.config.SoftLimitDaily)
	}
	if tracker.config.HardLimitDaily != 18 {
		t.Fatalf("HardLimitDaily = %d, want 18", tracker.config.HardLimitDaily)
	}
	if tracker.config.SoftLimitWeekly != 70 {
		t.Fatalf("SoftLimitWeekly = %d, want 70", tracker.config.SoftLimitWeekly)
	}
	if tracker.config.HardLimitWeekly != 100 {
		t.Fatalf("HardLimitWeekly = %d, want 100", tracker.config.HardLimitWeekly)
	}
}

func TestOnResumeFromQuota(t *testing.T) {
	t.Parallel()

	cfg := UsageTrackerConfig{
		SoftLimitDaily:  2,
		HardLimitDaily:  3,
		SoftLimitWeekly: 10,
		HardLimitWeekly: 15,
	}
	tracker, current := newTestUsageTracker(cfg)

	resumed := false
	tracker.OnResumeFromQuota(func() {
		resumed = true
	})

	// Record up to hard limit.
	tracker.Record(100, 50)
	tracker.Record(100, 50)
	tracker.Record(100, 50)

	if tracker.CanInvoke() {
		t.Fatal("expected blocked at hard limit")
	}
	if resumed {
		t.Fatal("should not have resumed yet")
	}

	// Simulate day rollover by advancing time.
	*current = current.Add(25 * time.Hour)

	// CanInvoke resets counters on new day -> should trigger resume.
	if !tracker.CanInvoke() {
		t.Fatal("expected unblocked after day reset")
	}
	if !resumed {
		t.Fatal("expected resume callback to fire after quota reset")
	}
}

func TestOnResumeFromQuota_NilTracker(t *testing.T) {
	t.Parallel()

	var tracker *UsageTracker
	tracker.OnResumeFromQuota(func() {}) // should not panic
}

func newTestUsageTracker(cfg UsageTrackerConfig) (*UsageTracker, *time.Time) {
	current := time.Date(2026, time.March, 6, 10, 0, 0, 0, time.UTC)
	tracker := NewUsageTracker(cfg, nil)
	tracker.nowFn = func() time.Time {
		return current
	}
	tracker.dailyResetAt = current
	tracker.weeklyResetAt = current

	return tracker, &current
}
