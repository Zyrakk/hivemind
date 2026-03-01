package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zyrakk/hivemind/internal/state"
)

var allowedEventTypes = map[string]struct{}{
	"task_completed": {},
	"worker_started": {},
	"worker_failed":  {},
	"pr_created":     {},
	"input_needed":   {},
}

type Server struct {
	store     *state.Store
	logger    *slog.Logger
	startedAt time.Time
}

func NewHandlers(store *state.Store, logger *slog.Logger, startedAt time.Time) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	return &Server{
		store:     store,
		logger:    logger,
		startedAt: startedAt,
	}
}

func (h *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("GET /api/state", h.handleGetState)
	mux.HandleFunc("GET /api/projects", h.handleGetProjects)
	mux.HandleFunc("GET /api/project/{id}", h.handleGetProject)
	mux.HandleFunc("POST /api/projects", h.handleCreateProject)
	mux.HandleFunc("POST /api/state/{project}", h.handleUpdateProjectState)
	mux.HandleFunc("POST /api/events", h.handleCreateEvent)
	mux.HandleFunc("POST /api/tasks", h.handleCreateTask)
	mux.HandleFunc("PUT /api/tasks/{id}", h.handleUpdateTask)
	mux.HandleFunc("POST /api/workers", h.handleCreateWorker)
	mux.HandleFunc("PUT /api/workers/{id}", h.handleUpdateWorker)
}

type apiError struct {
	Error string `json:"error"`
}

type statusResponse struct {
	Status string `json:"status"`
}

type idResponse struct {
	ID int64 `json:"id"`
}

type countersResponse struct {
	ActiveWorkers int `json:"active_workers"`
	PendingTasks  int `json:"pending_tasks"`
	PendingReview int `json:"pending_reviews"`
}

type projectSummaryResponse struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	ActiveWorkers int        `json:"active_workers"`
	PendingTasks  int        `json:"pending_tasks"`
	LastActivity  *time.Time `json:"last_activity,omitempty"`
}

type activeWorkerResponse struct {
	ID              int64      `json:"id"`
	ProjectID       string     `json:"project_id"`
	ProjectName     string     `json:"project_name"`
	SessionID       string     `json:"session_id"`
	TaskDescription string     `json:"task_description"`
	Branch          string     `json:"branch"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
}

type projectResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Status       string    `json:"status"`
	RepoURL      string    `json:"repo_url"`
	AgentsMDPath string    `json:"agents_md_path"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type taskResponse struct {
	ID               int64     `json:"id"`
	ProjectID        string    `json:"project_id"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	Status           string    `json:"status"`
	Priority         int       `json:"priority"`
	DependsOn        []string  `json:"depends_on"`
	AssignedWorkerID *int64    `json:"assigned_worker_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type workerResponse struct {
	ID              int64      `json:"id"`
	ProjectID       string     `json:"project_id"`
	SessionID       string     `json:"session_id"`
	TaskDescription string     `json:"task_description"`
	Branch          string     `json:"branch"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
}

type eventResponse struct {
	ID          int64     `json:"id"`
	ProjectID   string    `json:"project_id"`
	WorkerID    *int64    `json:"worker_id,omitempty"`
	EventType   string    `json:"event_type"`
	Description string    `json:"description"`
	Timestamp   time.Time `json:"timestamp"`
}

type workstreamProgressResponse struct {
	Name     string  `json:"name"`
	Progress float64 `json:"progress"`
}

type progressResponse struct {
	Overall     float64                      `json:"overall"`
	Workstreams []workstreamProgressResponse `json:"workstreams"`
}

type globalStateResponse struct {
	Projects      []projectSummaryResponse `json:"projects"`
	ActiveWorkers []activeWorkerResponse   `json:"active_workers"`
	Counters      countersResponse         `json:"counters"`
}

type projectDetailResponse struct {
	Project      projectResponse  `json:"project"`
	Tasks        []taskResponse   `json:"tasks"`
	Workers      []workerResponse `json:"workers"`
	RecentEvents []eventResponse  `json:"recent_events"`
	Progress     progressResponse `json:"progress"`
	Context      contextResponse  `json:"context"`
}

type contextResponse struct {
	Summary               string                         `json:"summary"`
	ArchitectureDecisions []architectureDecisionResponse `json:"architecture_decisions"`
	LastSession           *lastSessionResponse           `json:"last_session,omitempty"`
	QuickLinks            quickLinksResponse             `json:"quick_links"`
	ContributeNow         []string                       `json:"contribute_now"`
}

type architectureDecisionResponse struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

type lastSessionResponse struct {
	Date    *time.Time `json:"date,omitempty"`
	Task    string     `json:"task"`
	Result  string     `json:"result"`
	Did     []string   `json:"did"`
	Pending []string   `json:"pending"`
}

type quickLinksResponse struct {
	Repository   string            `json:"repository,omitempty"`
	OpenPRs      string            `json:"open_prs,omitempty"`
	AgentsMD     string            `json:"agents_md,omitempty"`
	ActiveBranch *activeBranchLink `json:"active_branch,omitempty"`
}

type activeBranchLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type updateProjectStateRequest struct {
	Status string `json:"status"`
}

type createProjectRequest struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Status       string `json:"status"`
	RepoURL      string `json:"repo_url"`
	AgentsMDPath string `json:"agents_md_path"`
}

type createEventRequest struct {
	ProjectID   string `json:"project_id"`
	WorkerID    *int64 `json:"worker_id"`
	EventType   string `json:"event_type"`
	Description string `json:"description"`
}

type createTaskRequest struct {
	ProjectID   string   `json:"project_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Priority    int      `json:"priority"`
	DependsOn   []string `json:"depends_on"`
}

type createWorkerRequest struct {
	ProjectID       string `json:"project_id"`
	SessionID       string `json:"session_id"`
	TaskDescription string `json:"task_description"`
	Branch          string `json:"branch"`
}

func (h *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	uptime := time.Since(h.startedAt).Round(time.Second).String()
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"uptime": uptime,
	})
}

