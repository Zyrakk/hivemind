package evaluator

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

func (s *Service) Evaluate(ctx context.Context, diff, criteria string) (*llm.Evaluation, error) {
	_ = ctx
	_ = diff
	_ = criteria
	return nil, llm.ErrNotImplemented
}
