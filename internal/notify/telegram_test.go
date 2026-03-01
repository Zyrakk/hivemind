package notify

import (
	"context"
	"testing"
)

func TestSendMessageReturnsNotImplemented(t *testing.T) {
	bot := NewBot("", 0)
	if err := bot.SendMessage(context.Background(), "ping"); err == nil {
		t.Fatal("expected not implemented error")
	}
}
