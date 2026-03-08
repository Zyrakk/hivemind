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

func Migrations() []string {
	return []string{SchemaSQL, migrationAddRejectedCancelled}
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
