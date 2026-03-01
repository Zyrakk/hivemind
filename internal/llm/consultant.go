package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const consultationConfidenceThreshold = 0.6

type Opinion struct {
	ConsultationType  string   `json:"consultation_type"`
	AgreeWithOriginal bool     `json:"agree_with_original"`
	Analysis          string   `json:"analysis"`
	Recommendations   []string `json:"recommendations"`
	RiskFlags         []string `json:"risk_flags"`
	Confidence        float64  `json:"confidence"`
}

type ConsultantClient interface {
	Consult(ctx context.Context, consultationType string, context string, question string) (*Opinion, error)
	GetName() string
	GetBudgetRemaining() float64
	IsAvailable() bool
}

func ConsultIfNeeded(
	ctx context.Context,
	confidence float64,
	consultationType string,
	consultationContext string,
	question string,
	claude ConsultantClient,
	gemini ConsultantClient,
) (*Opinion, error) {
	if confidence >= consultationConfidenceThreshold {
		return nil, nil
	}

	var failures []error
	if claude != nil && claude.IsAvailable() {
		opinion, err := claude.Consult(ctx, consultationType, consultationContext, question)
		if err == nil {
			return opinion, nil
		}
		failures = append(failures, fmt.Errorf("consult %s: %w", claude.GetName(), err))
	}

	if gemini != nil && gemini.IsAvailable() {
		opinion, err := gemini.Consult(ctx, consultationType, consultationContext, question)
		if err == nil {
			return opinion, nil
		}
		failures = append(failures, fmt.Errorf("consult %s: %w", gemini.GetName(), err))
	}

	if len(failures) == 0 {
		return nil, nil
	}

	return nil, errors.Join(failures...)
}

func loadConsultantPrompt(promptDir string) (string, error) {
	path, err := resolvePromptPath(promptDir, "consultant.txt")
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read consultant prompt: %w", err)
	}

	return strings.TrimSpace(string(data)), nil
}

func resolvePromptPath(promptDir, filename string) (string, error) {
	if strings.TrimSpace(promptDir) == "" {
		promptDir = "prompts"
	}

	if filepath.IsAbs(promptDir) {
		candidate := filepath.Join(promptDir, filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		return "", fmt.Errorf("prompt %q not found in %s", filename, promptDir)
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	searchDir := workingDir
	for {
		candidate := filepath.Join(searchDir, promptDir, filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			break
		}
		searchDir = parent
	}

	return "", fmt.Errorf("prompt %q not found (searched from %s)", filename, workingDir)
}

func parseOpinion(content string) (*Opinion, error) {
	jsonPayload, err := extractJSONObject(content)
	if err != nil {
		return nil, err
	}

	required := []string{
		"consultation_type",
		"agree_with_original",
		"analysis",
		"recommendations",
		"risk_flags",
		"confidence",
	}
	if _, err := requireFields(jsonPayload, required); err != nil {
		return nil, fmt.Errorf("invalid opinion json: %w", err)
	}

	var opinion Opinion
	if err := json.Unmarshal(jsonPayload, &opinion); err != nil {
		return nil, fmt.Errorf("decode opinion: %w", err)
	}

	return &opinion, nil
}

func extractJSONObject(content string) ([]byte, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, errors.New("empty response")
	}

	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```JSON")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimSpace(trimmed)
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 || end < start {
		return nil, errors.New("response does not contain a json object")
	}

	return []byte(trimmed[start : end+1]), nil
}

func buildConsultantUserMessage(consultationType, consultationContext, question string) string {
	return fmt.Sprintf(
		"Consultation type: %s\n\nContext:\n%s\n\nQuestion:\n%s",
		consultationType,
		consultationContext,
		question,
	)
}

func estimateTokenCount(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}

	// Fast approximation for budgeting: ~4 chars/token for mixed English/Spanish technical text.
	return int(math.Ceil(float64(len(trimmed)) / 4.0))
}
