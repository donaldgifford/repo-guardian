package workflows

import "time"

// Payloads carry identifiers, counts and timestamps, never repository
// content: evidence and outcomes live in Postgres, keyed by CheckKey
// (DESIGN-0026 § Workflows).

// Priority is a Temporal task priority key. Lower runs first.
//
// Priority orders tasks within one task queue only (INV-0022 F5): the
// evaluation and remediation queues are dispatched independently, so a
// priority-1 remediation task never jumps an evaluation task, and the
// two halves never compete for a slot.
type Priority int

// Priorities by trigger (DESIGN-0026 § Rate budget).
const (
	// PriorityHuman is a check or remediation a person asked for (the
	// API's recheck, a UI action): ahead of every automatic trigger.
	PriorityHuman    Priority = 1
	PriorityWebhook  Priority = 2
	PrioritySchedule Priority = 3
	PriorityRollout  Priority = 4

	// PriorityManual is a person-requested evaluation (the evaluate
	// command, an API recheck): the same key as PriorityHuman.
	PriorityManual Priority = 1
)

// Trigger values, matching the checks.trigger CHECK constraint.
const (
	TriggerSchedule      = "schedule"
	TriggerWebhook       = "webhook"
	TriggerPush          = "push"
	TriggerDiscovery     = "discovery"
	TriggerPolicyRollout = "policy_rollout"
	TriggerPREvent       = "pr_event"
	TriggerManual        = "manual"
)

// Park reasons, matching store.ParkReason.
const (
	ParkAccessDenied = "access_denied"
	ParkArchived     = "archived"
	ParkFork         = "fork"
)

// CheckKind says how an Evaluate activity ended.
type CheckKind string

// CheckKind values. A failed check is an activity error, not a kind.
const (
	CheckChecked  CheckKind = "checked"
	CheckDeferred CheckKind = "deferred"
	CheckParked   CheckKind = "parked"
)

// Rate is an installation's X-RateLimit-* values as the evaluation last
// saw them.
type Rate struct {
	Limit      int
	Remaining  int
	ResetAt    time.Time
	ObservedAt time.Time
}

// Recheck is the recheck signal payload. The controls evaluation loop
// also reads the changed paths: Paths selects the controls that own or
// read them, and Unknown (a forced push, a truncated commit list, too
// many paths) selects every control and dominates when coalescing.
type Recheck struct {
	Trigger  string
	Priority Priority
	Paths    []string `json:",omitempty"`
	Unknown  bool     `json:",omitempty"`
}

// MaxChangedPaths caps the changed paths a re-check carries, in a signal
// or coalesced in the workflow's carried state; past it the change set
// is unknown. It keeps an EvaluationWorkflow run's history under
// Temporal's warning limit at maxIterationsPerRun (INV-0022 spike 5(c),
// DESIGN-0032 § history growth).
const MaxChangedPaths = 256

// PolicyChanged is the policy_changed signal payload: re-check by By,
// at a random point between now and then.
type PolicyChanged struct {
	Version string
	By      time.Time
}

// AcquireInput is the AcquireBudget activity input. UpdateID makes a
// retried acquire return the first attempt's answer instead of taking a
// second lease.
type AcquireInput struct {
	// App is the controls App whose budget to draw from; empty is the
	// rc's single App.
	App string `json:",omitempty"`

	InstallationID int64
	UpdateID       string
	Request        AcquireRequest
}

// RemediationStart is one remediation an evaluation found due: the
// repository, the control (its slug), the installation for the task's
// fairness key, and the start priority.
type RemediationStart struct {
	RepositoryID   int64
	Control        string
	InstallationID int64
	Priority       Priority
}

// StartDueRemediationsInput is the StartDueRemediations activity input:
// the repository an evaluation just recorded.
type StartDueRemediationsInput struct {
	RepositoryID   int64
	InstallationID int64
}

// StartDueRemediationsResult counts the remediations signalled.
type StartDueRemediationsResult struct {
	Started int
}

// SetNextDueInput is the SetNextDue local activity input; a zero At
// clears it.
type SetNextDueInput struct {
	RepositoryID int64
	At           time.Time
}

// RemediationInput is RemediationWorkflow's input. The run re-reads
// everything else from the database (DESIGN-0032 § Remediation).
type RemediationInput struct {
	RepositoryID   int64
	Control        string
	InstallationID int64
}

// Remediate is RemediateSignal's payload. It carries nothing the run
// trusts: the run re-reads the generation from the database, so the
// signal only says "look again".
type Remediate struct{}

// WebhookRepo identifies one repository in a webhook. ID is GitHub's
// repository id, which survives renames and transfers.
type WebhookRepo struct {
	ID   int64
	Org  string
	Name string

	// Archived and Fork are the payload's flags where it carries them
	// (repository events); the controls routing parks on them.
	Archived bool `json:",omitempty"`
	Fork     bool `json:",omitempty"`
}

// The controls Apps as WebhookInput.App names them; they match
// config.AppEval and config.AppRemediate.
const (
	AppEval      = "eval"
	AppRemediate = "remediate"
)

// WebhookInput is WebhookWorkflow's input: the routing facts of one
// delivery, never its payload.
type WebhookInput struct {
	// App is the controls App whose route received the delivery, eval or
	// remediate, stamped by ingest from the route and never from a
	// header; empty on the rc's single-App route (IMPL-0028 task 3.2).
	App string `json:",omitempty"`

	DeliveryID     string
	Event          string
	Action         string
	InstallationID int64
	AccountLogin   string
	Repositories   []WebhookRepo

	// RepositorySelection is an installation or installation_repositories
	// delivery's repository_selection, "all" or "selected"; empty for
	// other events.
	RepositorySelection string `json:",omitempty"`

	// AccountType is an installation-level delivery's account type on a
	// controls App's route: control.AccountUser for a personal account,
	// else control.AccountOrganization.
	AccountType string `json:",omitempty"`

	// Paths and Unknown are an Evaluation App push's change set: the
	// watched paths it touched, or Unknown when the payload cannot say
	// (2048 commits, a forced push, or more than 256 watched paths).
	Paths   []string `json:",omitempty"`
	Unknown bool     `json:",omitempty"`
}
