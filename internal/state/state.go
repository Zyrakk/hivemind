package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotImplemented = errors.New("not implemented")
	ErrNotFound       = errors.New("not found")
	ErrInvalidInput   = errors.New("invalid input")
	ErrInvalidStatus  = errors.New("invalid status")
)

const (
	ProjectStatusWorking       = "working"
	ProjectStatusNeedsInput    = "needs_input"
	ProjectStatusPendingReview = "pending_review"
	ProjectStatusBlocked       = "blocked"
	ProjectStatusPaused        = "paused"
)

const (
	WorkerStatusRunning   = "running"
	WorkerStatusPaused    = "paused"
	WorkerStatusCompleted = "completed"
	WorkerStatusFailed    = "failed"
	WorkerStatusBlocked   = "blocked"
	WorkerStatusCancelled = "cancelled"
)

const (
	TaskStatusPending    = "pending"
	TaskStatusInProgress = "in_progress"
	TaskStatusCompleted  = "completed"
	TaskStatusFailed     = "failed"
	TaskStatusBlocked    = "blocked"
	TaskStatusRejected   = "rejected"
)

const (
	PlanStatusPending   = "pending"
	PlanStatusApproved  = "approved"
	PlanStatusExecuting = "executing"
	PlanStatusCompleted = "completed"
	PlanStatusFailed    = "failed"
	PlanStatusCancelled = "cancelled"
)

const (
	BatchStatusPending   = "pending"
	BatchStatusRunning   = "running"
	BatchStatusCompleted = "completed"
	BatchStatusFailed    = "failed"
	BatchStatusPaused    = "paused"
)

const (
	BatchItemStatusPending   = "pending"
	BatchItemStatusRunning   = "running"
	BatchItemStatusCompleted = "completed"
	BatchItemStatusFailed    = "failed"
	BatchItemStatusSkipped   = "skipped"
)

