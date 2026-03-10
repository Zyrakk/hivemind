package planner

import (
	"fmt"
	"regexp"
	"strings"
)

// ErrDirectiveInvalid is a sentinel error type for directive validation failures.
// Callers can use errors.As to detect validation errors and handle them distinctly.
type ErrDirectiveInvalid struct {
	Reason string
}

func (e *ErrDirectiveInvalid) Error() string {
	return e.Reason
}

var (
	directiveVerbs = map[string]struct{}{
		"add": {}, "implement": {}, "create": {}, "replace": {},
		"wire": {}, "remove": {}, "update": {}, "fix": {},
		"refactor": {},
	}

	directiveNouns = map[string]struct{}{
		// Structure
		"command": {}, "flag": {}, "endpoint": {}, "function": {},
		"module": {}, "file": {}, "test": {}, "config": {},
		// Code
		"handler": {}, "middleware": {}, "client": {}, "service": {},
		"worker": {}, "parser": {}, "reporter": {}, "writer": {},
		// Data
		"table": {}, "column": {}, "schema": {}, "migration": {},
		"model": {}, "route": {},
		// Infra
		"metric": {}, "logger": {}, "webhook": {}, "probe": {},
		"pipeline": {},
		// UI
		"page": {}, "component": {}, "view": {}, "dashboard": {},
	}

	urlPattern = regexp.MustCompile(`https?://`)
)

// ValidateDirective checks that a directive meets minimum quality standards
// before spending engine quota on planning. Returns the cleaned directive
// on success or an error with a helpful suggestion on failure.
func ValidateDirective(directive string) (string, error) {
	cleaned := strings.TrimSpace(directive)
	if cleaned == "" {
		return "", &ErrDirectiveInvalid{Reason: "directive is empty"}
	}

	words := strings.Fields(cleaned)

	if len(words) < 8 {
		return "", &ErrDirectiveInvalid{
			Reason: "directive too vague — include what to add/change and where (e.g., 'Add a --json flag to the audit command')",
		}
	}

	if len(words) > 200 {
		return "", &ErrDirectiveInvalid{
			Reason: "directive too long — break it into smaller tasks or add context to AGENTS.md instead",
		}
	}

	if urlPattern.MatchString(cleaned) {
		return "", &ErrDirectiveInvalid{
			Reason: "directive contains a URL — describe the change directly instead of linking to an issue",
		}
	}

	hasVerb := false
	for _, w := range words {
		if _, ok := directiveVerbs[strings.ToLower(w)]; ok {
			hasVerb = true
			break
		}
	}
	if !hasVerb {
		return "", &ErrDirectiveInvalid{
			Reason: fmt.Sprintf("directive missing an action verb — use one of: %s", joinSorted(directiveVerbs)),
		}
	}

	hasNoun := false
	for _, w := range words {
		if _, ok := directiveNouns[strings.ToLower(w)]; ok {
			hasNoun = true
			break
		}
	}
	if !hasNoun {
		return "", &ErrDirectiveInvalid{
			Reason: fmt.Sprintf("directive missing a scope noun — use one of: %s", joinSorted(directiveNouns)),
		}
	}

	return cleaned, nil
}

func joinSorted(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple sort for deterministic output.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return strings.Join(keys, ", ")
}
