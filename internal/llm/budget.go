package llm

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type BudgetTracker struct {
	MaxMonthlyUSD   float64
	MaxDailyCalls   int
	CurrentMonthUSD float64
	CurrentDayCalls int
	LastResetDate   time.Time

	mu             sync.Mutex
	consultantName string
	db             *sql.DB
	ownsDB         bool
	logger         *slog.Logger
	nowFn          func() time.Time
}

type BudgetConfig struct {
	ConsultantName string
	MaxMonthlyUSD  float64
	MaxDailyCalls  int

	DBPath string
	DB     *sql.DB

	Logger *slog.Logger
	NowFn  func() time.Time
}

func NewBudgetTracker(config BudgetConfig) (*BudgetTracker, error) {
	name := strings.TrimSpace(config.ConsultantName)
	if name == "" {
		return nil, fmt.Errorf("consultant name is required")
	}

	tracker := &BudgetTracker{
		MaxMonthlyUSD:   maxFloat64(config.MaxMonthlyUSD, 0),
		MaxDailyCalls:   maxInt(config.MaxDailyCalls, 0),
		CurrentMonthUSD: 0,
		CurrentDayCalls: 0,
		LastResetDate:   nowUTC(),
		consultantName:  name,
		logger:          config.Logger,
		nowFn:           config.NowFn,
	}

	if tracker.logger == nil {
		tracker.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if tracker.nowFn == nil {
		tracker.nowFn = nowUTC
	}

	if config.DB != nil {
		tracker.db = config.DB
	} else if strings.TrimSpace(config.DBPath) != "" {
		dbPath := config.DBPath
		if dbPath != ":memory:" {
			dbPath = filepath.Clean(dbPath)
			if dir := filepath.Dir(dbPath); dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, fmt.Errorf("create budget db dir: %w", err)
				}
			}
		}

		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			return nil, fmt.Errorf("open budget sqlite db: %w", err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		tracker.db = db
		tracker.ownsDB = true
	}

	if tracker.db != nil {
		if err := tracker.ensureSchema(); err != nil {
			_ = tracker.Close()
			return nil, err
		}
		if err := tracker.loadOrCreate(); err != nil {
			_ = tracker.Close()
			return nil, err
		}
	}

	return tracker, nil
}