type Project struct {
	ID           int64
	Name         string
	Description  string
	Status       string
	RepoURL      string
	AgentsMDPath string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Worker struct {
	ID              int64
	ProjectID       int64
	SessionID       string
	TaskDescription string
	Branch          string
	Status          string
	StartedAt       time.Time
	FinishedAt      *time.Time
	ErrorMessage    string
}

type Task struct {
	ID               int64
	ProjectID        int64
	Title            string
	Description      string
	Status           string
	Priority         int
	DependsOn        string
	AssignedWorkerID *int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Event struct {
	ID          int64
	ProjectID   int64
	WorkerID    *int64
	EventType   string
	Description string
	Timestamp   time.Time
}

type Plan struct {
	ID        string
	ProjectID int64
	Directive string
	Status    string
	Engine    string
	Summary   string
	PlanData  []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Batch struct {
	ID             string
	ProjectID      int64
	Name           string
	Status         string
	TotalItems     int
	CompletedItems int
	RecoveryNote   *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type BatchItem struct {
	ID             int64
	BatchID        string
	Sequence       int
	Directive      string
	Status         string
	PlanID         *string
	Phase          *string
	PhaseDependsOn *string
	Error          *string
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

type ProjectSummary struct {
	ID            string
	InternalID    int64
	Name          string
	Status        string
	RepoURL       string
	ActiveWorkers int
	PendingTasks  int
	LastActivity  *time.Time
}

type ActiveWorker struct {
	ID              int64
	ProjectID       int64
	ProjectRef      string
	ProjectName     string
	SessionID       string
	TaskDescription string
	Branch          string
	Status          string
	StartedAt       time.Time
	FinishedAt      *time.Time
	ErrorMessage    string
}

type Counters struct {
	ActiveWorkers int
	PendingTasks  int
	PendingReview int
}

type GlobalState struct {
	Projects      []ProjectSummary
	ActiveWorkers []ActiveWorker
	Counters      Counters
}

type WorkstreamProgress struct {
	Name     string
	Progress float64
}

type Progress struct {
	Overall     float64
	Workstreams []WorkstreamProgress
}

type ProjectDetail struct {
	Project    Project
	ProjectRef string
	Tasks      []Task
	Workers    []Worker
	Events     []Event
	Progress   Progress
}

type TaskUpdate struct {
	Title               *string
	Description         *string
	Status              *string
	Priority            *int
	DependsOn           *string
	AssignedWorkerID    *int64
	AssignedWorkerIDSet bool
}

type WorkerUpdate struct {
	Status          *string
	ErrorMessage    *string
	ErrorMessageSet bool
}

type Store struct {
	db   *sql.DB
	path string
}

func New(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("empty database path: %w", ErrInvalidInput)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	store := &Store{db: db, path: path}
	if err := store.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	return ApplyMigrations(ctx, s.db)
}

func (s *Store) CreateProject(ctx context.Context, project Project) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(project.Name) == "" {
		return 0, fmt.Errorf("project name is required: %w", ErrInvalidInput)
	}

	status := strings.TrimSpace(project.Status)
	if status == "" {
		status = ProjectStatusWorking
	}
	if !isValidProjectStatus(status) {
		return 0, fmt.Errorf("project status %q: %w", status, ErrInvalidStatus)
	}

	now := nowUTC()
	res, err := s.db.ExecContext(
		ctx,
		`INSERT INTO projects (name, description, status, repo_url, agents_md_path, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		project.Name,
		emptyOrValue(project.Description),
		status,
		emptyOrValue(project.RepoURL),
		emptyOrValue(project.AgentsMDPath),
		formatTime(now),
		formatTime(now),
	)
	if err != nil {
		return 0, fmt.Errorf("insert project: %w", err)
	}

	projectID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get project id: %w", err)
	}

	return projectID, nil
}

func (s *Store) UpdateProjectStatus(ctx context.Context, projectID int64, status string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if projectID <= 0 {
		return fmt.Errorf("invalid project id: %w", ErrInvalidInput)
	}
	if !isValidProjectStatus(status) {
		return fmt.Errorf("project status %q: %w", status, ErrInvalidStatus)
	}

	res, err := s.db.ExecContext(
		ctx,
		`UPDATE projects SET status = ?, updated_at = ? WHERE id = ?`,
		status,
		formatTime(nowUTC()),
		projectID,
	)
	if err != nil {
		return fmt.Errorf("update project status: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (project status): %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Store) UpdateProjectStatusByReference(ctx context.Context, projectRef, status string) error {
	projectID, err := s.ResolveProjectID(ctx, projectRef)
	if err != nil {
		return err
	}

	return s.UpdateProjectStatus(ctx, projectID, status)
}

func (s *Store) CreateWorker(ctx context.Context, worker Worker) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if worker.ProjectID <= 0 {
		return 0, fmt.Errorf("project_id is required: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(worker.SessionID) == "" {
		return 0, fmt.Errorf("session_id is required: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(worker.TaskDescription) == "" {
		return 0, fmt.Errorf("task_description is required: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(worker.Branch) == "" {
		return 0, fmt.Errorf("branch is required: %w", ErrInvalidInput)
	}

	status := strings.TrimSpace(worker.Status)
	if status == "" {
		status = WorkerStatusRunning
	}
	if !isValidWorkerStatus(status) {
		return 0, fmt.Errorf("worker status %q: %w", status, ErrInvalidStatus)
	}

	startedAt := worker.StartedAt
	if startedAt.IsZero() {
		startedAt = nowUTC()
	}

	var finishedAt any
	if worker.FinishedAt != nil && !worker.FinishedAt.IsZero() {
		finishedAt = formatTime(*worker.FinishedAt)
	}

	res, err := s.db.ExecContext(
		ctx,
		`INSERT INTO workers (project_id, session_id, task_description, branch, status, started_at, finished_at, error_message)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		worker.ProjectID,
		worker.SessionID,
		worker.TaskDescription,
		worker.Branch,
		status,
		formatTime(startedAt),
		finishedAt,
		emptyOrValue(worker.ErrorMessage),
	)
	if err != nil {
		return 0, fmt.Errorf("insert worker: %w", err)
	}

	workerID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get worker id: %w", err)
	}

	return workerID, nil
}

func (s *Store) CreateTask(ctx context.Context, task Task) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if task.ProjectID <= 0 {
		return 0, fmt.Errorf("project_id is required: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(task.Title) == "" {
		return 0, fmt.Errorf("title is required: %w", ErrInvalidInput)
	}

	status := strings.TrimSpace(task.Status)
	if status == "" {
		status = TaskStatusPending
	}
	if !isValidTaskStatus(status) {
		return 0, fmt.Errorf("task status %q: %w", status, ErrInvalidStatus)
	}

	priority := task.Priority
	if priority == 0 {
		priority = 3
	}
	if priority < 1 || priority > 5 {
		return 0, fmt.Errorf("priority must be between 1 and 5: %w", ErrInvalidInput)
	}

	dependsOn := strings.TrimSpace(task.DependsOn)
	if dependsOn == "" {
		dependsOn = "[]"
	}
	var dependsOnParsed []string
	if err := json.Unmarshal([]byte(dependsOn), &dependsOnParsed); err != nil {
		return 0, fmt.Errorf("depends_on must be a JSON array string: %w", ErrInvalidInput)
	}

	now := nowUTC()
	res, err := s.db.ExecContext(
		ctx,
		`INSERT INTO tasks (project_id, title, description, status, priority, depends_on, assigned_worker_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ProjectID,
		task.Title,
		emptyOrValue(task.Description),
		status,
		priority,
		dependsOn,
		task.AssignedWorkerID,
		formatTime(now),
		formatTime(now),
	)
	if err != nil {
		return 0, fmt.Errorf("insert task: %w", err)
	}

	taskID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get task id: %w", err)
	}

	return taskID, nil
}

func (s *Store) AppendEvent(ctx context.Context, event Event) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if event.ProjectID <= 0 {
		return fmt.Errorf("project_id is required: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(event.EventType) == "" {
		return fmt.Errorf("event_type is required: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(event.Description) == "" {
		return fmt.Errorf("description is required: %w", ErrInvalidInput)
	}

	timestamp := event.Timestamp
	if timestamp.IsZero() {
		timestamp = nowUTC()
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO events (project_id, worker_id, event_type, description, timestamp)
		 VALUES (?, ?, ?, ?, ?)`,
		event.ProjectID,
		event.WorkerID,
		event.EventType,
		event.Description,
		formatTime(timestamp),
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	return nil
}

func (s *Store) ResolveProjectID(ctx context.Context, projectRef string) (int64, error) {
	project, err := s.GetProjectByReference(ctx, projectRef)
	if err != nil {
		return 0, err
	}

	return project.ID, nil
}

func (s *Store) GetProjectByReference(ctx context.Context, projectRef string) (Project, error) {
	projectRef = strings.TrimSpace(projectRef)
	if projectRef == "" {
		return Project{}, fmt.Errorf("project reference is required: %w", ErrInvalidInput)
	}

	if numericID, err := strconv.ParseInt(projectRef, 10, 64); err == nil && numericID > 0 {
		project, getErr := s.GetProjectByID(ctx, numericID)
		if getErr == nil {
			return project, nil
		}
		if !errors.Is(getErr, ErrNotFound) {
			return Project{}, getErr
		}
	}

	project, err := s.getProjectByName(ctx, projectRef)
	if err == nil {
		return project, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Project{}, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, name, description, status, repo_url, agents_md_path, created_at, updated_at
		 FROM projects`,
	)
	if err != nil {
		return Project{}, fmt.Errorf("list projects for project lookup: %w", err)
	}
	defer rows.Close()

	normalized := normalizeProjectRef(projectRef)
	for rows.Next() {
		project, scanErr := scanProject(rows)
		if scanErr != nil {
			return Project{}, scanErr
		}

		if normalizeProjectRef(projectExternalID(project.Name, project.ID)) == normalized {
			return project, nil
		}
	}
	if err := rows.Err(); err != nil {
		return Project{}, fmt.Errorf("iterate projects for project lookup: %w", err)
	}

	return Project{}, ErrNotFound
}

func (s *Store) GetProjectByID(ctx context.Context, projectID int64) (Project, error) {
	if projectID <= 0 {
		return Project{}, fmt.Errorf("invalid project id: %w", ErrInvalidInput)
	}

	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, name, description, status, repo_url, agents_md_path, created_at, updated_at
		 FROM projects WHERE id = ?`,
		projectID,
	)

	project, err := scanProject(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, err
	}

	return project, nil
}

func (s *Store) ListProjectSummaries(ctx context.Context) ([]ProjectSummary, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
		    p.id,
		    p.name,
		    p.status,
		    p.repo_url,
		    (SELECT COUNT(1) FROM workers w WHERE w.project_id = p.id AND w.status = ?) AS active_workers,
		    (SELECT COUNT(1) FROM tasks t WHERE t.project_id = p.id AND t.status = ?) AS pending_tasks,
		    COALESCE((SELECT MAX(e.timestamp) FROM events e WHERE e.project_id = p.id), p.updated_at) AS last_activity
		 FROM projects p
		 ORDER BY p.id ASC`,
		WorkerStatusRunning,
		TaskStatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("list project summaries: %w", err)
	}
	defer rows.Close()

	summaries := make([]ProjectSummary, 0)
	for rows.Next() {
		var (
			summary         ProjectSummary
			lastActivityRaw any
		)

		if scanErr := rows.Scan(
			&summary.InternalID,
			&summary.Name,
			&summary.Status,
			&summary.RepoURL,
			&summary.ActiveWorkers,
			&summary.PendingTasks,
			&lastActivityRaw,
		); scanErr != nil {
			return nil, fmt.Errorf("scan project summary: %w", scanErr)
		}

		timestamp, parseErr := parseTimeValue(lastActivityRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		summary.LastActivity = &timestamp
		summary.ID = projectExternalID(summary.Name, summary.InternalID)
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project summaries: %w", err)
	}

	return summaries, nil
}

func (s *Store) ListActiveWorkers(ctx context.Context) ([]ActiveWorker, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
		    w.id,
		    w.project_id,
		    p.name,
		    w.session_id,
		    w.task_description,
		    w.branch,
		    w.status,
		    w.started_at,
		    w.finished_at,
		    w.error_message
		 FROM workers w
		 JOIN projects p ON p.id = w.project_id
		 WHERE w.status = ?
		 ORDER BY w.started_at DESC, w.id DESC`,
		WorkerStatusRunning,
	)
	if err != nil {
		return nil, fmt.Errorf("list active workers: %w", err)
	}
	defer rows.Close()

	workers := make([]ActiveWorker, 0)
	for rows.Next() {
		var (
			worker      ActiveWorker
			startedRaw  any
			finishedRaw any
		)

		if scanErr := rows.Scan(
			&worker.ID,
			&worker.ProjectID,
			&worker.ProjectName,
			&worker.SessionID,
			&worker.TaskDescription,
			&worker.Branch,
			&worker.Status,
			&startedRaw,
			&finishedRaw,
			&worker.ErrorMessage,
		); scanErr != nil {
			return nil, fmt.Errorf("scan active worker: %w", scanErr)
		}

		startedAt, parseErr := parseTimeValue(startedRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		finishedAt, parseErr := parseOptionalTimeValue(finishedRaw)
		if parseErr != nil {
			return nil, parseErr
		}

		worker.StartedAt = startedAt
		worker.FinishedAt = finishedAt
		worker.ProjectRef = projectExternalID(worker.ProjectName, worker.ProjectID)
		workers = append(workers, worker)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active workers: %w", err)
	}

	return workers, nil
}

func (s *Store) GetGlobalCounters(ctx context.Context) (Counters, error) {
	var counters Counters

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(1) FROM workers WHERE status = ?`,
		WorkerStatusRunning,
	).Scan(&counters.ActiveWorkers); err != nil {
		return Counters{}, fmt.Errorf("count active workers: %w", err)
	}

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(1) FROM tasks WHERE status = ?`,
		TaskStatusPending,
	).Scan(&counters.PendingTasks); err != nil {
		return Counters{}, fmt.Errorf("count pending tasks: %w", err)
	}

	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(1) FROM projects WHERE status = ?`,
		ProjectStatusPendingReview,
	).Scan(&counters.PendingReview); err != nil {
		return Counters{}, fmt.Errorf("count pending review projects: %w", err)
	}

	return counters, nil
}

func (s *Store) GetGlobalState(ctx context.Context) (GlobalState, error) {
	projects, err := s.ListProjectSummaries(ctx)
	if err != nil {
		return GlobalState{}, err
	}

	activeWorkers, err := s.ListActiveWorkers(ctx)
	if err != nil {
		return GlobalState{}, err
	}

	counters, err := s.GetGlobalCounters(ctx)
	if err != nil {
		return GlobalState{}, err
	}

	return GlobalState{
		Projects:      projects,
		ActiveWorkers: activeWorkers,
		Counters:      counters,
	}, nil
}

func (s *Store) GetProjectDetail(ctx context.Context, projectRef string) (ProjectDetail, error) {
	project, err := s.GetProjectByReference(ctx, projectRef)
	if err != nil {
		return ProjectDetail{}, err
	}

	tasks, err := s.listTasksByProjectID(ctx, project.ID)
	if err != nil {
		return ProjectDetail{}, err
	}

	workers, err := s.listWorkersByProjectID(ctx, project.ID)
	if err != nil {
		return ProjectDetail{}, err
	}

	events, err := s.listEventsByProjectID(ctx, project.ID, 20)
	if err != nil {
		return ProjectDetail{}, err
	}

	return ProjectDetail{
		Project:    project,
		ProjectRef: projectExternalID(project.Name, project.ID),
		Tasks:      tasks,
		Workers:    workers,
		Events:     events,
		Progress:   buildProgress(tasks),
	}, nil
}

func (s *Store) UpdateTask(ctx context.Context, taskID int64, update TaskUpdate) error {
	if taskID <= 0 {
		return fmt.Errorf("invalid task id: %w", ErrInvalidInput)
	}

	if update.Status != nil && !isValidTaskStatus(*update.Status) {
		return fmt.Errorf("task status %q: %w", *update.Status, ErrInvalidStatus)
	}
	if update.Priority != nil && (*update.Priority < 1 || *update.Priority > 5) {
		return fmt.Errorf("priority must be between 1 and 5: %w", ErrInvalidInput)
	}
	if update.DependsOn != nil {
		var dependsOnParsed []string
		if err := json.Unmarshal([]byte(*update.DependsOn), &dependsOnParsed); err != nil {
			return fmt.Errorf("depends_on must be a JSON array string: %w", ErrInvalidInput)
		}
	}

	setParts := make([]string, 0, 8)
	args := make([]any, 0, 8)

	if update.Title != nil {
		setParts = append(setParts, "title = ?")
		args = append(args, strings.TrimSpace(*update.Title))
	}
	if update.Description != nil {
		setParts = append(setParts, "description = ?")
		args = append(args, *update.Description)
	}
	if update.Status != nil {
		setParts = append(setParts, "status = ?")
		args = append(args, *update.Status)
	}
	if update.Priority != nil {
		setParts = append(setParts, "priority = ?")
		args = append(args, *update.Priority)
	}
	if update.DependsOn != nil {
		setParts = append(setParts, "depends_on = ?")
		args = append(args, *update.DependsOn)
	}
	if update.AssignedWorkerIDSet {
		if update.AssignedWorkerID == nil {
			setParts = append(setParts, "assigned_worker_id = NULL")
		} else {
			setParts = append(setParts, "assigned_worker_id = ?")
			args = append(args, *update.AssignedWorkerID)
		}
	}

	if len(setParts) == 0 {
		return fmt.Errorf("no fields to update: %w", ErrInvalidInput)
	}

	setParts = append(setParts, "updated_at = ?")
	args = append(args, formatTime(nowUTC()), taskID)

	query := "UPDATE tasks SET " + strings.Join(setParts, ", ") + " WHERE id = ?"
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (task update): %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Store) UpdateWorker(ctx context.Context, workerID int64, update WorkerUpdate) error {
	if workerID <= 0 {
		return fmt.Errorf("invalid worker id: %w", ErrInvalidInput)
	}
	if update.Status != nil && !isValidWorkerStatus(*update.Status) {
		return fmt.Errorf("worker status %q: %w", *update.Status, ErrInvalidStatus)
	}
	if update.Status == nil && !update.ErrorMessageSet {
		return fmt.Errorf("no fields to update: %w", ErrInvalidInput)
	}

	setParts := make([]string, 0, 6)
	args := make([]any, 0, 6)

	if update.Status != nil {
		setParts = append(setParts, "status = ?")
		args = append(args, *update.Status)

		switch *update.Status {
		case WorkerStatusCompleted, WorkerStatusFailed, WorkerStatusBlocked:
			setParts = append(setParts, "finished_at = ?")
			args = append(args, formatTime(nowUTC()))
		case WorkerStatusRunning:
			setParts = append(setParts, "finished_at = NULL")
		}
	}

	if update.ErrorMessageSet {
		if update.ErrorMessage == nil {
			setParts = append(setParts, "error_message = ''")
		} else {
			setParts = append(setParts, "error_message = ?")
			args = append(args, *update.ErrorMessage)
		}
	}

	query := "UPDATE workers SET " + strings.Join(setParts, ", ") + " WHERE id = ?"
	args = append(args, workerID)

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update worker: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (worker update): %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Store) CreatePlan(ctx context.Context, projectID int64, planID, directive, engine string, planData []byte) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(planID) == "" {
		return fmt.Errorf("plan ID is required: %w", ErrInvalidInput)
	}
	if projectID <= 0 {
		return fmt.Errorf("project_id is required: %w", ErrInvalidInput)
	}

	now := formatTime(nowUTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plans (id, project_id, directive, status, engine, plan_data, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		planID, projectID, directive, PlanStatusPending, engine, string(planData), now, now,
	)
	if err != nil {
		return fmt.Errorf("insert plan: %w", err)
	}
	return nil
}

func (s *Store) GetPlan(ctx context.Context, planID string) (*Plan, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	var (
		plan       Plan
		createdRaw any
		updatedRaw any
		planData   string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, directive, status, engine, summary, plan_data, created_at, updated_at
		 FROM plans WHERE id = ?`, planID,
	).Scan(&plan.ID, &plan.ProjectID, &plan.Directive, &plan.Status, &plan.Engine,
		&plan.Summary, &planData, &createdRaw, &updatedRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("plan %q: %w", planID, ErrNotFound)
		}
		return nil, fmt.Errorf("get plan: %w", err)
	}

	plan.PlanData = []byte(planData)

	createdAt, parseErr := parseTimeValue(createdRaw)
	if parseErr != nil {
		return nil, parseErr
	}
	updatedAt, parseErr := parseTimeValue(updatedRaw)
	if parseErr != nil {
		return nil, parseErr
	}
	plan.CreatedAt = createdAt
	plan.UpdatedAt = updatedAt

	return &plan, nil
}

func (s *Store) UpdatePlanStatus(ctx context.Context, planID, status string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if !isValidPlanStatus(status) {
		return fmt.Errorf("plan status %q: %w", status, ErrInvalidStatus)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE plans SET status = ?, updated_at = ? WHERE id = ?`,
		status, formatTime(nowUTC()), planID,
	)
	if err != nil {
		return fmt.Errorf("update plan status: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (plan status): %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdatePlanData(ctx context.Context, planID string, planData []byte) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE plans SET plan_data = ?, updated_at = ? WHERE id = ?`,
		string(planData), formatTime(nowUTC()), planID,
	)
	if err != nil {
		return fmt.Errorf("update plan data: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (plan data): %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetActivePlans(ctx context.Context) ([]Plan, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, directive, status, engine, summary, plan_data, created_at, updated_at
		 FROM plans WHERE status IN (?, ?)
		 ORDER BY created_at ASC`,
		PlanStatusApproved, PlanStatusExecuting,
	)
	if err != nil {
		return nil, fmt.Errorf("list active plans: %w", err)
	}
	defer rows.Close()

	plans := make([]Plan, 0)
	for rows.Next() {
		var (
			plan       Plan
			planData   string
			createdRaw any
			updatedRaw any
		)
		if scanErr := rows.Scan(&plan.ID, &plan.ProjectID, &plan.Directive,
			&plan.Status, &plan.Engine, &plan.Summary, &planData,
			&createdRaw, &updatedRaw); scanErr != nil {
			return nil, fmt.Errorf("scan plan: %w", scanErr)
		}
		plan.PlanData = []byte(planData)
		createdAt, parseErr := parseTimeValue(createdRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		updatedAt, parseErr := parseTimeValue(updatedRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		plan.CreatedAt = createdAt
		plan.UpdatedAt = updatedAt
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active plans: %w", err)
	}
	return plans, nil
}

func isValidPlanStatus(status string) bool {
	switch status {
	case PlanStatusPending, PlanStatusApproved, PlanStatusExecuting,
		PlanStatusCompleted, PlanStatusFailed, PlanStatusCancelled:
		return true
	default:
		return false
	}
}

func isValidBatchStatus(status string) bool {
	switch status {
	case BatchStatusPending, BatchStatusRunning, BatchStatusCompleted,
		BatchStatusFailed, BatchStatusPaused:
		return true
	default:
		return false
	}
}

func isValidBatchItemStatus(status string) bool {
	switch status {
	case BatchItemStatusPending, BatchItemStatusRunning, BatchItemStatusCompleted,
		BatchItemStatusFailed, BatchItemStatusSkipped:
		return true
	default:
		return false
	}
}

func generateBatchID() string {
	return fmt.Sprintf("batch-%d", time.Now().UnixNano())
}

func (s *Store) CreateBatch(ctx context.Context, projectID int64, name string, directives []string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if projectID <= 0 {
		return "", fmt.Errorf("project_id is required: %w", ErrInvalidInput)
	}
	if len(directives) == 0 {
		return "", fmt.Errorf("at least one directive is required: %w", ErrInvalidInput)
	}

	batchID := generateBatchID()
	now := formatTime(nowUTC())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO batches (id, project_id, name, status, total_items, completed_items, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?)`,
		batchID, projectID, name, BatchStatusPending, len(directives), now, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert batch: %w", err)
	}

	for i, directive := range directives {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO batch_items (batch_id, sequence, directive, status)
			 VALUES (?, ?, ?, ?)`,
			batchID, i+1, directive, BatchItemStatusPending,
		)
		if err != nil {
			return "", fmt.Errorf("insert batch item %d: %w", i+1, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit batch: %w", err)
	}

	return batchID, nil
}

func (s *Store) GetBatch(ctx context.Context, batchID string) (*Batch, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	var (
		batch        Batch
		recoveryNote sql.NullString
		createdRaw   any
		updatedRaw   any
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, status, total_items, completed_items, recovery_note, created_at, updated_at
		 FROM batches WHERE id = ?`, batchID,
	).Scan(&batch.ID, &batch.ProjectID, &batch.Name, &batch.Status,
		&batch.TotalItems, &batch.CompletedItems, &recoveryNote,
		&createdRaw, &updatedRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("batch %q: %w", batchID, ErrNotFound)
		}
		return nil, fmt.Errorf("get batch: %w", err)
	}

	if recoveryNote.Valid {
		batch.RecoveryNote = &recoveryNote.String
	}

	createdAt, parseErr := parseTimeValue(createdRaw)
	if parseErr != nil {
		return nil, parseErr
	}
	updatedAt, parseErr := parseTimeValue(updatedRaw)
	if parseErr != nil {
		return nil, parseErr
	}
	batch.CreatedAt = createdAt
	batch.UpdatedAt = updatedAt

	return &batch, nil
}

func (s *Store) GetBatchItems(ctx context.Context, batchID string) ([]BatchItem, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, batch_id, sequence, directive, status, plan_id, phase, phase_depends_on, error, started_at, completed_at
		 FROM batch_items WHERE batch_id = ?
		 ORDER BY sequence ASC`, batchID,
	)
	if err != nil {
		return nil, fmt.Errorf("query batch items: %w", err)
	}
	defer rows.Close()

	items := make([]BatchItem, 0)
	for rows.Next() {
		var (
			item           BatchItem
			planID         sql.NullString
			phase          sql.NullString
			phaseDependsOn sql.NullString
			errMsg         sql.NullString
			startedRaw     any
			completedRaw   any
		)
		if scanErr := rows.Scan(&item.ID, &item.BatchID, &item.Sequence, &item.Directive,
			&item.Status, &planID, &phase, &phaseDependsOn, &errMsg,
			&startedRaw, &completedRaw); scanErr != nil {
			return nil, fmt.Errorf("scan batch item: %w", scanErr)
		}
		if planID.Valid {
			item.PlanID = &planID.String
		}
		if phase.Valid {
			item.Phase = &phase.String
		}
		if phaseDependsOn.Valid {
			item.PhaseDependsOn = &phaseDependsOn.String
		}
		if errMsg.Valid {
			item.Error = &errMsg.String
		}
		startedAt, parseErr := parseOptionalTimeValue(startedRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		item.StartedAt = startedAt
		completedAt, parseErr := parseOptionalTimeValue(completedRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		item.CompletedAt = completedAt

		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate batch items: %w", err)
	}

	return items, nil
}

func (s *Store) UpdateBatchStatus(ctx context.Context, batchID, status string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if !isValidBatchStatus(status) {
		return fmt.Errorf("batch status %q: %w", status, ErrInvalidStatus)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE batches SET status = ?, updated_at = ? WHERE id = ?`,
		status, formatTime(nowUTC()), batchID,
	)
	if err != nil {
		return fmt.Errorf("update batch status: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (batch status): %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateBatchItemStatus(ctx context.Context, itemID int64, status, planID, errorMsg string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}
	if !isValidBatchItemStatus(status) {
		return fmt.Errorf("batch item status %q: %w", status, ErrInvalidStatus)
	}

	now := formatTime(nowUTC())

	var planIDVal, errorVal any
	if strings.TrimSpace(planID) != "" {
		planIDVal = planID
	}
	if strings.TrimSpace(errorMsg) != "" {
		errorVal = errorMsg
	}

	// Set started_at when transitioning to running; completed_at for terminal states.
	var startedAt, completedAt any
	if status == BatchItemStatusRunning {
		startedAt = now
	}
	if status == BatchItemStatusCompleted || status == BatchItemStatusFailed || status == BatchItemStatusSkipped {
		completedAt = now
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE batch_items SET status = ?,
			plan_id = COALESCE(?, plan_id),
			error = COALESCE(?, error),
			started_at = COALESCE(?, started_at),
			completed_at = COALESCE(?, completed_at)
		 WHERE id = ?`,
		status, planIDVal, errorVal, startedAt, completedAt, itemID,
	)
	if err != nil {
		return fmt.Errorf("update batch item status: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (batch item status): %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetNextPendingBatchItem(ctx context.Context, batchID string) (*BatchItem, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	var (
		item           BatchItem
		planID         sql.NullString
		phase          sql.NullString
		phaseDependsOn sql.NullString
		errMsg         sql.NullString
		startedRaw     any
		completedRaw   any
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, batch_id, sequence, directive, status, plan_id, phase, phase_depends_on, error, started_at, completed_at
		 FROM batch_items
		 WHERE batch_id = ? AND status = ?
		 ORDER BY sequence ASC
		 LIMIT 1`, batchID, BatchItemStatusPending,
	).Scan(&item.ID, &item.BatchID, &item.Sequence, &item.Directive,
		&item.Status, &planID, &phase, &phaseDependsOn, &errMsg,
		&startedRaw, &completedRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // No pending items — batch is done.
		}
		return nil, fmt.Errorf("get next pending batch item: %w", err)
	}

	if planID.Valid {
		item.PlanID = &planID.String
	}
	if phase.Valid {
		item.Phase = &phase.String
	}
	if phaseDependsOn.Valid {
		item.PhaseDependsOn = &phaseDependsOn.String
	}
	if errMsg.Valid {
		item.Error = &errMsg.String
	}
	startedAt, parseErr := parseOptionalTimeValue(startedRaw)
	if parseErr != nil {
		return nil, parseErr
	}
	item.StartedAt = startedAt
	completedAt, parseErr := parseOptionalTimeValue(completedRaw)
	if parseErr != nil {
		return nil, parseErr
	}
	item.CompletedAt = completedAt

	return &item, nil
}

func (s *Store) IncrementBatchProgress(ctx context.Context, batchID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE batches SET completed_items = completed_items + 1, updated_at = ? WHERE id = ?`,
		formatTime(nowUTC()), batchID,
	)
	if err != nil {
		return fmt.Errorf("increment batch progress: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (batch progress): %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetRunningBatches(ctx context.Context) ([]Batch, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, status, total_items, completed_items, recovery_note, created_at, updated_at
		 FROM batches WHERE status = ?
		 ORDER BY created_at ASC`, BatchStatusRunning,
	)
	if err != nil {
		return nil, fmt.Errorf("query running batches: %w", err)
	}
	defer rows.Close()

	batches := make([]Batch, 0)
	for rows.Next() {
		var (
			batch        Batch
			recoveryNote sql.NullString
			createdRaw   any
			updatedRaw   any
		)
		if scanErr := rows.Scan(&batch.ID, &batch.ProjectID, &batch.Name, &batch.Status,
			&batch.TotalItems, &batch.CompletedItems, &recoveryNote,
			&createdRaw, &updatedRaw); scanErr != nil {
			return nil, fmt.Errorf("scan batch: %w", scanErr)
		}
		if recoveryNote.Valid {
			batch.RecoveryNote = &recoveryNote.String
		}
		createdAt, parseErr := parseTimeValue(createdRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		updatedAt, parseErr := parseTimeValue(updatedRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		batch.CreatedAt = createdAt
		batch.UpdatedAt = updatedAt
		batches = append(batches, batch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate running batches: %w", err)
	}

	return batches, nil
}

// RecoverFromRestart detects zombie states left by a process crash and
// transitions them to safe states. Returns the count of recovered items.
func (s *Store) RecoverFromRestart(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("state store is not initialized: %w", ErrInvalidInput)
	}

	recovered := 0
	now := formatTime(nowUTC())

	// 1. Plans stuck in "executing" → "failed".
	res, err := s.db.ExecContext(ctx,
		`UPDATE plans SET status = ?, updated_at = ? WHERE status = ?`,
		PlanStatusFailed, now, PlanStatusExecuting,
	)
	if err != nil {
		return 0, fmt.Errorf("recover executing plans: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		recovered += int(n)
	}

	// 2. Workers stuck in "running" → "failed".
	res, err = s.db.ExecContext(ctx,
		`UPDATE workers SET status = ?, finished_at = ?, error_message = ? WHERE status = ?`,
		WorkerStatusFailed, now, "process restarted", WorkerStatusRunning,
	)
	if err != nil {
		return 0, fmt.Errorf("recover running workers: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		recovered += int(n)
	}

	// 3. Batch items stuck in "running" → "failed".
	res, err = s.db.ExecContext(ctx,
		`UPDATE batch_items SET status = ?, error = ?, completed_at = ? WHERE status = ?`,
		BatchItemStatusFailed, "process restarted", now, BatchItemStatusRunning,
	)
	if err != nil {
		return 0, fmt.Errorf("recover running batch items: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		recovered += int(n)
	}

	// 4. Batches stuck in "running" → "paused".
	res, err = s.db.ExecContext(ctx,
		`UPDATE batches SET status = ?, recovery_note = ?, updated_at = ? WHERE status = ?`,
		BatchStatusPaused, "process restarted", now, BatchStatusRunning,
	)
	if err != nil {
		return 0, fmt.Errorf("recover running batches: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		recovered += int(n)
	}

	return recovered, nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) getProjectByName(ctx context.Context, projectName string) (Project, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, name, description, status, repo_url, agents_md_path, created_at, updated_at
		 FROM projects
		 WHERE lower(name) = lower(?)
		 ORDER BY id ASC
		 LIMIT 1`,
		projectName,
	)

	project, err := scanProject(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, err
	}

	return project, nil
}

func (s *Store) listTasksByProjectID(ctx context.Context, projectID int64) ([]Task, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, project_id, title, description, status, priority, depends_on, assigned_worker_id, created_at, updated_at
		 FROM tasks
		 WHERE project_id = ?
		 ORDER BY created_at ASC, id ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks by project: %w", err)
	}
	defer rows.Close()

	tasks := make([]Task, 0)
	for rows.Next() {
		var (
			task       Task
			assignedID sql.NullInt64
			createdRaw any
			updatedRaw any
		)

		if scanErr := rows.Scan(
			&task.ID,
			&task.ProjectID,
			&task.Title,
			&task.Description,
			&task.Status,
			&task.Priority,
			&task.DependsOn,
			&assignedID,
			&createdRaw,
			&updatedRaw,
		); scanErr != nil {
			return nil, fmt.Errorf("scan task: %w", scanErr)
		}

		if assignedID.Valid {
			assigned := assignedID.Int64
			task.AssignedWorkerID = &assigned
		}

		createdAt, parseErr := parseTimeValue(createdRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		updatedAt, parseErr := parseTimeValue(updatedRaw)
		if parseErr != nil {
			return nil, parseErr
		}

		task.CreatedAt = createdAt
		task.UpdatedAt = updatedAt
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks by project: %w", err)
	}

	return tasks, nil
}

func (s *Store) listWorkersByProjectID(ctx context.Context, projectID int64) ([]Worker, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, project_id, session_id, task_description, branch, status, started_at, finished_at, error_message
		 FROM workers
		 WHERE project_id = ?
		 ORDER BY started_at DESC, id DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list workers by project: %w", err)
	}
	defer rows.Close()

	workers := make([]Worker, 0)
	for rows.Next() {
		var (
			worker      Worker
			startedRaw  any
			finishedRaw any
		)

		if scanErr := rows.Scan(
			&worker.ID,
			&worker.ProjectID,
			&worker.SessionID,
			&worker.TaskDescription,
			&worker.Branch,
			&worker.Status,
			&startedRaw,
			&finishedRaw,
			&worker.ErrorMessage,
		); scanErr != nil {
			return nil, fmt.Errorf("scan worker: %w", scanErr)
		}

		startedAt, parseErr := parseTimeValue(startedRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		finishedAt, parseErr := parseOptionalTimeValue(finishedRaw)
		if parseErr != nil {
			return nil, parseErr
		}

		worker.StartedAt = startedAt
		worker.FinishedAt = finishedAt
		workers = append(workers, worker)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workers by project: %w", err)
	}

	return workers, nil
}

func (s *Store) listEventsByProjectID(ctx context.Context, projectID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, project_id, worker_id, event_type, description, timestamp
		 FROM events
		 WHERE project_id = ?
		 ORDER BY timestamp DESC, id DESC
		 LIMIT ?`,
		projectID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list events by project: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var (
			event        Event
			workerID     sql.NullInt64
			timestampRaw any
		)

		if scanErr := rows.Scan(
			&event.ID,
			&event.ProjectID,
			&workerID,
			&event.EventType,
			&event.Description,
			&timestampRaw,
		); scanErr != nil {
			return nil, fmt.Errorf("scan event: %w", scanErr)
		}

		if workerID.Valid {
			wID := workerID.Int64
			event.WorkerID = &wID
		}

		timestamp, parseErr := parseTimeValue(timestampRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		event.Timestamp = timestamp
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events by project: %w", err)
	}

	return events, nil
}

func scanProject(scanner interface{ Scan(dest ...any) error }) (Project, error) {
	var (
		project    Project
		createdRaw any
		updatedRaw any
	)

	if err := scanner.Scan(
		&project.ID,
		&project.Name,
		&project.Description,
		&project.Status,
		&project.RepoURL,
		&project.AgentsMDPath,
		&createdRaw,
		&updatedRaw,
	); err != nil {
		return Project{}, err
	}

	createdAt, err := parseTimeValue(createdRaw)
	if err != nil {
		return Project{}, err
	}
	updatedAt, err := parseTimeValue(updatedRaw)
	if err != nil {
		return Project{}, err
	}

	project.CreatedAt = createdAt
	project.UpdatedAt = updatedAt
	return project, nil
}

func buildProgress(tasks []Task) Progress {
	if len(tasks) == 0 {
		return Progress{
			Overall:     0,
			Workstreams: []WorkstreamProgress{},
		}
	}

	workstreams := make([]WorkstreamProgress, 0, len(tasks))
	completed := 0
	active := 0
	for _, task := range tasks {
		if task.Status == TaskStatusRejected {
			continue
		}
		active++
		if task.Status == TaskStatusCompleted {
			completed++
		}

		name := strings.TrimSpace(task.Title)
		if name == "" {
			name = fmt.Sprintf("Task %d", task.ID)
		}

		workstreams = append(workstreams, WorkstreamProgress{
			Name:     name,
			Progress: progressFromTaskStatus(task.Status),
		})
	}

	var overall float64
	if active > 0 {
		overall = float64(completed) / float64(active)
	}

	return Progress{
		Overall:     overall,
		Workstreams: workstreams,
	}
}

func progressFromTaskStatus(status string) float64 {
	switch status {
	case TaskStatusCompleted:
		return 1
	case TaskStatusInProgress:
		return 0.5
	default:
		return 0
	}
}

func parseTimeValue(raw any) (time.Time, error) {
	switch value := raw.(type) {
	case time.Time:
		return value.UTC(), nil
	case string:
		return parseTimeString(value)
	case []byte:
		return parseTimeString(string(value))
	case nil:
		return time.Time{}, fmt.Errorf("missing timestamp: %w", ErrInvalidInput)
	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp type %T: %w", raw, ErrInvalidInput)
	}
}

func parseOptionalTimeValue(raw any) (*time.Time, error) {
	if raw == nil {
		return nil, nil
	}

	parsed, err := parseTimeValue(raw)
	if err != nil {
		return nil, err
	}
	if parsed.IsZero() {
		return nil, nil
	}

	return &parsed, nil
}

func parseTimeString(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("empty timestamp: %w", ErrInvalidInput)
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}

	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("parse timestamp %q: %w", trimmed, ErrInvalidInput)
}

func formatTime(ts time.Time) string {
	return ts.UTC().Format(time.RFC3339Nano)
}

func nowUTC() time.Time {
	return time.Now().UTC()
}

func emptyOrValue(value string) string {
	return strings.TrimSpace(value)
}

func projectExternalID(name string, fallbackID int64) string {
	normalized := normalizeProjectRef(name)
	if normalized == "" {
		return strconv.FormatInt(fallbackID, 10)
	}

	return normalized
}

func normalizeProjectRef(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(trimmed))
	lastWasHyphen := false
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastWasHyphen = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastWasHyphen = false
		case r == '-' || r == '_' || r == ' ':
			if !lastWasHyphen {
				b.WriteRune('-')
				lastWasHyphen = true
			}
		default:
			if !lastWasHyphen {
				b.WriteRune('-')
				lastWasHyphen = true
			}
		}
	}

	result := strings.Trim(b.String(), "-")
	return result
}

func isValidProjectStatus(status string) bool {
	switch status {
	case ProjectStatusWorking, ProjectStatusNeedsInput, ProjectStatusPendingReview, ProjectStatusBlocked, ProjectStatusPaused:
		return true
	default:
		return false
	}
}

func isValidWorkerStatus(status string) bool {
	switch status {
	case WorkerStatusRunning, WorkerStatusPaused, WorkerStatusCompleted, WorkerStatusFailed, WorkerStatusBlocked, WorkerStatusCancelled:
		return true
	default:
		return false
	}
}

func isValidTaskStatus(status string) bool {
	switch status {
	case TaskStatusPending, TaskStatusInProgress, TaskStatusCompleted, TaskStatusFailed, TaskStatusBlocked, TaskStatusRejected:
		return true
	default:
		return false
	}
}
