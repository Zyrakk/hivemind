package llm

import (
	"context"
	"net/http"
	"time"
)

type GeminiClient struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

func NewGeminiClient(apiKey, model string) *GeminiClient {
	return &GeminiClient{
		APIKey: apiKey,
		Model:  model,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *GeminiClient) Consult(ctx context.Context, context, question string) (*Opinion, error) {
	_ = ctx
	_ = context
	_ = question
	return nil, ErrNotImplemented
}
