package store

import (
	"time"

	"github.com/donaldgifford/repo-guardian/internal/findings"
)

// This file holds the v2 domain types (DESIGN-0025). They sit beside
// v1's RepoState/RuleState until the v1 runtime is deleted in
// IMPL-0025 Phase 16.

// Finding is the current verdict of one rule for one repository.
type Finding struct {
	RepositoryID    int64
	RuleKind        findings.RuleKind
	RuleName        string
	Status          findings.Status
	Reason          findings.Reason
	Remediation     findings.Remediation
	Evidence        findings.Evidence
	EvidenceVersion int
	StatusSince     time.Time
	LastEvaluatedAt time.Time
	PolicyVersion   string
}

// Outcome is one rule's result from a single check, as handed to
// RecordCheck.
type Outcome struct {
	RuleKind    findings.RuleKind
	RuleName    string
	Status      findings.Status
	Reason      findings.Reason
	Remediation findings.Remediation
	Evidence    findings.Evidence
	// PR is set when Remediation is pr_open and ForeignPR when it is
	// foreign_pr. Either is recorded under the evidence "pr" key.
	PR        *findings.PREvidence
	ForeignPR *findings.ForeignPREvidence
}

// FindingTransition is one created, changed or removed finding. A nil
// From* marks a created finding; a nil To* marks a removed one. It
// mirrors a finding_events row.
type FindingTransition struct {
	RuleKind        findings.RuleKind
	RuleName        string
	FromStatus      *findings.Status
	ToStatus        *findings.Status
	FromReason      *findings.Reason
	ToReason        *findings.Reason
	FromRemediation *findings.Remediation
	ToRemediation   *findings.Remediation
}

// Trigger names what started a check. It matches the checks.trigger
// CHECK constraint.
type Trigger string

// Trigger values.
const (
	TriggerSchedule      Trigger = "schedule"
	TriggerWebhook       Trigger = "webhook"
	TriggerPush          Trigger = "push"
	TriggerDiscovery     Trigger = "discovery"
	TriggerPolicyRollout Trigger = "policy_rollout"
	TriggerBootstrap     Trigger = "bootstrap"
)

// RateSnapshot is an installation's last observed X-RateLimit-* values.
// The store keeps the newest by ObservedAt.
type RateSnapshot struct {
	Limit      int
	Remaining  int
	ResetAt    time.Time
	ObservedAt time.Time
}

// CheckRecord is the input to StageCheck and RecordCheck.
//
// Key is the control plane's idempotency key and is unique across all
// checks. RecordCheck finalizes a row StageCheck wrote under the same
// Key when Outcomes is nil, reading the outcomes from the staged
// payload; otherwise it uses Outcomes inline.
type CheckRecord struct {
	Key            string
	RepositoryID   int64
	InstallationID int64
	Trigger        Trigger
	PolicyVersion  string
	StartedAt      time.Time
	FinishedAt     time.Time
	Outcomes       []Outcome
	CatalogParseOK *bool
	// ProviderRepoID and Name refresh the repository's identity when
	// set (DESIGN-0025 § Identity).
	ProviderRepoID *int64
	Name           string
	Rate           *RateSnapshot
}

// CheckErrorRecord records a check that learned nothing. Findings are
// never touched: the nil contract.
type CheckErrorRecord struct {
	Key           string
	RepositoryID  int64
	Trigger       Trigger
	PolicyVersion string
	StartedAt     time.Time
	FinishedAt    time.Time
	Err           string
}

// CheckApplied is RecordCheck's result. AlreadyFinal reports a retry of
// a check that had already committed; Transitions are then the stored
// ones, identical to the first call's.
type CheckApplied struct {
	CheckID      int64
	Transitions  []FindingTransition
	AlreadyFinal bool
}

// ParkReason says why a repository was parked. It matches the
// repositories.park_reason CHECK constraint.
type ParkReason string

// ParkReason values.
const (
	ParkAccessDenied        ParkReason = "access_denied"
	ParkArchived            ParkReason = "archived"
	ParkFork                ParkReason = "fork"
	ParkRemoved             ParkReason = "removed"
	ParkInstallationRemoved ParkReason = "installation_removed"
	ParkUnknown             ParkReason = "unknown"
)

// DiscoveredRepo is a repository seen by discovery or a webhook.
type DiscoveredRepo struct {
	Provider       string
	Host           string
	Org            string
	Name           string
	ProviderRepoID *int64
	InstallationID int64
	// NextDueAt seeds a created row's first check time.
	NextDueAt time.Time
}

// UpsertResult reports what UpsertDiscovered did.
type UpsertResult struct {
	ID          int64
	Created     bool
	Reactivated bool
	Renamed     bool
}

// Repository is one row of the repositories table.
type Repository struct {
	ID               int64
	Provider         string
	Host             string
	Org              string
	Name             string
	ProviderRepoID   *int64
	InstallationID   int64
	Active           bool
	ParkReason       *ParkReason
	ParkedAt         *time.Time
	DiscoveredAt     time.Time
	NextDueAt        *time.Time
	LastCheckedAt    *time.Time
	LastCheckOutcome string
	LastError        string
	PolicyVersion    string
	CatalogParseOK   *bool
}

// Installation is one GitHub App installation.
type Installation struct {
	InstallationID int64
	Provider       string
	Host           string
	AccountLogin   string
	SuspendedAt    *time.Time
}

// PolicyRule describes one rule in a PolicySummary.
type PolicyRule struct {
	Kind        findings.RuleKind `json:"kind"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Scope       []string          `json:"scope,omitempty"`
	CheckMode   string            `json:"check_mode,omitempty"`
}

// PolicySummary is the enforced policy as the API shows it. It never
// carries template bodies.
type PolicySummary struct {
	Rules []PolicyRule `json:"rules"`
}

// ServiceRunKind names a service run. It matches the service_runs.kind
// CHECK constraint.
type ServiceRunKind string

// ServiceRunKind values.
const (
	ServiceRunDiscovery     ServiceRunKind = "discovery"
	ServiceRunSnapshot      ServiceRunKind = "snapshot"
	ServiceRunPolicyRollout ServiceRunKind = "policy_rollout"
	ServiceRunBootstrap     ServiceRunKind = "bootstrap"
)

// ServiceRun is one discovery, snapshot, rollout or bootstrap run.
type ServiceRun struct {
	Kind       ServiceRunKind
	Success    bool
	StartedAt  time.Time
	FinishedAt time.Time
	Detail     map[string]any
}

// Scope restricts a read to a set of orgs. An empty Orgs means every
// org.
type Scope struct {
	Orgs []string
}
