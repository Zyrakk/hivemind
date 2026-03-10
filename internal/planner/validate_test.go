package planner

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDirective(t *testing.T) {
	tests := []struct {
		name      string
		directive string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "valid directive with verb and noun",
			directive: "Add a --json output flag to the audit command",
			wantErr:   false,
		},
		{
			name:      "valid directive with config noun",
			directive: "Implement YAML/JSON config parser for scoring rule definitions in the module",
			wantErr:   false,
		},
		{
			name:      "too short",
			directive: "fix bugs",
			wantErr:   true,
			errSubstr: "too vague",
		},
		{
			name:      "too long",
			directive: generateLongDirective(250),
			wantErr:   true,
			errSubstr: "too long",
		},
		{
			name:      "no verb",
			directive: "the audit command json output flag for the endpoint config module test",
			wantErr:   true,
			errSubstr: "action verb",
		},
		{
			name:      "no noun",
			directive: "add and implement the new thing for the refactored streaming output layer",
			wantErr:   true,
			errSubstr: "scope noun",
		},
		{
			name:      "contains URL",
			directive: "fix the issue described at https://github.com/issue/123 in the command module",
			wantErr:   true,
			errSubstr: "URL",
		},
		{
			name:      "empty directive",
			directive: "",
			wantErr:   true,
			errSubstr: "empty",
		},
		{
			name:      "whitespace only",
			directive: "   \t\n  ",
			wantErr:   true,
			errSubstr: "empty",
		},
		{
			name:      "exactly 8 words with verb and noun",
			directive: "Add a streaming writer to the command module",
			wantErr:   false,
		},
		{
			name:      "valid directive with handler noun",
			directive: "Add a request handler for the healthcheck route in the service layer",
			wantErr:   false,
		},
		{
			name:      "valid directive with metric noun",
			directive: "Implement Prometheus metric collection for audit request latency tracking",
			wantErr:   false,
		},
		{
			name:      "valid directive with table noun",
			directive: "Create a migration to add the batches table to the SQLite schema",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateDirective(tt.directive)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var valErr *ErrDirectiveInvalid
				if !errors.As(err, &valErr) {
					t.Fatalf("expected ErrDirectiveInvalid, got %T: %v", err, err)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result == "" {
					t.Fatal("expected non-empty result")
				}
			}
		})
	}
}

func generateLongDirective(wordCount int) string {
	// Start with verb + noun to pass those checks, then pad.
	words := []string{"Add", "a", "new", "command", "that", "does", "something", "for"}
	for len(words) < wordCount {
		words = append(words, "word")
	}
	return strings.Join(words, " ")
}
