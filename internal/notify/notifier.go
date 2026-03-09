package notify

import "context"

// Notifier abstracts outbound notifications and inbound lifecycle hooks.
type Notifier interface {
	// Lifecycle
	Start(ctx context.Context) error
	Stop() error

	// Outbound notifications
	NotifyNeedsInput(ctx context.Context, projectID, question, approvalID string) error
	NotifyPRReady(ctx context.Context, projectID, branch, approvalID string, autoResults []CheckResult, userChecks []UserCheck) error
	NotifyWorkerFailed(ctx context.Context, projectID, taskTitle, errMsg string) error
	NotifyTaskCompleted(ctx context.Context, projectID, taskTitle string) error
	NotifyConsultantUsed(ctx context.Context, consultantName, question, summary string) error
	NotifyBudgetWarning(ctx context.Context, consultantName string, percentUsed float64) error
	NotifyProgress(ctx context.Context, project, stage, detail string) error
}

type NoopNotifier struct{}

func (n NoopNotifier) Start(ctx context.Context) error {
	_ = ctx
	return nil
}

func (n NoopNotifier) Stop() error {
	return nil
}

func (n NoopNotifier) NotifyNeedsInput(ctx context.Context, projectID, question, approvalID string) error {
	_ = ctx
	_ = projectID
	_ = question
	_ = approvalID
	return nil
}

func (n NoopNotifier) NotifyPRReady(ctx context.Context, projectID, branch, approvalID string, autoResults []CheckResult, userChecks []UserCheck) error {
	_ = ctx
	_ = projectID
	_ = branch
	_ = approvalID
	_ = autoResults
	_ = userChecks
	return nil
}

func (n NoopNotifier) NotifyWorkerFailed(ctx context.Context, projectID, taskTitle, errMsg string) error {
	_ = ctx
	_ = projectID
	_ = taskTitle
	_ = errMsg
	return nil
}

func (n NoopNotifier) NotifyTaskCompleted(ctx context.Context, projectID, taskTitle string) error {
	_ = ctx
	_ = projectID
	_ = taskTitle
	return nil
}

func (n NoopNotifier) NotifyConsultantUsed(ctx context.Context, consultantName, question, summary string) error {
	_ = ctx
	_ = consultantName
	_ = question
	_ = summary
	return nil
}

func (n NoopNotifier) NotifyBudgetWarning(ctx context.Context, consultantName string, percentUsed float64) error {
	_ = ctx
	_ = consultantName
	_ = percentUsed
	return nil
}

func (n NoopNotifier) NotifyProgress(ctx context.Context, project, stage, detail string) error {
	_ = ctx
	_ = project
	_ = stage
	_ = detail
	return nil
}

var (
	_ Notifier = (*TelegramBot)(nil)
	_ Notifier = NoopNotifier{}
)
