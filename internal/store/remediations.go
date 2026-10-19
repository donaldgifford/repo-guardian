package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Remediation errors (IMPL-0030 Phase 1).
var (
	// ErrPRCap means the repository already holds max_open_prs open or
	// reserved pull requests; the run holds pr_cap.
	ErrPRCap = errors.New("pull request cap reached")
	// ErrOperationHeld means another attempt holds the operation's row
	// lock; this attempt does nothing and lets that one finish.
	ErrOperationHeld = errors.New("remediation operation held by another attempt")
	// ErrRemediationNotFound means no remediation has the operation key.
	ErrRemediationNotFound = errors.New("remediation not found")
)

// The remediation kinds and states (DESIGN-0032 § Data model).
const (
	RemediationPullRequest = "pull_request"
	RemediationAPI         = "api"

	StateReserved        = "reserved"
	StateOpen            = "open"
	StateMerged          = "merged"
	StateClosedCompliant = "closed_compliant"
	StateClosedByUser    = "closed_by_user"
	StateClosedWithdrawn = "closed_withdrawn"
	StateRecommended     = "recommended"
	StateSuperseded      = "superseded"
	StateWithdrawn       = "withdrawn"
	StateResolved        = "resolved"
	StateApplied         = "applied"
	StateFailed          = "failed"
)

// Remediations is the remediator's store over the controls chain
// (IMPL-0030 Phase 1). It runs as rg_remediator.
type Remediations interface {
	// ReserveSlot takes a pull-request slot for one control under the
	// repository's max_open_prs, serialized on its policy-state row. It
	// returns ErrPRCap when the cap is reached, and the existing row when
	// the operation key was reserved before (a retried activity).
	ReserveSlot(ctx context.Context, in *SlotRequest) (*Remediation, error)
	// ClaimOperation locks the operation's row and runs fn while holding
	// it; the lock is released when fn returns. Another attempt holding it
	// returns ErrOperationHeld without calling fn. Journal writes
	// (AppendStep, AppendEvent) commit on their own and survive fn
	// failing; writes to the row itself go through op.
	ClaimOperation(ctx context.Context, operationKey string, fn func(ctx context.Context, op ClaimedOperation) error) error
	// LatestRemediation is the newest remediation of one control on one
	// repository, or nil when there has been none.
	LatestRemediation(ctx context.Context, repositoryID int64, controlID string) (*Remediation, error)
	// AppendStep journals one completed external step and returns its
	// sequence number.
	AppendStep(ctx context.Context, step *RemediationStep) (int, error)
	// Steps is a remediation's journal in order.
	Steps(ctx context.Context, remediationID int64) ([]RemediationStep, error)
	// AppendEvent records one remediation event.
	AppendEvent(ctx context.Context, remediationID int64, kind string, detail json.RawMessage) error
	// RecordRun writes what a finished run did (record-is-narrow). It
	// reports false when the assignment epoch moved and the run was
	// discarded. Call it after ClaimOperation returns: it updates the
	// claimed row from its own connection.
	RecordRun(ctx context.Context, run *RunRecord) (bool, error)
	// SetHold records why a control cannot be remediated (one of the
	// Hold* values, or empty to clear it) on its result, fenced on the
	// epoch. When the result is gone, HoldPermission lands on the
	// control's newest remediation (blocked cleanup) and any other hold
	// is dropped. It reports whether a row took the hold. Like RecordRun,
	// call it outside ClaimOperation; inside, set Remediation.Hold.
	SetHold(ctx context.Context, repositoryID int64, controlID string, epoch int64, hold string) (bool, error)
	// DueRemediations reads up to batch rows of remediation_due, the
	// controls that need a new run, by repository and policy order.
	DueRemediations(ctx context.Context, batch int) ([]DueRemediation, error)
	// MaintenanceCandidates reads up to batch rows of
	// remediation_maintenance, the durable rows with a lifecycle action.
	MaintenanceCandidates(ctx context.Context, batch int) ([]MaintenanceCandidate, error)
	// ExpireReservations deletes the reservations past reserved_until at
	// now and returns how many it freed.
	ExpireReservations(ctx context.Context, now time.Time) (int, error)
	// RemediationTarget reads one control on one repository as a
	// remediation run and its epoch fence see it; nil when the control is
	// not assigned (withdrawn) or the repository is unknown.
	RemediationTarget(ctx context.Context, repositoryID int64, controlID string) (*RemediationTarget, error)

	// InsertAPIRemediation writes an API remediation row (state
	// StateRecommended or, for a direct apply, StateFailed until every
	// step is done) and returns it; a retried operation key returns the
	// existing row. It takes the one-open slot, never the PR cap
	// (IMPL-0030 5.1, 5.3).
	InsertAPIRemediation(ctx context.Context, in *APIRemediation) (*Remediation, error)
	// TouchLastRemediation stamps the repository's last_remediation_at
	// after a recorded run (IMPL-0030 4.15).
	TouchLastRemediation(ctx context.Context, repositoryID int64) error
}

