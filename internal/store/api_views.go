package store

import (
	"encoding/json"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Page is one keyset page of an API list. More reports that a further
// page exists; the caller builds the next cursor from the last item.
type Page[T any] struct {
	Items []T
	More  bool
}

// CompliantPercent is the shared compliance math (the floor_percent SQL
// function): Compliant / (Compliant + NonCompliant), floored to one
// decimal, nil when nothing is measured. It exists for totals summed
// across the compliance query's rows, so a total agrees with the per-row
// values the query computes in SQL.
func CompliantPercent(c StatusCounts) *float64 {
	return FloorPercent(c.Compliant, c.Compliant+c.NonCompliant)
}

// FloorPercent is the floor_percent SQL function: num / den floored to
// one decimal, nil when den is zero.
func FloorPercent(num, den int) *float64 {
	if den == 0 {
		return nil
	}

	p := float64(num*1000/den) / 10

	return &p
}

// Add returns the sum of two counts.
func (c StatusCounts) Add(o StatusCounts) StatusCounts {
	return StatusCounts{
		Compliant: c.Compliant + o.Compliant, NonCompliant: c.NonCompliant + o.NonCompliant,
		NotApplicable: c.NotApplicable + o.NotApplicable, Unknown: c.Unknown + o.Unknown,
	}
}

// Plus sums two posture rows and recomputes Percent and Coverage from
// the sums with FloorPercent, as the compliance query does per row. Org
// and ControlID are kept from p; Decision is cleared when they differ.
func (p *ControlPosture) Plus(o *ControlPosture) ControlPosture {
	out := ControlPosture{
		Org: p.Org, ControlID: p.ControlID, Decision: p.Decision,
		Compliant: p.Compliant + o.Compliant, NonCompliant: p.NonCompliant + o.NonCompliant,
		Pending: p.Pending + o.Pending, Stale: p.Stale + o.Stale,
		UnmeasurableAccessDenied: p.UnmeasurableAccessDenied + o.UnmeasurableAccessDenied,
		UnmeasurableSuspended:    p.UnmeasurableSuspended + o.UnmeasurableSuspended,
		UnmeasurableConflict:     p.UnmeasurableConflict + o.UnmeasurableConflict,
		NotApplicable:            p.NotApplicable + o.NotApplicable, Unknown: p.Unknown + o.Unknown,
		Excluded: p.Excluded + o.Excluded, Assigned: p.Assigned + o.Assigned,
	}

	if p.Decision != o.Decision {
		out.Decision = ""
	}

	out.Percent = FloorPercent(out.Compliant, out.Compliant+out.NonCompliant)
	out.Coverage = FloorPercent(out.Compliant+out.NonCompliant+out.NotApplicable, out.Assigned)

	return out
}

// Counts is the posture row's result buckets as StatusCounts.
func (p *ControlPosture) Counts() StatusCounts {
	return StatusCounts{Compliant: p.Compliant, NonCompliant: p.NonCompliant, NotApplicable: p.NotApplicable, Unknown: p.Unknown}
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
	ParkSuspended           ParkReason = "suspended"
	ParkUnknown             ParkReason = "unknown"
)

// Repository is one repository with its policy state and App access.
type Repository struct {
	ID             int64
	Org            string
	Name           string
	ProviderRepoID *int64
	Active         bool
	ParkReason     *ParkReason
	ParkedAt       *time.Time
	DiscoveredAt   time.Time
	NextDueAt      *time.Time
	// LastEvaluatedAt is the newest successful evaluation; nil before one.
	LastEvaluatedAt *time.Time
	// PolicyState is empty before the first resolution.
	PolicyState  string
	PolicyReason string
	// Evaluable: the Evaluation App can reach the repository. Remediable:
	// the Remediation App can and is not suspended.
	Evaluable  bool
	Remediable bool
}

// RepositoryFilter selects repositories. Zero fields do not filter.
type RepositoryFilter struct {
	Org        string
	Active     *bool
	ParkReason ParkReason
	// PolicyState is a repository_policy_state.state.
	PolicyState string
	// NamePrefix matches the start of the name, case-insensitively.
	NamePrefix string
	// AfterID pages past this id; zero starts at the beginning.
	AfterID int64
	Limit   int
}

// OrgRepositoryCount counts one org's repositories in one state.
type OrgRepositoryCount struct {
	Org        string
	Active     bool
	ParkReason ParkReason
	Count      int
}

// OrgsView is every visible org's repositories and compliance.
type OrgsView struct {
	Repositories []OrgRepositoryCount
	// Compliance holds the controls compliance rows, every lineage.
	Compliance []ControlPosture
	// OpenPRs counts open repo-guardian pull requests per org, keyed by
	// the lower-cased org.
	OpenPRs map[string]int
}

// SummaryView is the fleet totals over the visible orgs.
type SummaryView struct {
	Repositories []OrgRepositoryCount
	// Compliance holds the controls compliance rows, every lineage.
	Compliance []ControlPosture
	OpenPRs    int
	// Evaluated counts repositories evaluated in the last 24 hours.
	Evaluated int
	// LastEvaluatedAt is nil before the first evaluation.
	LastEvaluatedAt *time.Time
	// PolicyVersion is empty before a version is activated.
	PolicyVersion string
	// RemediationBacklog counts outstanding remediations: reserved, open,
	// recommended or failed.
	RemediationBacklog int
}

// ControlVersion is one version and revision of a control assigned in
// an org and lineage, with the managed repositories carrying it.
type ControlVersion struct {
	Org          string
	ControlID    string
	Decision     string
	Version      int
	Revision     string
	Repositories int
}

// ControlsView is the controls compliance rows (every lineage) and the
// assigned versions behind /controls and /controls/{id}.
type ControlsView struct {
	Compliance []ControlPosture
	Versions   []ControlVersion
	// Catalogue is the active policy's definitions; nil before one is
	// activated.
	Catalogue []policy.ControlSummary
}

// OrgView is one org's repositories, compliance (every lineage) and
// exclusions.
type OrgView struct {
	Repositories []OrgRepositoryCount
	Compliance   []ControlPosture
	Exclusions   []Exclusion
}

// RuleResult is one stored rule result.
type RuleResult struct {
	RuleID    string
	Status    string
	Remediate bool
	// Evidence is the stored JSON object, evidence_kind and
	// evidence_version included.
	Evidence json.RawMessage
}

// RepositoryControl is one assignment of a repository with its result.
// The result fields are zero before the first evaluation.
type RepositoryControl struct {
	ControlID       string
	Version         int
	Revision        string
	State           string
	Decision        string
	BaselineControl string
	Source          string
	SourceBlock     string
	Replaced        string
	ExcludedBy      string
	Reason          string
	Error           string
	RequestedMode   string
	EffectiveMode   string
	ModeSource      string
	ModeReason      string
	Epoch           int64
	// Bucket is the posture bucket, as ControlCompliance counts it.
	Bucket        string
	Status        string
	EvaluatedSHA  string
	EvaluatedAt   *time.Time
	LastChangedAt *time.Time
	Hold          string
	HoldSince     *time.Time
	Rules         []RuleResult
	// Remediation is the control's latest remediation; nil before one.
	Remediation *RemediationView
}

// RemediationView is one remediation as the API shows it: a
// remediations row, or a result held before a pull request exists (ID
// zero, State empty, Kind pull_request, CreatedAt its hold_since).
type RemediationView struct {
	ID                  int64
	RepositoryID        int64
	Org                 string
	Repository          string
	ControlID           string
	Kind                string
	State               string
	IntentRevision      string
	Branch              string
	PRNumber            int
	PRURL               string
	ObservedHeadSHA     string
	AcknowledgedHeadSHA string
	Hold                string
	HoldSince           *time.Time
	Proposal            json.RawMessage
	CreatedAt           time.Time
	LastRunAt           *time.Time
	ClosedAt            *time.Time
	CloseReason         string
	Steps               []RemediationStep
}

// RemediationKey orders /remediations for paging, newest first.
type RemediationKey struct {
	At           time.Time
	RepositoryID int64
	ControlID    string
	ID           int64
}

// RemediationFilter selects remediations. Zero fields do not filter.
type RemediationFilter struct {
	Kind      string
	State     string
	Org       string
	ControlID string
	// Holds matches any of these holds. A branch hold also lists the
	// results held before a pull request exists, unless Kind is api or
	// State is set.
	Holds []string
	// Before keeps remediations created (or held) before it.
	Before *time.Time
	After  *RemediationKey
	Limit  int
}

// History aggregations (IMPL-0030 7.6).
const (
	HistoryNone    = "none"
	HistoryFleet   = "fleet"
	HistoryOrg     = "org"
	HistoryControl = "control"
)

// HistoryFilter selects compliance snapshots. Empty fields do not filter.
type HistoryFilter struct {
	ControlID string
	Org       string
	Since     time.Time
	// Aggregate sums each UTC day's rows over the fleet, per org or per
	// control; empty or HistoryNone returns the rows themselves.
	Aggregate string
}

// ComplianceSnapshot is one compliance_snapshots row, or a day's sum of
// them when aggregated: SnapshotAt is then the UTC day, and the fields
// the aggregation leaves out (Org, ControlID, Decision) are empty.
type ComplianceSnapshot struct {
	ControlPosture
	SnapshotAt time.Time
}

// ActivePolicy is the active policy version and its summary.
type ActivePolicy struct {
	Version            string
	FirstSeenAt        time.Time
	ActivatedAt        time.Time
	RolloutCompletedAt *time.Time
	Summary            policy.Summary
}

// Check is one evaluation of a repository (a checks row).
type Check struct {
	ID            int64
	Trigger       string
	Outcome       string
	PolicyVersion string
	StartedAt     time.Time
	FinishedAt    *time.Time
	Error         string
}

// RepositoryDetail is a repository with its last check.
type RepositoryDetail struct {
	Repository Repository
	LastCheck  *Check
}

// Event sources in the merged timeline.
const (
	EventSourceResult      = "result"
	EventSourceRepository  = "repository"
	EventSourceRemediation = "remediation"
)

// Event is one timeline entry: a control result transition, a
// repository event or a remediation event, by Source.
type Event struct {
	Source     string
	ID         int64
	OccurredAt time.Time

	// Result events: the control, the rule for a rule-result
	// transition, and the status transition. ToStatus is nil when the
	// result was withdrawn.
	ControlID  string
	RuleID     string
	FromStatus *string
	ToStatus   *string

	// Repository and remediation events. A remediation event carries
	// its control too, and remediation_id in Detail.
	Kind   string
	Detail json.RawMessage
}

// EventKey orders the timeline for paging, newest first.
type EventKey struct {
	OccurredAt time.Time
	Source     string
	ID         int64
}

// ServiceRunKind names a service run. It matches the service_runs.kind
// CHECK constraint.
type ServiceRunKind string

// ServiceRunKind values.
const (
	ServiceRunDiscovery            ServiceRunKind = "discovery"
	ServiceRunSnapshot             ServiceRunKind = "snapshot"
	ServiceRunPolicyRollout        ServiceRunKind = "policy_rollout"
	ServiceRunSweep                ServiceRunKind = "sweep"
	ServiceRunMaintenance          ServiceRunKind = "maintenance"
	ServiceRunRemediationDiscovery ServiceRunKind = "remediation_discovery"
)

// ServiceRun is one service workflow run.
type ServiceRun struct {
	Kind       ServiceRunKind
	Success    bool
	StartedAt  time.Time
	FinishedAt time.Time
	Detail     map[string]any
}

// AppInstallations is one GitHub App's installations, by count only.
type AppInstallations struct {
	App           string
	Installations int
	Suspended     int
	// Repositories counts the repositories the App can reach.
	Repositories int
}

// StatusInputs is everything the status page is computed from, read in
// one pass by its refresher (DESIGN-0027 § Status page). It is global:
// the page is aggregate-only.
type StatusInputs struct {
	// LastCheckSuccess and LastWebhookCheck are nil before the first.
	LastCheckSuccess *time.Time
	LastWebhookCheck *time.Time
	// ChecksLastHour counts finished checks, ErrorsLastHour the failed.
	ChecksLastHour     int
	ErrorsLastHour     int
	ActiveRepositories int
	// LastServiceSuccess holds each service's last successful run.
	LastServiceSuccess map[ServiceRunKind]time.Time
	// Apps holds each App with an installation, ordered by App.
	Apps []AppInstallations
	// Compliance is the fleet's posture: every compliance row summed.
	Compliance ControlPosture
	// History is fleet compliance at each UTC day's last snapshot over
	// the last 30 days, oldest first.
	History []CompliancePoint
	// EvaluatedLastDay counts repositories with a finished evaluation in
	// the last 24h.
	EvaluatedLastDay int
	// Overdue counts active managed repositories with no successful
	// evaluation within the freshness window the caller passed.
	Overdue int
	// Rollout is the current policy version's rollout; nil before any
	// version is activated.
	Rollout *Rollout
	// LastSnapshot is nil before the first compliance snapshot.
	LastSnapshot *time.Time
	// RemediationBacklog counts outstanding remediations fleet-wide.
	RemediationBacklog int
}

// CompliancePoint is fleet compliance at one snapshot; Percent is nil
// when nothing was measured.
type CompliancePoint struct {
	At      time.Time
	Percent *float64
}

// Rollout is a policy version's rollout; CompletedAt is nil while it is
// in progress.
type Rollout struct {
	CompletedAt *time.Time
}
