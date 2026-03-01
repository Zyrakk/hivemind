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
	"net/url"
	"strings"
	"time"
)

const (
	defaultGeminiBaseURL     = "https://generativelanguage.googleapis.com/v1beta/models"
	defaultGeminiModel       = "gemini-2.5-flash"
	defaultGeminiTemperature = 0.2
)

type GeminiConfig struct {
	APIKey  string
	BaseURL string
	Model   string

	HTTPClient *http.Client
	Timeout    time.Duration
	Logger     *slog.Logger

	PromptDir    string
	Temperature  float64
	Budget       *BudgetTracker
	ResponseMIME string
}

type GeminiClient struct {
	apiKey      string
	baseURL     string
	model       string
	httpClient  *http.Client
	logger      *slog.Logger
	promptDir   string
	temperature float64

	responseMIME string
	budget       *BudgetTracker
}

func NewGeminiClient(config GeminiConfig) *GeminiClient {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultGeminiBaseURL
	}

	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = defaultGeminiModel
	}

	promptDir := strings.TrimSpace(config.PromptDir)
	if promptDir == "" {
		promptDir = "prompts"
	}

	temperature := config.Temperature
	if temperature == 0 {
		temperature = defaultGeminiTemperature
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	responseMIME := strings.TrimSpace(config.ResponseMIME)
	if responseMIME == "" {
		responseMIME = "application/json"
	}

	return &GeminiClient{
		apiKey:       strings.TrimSpace(config.APIKey),
		baseURL:      baseURL,
		model:        model,
		httpClient:   buildHTTPClient(config.HTTPClient, config.Timeout),
		logger:       logger,
		promptDir:    promptDir,
		temperature:  temperature,
		responseMIME: responseMIME,
		budget:       config.Budget,
	}
}

func (c *GeminiClient) Consult(ctx context.Context, consultationType string, consultationContext string, question string) (*Opinion, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, errors.New("gemini api key is empty")
	}

	if !c.IsAvailable() {
		return nil, fmt.Errorf("%s budget exhausted", c.GetName())
	}

	if c.budget != nil && !c.budget.CanAfford(0) {
		return nil, fmt.Errorf("%s daily call budget reached", c.GetName())
	}

	systemPrompt, err := loadConsultantPrompt(c.promptDir)
	if err != nil {
		return nil, err
	}

	userMessage := buildConsultantUserMessage(consultationType, consultationContext, question)

	payload := geminiGenerateContentRequest{
		SystemInstruction: geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		},
		Contents: []geminiContent{
			{
				Role:  "user",
				Parts: []geminiPart{{Text: userMessage}},
			},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:      c.temperature,
			ResponseMIMEType: c.responseMIME,
		},
	}

	requestBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini request: %w", err)
	}

	endpoint, err := c.endpointURL()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("build gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute gemini request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read gemini response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("gemini api returned status %d: %s", resp.StatusCode, extractAPIError(respBody))
	}

	var completion geminiGenerateContentResponse
	if err := json.Unmarshal(respBody, &completion); err != nil {
		return nil, fmt.Errorf("decode gemini response: %w", err)
	}

	textContent := strings.TrimSpace(firstGeminiText(completion.Candidates))
	if textContent == "" {
		return nil, errors.New("gemini response returned empty content")
	}

	opinion, err := parseOpinion(textContent)
	if err != nil {
		return nil, err
	}

	if c.budget != nil {
		c.budget.Record(0)
	}

	latency := time.Since(start)
	c.logger.Info(
		"gemini consultation completed",
		slog.String("model", c.model),
		slog.String("consultation_type", consultationType),
		slog.Int("status", resp.StatusCode),
		slog.Duration("latency", latency),
		slog.Int("prompt_tokens", completion.UsageMetadata.PromptTokenCount),
		slog.Int("completion_tokens", completion.UsageMetadata.CandidatesTokenCount),
		slog.Int("total_tokens", completion.UsageMetadata.TotalTokenCount),
	)

	return opinion, nil
}

func (c *GeminiClient) GetName() string {
	return "gemini"
}

func (c *GeminiClient) GetBudgetRemaining() float64 {
	if c.budget == nil {
		return math.Inf(1)
	}

	return c.budget.Remaining()
}

func (c *GeminiClient) IsAvailable() bool {
	if strings.TrimSpace(c.apiKey) == "" {
		return false
	}
	if c.budget == nil {
		return true
	}

	return c.budget.CanAfford(0)
}

func (c *GeminiClient) endpointURL() (string, error) {
	base := strings.TrimSpace(c.baseURL)
	if base == "" {
		base = defaultGeminiBaseURL
	}

	endpoint := strings.TrimRight(base, "/")
	if strings.Contains(endpoint, "{model}") {
		endpoint = strings.ReplaceAll(endpoint, "{model}", c.model)
	} else if !strings.HasSuffix(endpoint, ":generateContent") {
		endpoint = endpoint + "/" + c.model + ":generateContent"
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse gemini endpoint: %w", err)
	}

	q := parsed.Query()
	q.Set("key", c.apiKey)
	parsed.RawQuery = q.Encode()

	return parsed.String(), nil
}

func firstGeminiText(candidates []geminiCandidate) string {
	for _, candidate := range candidates {
		for _, part := range candidate.Content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				return text
			}
		}
	}

	return ""
}

type geminiGenerateContentRequest struct {
	SystemInstruction geminiContent          `json:"system_instruction"`
	Contents          []geminiContent        `json:"contents"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
}

type geminiGenerationConfig struct {
	Temperature      float64 `json:"temperature,omitempty"`
	ResponseMIMEType string  `json:"responseMimeType,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerateContentResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}
