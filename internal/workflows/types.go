package workflows

import "time"

// Payloads carry identifiers, counts and timestamps, never repository
// content: evidence and outcomes live in Postgres, keyed by CheckKey
// (DESIGN-0026 § Workflows).

// Priority is a Temporal task priority key. Lower runs first.
type Priority int

// Priorities by trigger (DESIGN-0026 § Rate budget).
const (
	PriorityWebhook  Priority = 2
	PrioritySchedule Priority = 3
	PriorityRollout  Priority = 4
)

// Trigger values, matching store.Trigger and the checks.trigger CHECK
// constraint.
const (
	TriggerSchedule      = "schedule"
	TriggerWebhook       = "webhook"
	TriggerPush          = "push"
	TriggerDiscovery     = "discovery"
	TriggerPolicyRollout = "policy_rollout"
	TriggerBootstrap     = "bootstrap"
)

// Park reasons, matching store.ParkReason.
const (
	ParkAccessDenied = "access_denied"
	ParkArchived     = "archived"
	ParkFork         = "fork"
)

// CheckKind says how a CheckRepo activity ended.
type CheckKind string

// CheckKind values. A failed check is an activity error, not a kind.
const (
	CheckChecked  CheckKind = "checked"
	CheckDeferred CheckKind = "deferred"
	CheckParked   CheckKind = "parked"
)

// CheckRepoInput is the CheckRepo activity input.
type CheckRepoInput struct {
	RepositoryID int64
	CheckKey     string
	Trigger      string

	// Deferrals counts consecutive Deferred results before this one. It
	// keys the backoff when a throttle carries no usable reset time.
	Deferrals int
}

// Rate is an installation's X-RateLimit-* values as the check last saw
// them.
type Rate struct {
	Limit      int
	Remaining  int
	ResetAt    time.Time
	ObservedAt time.Time
}

// CheckRepoResult is the CheckRepo activity result.
type CheckRepoResult struct {
	Kind     CheckKind
	CheckKey string

	// Calls is the number of GitHub requests the check sent, and Rate
	// the last rate-limit headers it saw (nil when none were seen).
	Calls int
	Rate  *Rate

	// Until is when a Deferred check may run again.
	Until time.Time

	// ParkReason, ClearFindings and Cause describe a Parked check.
	// Cause is the clipped error text, empty for archived and fork.
	ParkReason    string
	ClearFindings bool
	Cause         string

	// Checked-only fields, handed to RecordCheck.
	InstallationID int64
	PolicyVersion  string
	ProviderRepoID *int64
	Org            string
	Name           string
	CatalogParseOK *bool
	StartedAt      time.Time
	FinishedAt     time.Time
}

// RecordCheckInput is the RecordCheck activity input: a Checked result
// plus the trigger that started it. The outcomes are read back from the
// staged checks row.
type RecordCheckInput struct {
	RepositoryID int64
	Trigger      string
	Result       CheckRepoResult
}

// RecordCheckResult summarizes what RecordCheck applied.
type RecordCheckResult struct {
	Transitions  int
	AlreadyFinal bool
}

// RecordCheckErrorInput is the RecordCheckError activity input.
type RecordCheckErrorInput struct {
	RepositoryID int64
	CheckKey     string
	Trigger      string
	StartedAt    time.Time
	FinishedAt   time.Time
	Error        string
}

// ParkInput is the Park activity input.
type ParkInput struct {
	RepositoryID   int64
	InstallationID int64
	CheckKey       string
	Trigger        string
	Reason         string
	ClearFindings  bool
	Cause          string
}

// Recheck is the recheck signal payload.
type Recheck struct {
	Trigger  string
	Priority Priority
}

// PolicyChanged is the policy_changed signal payload: re-check by By,
// at a random point between now and then.
type PolicyChanged struct {
	Version string
	By      time.Time
}

// Park is the park signal payload.
type Park struct {
	Reason        string
	ClearFindings bool
}

// RepoWorkflowInput is RepoWorkflow's input and its ContinueAsNew
// state.
type RepoWorkflowInput struct {
	RepositoryID   int64
	InstallationID int64

	// CheckInterval is the time between scheduled checks.
	CheckInterval time.Duration

	// NextDue is when the next scheduled check runs. Zero runs it now.
	NextDue time.Time

	// Pending is a signalled re-check that has not run yet.
	Pending *Recheck

	// PolicyVersion is the last policy version this workflow was told
	// about.
	PolicyVersion string

	// Iteration counts checks across ContinueAsNew; it makes check keys
	// unique within a run.
	Iteration int
}

// AcquireInput is the AcquireBudget activity input. UpdateID makes a
// retried acquire return the first attempt's answer instead of taking a
// second lease.
type AcquireInput struct {
	InstallationID int64
	UpdateID       string
	Request        AcquireRequest
}
