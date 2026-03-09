package checklist

// CheckResult carries the outcome of a single automated check.
type CheckResult struct {
	Description string
	Command     string
	Passed      bool
	Skipped     bool
	Output      string
}

// UserCheck describes a manual verification step.
type UserCheck struct {
	Description string
}
