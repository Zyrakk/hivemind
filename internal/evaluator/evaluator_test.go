package evaluator

import (
	"context"
	"testing"
)

func TestEvaluateReturnsNotImplemented(t *testing.T) {
	svc := New(nil, "")
	_, err := svc.Evaluate(context.Background(), "diff", "criteria")
	if err == nil {
		t.Fatal("expected not implemented error")
	}
}
