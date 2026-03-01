package dashboard

import "testing"

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Port == 0 {
		t.Fatal("expected default port")
	}
	if cfg.Host == "" {
		t.Fatal("expected default host")
	}
}
