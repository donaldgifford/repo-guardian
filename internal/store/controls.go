package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// The Apps an access row or installation belongs to (DESIGN-0030 D15).
const (
	AppEval      = "eval"
	AppRemediate = "remediate"
)

// Controls is the evaluator's store over the controls chain (IMPL-0029
// 4.5): discovery, resolution, and staging and recording evaluations. It
// runs as rg_evaluator.
type Controls interface {
	// UpsertInstallation refreshes one App installation's facts.
	UpsertInstallation(ctx context.Context, in AppInstallation) error
	// UpsertRepositories writes each repository row and its
	// app = 'eval' access row in one transaction, then resolves it with
	// r. It is the only un-parker.
	UpsertRepositories(ctx context.Context, repos []DiscoveredRepository, r Resolver) ([]ResolvedRepository, error)
	// Resolve re-resolves one stored repository with r, reading its row
	// and both Apps' access.
	Resolve(ctx context.Context, repoID int64, r Resolver) (ResolutionChange, error)
	// ApplyResolution persists res, computed under policy version, as
	// the repository's policy state and assignments (DESIGN-0030 step 7).
	ApplyResolution(ctx context.Context, repoID int64, version string, res policy.Resolution) (ResolutionChange, error)
	// StageEvaluation stages an evaluation's per-control payload under
	// its check key; a retry replaces it until it is recorded.
	StageEvaluation(ctx context.Context, st *EvaluationStage) error
	// RecordEvaluation commits a staged evaluation, fenced on each
	// control's assignment epoch. It is idempotent on the check key.
	RecordEvaluation(ctx context.Context, checkKey string) (RecordOutcome, error)
	// Park parks a repository (IMPL-0029 4.6): archived, fork, removed
	// and installation_removed clear its results; access_denied and
	// unknown keep them, because access loss must never improve posture.
	Park(ctx context.Context, repoID int64, reason string) error
	// CurrentActivation is the most recently activated policy version.
	CurrentActivation(ctx context.Context) (version string, activatedAt time.Time, err error)
}

// Resolver resolves a repository against a loaded policy;
// *policy.Snapshot satisfies it.
type Resolver interface {
	Resolve(repo policy.Repository, access policy.AppAccess) policy.Resolution
	Version() string
}

// AppInstallation is one App installation's facts.
type AppInstallation struct {
	App                 string
	InstallationID      int64
	AccountLogin        string
	SuspendedAt         *time.Time
	RepositorySelection string // "all" or "selected"
}

// DiscoveredRepository is one repository the Evaluation App's discovery
// listed under InstallationID.
type DiscoveredRepository struct {
	Org            string
	Name           string
	ProviderRepoID int64
	InstallationID int64
	Archived       bool
	Fork           bool
}

// ResolvedRepository is one upserted repository and how it resolved.
type ResolvedRepository struct {
	ID       int64
	Org      string
	Name     string
	Created  bool
	Unparked bool
	State    policy.State
	Change   ResolutionChange
}

// ResolutionChange is what a resolution moved, by control id.
type ResolutionChange struct {
	// Changed holds every assignment that is new, re-epoched or removed.
	Changed []string
	// Withdrawn holds every control whose result was cleared.
	Withdrawn []string
}

// EvaluationStage is one evaluation's staged payload: only the controls
// it selected, so an unselected control's evaluated_sha and evidence are
// left as they were.
type EvaluationStage struct {
	CheckKey      string
	RepositoryID  int64
	Trigger       string
	PolicyVersion string
	StartedAt     time.Time
	Controls      []ControlEvaluation
}

// ControlEvaluation is one control's staged result, at the assignment
// epoch the evaluation read.
type ControlEvaluation struct {
	ControlID      string            `json:"control_id"`
	ControlVersion int               `json:"control_version"`
	Revision       string            `json:"revision"`
	Epoch          int64             `json:"epoch"`
	Status         string            `json:"status"`
	Fingerprint    string            `json:"fingerprint"`
	Apply          string            `json:"apply"`
	Remediable     bool              `json:"remediable"`
	EvaluatedSHA   string            `json:"evaluated_sha"`
	Rules          []RuleEvaluation  `json:"rules"`
	Reads          []json.RawMessage `json:"reads,omitempty"`
}

// RuleEvaluation is one rule's staged result.
type RuleEvaluation struct {
	RuleID          string          `json:"rule_id"`
	Status          string          `json:"status"`
	Remediate       bool            `json:"remediate"`
	Evidence        json.RawMessage `json:"evidence"`
	EvidenceKind    string          `json:"evidence_kind"`
	EvidenceVersion int             `json:"evidence_version"`
}

// RecordOutcome is what RecordEvaluation committed.
type RecordOutcome struct {
	// AlreadyFinal is true when the check key was recorded before.
	AlreadyFinal bool
	// Recorded holds the controls written.
	Recorded []string
	// Discarded holds the controls fenced out by a moved epoch.
	Discarded []string
	// Changed holds the recorded controls whose generation was bumped.
	Changed []string
}

// The compliance lineage filters (DESIGN-0030 § Compliance queries).
const (
	LineageAll      = "all"
	LineageBaseline = "baseline"
	LineageOrgAdded = "org_added"
)

// ControlPosture is one (org, control, decision) row of the controls
// compliance query: every posture bucket, with the percentage and
// coverage computed in SQL. Percent and Coverage are nil, "no data", on
// an empty denominator, never 100.
type ControlPosture struct {
	Org                      string
	ControlID                string
	Decision                 string
	Compliant                int
	NonCompliant             int
	Pending                  int
	Stale                    int
	UnmeasurableAccessDenied int
	UnmeasurableSuspended    int
	UnmeasurableConflict     int
	NotApplicable            int
	Unknown                  int
	Excluded                 int
	Assigned                 int
	Percent                  *float64
	Coverage                 *float64
}

// Exclusion is one row of the exclusions report: an excluded or
// conflicted assignment (ControlID set) or an excluded repository.
type Exclusion struct {
	Org        string
	Repository string
	ControlID  string
	State      string
	Reason     string
	ExcludedBy string
}
