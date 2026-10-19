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
	// TTL bounds the reservation; maintenance expires it after.
	TTL time.Duration
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
