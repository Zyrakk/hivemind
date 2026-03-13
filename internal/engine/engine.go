package engine

import "context"

// Engine is the brain of the orchestrator. It thinks, proposes plans, and evaluates worker output.
// It NEVER reads repos directly. All repo data arrives as text in request structs.
type Engine interface {
	// Think starts or continues an iterative thinking session.
	// Returns one of: question (needs user input), info_request (needs repo data), ready (can propose).
	Think(ctx context.Context, req ThinkRequest) (*ThinkResult, error)

	// Propose generates a formal TaskPlan from completed thinking.
	// Called only after Think returns type="ready".
	Propose(ctx context.Context, req ProposeRequest) (*PlanResult, error)

	// Rebuild takes a rejected plan + user feedback and produces a revised plan.
	// Does NOT re-enter Think loop - direct refinement in one call.
	Rebuild(ctx context.Context, req RebuildRequest) (*PlanResult, error)

	// Evaluate reviews worker output and decides pass/retry/escalate.
	// Receives diff, build output, test output, vet output as text - no repo access.
	Evaluate(ctx context.Context, req EvalRequest) (*EvalResult, error)

	// Name returns engine identifier for logging and notifications.
	Name() string

	// Available checks if the engine is operational (auth OK, within quota).
	Available(ctx context.Context) bool
}

// ThinkTurn represents one turn in the thinking conversation.
type ThinkTurn struct {
	Role    string `json:"role"` // "engine", "operator", "recon"
	Content string `json:"content"`
}

// ThinkRequest is a single turn in the thinking conversation.
type ThinkRequest struct {
	// First turn: all fields set. Subsequent turns: PreviousThinking + Response.
	Directive   string   `json:"directive"`
	ProjectName string   `json:"project_name"`
	AgentsMD    string   `json:"agents_md"`
	ReconData   string   `json:"recon_data"` // output from orchestrator's local recon
	Cache       string   `json:"cache"`
	Hints       []string `json:"hints"`

	// Multi-turn context
	PreviousThinking []ThinkTurn `json:"previous_thinking,omitempty"`
	Response         string      `json:"response,omitempty"` // answer to question or info_request
}

// ThinkResult is the engine's response to a Think call.
type ThinkResult struct {
	Type string `json:"type"` // "question", "info_request", "ready"

	// Type == "question": question text for the operator (sent via Telegram)
	Question string `json:"question,omitempty"`

	// Type == "info_request": read-only shell commands for the orchestrator to run locally
	Commands []string `json:"commands,omitempty"`

	// Type == "ready": summary of thinking, passed to Propose
	Summary string `json:"summary,omitempty"`

	// Web research performed during this turn (if any)
	WebResearch []WebFinding `json:"web_research,omitempty"`
}

type WebFinding struct {
	Query   string   `json:"query"`
	Summary string   `json:"summary"`
	Sources []string `json:"sources"`
}

type ProposeRequest struct {
	Directive       string      `json:"directive"`
	ProjectName     string      `json:"project_name"`
	AgentsMD        string      `json:"agents_md"`
	ReconData       string      `json:"recon_data"`
	ThinkingSummary string      `json:"thinking_summary"`
	ThinkingHistory []ThinkTurn `json:"thinking_history"`
}

type RebuildRequest struct {
	PreviousPlan *PlanResult `json:"previous_plan"`
	Feedback     string      `json:"feedback"`
	Directive    string      `json:"directive"`
	ProjectName  string      `json:"project_name"`
	AgentsMD     string      `json:"agents_md"`
	ReconData    string      `json:"recon_data"`
}

type PlanResult struct {
	Tasks      []PlanTask `json:"tasks"`
	Summary    string     `json:"summary"`
	Confidence float64    `json:"confidence"`
}

type Check struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	Type        string `json:"type"` // "build", "test", "file_exists", "grep", "manual"
}

type PlanTask struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	BranchName   string   `json:"branch_name"`
	Dependencies []string `json:"dependencies"`
	Priority     int      `json:"priority"`
	Type         string   `json:"type"`   // "coding", "qa", "research"
	Prompt       string   `json:"prompt"` // complete self-contained prompt for Codex worker

	// Artifact fields
	Briefing           string  `json:"briefing,omitempty"`            // 2-3 sentences for Telegram
	ExecutionPrompt    string  `json:"execution_prompt,omitempty"`    // precise step-by-step for Codex
	AutomatedChecklist []Check `json:"automated_checklist,omitempty"` // machine-executable checks
	UserChecklist      []Check `json:"user_checklist,omitempty"`      // human-only checks (UI/UX, design)
}

type EvalRequest struct {
	TaskID      string   `json:"task_id"`
	TaskTitle   string   `json:"task_title"`
	TaskDesc    string   `json:"task_desc"`
	DiffContent string   `json:"diff_content"` // git diff output
	BuildOutput string   `json:"build_output"` // go build ./... output
	TestOutput  string   `json:"test_output"`  // go test ./... output
	VetOutput   string   `json:"vet_output"`   // go vet ./... output
	AgentsMD    string   `json:"agents_md"`
	Criteria    []string `json:"criteria"`
}

type EvalResult struct {
	TaskID      string   `json:"task_id"`
	Verdict     string   `json:"verdict"` // "pass", "retry", "escalate"
	Analysis    string   `json:"analysis"`
	Suggestions []string `json:"suggestions"`
	RetryPrompt string   `json:"retry_prompt"` // if verdict=retry
	Confidence  float64  `json:"confidence"`
}

// MetaPlanner is an optional interface for engines that can decompose
// a high-level roadmap into phased directives. Call sites type-assert.
type MetaPlanner interface {
	MetaPlan(ctx context.Context, req MetaPlanRequest) (*MetaPlanResult, error)
}

type MetaPlanRequest struct {
	ProjectName string `json:"project_name"`
	AgentsMD    string `json:"agents_md"`
	ReconData   string `json:"recon_data"`
	Roadmap     string `json:"roadmap"`
	Feedback    string `json:"feedback,omitempty"` // for reject-and-revise
}

type MetaPlanResult struct {
	Phases []RoadmapPhase `json:"phases"`
}

type RoadmapPhase struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Directives  []string `json:"directives"`
	DependsOn   []string `json:"depends_on"`
}
