package llm

import (
	"context"
	"net/http"
	"time"
)

type ClaudeClient struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

func NewClaudeClient(apiKey, model string) *ClaudeClient {
	return &ClaudeClient{
		APIKey: apiKey,
		Model:  model,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *ClaudeClient) Consult(ctx context.Context, context, question string) (*Opinion, error) {
	_ = ctx
	_ = context
	_ = question
	return nil, ErrNotImplemented
}
