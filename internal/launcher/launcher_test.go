package launcher

import (
	"context"
	"testing"
	"time"
)

func TestLaunchReturnsNotImplemented(t *testing.T) {
	mgr := New("codex", 5*time.Minute)
	_, err := mgr.Launch(context.Background(), WorkerSpec{})
	if err == nil {
		t.Fatal("expected not implemented error")
	}
}
