package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	defaultClaudeBaseURL           = "https://api.anthropic.com/v1/messages"
	defaultClaudeModel             = "claude-sonnet-4-5-20250929"
	defaultClaudeMaxTokens         = 1200
	defaultClaudeTemperature       = 0.1
	defaultClaudeInputPriceUSD     = 0.000003
	defaultClaudeOutputPriceUSD    = 0.000015
	defaultClaudeEstimatedOutRatio = 0.35
)

type ClaudeConfig struct {
	APIKey  string
	BaseURL string
	Model   string

	HTTPClient *http.Client
	Timeout    time.Duration
	Logger     *slog.Logger

	PromptDir string
	MaxTokens int

	Temperature float64

	InputTokenPriceUSD  float64
	OutputTokenPriceUSD float64

	Budget *BudgetTracker
}

type ClaudeClient struct {
	apiKey      string
	baseURL     string
	model       string
	httpClient  *http.Client
	logger      *slog.Logger
	promptDir   string
	maxTokens   int
	temperature float64

	inputTokenPriceUSD  float64
	outputTokenPriceUSD float64

	budget *BudgetTracker
}

func NewClaudeClient(config ClaudeConfig) *ClaudeClient {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultClaudeBaseURL
	}

	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = defaultClaudeModel
	}

	promptDir := strings.TrimSpace(config.PromptDir)
	if promptDir == "" {
		promptDir = "prompts"
	}

	maxTokens := config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultClaudeMaxTokens
	}

	temperature := config.Temperature
	if temperature == 0 {
		temperature = defaultClaudeTemperature
	}

	inPrice := config.InputTokenPriceUSD
	if inPrice <= 0 {
		inPrice = defaultClaudeInputPriceUSD
	}

	outPrice := config.OutputTokenPriceUSD
	if outPrice <= 0 {
		outPrice = defaultClaudeOutputPriceUSD
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &ClaudeClient{
		apiKey:              strings.TrimSpace(config.APIKey),
		baseURL:             baseURL,
		model:               model,
		httpClient:          buildHTTPClient(config.HTTPClient, config.Timeout),
		logger:              logger,
		promptDir:           promptDir,
		maxTokens:           maxTokens,
		temperature:         temperature,
		inputTokenPriceUSD:  inPrice,
		outputTokenPriceUSD: outPrice,
		budget:              config.Budget,
	}
}

func (c *ClaudeClient) Consult(ctx context.Context, consultationType string, consultationContext string, question string) (*Opinion, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, errors.New("claude api key is empty")
	}

	if !c.IsAvailable() {
		return nil, fmt.Errorf("%s budget exhausted", c.GetName())
	}

	systemPrompt, err := loadConsultantPrompt(c.promptDir)
	if err != nil {
		return nil, err
	}

	userMessage := buildConsultantUserMessage(consultationType, consultationContext, question)
	estimatedIn := estimateTokenCount(systemPrompt + "\n" + userMessage)
	estimatedOut := int(math.Max(64, float64(c.maxTokens)*defaultClaudeEstimatedOutRatio))
	estimatedCost := c.estimateCost(estimatedIn, estimatedOut)

	if c.budget != nil && !c.budget.CanAfford(estimatedCost) {
		return nil, fmt.Errorf("%s budget limit reached (estimated cost %.6f USD)", c.GetName(), estimatedCost)
	}

	payload := claudeMessagesRequest{
		Model:       c.model,
		MaxTokens:   c.maxTokens,
		System:      systemPrompt,
		Temperature: c.temperature,
		Messages: []claudeMessage{
			{Role: "user", Content: userMessage},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal claude request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build claude request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute claude request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read claude response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("claude api returned status %d: %s", resp.StatusCode, extractAPIError(respBody))
	}

	var completion claudeMessagesResponse
	if err := json.Unmarshal(respBody, &completion); err != nil {
		return nil, fmt.Errorf("decode claude response: %w", err)
	}

	textContent := strings.TrimSpace(firstClaudeTextPart(completion.Content))
	if textContent == "" {
		return nil, errors.New("claude response returned empty content")
	}

	opinion, err := parseOpinion(textContent)
	if err != nil {
		return nil, err
	}

	actualCost := c.estimateCost(completion.Usage.InputTokens, completion.Usage.OutputTokens)
	if c.budget != nil {
		c.budget.Record(actualCost)
	}

	latency := time.Since(start)
	c.logger.Info(
		"claude consultation completed",
		slog.String("model", c.model),
		slog.String("consultation_type", consultationType),
		slog.Int("status", resp.StatusCode),
		slog.Duration("latency", latency),
		slog.Int("input_tokens", completion.Usage.InputTokens),
		slog.Int("output_tokens", completion.Usage.OutputTokens),
		slog.Float64("estimated_cost_usd", estimatedCost),
		slog.Float64("actual_cost_usd", actualCost),
	)

	return opinion, nil
}

func (c *ClaudeClient) GetName() string {
	return "claude"
}

func (c *ClaudeClient) GetBudgetRemaining() float64 {
	if c.budget == nil {
		return math.Inf(1)
	}

	return c.budget.Remaining()
}

func (c *ClaudeClient) IsAvailable() bool {
	if strings.TrimSpace(c.apiKey) == "" {
		return false
	}
	if c.budget == nil {
		return true
	}

	return c.budget.CanAfford(0)
}

func (c *ClaudeClient) estimateCost(inputTokens, outputTokens int) float64 {
	in := float64(maxInt(inputTokens, 0)) * c.inputTokenPriceUSD
	out := float64(maxInt(outputTokens, 0)) * c.outputTokenPriceUSD
	return in + out
}

func firstClaudeTextPart(parts []claudeContentPart) string {
	for _, part := range parts {
		if strings.EqualFold(part.Type, "text") {
			if text := strings.TrimSpace(part.Text); text != "" {
				return text
			}
		}
	}

	return ""
}

type claudeMessagesRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	System      string          `json:"system"`
	Messages    []claudeMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeMessagesResponse struct {
	Content []claudeContentPart `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type claudeContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
