package planner

import (
	"context"

	"github.com/zyrak/hivemind/internal/llm"
)

type Service struct {
	client       llm.LLMClient
	systemPrompt string
}

func New(client llm.LLMClient, systemPrompt string) *Service {
	return &Service{
		client:       client,
		systemPrompt: systemPrompt,
	}
}

func (s *Service) BuildPlan(ctx context.Context, directive, agentsMD string) (*llm.TaskPlan, error) {
	_ = ctx
	_ = directive
	_ = agentsMD
	return nil, llm.ErrNotImplemented
}
