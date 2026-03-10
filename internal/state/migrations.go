package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const SchemaSQL = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('working', 'needs_input', 'pending_review', 'blocked', 'paused')),
    repo_url TEXT NOT NULL,
    agents_md_path TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS workers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    session_id TEXT NOT NULL,
    task_description TEXT NOT NULL,
    branch TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('running', 'paused', 'completed', 'failed', 'blocked', 'cancelled')),
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    error_message TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('pending', 'in_progress', 'completed', 'failed', 'blocked', 'rejected')),
    priority INTEGER NOT NULL DEFAULT 0,
    depends_on TEXT NOT NULL DEFAULT '',
    assigned_worker_id INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY(assigned_worker_id) REFERENCES workers(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    worker_id INTEGER,
    event_type TEXT NOT NULL,
    description TEXT NOT NULL,
    timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY(worker_id) REFERENCES workers(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_workers_project_id ON workers(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_project_id ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_assigned_worker_id ON tasks(assigned_worker_id);
CREATE INDEX IF NOT EXISTS idx_events_project_id ON events(project_id);
CREATE INDEX IF NOT EXISTS idx_events_worker_id ON events(worker_id);
`

const migrationAddRejectedCancelled = `
PRAGMA foreign_keys = OFF;

-- Recreate workers table with 'cancelled' status
CREATE TABLE IF NOT EXISTS workers_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    session_id TEXT NOT NULL,
    task_description TEXT NOT NULL,
    branch TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('running', 'paused', 'completed', 'failed', 'blocked', 'cancelled')),
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    error_message TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);
INSERT OR IGNORE INTO workers_new SELECT * FROM workers;
DROP TABLE IF EXISTS workers;
ALTER TABLE workers_new RENAME TO workers;
CREATE INDEX IF NOT EXISTS idx_workers_project_id ON workers(project_id);

-- Recreate tasks table with 'rejected' status
CREATE TABLE IF NOT EXISTS tasks_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('pending', 'in_progress', 'completed', 'failed', 'blocked', 'rejected')),
    priority INTEGER NOT NULL DEFAULT 0,
    depends_on TEXT NOT NULL DEFAULT '',
    assigned_worker_id INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY(assigned_worker_id) REFERENCES workers(id) ON DELETE SET NULL
);
INSERT OR IGNORE INTO tasks_new SELECT * FROM tasks;
DROP TABLE IF EXISTS tasks;
ALTER TABLE tasks_new RENAME TO tasks;
CREATE INDEX IF NOT EXISTS idx_tasks_project_id ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_assigned_worker_id ON tasks(assigned_worker_id);

PRAGMA foreign_keys = ON;
`

const migrationAddPlansTable = `
CREATE TABLE IF NOT EXISTS plans (
    id TEXT PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id),
    directive TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','approved','executing','completed','failed','cancelled')),
    engine TEXT NOT NULL DEFAULT '',
    summary TEXT DEFAULT '',
    plan_data TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_plans_project_id ON plans(project_id);
CREATE INDEX IF NOT EXISTS idx_plans_status ON plans(status);
`

const migrationAddBatchTables = `
CREATE TABLE IF NOT EXISTS batches (
    id TEXT PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id),
    name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','running','completed','failed','paused')),
    total_items INTEGER NOT NULL DEFAULT 0,
    completed_items INTEGER NOT NULL DEFAULT 0,
    recovery_note TEXT DEFAULT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS batch_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    batch_id TEXT NOT NULL REFERENCES batches(id),
    sequence INTEGER NOT NULL,
    directive TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','running','completed','failed','skipped')),
    plan_id TEXT DEFAULT NULL REFERENCES plans(id),
    phase TEXT DEFAULT NULL,
    phase_depends_on TEXT DEFAULT NULL,
    error TEXT DEFAULT NULL,
    started_at DATETIME DEFAULT NULL,
    completed_at DATETIME DEFAULT NULL,
    UNIQUE(batch_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_batches_project_id ON batches(project_id);
CREATE INDEX IF NOT EXISTS idx_batches_status ON batches(status);
CREATE INDEX IF NOT EXISTS idx_batch_items_batch_id ON batch_items(batch_id);
CREATE INDEX IF NOT EXISTS idx_batch_items_status ON batch_items(status);
`

func Migrations() []string {
	return []string{SchemaSQL, migrationAddRejectedCancelled, migrationAddPlansTable, migrationAddBatchTables}
}

func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("nil database")
	}

	for idx, migration := range Migrations() {
		if strings.TrimSpace(migration) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("run migration %d: %w", idx+1, err)
		}
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}

	return nil
}
