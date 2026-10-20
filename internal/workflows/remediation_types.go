package workflows

import "time"

// RemediationKind is how one Remediate activity ended.
type RemediationKind string

// The Remediate activity's dispositions (IMPL-0030 3.1, 3.6).
const (
	// RemediationDone means the run acted, or found nothing to do, and
	// recorded what it did.
	RemediationDone RemediationKind = "done"
	// RemediationDeferred means GitHub throttled the run; it waits on a
	// workflow timer until Until and runs the same loop again, having
	// recorded nothing.
	RemediationDeferred RemediationKind = "deferred"
	// RemediationHeld means the control cannot be remediated now; the
	// hold is recorded and the run stops.
	RemediationHeld RemediationKind = "held"
	// RemediationBusy means another attempt holds the operation's row
	// lock (store.ErrOperationHeld); this one did nothing.
	RemediationBusy RemediationKind = "busy"
	// RemediationStale means the assignment moved under the run (the
	// epoch fence); it stopped before its next write.
	RemediationStale RemediationKind = "stale"
)

// Errors a Remediate attempt never retries (IMPL-0030 3.3), as Temporal
// application error types. The activity turns each into a result, so
// they reach the retry policy only if one escapes.
const (
	ErrTypeOperationHeld = "OperationHeld"
	ErrTypeStaleEpoch    = "StaleEpoch"
	ErrTypeHold          = "RemediationHold"
)

// RemediationState is what a remediation run compares before and after
// a loop (DESIGN-0032 D6): the evaluator's eval_generation and
// intent_revision on the control's result and the observed head of its
// newest remediation. Any of them moving means the run loops again.
type RemediationState struct {
	Generation     int64
	IntentRevision string `json:",omitempty"`
	ObservedHead   string `json:",omitempty"`
}

// RemediateInput is the Remediate activity input. OperationKey is
// <workflow id>/<run id>/<loop>, reused across deferrals of one loop.
type RemediateInput struct {
	RepositoryID   int64
	Control        string
	InstallationID int64
	OperationKey   string
	Deferrals      int
}

// RemediateResult is one Remediate attempt's disposition, the state it
// started from, and what it spent of the Remediation App's budget.
type RemediateResult struct {
	Kind  RemediationKind
	Until time.Time `json:",omitempty"`
	Hold  string    `json:",omitempty"`
	Start RemediationState
	Calls int
	Rate  *Rate `json:",omitempty"`
}

// ReadRemediationStateInput is the ReadRemediationState activity input.
type ReadRemediationStateInput struct {
	RepositoryID int64
	Control      string
}
