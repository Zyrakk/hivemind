package launcher

import (
	"context"
	"errors"
	"time"
)

var ErrNotImplemented = errors.New("not implemented")

type WorkerSpec struct {
	ProjectID       int64
	TaskDescription string
	Branch          string
}

type WorkerSession struct {
	ID         string
	Status     string
	StartedAt  time.Time
	FinishedAt *time.Time
}

type Manager struct {
	binaryPath string
	timeout    time.Duration
}

func New(binaryPath string, timeout time.Duration) *Manager {
	return &Manager{
		binaryPath: binaryPath,
		timeout:    timeout,
	}
}

func (m *Manager) Launch(ctx context.Context, spec WorkerSpec) (*WorkerSession, error) {
	_ = ctx
	_ = spec
	return nil, ErrNotImplemented
}

func (m *Manager) Monitor(ctx context.Context, sessionID string) (*WorkerSession, error) {
	_ = ctx
	_ = sessionID
	return nil, ErrNotImplemented
}

func (m *Manager) Stop(ctx context.Context, sessionID string) error {
	_ = ctx
	_ = sessionID
	return ErrNotImplemented
}
