package notify

import (
	"strings"
	"testing"
)

func TestParseRoadmapArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        string
		wantProject string
		wantRoadmap string
	}{
		{
			name:        "single line",
			args:        "flux Build a REST API with auth and metrics",
			wantProject: "flux",
			wantRoadmap: "Build a REST API with auth and metrics",
		},
		{
			name:        "multiline",
			args:        "flux\nBuild REST API\nAdd auth\nAdd metrics",
			wantProject: "flux",
			wantRoadmap: "Build REST API\nAdd auth\nAdd metrics",
		},
		{
			name:        "empty",
			args:        "",
			wantProject: "",
			wantRoadmap: "",
		},
		{
			name:        "project only",
			args:        "flux",
			wantProject: "flux",
			wantRoadmap: "",
		},
		{
			name:        "with leading whitespace",
			args:        "  flux  Build something cool  ",
			wantProject: "flux",
			wantRoadmap: "Build something cool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, roadmap := parseRoadmapArgs(tt.args)
			if project != tt.wantProject {
				t.Fatalf("project = %q, want %q", project, tt.wantProject)
			}
			if roadmap != tt.wantRoadmap {
				t.Fatalf("roadmap = %q, want %q", roadmap, tt.wantRoadmap)
			}
		})
	}
}

func TestFormatRoadmapMessage_WarningMarkers(t *testing.T) {
	t.Parallel()

	msg := FormatRoadmapMessage("test-proj", "roadmap-1", nil, 0, 0)
	if !strings.Contains(msg, "ROADMAP") {
		t.Fatal("expected ROADMAP header")
	}
	if !strings.Contains(msg, "test-proj") {
		t.Fatal("expected project name")
	}
}
