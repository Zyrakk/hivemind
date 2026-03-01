package notify

import (
	"context"
	"errors"
)

var ErrNotImplemented = errors.New("not implemented")

type Bot struct {
	Token         string
	AllowedChatID int64
}

func NewBot(token string, allowedChatID int64) *Bot {
	return &Bot{
		Token:         token,
		AllowedChatID: allowedChatID,
	}
}

func (b *Bot) SendMessage(ctx context.Context, message string) error {
	_ = ctx
	_ = message
	return ErrNotImplemented
}

func (b *Bot) HandleCommand(ctx context.Context, command string) error {
	_ = ctx
	_ = command
	return ErrNotImplemented
}
