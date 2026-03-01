package llm

import (
	"context"
	"errors"
)

var ErrNotImplemented = errors.New("not implemented")

type LLMClient interface {
	Plan(ctx context.Context, directive, agentsMD string) (*TaskPlan, error)
	Evaluate(ctx context.Context, task, diff, agentsMD string) (*Evaluation, error)
	Chat(ctx context.Context, systemPrompt, userMessage string) (string, TokenUsage, error)
}