func (h *Server) handleGetState(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	global, err := h.store.GetGlobalState(r.Context())
	if err != nil {
		h.writeStoreError(w, r, "get global state", err)
		return
	}

	resp := globalStateResponse{
		Projects:      make([]projectSummaryResponse, 0, len(global.Projects)),
		ActiveWorkers: make([]activeWorkerResponse, 0, len(global.ActiveWorkers)),
		Counters: countersResponse{
			ActiveWorkers: global.Counters.ActiveWorkers,
			PendingTasks:  global.Counters.PendingTasks,
			PendingReview: global.Counters.PendingReview,
		},
	}

	for _, project := range global.Projects {
		resp.Projects = append(resp.Projects, projectSummaryResponse{
			ID:            project.ID,
			Name:          project.Name,
			Status:        project.Status,
			ActiveWorkers: project.ActiveWorkers,
			PendingTasks:  project.PendingTasks,
			LastActivity:  project.LastActivity,
		})
	}

	for _, worker := range global.ActiveWorkers {
		resp.ActiveWorkers = append(resp.ActiveWorkers, activeWorkerResponse{
			ID:              worker.ID,
			ProjectID:       worker.ProjectRef,
			ProjectName:     worker.ProjectName,
			SessionID:       worker.SessionID,
			TaskDescription: worker.TaskDescription,
			Branch:          worker.Branch,
			Status:          worker.Status,
			StartedAt:       worker.StartedAt,
			FinishedAt:      worker.FinishedAt,
			ErrorMessage:    worker.ErrorMessage,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Server) handleGetProjects(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	projects, err := h.store.ListProjectSummaries(r.Context())
	if err != nil {
		h.writeStoreError(w, r, "list projects", err)
		return
	}

	resp := make([]projectSummaryResponse, 0, len(projects))
	for _, project := range projects {
		resp = append(resp, projectSummaryResponse{
			ID:            project.ID,
			Name:          project.Name,
			Status:        project.Status,
			ActiveWorkers: project.ActiveWorkers,
			PendingTasks:  project.PendingTasks,
			LastActivity:  project.LastActivity,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	projectRef := strings.TrimSpace(r.PathValue("id"))
	if projectRef == "" {
		writeError(w, http.StatusBadRequest, "project id is required")
		return
	}

	detail, err := h.store.GetProjectDetail(r.Context(), projectRef)
	if err != nil {
		h.writeStoreError(w, r, "get project detail", err)
		return
	}

	resp := projectDetailResponse{
		Project: projectResponse{
			ID:           detail.ProjectRef,
			Name:         detail.Project.Name,
			Description:  detail.Project.Description,
			Status:       detail.Project.Status,
			RepoURL:      detail.Project.RepoURL,
			AgentsMDPath: detail.Project.AgentsMDPath,
			CreatedAt:    detail.Project.CreatedAt,
			UpdatedAt:    detail.Project.UpdatedAt,
		},
		Tasks:        make([]taskResponse, 0, len(detail.Tasks)),
		Workers:      make([]workerResponse, 0, len(detail.Workers)),
		RecentEvents: make([]eventResponse, 0, len(detail.Events)),
		Progress: progressResponse{
			Overall:     detail.Progress.Overall,
			Workstreams: make([]workstreamProgressResponse, 0, len(detail.Progress.Workstreams)),
		},
		Context: buildProjectContext(detail),
	}

	for _, task := range detail.Tasks {
		resp.Tasks = append(resp.Tasks, taskResponse{
			ID:               task.ID,
			ProjectID:        detail.ProjectRef,
			Title:            task.Title,
			Description:      task.Description,
			Status:           task.Status,
			Priority:         task.Priority,
			DependsOn:        decodeDependsOn(task.DependsOn),
			AssignedWorkerID: task.AssignedWorkerID,
			CreatedAt:        task.CreatedAt,
			UpdatedAt:        task.UpdatedAt,
		})
	}

	for _, worker := range detail.Workers {
		resp.Workers = append(resp.Workers, workerResponse{
			ID:              worker.ID,
			ProjectID:       detail.ProjectRef,
			SessionID:       worker.SessionID,
			TaskDescription: worker.TaskDescription,
			Branch:          worker.Branch,
			Status:          worker.Status,
			StartedAt:       worker.StartedAt,
			FinishedAt:      worker.FinishedAt,
			ErrorMessage:    worker.ErrorMessage,
		})
	}

	for _, event := range detail.Events {
		resp.RecentEvents = append(resp.RecentEvents, eventResponse{
			ID:          event.ID,
			ProjectID:   detail.ProjectRef,
			WorkerID:    event.WorkerID,
			EventType:   event.EventType,
			Description: event.Description,
			Timestamp:   event.Timestamp,
		})
	}

	for _, stream := range detail.Progress.Workstreams {
		resp.Progress.Workstreams = append(resp.Progress.Workstreams, workstreamProgressResponse{
			Name:     stream.Name,
			Progress: stream.Progress,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	var req createProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.Status = strings.TrimSpace(req.Status)
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.AgentsMDPath = strings.TrimSpace(req.AgentsMDPath)

	name := req.Name
	if name == "" {
		name = req.ID
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "name or id is required")
		return
	}

	status := req.Status
	if status == "" {
		status = state.ProjectStatusWorking
	}

	projectID, err := h.store.CreateProject(r.Context(), state.Project{
		Name:         name,
		Description:  req.Description,
		Status:       status,
		RepoURL:      req.RepoURL,
		AgentsMDPath: req.AgentsMDPath,
	})
	if err != nil {
		h.writeStoreError(w, r, "create project", err)
		return
	}

	writeJSON(w, http.StatusCreated, idResponse{ID: projectID})
}

func (h *Server) handleUpdateProjectState(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	projectRef := strings.TrimSpace(r.PathValue("project"))
	if projectRef == "" {
		writeError(w, http.StatusBadRequest, "project id is required")
		return
	}

	var req updateProjectStateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.Status = strings.TrimSpace(req.Status)
	if req.Status == "" {
		writeError(w, http.StatusBadRequest, "status is required")
		return
	}

	if err := h.store.UpdateProjectStatusByReference(r.Context(), projectRef, req.Status); err != nil {
		h.writeStoreError(w, r, "update project state", err)
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

func (h *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	var req createEventRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.EventType = strings.TrimSpace(req.EventType)
	req.Description = strings.TrimSpace(req.Description)

	if req.ProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	if req.EventType == "" {
		writeError(w, http.StatusBadRequest, "event_type is required")
		return
	}
	if _, ok := allowedEventTypes[req.EventType]; !ok {
		writeError(w, http.StatusBadRequest, "invalid event_type")
		return
	}
	if req.Description == "" {
		writeError(w, http.StatusBadRequest, "description is required")
		return
	}

	projectID, err := h.store.ResolveProjectID(r.Context(), req.ProjectID)
	if err != nil {
		h.writeStoreError(w, r, "resolve project", err)
		return
	}

	event := state.Event{
		ProjectID:   projectID,
		WorkerID:    req.WorkerID,
		EventType:   req.EventType,
		Description: req.Description,
		Timestamp:   time.Now().UTC(),
	}
	if err := h.store.AppendEvent(r.Context(), event); err != nil {
		h.writeStoreError(w, r, "create event", err)
		return
	}

	writeJSON(w, http.StatusCreated, statusResponse{Status: "created"})
}

func (h *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	var req createTaskRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)

	if req.ProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Priority < 1 || req.Priority > 5 {
		writeError(w, http.StatusBadRequest, "priority must be between 1 and 5")
		return
	}

	projectID, err := h.store.ResolveProjectID(r.Context(), req.ProjectID)
	if err != nil {
		h.writeStoreError(w, r, "resolve project", err)
		return
	}

	dependsOn := req.DependsOn
	if dependsOn == nil {
		dependsOn = []string{}
	}
	dependsOnJSON, err := json.Marshal(dependsOn)
	if err != nil {
		h.writeStoreError(w, r, "encode depends_on", err)
		return
	}

	taskID, err := h.store.CreateTask(r.Context(), state.Task{
		ProjectID:   projectID,
		Title:       req.Title,
		Description: req.Description,
		Status:      state.TaskStatusPending,
		Priority:    req.Priority,
		DependsOn:   string(dependsOnJSON),
	})
	if err != nil {
		h.writeStoreError(w, r, "create task", err)
		return
	}

	writeJSON(w, http.StatusCreated, idResponse{ID: taskID})
}

func (h *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	taskID, err := parsePathInt64(r.PathValue("id"), "task id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	body, err := decodeRawObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var update state.TaskUpdate
	for key, raw := range body {
		switch key {
		case "title":
			var title string
			if err := json.Unmarshal(raw, &title); err != nil {
				writeError(w, http.StatusBadRequest, "title must be a string")
				return
			}
			title = strings.TrimSpace(title)
			if title == "" {
				writeError(w, http.StatusBadRequest, "title cannot be empty")
				return
			}
			update.Title = &title
		case "description":
			var description string
			if err := json.Unmarshal(raw, &description); err != nil {
				writeError(w, http.StatusBadRequest, "description must be a string")
				return
			}
			update.Description = &description
		case "status":
			var status string
			if err := json.Unmarshal(raw, &status); err != nil {
				writeError(w, http.StatusBadRequest, "status must be a string")
				return
			}
			status = strings.TrimSpace(status)
			if status == "" {
				writeError(w, http.StatusBadRequest, "status cannot be empty")
				return
			}
			update.Status = &status
		case "priority":
			var priority int
			if err := json.Unmarshal(raw, &priority); err != nil {
				writeError(w, http.StatusBadRequest, "priority must be an integer")
				return
			}
			update.Priority = &priority
		case "depends_on":
			var dependsOn []string
			if err := json.Unmarshal(raw, &dependsOn); err != nil {
				writeError(w, http.StatusBadRequest, "depends_on must be an array of strings")
				return
			}
			encoded, err := json.Marshal(dependsOn)
			if err != nil {
				h.writeStoreError(w, r, "encode depends_on", err)
				return
			}
			dependsOnRaw := string(encoded)
			update.DependsOn = &dependsOnRaw
		case "assigned_worker_id":
			update.AssignedWorkerIDSet = true
			if string(raw) == "null" {
				update.AssignedWorkerID = nil
				continue
			}
			var workerID int64
			if err := json.Unmarshal(raw, &workerID); err != nil {
				writeError(w, http.StatusBadRequest, "assigned_worker_id must be an integer or null")
				return
			}
			if workerID <= 0 {
				writeError(w, http.StatusBadRequest, "assigned_worker_id must be > 0")
				return
			}
			update.AssignedWorkerID = &workerID
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown field %q", key))
			return
		}
	}

	if err := h.store.UpdateTask(r.Context(), taskID, update); err != nil {
		h.writeStoreError(w, r, "update task", err)
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

func (h *Server) handleCreateWorker(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	var req createWorkerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.TaskDescription = strings.TrimSpace(req.TaskDescription)
	req.Branch = strings.TrimSpace(req.Branch)

	if req.ProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	if req.TaskDescription == "" {
		writeError(w, http.StatusBadRequest, "task_description is required")
		return
	}
	if req.Branch == "" {
		writeError(w, http.StatusBadRequest, "branch is required")
		return
	}

	projectID, err := h.store.ResolveProjectID(r.Context(), req.ProjectID)
	if err != nil {
		h.writeStoreError(w, r, "resolve project", err)
		return
	}

	workerID, err := h.store.CreateWorker(r.Context(), state.Worker{
		ProjectID:       projectID,
		SessionID:       req.SessionID,
		TaskDescription: req.TaskDescription,
		Branch:          req.Branch,
		Status:          state.WorkerStatusRunning,
		StartedAt:       time.Now().UTC(),
	})
	if err != nil {
		h.writeStoreError(w, r, "create worker", err)
		return
	}

	writeJSON(w, http.StatusCreated, idResponse{ID: workerID})
}

func (h *Server) handleUpdateWorker(w http.ResponseWriter, r *http.Request) {
	if !h.ensureStore(w) {
		return
	}

	workerID, err := parsePathInt64(r.PathValue("id"), "worker id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	body, err := decodeRawObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var update state.WorkerUpdate
	for key, raw := range body {
		switch key {
		case "status":
			var status string
			if err := json.Unmarshal(raw, &status); err != nil {
				writeError(w, http.StatusBadRequest, "status must be a string")
				return
			}
			status = strings.TrimSpace(status)
			if status == "" {
				writeError(w, http.StatusBadRequest, "status cannot be empty")
				return
			}
			update.Status = &status
		case "error_message":
			update.ErrorMessageSet = true
			if string(raw) == "null" {
				update.ErrorMessage = nil
				continue
			}
			var message string
			if err := json.Unmarshal(raw, &message); err != nil {
				writeError(w, http.StatusBadRequest, "error_message must be a string or null")
				return
			}
			update.ErrorMessage = &message
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown field %q", key))
			return
		}
	}

	if err := h.store.UpdateWorker(r.Context(), workerID, update); err != nil {
		h.writeStoreError(w, r, "update worker", err)
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

func buildProjectContext(detail state.ProjectDetail) contextResponse {
	return contextResponse{
		Summary:               buildSummary(detail),
		ArchitectureDecisions: buildArchitectureDecisions(detail),
		LastSession:           buildLastSession(detail),
		QuickLinks:            buildQuickLinks(detail),
		ContributeNow:         buildContributeNow(detail),
	}
}

func buildSummary(detail state.ProjectDetail) string {
	name := detail.Project.Name
	if strings.TrimSpace(name) == "" {
		name = "Este proyecto"
	}

	statusLabel := strings.ReplaceAll(detail.Project.Status, "_", " ")
	activeWorkers := 0
	for _, worker := range detail.Workers {
		if worker.Status == state.WorkerStatusRunning {
			activeWorkers++
		}
	}

	pendingTasks := 0
	for _, task := range detail.Tasks {
		if task.Status == state.TaskStatusPending || task.Status == state.TaskStatusInProgress || task.Status == state.TaskStatusBlocked {
			pendingTasks++
		}
	}

	description := strings.TrimSpace(detail.Project.Description)
	if description == "" {
		description = "No hay descripcion registrada en la base de datos."
	}

	return fmt.Sprintf(
		"%s. Estado actual: %s, %d workers activos y %d tareas pendientes. %s",
		description,
		statusLabel,
		activeWorkers,
		pendingTasks,
		"Usa esta vista para retomar decisiones clave y siguiente paso operativo.",
	)
}

func buildArchitectureDecisions(detail state.ProjectDetail) []architectureDecisionResponse {
	repoText := "No hay repositorio definido."
	if strings.TrimSpace(detail.Project.RepoURL) != "" {
		repoText = fmt.Sprintf("Repositorio principal: %s.", detail.Project.RepoURL)
	}

	return []architectureDecisionResponse{
		{
			ID:          "db-sqlite",
			Title:       "SQLite como estado operativo",
			Description: "Decision: almacenar estado en SQLite con migraciones al arranque. Razon: despliegue simple y persistencia local para bootstrap.",
			Type:        "database",
		},
		{
			ID:          "api-nethttp",
			Title:       "API REST con net/http",
			Description: "Decision: usar net/http estandar sin framework adicional. Razon: reducir complejidad y mantener contratos JSON explicitos.",
			Type:        "api",
		},
		{
			ID:          "single-binary",
			Title:       "Binario unico para orquestador + dashboard",
			Description: "Decision: empaquetar backend API y frontend en el mismo proceso. Razon: operacion mas simple en k3s y menor superficie de fallo.",
			Type:        "structure",
		},
		{
			ID:          "no-secrets-in-repo",
			Title:       "Secretos fuera del repositorio",
			Description: "Decision: inyectar credenciales por Secret de Kubernetes y entorno. Razon: evitar exposicion accidental de tokens/API keys.",
			Type:        "security",
		},
		{
			ID:          "repo-reference",
			Title:       "Fuente de verdad del proyecto",
			Description: "Decision: mantener AGENTS.md y repo como contexto principal versionado. Razon: trazabilidad de decisiones y sesiones. " + repoText,
			Type:        "structure",
		},
	}
}

func buildLastSession(detail state.ProjectDetail) *lastSessionResponse {
	if len(detail.Events) == 0 {
		return nil
	}

	latest := detail.Events[0]
	result := "partial"
	switch latest.EventType {
	case "worker_failed":
		result = "failed"
	case "task_completed", "pr_created":
		result = "success"
	}

	did := make([]string, 0, 3)
	for _, event := range detail.Events {
		if len(did) >= 3 {
			break
		}
		if event.EventType == "task_completed" || event.EventType == "pr_created" || event.EventType == "worker_started" {
			did = append(did, event.Description)
		}
	}
	if len(did) == 0 {
		did = append(did, latest.Description)
	}

	pending := make([]string, 0, 3)
	for _, task := range detail.Tasks {
		if len(pending) >= 3 {
			break
		}
		if task.Status == state.TaskStatusPending || task.Status == state.TaskStatusBlocked || task.Status == state.TaskStatusInProgress {
			pending = append(pending, task.Title)
		}
	}

	latestTimestamp := latest.Timestamp
	return &lastSessionResponse{
		Date:    &latestTimestamp,
		Task:    latest.Description,
		Result:  result,
		Did:     did,
		Pending: pending,
	}
}

func buildQuickLinks(detail state.ProjectDetail) quickLinksResponse {
	repo := normalizeRepoURL(detail.Project.RepoURL)
	quickLinks := quickLinksResponse{
		Repository: repo,
		OpenPRs:    deriveOpenPRsURL(repo),
		AgentsMD:   strings.TrimSpace(detail.Project.AgentsMDPath),
	}

	if len(detail.Workers) > 0 {
		branch := strings.TrimSpace(detail.Workers[0].Branch)
		if branch != "" {
			quickLinks.ActiveBranch = &activeBranchLink{
				Name: branch,
				URL:  deriveBranchURL(repo, branch),
			}
		}
	}

	return quickLinks
}

func buildContributeNow(detail state.ProjectDetail) []string {
	pendingTasks := 0
	activeBranches := make([]string, 0, 3)
	seenBranches := map[string]struct{}{}

	for _, task := range detail.Tasks {
		if task.Status == state.TaskStatusPending || task.Status == state.TaskStatusInProgress || task.Status == state.TaskStatusBlocked {
			pendingTasks++
		}
	}

	for _, worker := range detail.Workers {
		branch := strings.TrimSpace(worker.Branch)
		if branch == "" {
			continue
		}
		if _, exists := seenBranches[branch]; exists {
			continue
		}
		seenBranches[branch] = struct{}{}
		activeBranches = append(activeBranches, branch)
		if len(activeBranches) >= 3 {
			break
		}
	}

	branchSummary := "No hay ramas activas registradas."
	if len(activeBranches) > 0 {
		branchSummary = "Ramas activas: " + strings.Join(activeBranches, ", ") + "."
	}

	return []string{
		fmt.Sprintf("Estado actual del proyecto: %s.", strings.ReplaceAll(detail.Project.Status, "_", " ")),
		fmt.Sprintf("Tareas pendientes/en curso/bloqueadas: %d.", pendingTasks),
		"Stack operativo: Go + net/http + SQLite + dashboard React embebido.",
		branchSummary,
		"Evitar cambios de schema sin migracion versionada y no mezclar scope en una sola sesion.",
		"No introducir secretos reales en el repositorio; usar secretos de entorno/Kubernetes.",
	}
}

func normalizeRepoURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	trimmed = strings.TrimSuffix(trimmed, ".git")
	return strings.TrimRight(trimmed, "/")
}

func deriveOpenPRsURL(repo string) string {
	if repo == "" {
		return ""
	}

	parsed, err := url.Parse(repo)
	if err != nil {
		return ""
	}

	host := strings.ToLower(parsed.Host)
	switch {
	case strings.Contains(host, "github.com"):
		return repo + "/pulls?q=is%3Aopen+is%3Apr"
	case strings.Contains(host, "gitlab"):
		return repo + "/-/merge_requests?scope=all&state=opened"
	default:
		return ""
	}
}

func deriveBranchURL(repo, branch string) string {
	if repo == "" {
		return ""
	}

	parsed, err := url.Parse(repo)
	if err != nil {
		return ""
	}

	escapedBranch := url.PathEscape(branch)
	host := strings.ToLower(parsed.Host)
	switch {
	case strings.Contains(host, "github.com"):
		return repo + "/tree/" + escapedBranch
	case strings.Contains(host, "gitlab"):
		return repo + "/-/tree/" + escapedBranch
	default:
		return ""
	}
}

func (h *Server) ensureStore(w http.ResponseWriter) bool {
	if h.store != nil {
		return true
	}

	writeError(w, http.StatusInternalServerError, "state store is not configured")
	return false
}

func (h *Server) writeStoreError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	status := httpStatusFromStoreError(err)
	message := "internal server error"

	switch status {
	case http.StatusBadRequest:
		message = err.Error()
	case http.StatusNotFound:
		message = "resource not found"
	}

	h.logger.Error(
		"dashboard request failed",
		slog.String("operation", operation),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.Any("error", err),
	)

	writeError(w, status, message)
}

func httpStatusFromStoreError(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, state.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, state.ErrInvalidInput), errors.Is(err, state.ErrInvalidStatus):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func decodeDependsOn(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}
	}

	dependsOn := make([]string, 0)
	if err := json.Unmarshal([]byte(trimmed), &dependsOn); err != nil {
		return []string{}
	}

	return dependsOn
}

func parsePathInt64(raw, fieldName string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("%s is required", fieldName)
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", fieldName)
	}

	return parsed, nil
}

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}

	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return normalizeDecodeError(err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}

	return nil
}

func decodeRawObject(r *http.Request) (map[string]json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := decodeJSON(r, &body); err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("request body must include at least one field")
	}

	return body, nil
}

func normalizeDecodeError(err error) error {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("invalid JSON at position %d", syntaxErr.Offset)
	}

	if errors.Is(err, io.EOF) {
		return errors.New("request body is required")
	}

	return fmt.Errorf("invalid request body: %w", err)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiError{Error: message})
}
