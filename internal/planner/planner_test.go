package planner

import (
	"context"
	"testing"
)

func TestBuildPlanReturnsNotImplemented(t *testing.T) {
	svc := New(nil, "")
	_, err := svc.BuildPlan(context.Background(), "implement feature", "agent docs")
	if err == nil {
		t.Fatal("expected not implemented error")
	}
}