// RemediationTarget is one control on one repository as the remediator
// reads it (IMPL-0030 3.1, 3.5). InstallationID is 0 when the
// Remediation App has no access row; Remediable is false then or while
// its installation is suspended. The result fields are zero when the
// control has no result yet.
type RemediationTarget struct {
	Org    string
	Name   string
	Active bool

	// The assignment, as the fence compares it.
	Epoch         int64
	State         string
	EffectiveMode string
	Revision      string

	InstallationID int64
	Remediable     bool

	HasResult      bool
	ResultEpoch    int64
	Generation     int64
	IntentRevision string
	Status         string
	Hold           string

	// ObservedHead is the newest remediation's observed head, if any.
	ObservedHead string

	// ProviderRepoID is GitHub's repository id, which an adopted pull
	// request's head repository must equal.
	ProviderRepoID int64
	ControlVersion int
	// AccountType is the Remediation App installation's account type.
	AccountType string
	// Due is the remediation_due predicate for this one row (IMPL-0030
	// 4.1).
	Due bool
}

// DueRemediation is one remediation_due row.
type DueRemediation struct {
	RepositoryID int64
	ControlID    string
	Ordinal      int
}

// The remediation_maintenance actions.
const (
	MaintenanceExpire         = "expire"
	MaintenanceWithdraw       = "withdraw"
	MaintenanceSupersede      = "supersede"
	MaintenanceCloseCompliant = "close_compliant"
)

// MaintenanceCandidate is one remediation_maintenance row; Action is a
// Maintenance* value.
type MaintenanceCandidate struct {
	RemediationID int64
	RepositoryID  int64
	ControlID     string
	Kind          string
	State         string
	Action        string
}

// The holds a control result can carry (DESIGN-0032 § Data model); a
// remediation row carries only HoldPermission.
const (
	HoldPRCap         = "pr_cap"
	HoldForeignBranch = "foreign_branch"
	HoldStaleBranch   = "stale_branch"
	HoldConflict      = "conflict"
	HoldPermission    = "permission"
)

// RunRecord is a finished remediation run. Generation is the
// eval_generation read at run start and ProcessedHead the pull request
// head the run acted on (empty for an API control). An empty
// OperationKey records a run that opened nothing: it acknowledges the
// generation on the result alone.
type RunRecord struct {
	Org            string
	RepositoryID   int64
	ControlID      string
	Epoch          int64
	OperationKey   string
	Generation     int64
	IntentRevision string
	ProcessedHead  string
}

// ClaimedOperation is a remediation row held by ClaimOperation.
type ClaimedOperation interface {
	// Remediation is the row as claimed, with any Update applied.
	Remediation() Remediation
	// Update writes the remediator's columns of the claimed row from r.
	// The evaluator's observation columns are never written.
	Update(ctx context.Context, r *Remediation) error
	// SetIntent moves the row's intent revision.
	SetIntent(ctx context.Context, intentRevision string) error
}

// SlotRequest asks for a pull-request slot.
type SlotRequest struct {
	RepositoryID int64
	ControlID    string
	Epoch        int64
	OperationKey string
	Intent       string
	// Generation is the control's eval_generation the run started from.
	Generation int64
	// The run's intent, committed with the reservation before any
	// external step (IMPL-0030 4.4).
	BaseSHA      string
	Branch       string
	ExpectedHead string
	// TTL bounds the reservation; maintenance expires it after.
	TTL time.Duration
}

// APIRemediation is a new API remediation row.
type APIRemediation struct {
	RepositoryID int64
	ControlID    string
	Epoch        int64
	OperationKey string
	State        string
	Intent       string
	Generation   int64
	// Proposal is the per-resource change: key, before, after.
	Proposal json.RawMessage
}

// Remediation is one remediations row.
type Remediation struct {
	ID                   int64
	RepositoryID         int64
	ControlID            string
	AssignmentEpoch      int64
	OperationKey         string
	Kind                 string
	State                string
	IntentRevision       string
	BaseSHA              string
	Branch               string
	ExpectedHead         string
	ReservedUntil        *time.Time
	PRNumber             int
	PRURL                string
	ObservedHeadSHA      string
	ObservedAt           *time.Time
	AcknowledgedHeadSHA  string
	Proposal             json.RawMessage
	RemediatedGeneration int64
	ClosedGeneration     *int64
	NextEligibleAt       *time.Time
	Hold                 string
	CreatedAt            time.Time
	LastRunAt            time.Time
	ClosedAt             *time.Time
	CloseReason          string
}

// RemediationStep is one journaled external step.
type RemediationStep struct {
	RemediationID int64
	OperationKey  string
	Seq           int
	Kind          string
	ResourceKey   string
	BlobSHA       string
	CommittedSHA  string
	PRNumber      int
	Status        string
	Before        json.RawMessage
	After         json.RawMessage
	Error         string
	OccurredAt    time.Time
}

// TrackedPR is an open remediation pull request the evaluator observes.
type TrackedPR struct {
	RemediationID int64
	ControlID     string
	PRNumber      int
}

// The states an observation reports.
const (
	PRObservedOpen   = "open"
	PRObservedMerged = "merged"
	PRObservedClosed = "closed"
)

// PRObservation is what the Evaluation App saw of one tracked pull
// request: still open at HeadSHA, merged, or closed unmerged (by a user:
// the remediator's own closes leave the row terminal, so it is never
// tracked).
type PRObservation struct {
	RemediationID int64
	PRNumber      int
	State         string
	HeadSHA       string
	ClosedAt      time.Time
}
