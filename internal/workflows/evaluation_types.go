package workflows

import "time"

// CheckSkipped is an evaluation that measured nothing because the
// repository is not managed (excluded or unmanaged by policy): nothing
// is recorded and the loop keeps its schedule.
const CheckSkipped CheckKind = "skipped"

// EvaluationWorkflowInput is EvaluationWorkflow's input and its
// ContinueAsNew state.
type EvaluationWorkflowInput struct {
	RepositoryID   int64
	InstallationID int64

	// Interval is EVAL_INTERVAL, the time between scheduled evaluations.
	Interval time.Duration

	// NextDue is when the next scheduled evaluation runs; zero runs now.
	NextDue time.Time

	// Pending is the coalesced re-check not yet run: the union of the
	// signalled paths, Unknown dominating, at the highest priority.
	Pending *Recheck

	// PolicyPending is a policy_changed signal not yet acted on: the
	// next evaluation selects every control.
	PolicyPending bool

	// PolicyVersion is the last policy version this workflow was told
	// about.
	PolicyVersion string

	// Iteration counts evaluations across ContinueAsNew; it makes check
	// keys unique within a run.
	Iteration int
}

// EvaluateInput is the Evaluate activity input. All selects every
// control; otherwise Paths selects the controls owning or reading them.
// Scheduled is the schedule's evaluation, the only one that reads API
// resources (DESIGN-0032 § When it runs).
type EvaluateInput struct {
	RepositoryID   int64
	InstallationID int64
	CheckKey       string
	Trigger        string
	Paths          []string `json:",omitempty"`
	All            bool
	Scheduled      bool
	Deferrals      int
}

// EvaluateResult is how an Evaluate activity ended. CheckChecked staged
// a payload under the check key; CheckDeferred staged nothing and waits
// until Until; CheckParked found repository-level access loss
// (ParkReason set) or an already parked repository (ParkReason empty);
// CheckSkipped measured nothing.
type EvaluateResult struct {
	Kind       CheckKind
	Until      time.Time `json:",omitempty"`
	ParkReason string    `json:",omitempty"`
	Cause      string    `json:",omitempty"`
	Controls   int
	Calls      int
	Rate       *Rate `json:",omitempty"`
}

// RecordEvaluationInput is the RecordEvaluation activity input.
type RecordEvaluationInput struct {
	CheckKey string
}

// RecordEvaluationResult is what the record step committed.
type RecordEvaluationResult struct {
	AlreadyFinal bool
	Recorded     int
	Discarded    int
	Changed      int
}

// ParkRepositoryInput is the ParkRepository activity input.
type ParkRepositoryInput struct {
	RepositoryID int64
	CheckKey     string
	Reason       string
	Cause        string `json:",omitempty"`
}

// DiscoveredRepo is one repository the Evaluation App lists. Archived
// and forked repositories are kept: resolution parks them and clears
// their results, so discovery needs no filter of its own.
type DiscoveredRepo struct {
	ID       int64
	Org      string
	Name     string
	Archived bool `json:",omitempty"`
	Fork     bool `json:",omitempty"`
}

// ControlsUpsertInput is one batch of an installation's listing.
type ControlsUpsertInput struct {
	InstallationID int64
	Repos          []DiscoveredRepo
}