func (b *BudgetTracker) CanAfford(estimatedCost float64) bool {
	if b == nil {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.resetIfNeededLocked(b.nowFn())

	estimatedCost = maxFloat64(estimatedCost, 0)

	if b.MaxDailyCalls > 0 && b.CurrentDayCalls+1 > b.MaxDailyCalls {
		return false
	}
	if b.MaxMonthlyUSD > 0 {
		if estimatedCost == 0 {
			if b.CurrentMonthUSD >= b.MaxMonthlyUSD {
				return false
			}
		} else if b.CurrentMonthUSD+estimatedCost > b.MaxMonthlyUSD {
			return false
		}
	}

	return true
}

func (b *BudgetTracker) Record(actualCost float64) {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.resetIfNeededLocked(b.nowFn())

	b.CurrentDayCalls++
	b.CurrentMonthUSD += maxFloat64(actualCost, 0)

	if err := b.persistLocked(); err != nil {
		b.logger.Warn("persist consultant budget", slog.String("consultant", b.consultantName), slog.Any("error", err))
	}
}

func (b *BudgetTracker) Remaining() float64 {
	if b == nil {
		return 0
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.resetIfNeededLocked(b.nowFn())

	remainingMonthly := math.Inf(1)
	if b.MaxMonthlyUSD > 0 {
		remainingMonthly = b.MaxMonthlyUSD - b.CurrentMonthUSD
		if remainingMonthly < 0 {
			remainingMonthly = 0
		}
	}

	remainingDaily := math.Inf(1)
	if b.MaxDailyCalls > 0 {
		remainingDaily = float64(b.MaxDailyCalls - b.CurrentDayCalls)
		if remainingDaily < 0 {
			remainingDaily = 0
		}
	}

	if remainingMonthly < remainingDaily {
		return remainingMonthly
	}

	return remainingDaily
}

func (b *BudgetTracker) Snapshot() BudgetTracker {
	if b == nil {
		return BudgetTracker{}
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.resetIfNeededLocked(b.nowFn())

	return BudgetTracker{
		MaxMonthlyUSD:   b.MaxMonthlyUSD,
		MaxDailyCalls:   b.MaxDailyCalls,
		CurrentMonthUSD: b.CurrentMonthUSD,
		CurrentDayCalls: b.CurrentDayCalls,
		LastResetDate:   b.LastResetDate,
	}
}

func (b *BudgetTracker) Close() error {
	if b == nil || !b.ownsDB || b.db == nil {
		return nil
	}

	return b.db.Close()
}

func (b *BudgetTracker) ensureSchema() error {
	if b.db == nil {
		return nil
	}

	const schema = `
CREATE TABLE IF NOT EXISTS consultant_budget (
	consultant_name TEXT PRIMARY KEY,
	max_monthly_usd REAL NOT NULL DEFAULT 0,
	max_daily_calls INTEGER NOT NULL DEFAULT 0,
	current_month_usd REAL NOT NULL DEFAULT 0,
	current_day_calls INTEGER NOT NULL DEFAULT 0,
	last_reset_date TEXT NOT NULL,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

	if _, err := b.db.Exec(schema); err != nil {
		return fmt.Errorf("create consultant_budget table: %w", err)
	}

	return nil
}

func (b *BudgetTracker) loadOrCreate() error {
	if b.db == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	const query = `
SELECT max_monthly_usd, max_daily_calls, current_month_usd, current_day_calls, last_reset_date
FROM consultant_budget
WHERE consultant_name = ?`

	var maxMonthly float64
	var maxDaily int
	var currentMonth float64
	var currentDay int
	var lastResetRaw string

	err := b.db.QueryRow(query, b.consultantName).Scan(&maxMonthly, &maxDaily, &currentMonth, &currentDay, &lastResetRaw)
	if err != nil {
		if err == sql.ErrNoRows {
			b.LastResetDate = b.nowFn()
			return b.persistLocked()
		}
		return fmt.Errorf("load consultant budget: %w", err)
	}

	if b.MaxMonthlyUSD == 0 {
		b.MaxMonthlyUSD = maxFloat64(maxMonthly, 0)
	}
	if b.MaxDailyCalls == 0 {
		b.MaxDailyCalls = maxInt(maxDaily, 0)
	}
	b.CurrentMonthUSD = maxFloat64(currentMonth, 0)
	b.CurrentDayCalls = maxInt(currentDay, 0)

	parsedTime, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(lastResetRaw))
	if parseErr != nil {
		b.LastResetDate = b.nowFn()
	} else {
		b.LastResetDate = parsedTime
	}

	b.resetIfNeededLocked(b.nowFn())
	return b.persistLocked()
}

func (b *BudgetTracker) persistLocked() error {
	if b.db == nil {
		return nil
	}

	if b.LastResetDate.IsZero() {
		b.LastResetDate = b.nowFn()
	}

	const upsert = `
INSERT INTO consultant_budget (
	consultant_name,
	max_monthly_usd,
	max_daily_calls,
	current_month_usd,
	current_day_calls,
	last_reset_date,
	updated_at
)
VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(consultant_name) DO UPDATE SET
	max_monthly_usd = excluded.max_monthly_usd,
	max_daily_calls = excluded.max_daily_calls,
	current_month_usd = excluded.current_month_usd,
	current_day_calls = excluded.current_day_calls,
	last_reset_date = excluded.last_reset_date,
	updated_at = CURRENT_TIMESTAMP`

	_, err := b.db.Exec(
		upsert,
		b.consultantName,
		b.MaxMonthlyUSD,
		b.MaxDailyCalls,
		b.CurrentMonthUSD,
		b.CurrentDayCalls,
		b.LastResetDate.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert consultant budget: %w", err)
	}

	return nil
}

func (b *BudgetTracker) resetIfNeededLocked(now time.Time) {
	if b.LastResetDate.IsZero() {
		b.LastResetDate = now
		_ = b.persistLocked()
		return
	}

	current := now.UTC()
	last := b.LastResetDate.UTC()
	changed := false

	if current.Year() != last.Year() || current.Month() != last.Month() {
		b.CurrentMonthUSD = 0
		changed = true
	}

	if current.Year() != last.Year() || current.YearDay() != last.YearDay() {
		b.CurrentDayCalls = 0
		changed = true
	}

	if changed {
		b.LastResetDate = current
		if err := b.persistLocked(); err != nil {
			b.logger.Warn("persist consultant budget reset", slog.String("consultant", b.consultantName), slog.Any("error", err))
		}
	}
}

func maxFloat64(v float64, min float64) float64 {
	if v < min {
		return min
	}
	return v
}

func maxInt(v int, min int) int {
	if v < min {
		return min
	}
	return v
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
