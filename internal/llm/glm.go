package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultGLMBaseURL = "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions"
	defaultGLMModel   = "glm-4.7"

	defaultRequestTimeout = 60 * time.Second
	defaultTemperature    = 0.3
	defaultMaxTokens      = 4096
)

type GLMConfig struct {
	APIKey       string
	BaseURL      string
	Model        string
	Timeout      time.Duration
	HTTPClient   *http.Client
	Logger       *slog.Logger
	Temperature  float64
	MaxTokens    int
	PromptDir    string
	RetryBackoff []time.Duration
}

type GLMClient struct {
	apiKey      string
	baseURL     string
	model       string
	httpClient  *http.Client
	logger      *slog.Logger
	temperature float64
	maxTokens   int
	promptDir   string

	tokenMu      sync.Mutex
	tokenCounter TokenUsage
	retryBackoff []time.Duration
}

func NewGLMClient(config GLMConfig) *GLMClient {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultGLMBaseURL
	}

	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = defaultGLMModel
	}

	temperature := config.Temperature
	if temperature == 0 {
		temperature = defaultTemperature
	}

	maxTokens := config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	promptDir := strings.TrimSpace(config.PromptDir)
	if promptDir == "" {
		promptDir = "prompts"
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	httpClient := buildHTTPClient(config.HTTPClient, config.Timeout)

	retryBackoff := config.RetryBackoff
	if len(retryBackoff) == 0 {
		retryBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	}

	return &GLMClient{
		apiKey:       strings.TrimSpace(config.APIKey),
		baseURL:      baseURL,
		model:        model,
		httpClient:   httpClient,
		logger:       logger,
		temperature:  temperature,
		maxTokens:    maxTokens,
		promptDir:    promptDir,
		retryBackoff: append([]time.Duration(nil), retryBackoff...),
	}
}

func buildHTTPClient(client *http.Client, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}

	if client == nil {
		return &http.Client{Timeout: timeout}
	}

	clone := *client
	clone.Timeout = timeout
	return &clone
}

func (c *GLMClient) Plan(ctx context.Context, directive, agentsMD string) (*TaskPlan, error) {
	systemPrompt, err := c.loadPrompt("planner.txt")
	if err != nil {
		return nil, err
	}

	userMessage := fmt.Sprintf("Directive:\n%s\n\nAGENTS.md:\n%s", directive, agentsMD)
	content, _, err := c.Chat(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, err
	}

	plan, err := parseTaskPlan(content)
	if err != nil {
		return nil, err
	}

	return plan, nil
}

func (c *GLMClient) Evaluate(ctx context.Context, task, diff, agentsMD string) (*Evaluation, error) {
	systemPrompt, err := c.loadPrompt("evaluator.txt")
	if err != nil {
		return nil, err
	}

	userMessage := fmt.Sprintf("Task:\n%s\n\nDiff:\n%s\n\nAGENTS.md:\n%s", task, diff, agentsMD)
	content, _, err := c.Chat(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, err
	}

	evaluation, err := parseEvaluation(content)
	if err != nil {
		return nil, err
	}

	return evaluation, nil
}

func (c *GLMClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, TokenUsage, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return "", TokenUsage{}, errors.New("glm api key is empty")
	}

	requestPayload := glmChatRequest{
		Model: c.model,
		Messages: []glmMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		},
		Temperature: c.temperature,
		MaxTokens:   c.maxTokens,
		ResponseFormat: glmResponseFormat{
			Type: "json_object",
		},
	}

	encodedRequest, err := json.Marshal(requestPayload)
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("marshal glm request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		start := time.Now()
		content, usage, statusCode, retryAfter, requestErr := c.doChatRequest(ctx, encodedRequest)
		latency := time.Since(start)

		if requestErr == nil {
			c.logger.Info(
				"glm request completed",
				slog.String("model", c.model),
				slog.Int("status", statusCode),
				slog.Duration("latency", latency),
				slog.Int("prompt_tokens", usage.PromptTokens),
				slog.Int("completion_tokens", usage.CompletionTokens),
				slog.Int("total_tokens", usage.TotalTokens),
			)
			c.addTokens(usage)
			return content, usage, nil
		}

		c.logger.Warn(
			"glm request failed",
			slog.String("model", c.model),
			slog.Int("status", statusCode),
			slog.Duration("latency", latency),
			slog.Int("prompt_tokens", usage.PromptTokens),
			slog.Int("completion_tokens", usage.CompletionTokens),
			slog.Int("total_tokens", usage.TotalTokens),
			slog.Int("attempt", attempt+1),
			slog.Any("error", requestErr),
		)

		shouldRetry := statusCode == http.StatusTooManyRequests || (statusCode >= http.StatusInternalServerError && statusCode <= 599)
		if !shouldRetry || attempt >= len(c.retryBackoff) {
			return "", TokenUsage{}, requestErr
		}

		delay := c.retryBackoff[attempt]
		if statusCode == http.StatusTooManyRequests && retryAfter != nil {
			delay = *retryAfter
		}

		if err := sleepContext(ctx, delay); err != nil {
			return "", TokenUsage{}, err
		}
	}
}

