package llm

import (
	"context"
	"errors"
)

var ErrNotImplemented = errors.New("not implemented")

type Task struct {
	ID          string
	Title       string
	Description string
	Priority    int
	DependsOn   []string
}

type TaskPlan struct {
	Summary string
	Tasks   []Task
}

type Evaluation struct {
	Score   int
	Verdict string
	Notes   string
}

type LLMClient interface {
	Plan(ctx context.Context, directive, agentsMD string) (*TaskPlan, error)
	Evaluate(ctx context.Context, diff, criteria string) (*Evaluation, error)
	Chat(ctx context.Context, systemPrompt, userMessage string) (string, error)
}
