package llm

import (
	"context"
	"net/http"
	"time"
)

type GLMClient struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

func NewGLMClient(apiKey, model, baseURL string) *GLMClient {
	return &GLMClient{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *GLMClient) Plan(ctx context.Context, directive, agentsMD string) (*TaskPlan, error) {
	_ = ctx
	_ = directive
	_ = agentsMD
	return nil, ErrNotImplemented
}

func (c *GLMClient) Evaluate(ctx context.Context, diff, criteria string) (*Evaluation, error) {
	_ = ctx
	_ = diff
	_ = criteria
	return nil, ErrNotImplemented
}

func (c *GLMClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	_ = ctx
	_ = systemPrompt
	_ = userMessage
	return "", ErrNotImplemented
}