func (c *GLMClient) doChatRequest(ctx context.Context, payload []byte) (string, TokenUsage, int, *time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return "", TokenUsage{}, 0, nil, fmt.Errorf("build glm request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", TokenUsage{}, 0, nil, fmt.Errorf("execute glm request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", TokenUsage{}, resp.StatusCode, nil, fmt.Errorf("read glm response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiError := extractAPIError(body)
		var retryAfter *time.Duration
		if resp.StatusCode == http.StatusTooManyRequests {
			if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
				retryAfter = &d
			}
		}
		return "", TokenUsage{}, resp.StatusCode, retryAfter, fmt.Errorf("glm api returned status %d: %s", resp.StatusCode, apiError)
	}

	var completion glmChatResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return "", TokenUsage{}, resp.StatusCode, nil, fmt.Errorf("decode glm response: %w", err)
	}

	if len(completion.Choices) == 0 {
		return "", TokenUsage{}, resp.StatusCode, nil, errors.New("glm response missing choices")
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return "", TokenUsage{}, resp.StatusCode, nil, errors.New("glm response returned empty content")
	}

	return content, completion.Usage, resp.StatusCode, nil, nil
}

func (c *GLMClient) GetTokensUsed() TokenUsage {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	return c.tokenCounter
}

func (c *GLMClient) addTokens(usage TokenUsage) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	c.tokenCounter.PromptTokens += usage.PromptTokens
	c.tokenCounter.CompletionTokens += usage.CompletionTokens
	c.tokenCounter.TotalTokens += usage.TotalTokens
}

func (c *GLMClient) loadPrompt(filename string) (string, error) {
	promptPath, err := c.resolvePromptPath(filename)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("read prompt %q: %w", filename, err)
	}

	return strings.TrimSpace(string(data)), nil
}

var glmPromptFallbackDirs = []string{"/app/prompts"}

func (c *GLMClient) resolvePromptPath(filename string) (string, error) {
	if filepath.IsAbs(c.promptDir) {
		path := filepath.Join(c.promptDir, filename)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		// Try fallback directories.
		for _, fallback := range glmPromptFallbackDirs {
			candidate := filepath.Join(fallback, filename)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("prompt %q not found in %s", filename, c.promptDir)
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	searchDir := workingDir
	for {
		candidate := filepath.Join(searchDir, c.promptDir, filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			break
		}
		searchDir = parent
	}

	// Try fallback directories.
	for _, fallback := range glmPromptFallbackDirs {
		candidate := filepath.Join(fallback, filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("prompt %q not found (searched from %s)", filename, workingDir)
}

func parseTaskPlan(content string) (*TaskPlan, error) {
	payload := []byte(content)

	root, err := requireFields(payload, []string{"confidence", "tasks", "questions", "notes"})
	if err != nil {
		return nil, fmt.Errorf("invalid task plan json: %w", err)
	}

	var taskItems []json.RawMessage
	if err := json.Unmarshal(root["tasks"], &taskItems); err != nil {
		return nil, fmt.Errorf("invalid task plan tasks: %w", err)
	}

	taskFields := []string{"id", "title", "description", "acceptance_criteria", "files_affected", "depends_on", "estimated_complexity", "branch_name"}
	for idx, taskPayload := range taskItems {
		if _, err := requireFields(taskPayload, taskFields); err != nil {
			return nil, fmt.Errorf("invalid task plan task[%d]: %w", idx, err)
		}
	}

	var plan TaskPlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return nil, fmt.Errorf("decode task plan: %w", err)
	}

	return &plan, nil
}

func parseEvaluation(content string) (*Evaluation, error) {
	payload := []byte(content)

	root, err := requireFields(payload, []string{"verdict", "confidence", "completeness", "correctness", "conventions", "scope_ok", "issues", "summary"})
	if err != nil {
		return nil, fmt.Errorf("invalid evaluation json: %w", err)
	}

	var issueItems []json.RawMessage
	if err := json.Unmarshal(root["issues"], &issueItems); err != nil {
		return nil, fmt.Errorf("invalid evaluation issues: %w", err)
	}

	issueFields := []string{"severity", "description", "suggestion"}
	for idx, issuePayload := range issueItems {
		if _, err := requireFields(issuePayload, issueFields); err != nil {
			return nil, fmt.Errorf("invalid issue[%d]: %w", idx, err)
		}
	}

	var evaluation Evaluation
	if err := json.Unmarshal(payload, &evaluation); err != nil {
		return nil, fmt.Errorf("decode evaluation: %w", err)
	}

	return &evaluation, nil
}

func requireFields(payload []byte, required []string) (map[string]json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, err
	}

	for _, field := range required {
		if _, ok := root[field]; !ok {
			return nil, fmt.Errorf("missing required field %q", field)
		}
	}

	return root, nil
}

func extractAPIError(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "empty error response"
	}

	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &parsed); err == nil {
		if msg := strings.TrimSpace(parsed.Error.Message); msg != "" {
			return msg
		}
	}

	if len(trimmed) > 300 {
		trimmed = trimmed[:300]
	}

	return trimmed
}

func parseRetryAfter(headerValue string) (time.Duration, bool) {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return 0, false
	}

	if seconds, err := strconv.Atoi(headerValue); err == nil {
		if seconds < 0 {
			seconds = 0
		}
		return time.Duration(seconds) * time.Second, true
	}

	timestamp, err := http.ParseTime(headerValue)
	if err != nil {
		return 0, false
	}

	delay := time.Until(timestamp)
	if delay < 0 {
		delay = 0
	}

	return delay, true
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type glmChatRequest struct {
	Model          string            `json:"model"`
	Messages       []glmMessage      `json:"messages"`
	Temperature    float64           `json:"temperature"`
	MaxTokens      int               `json:"max_tokens"`
	ResponseFormat glmResponseFormat `json:"response_format"`
}

type glmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type glmResponseFormat struct {
	Type string `json:"type"`
}

type glmChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage TokenUsage `json:"usage"`
}
