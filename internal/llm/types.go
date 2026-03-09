package llm

type TaskPlan struct {
	Confidence float64  `json:"confidence"`
	Tasks      []Task   `json:"tasks"`
	Questions  []string `json:"questions"`
	Notes      string   `json:"notes"`
}

type CheckItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	Type        string `json:"type"`
}

type Task struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	FilesAffected      []string `json:"files_affected"`
	DependsOn          []string `json:"depends_on"`
	Complexity         string   `json:"estimated_complexity"`
	BranchName         string   `json:"branch_name"`

	// New artifact fields (populated from engine.PlanTask)
	Briefing           string      `json:"briefing,omitempty"`
	ExecutionPrompt    string      `json:"execution_prompt,omitempty"`
	AutomatedChecklist []CheckItem `json:"automated_checklist,omitempty"`
	UserChecklist      []CheckItem `json:"user_checklist,omitempty"`
}

type Evaluation struct {
	Verdict      string  `json:"verdict"`
	Confidence   float64 `json:"confidence"`
	Completeness float64 `json:"completeness"`
	Correctness  float64 `json:"correctness"`
	Conventions  float64 `json:"conventions"`
	ScopeOK      bool    `json:"scope_ok"`
	Issues       []Issue `json:"issues"`
	Summary      string  `json:"summary"`
}

type Issue struct {
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
