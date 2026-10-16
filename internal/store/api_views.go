package store

import (
	"encoding/json"
	"time"
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

// Repository is one row of the repositories table.
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
	LastCheckedAt  *time.Time
}

// RepositoryFilter selects repositories. Zero fields do not filter.
type RepositoryFilter struct {
	Org        string
	Active     *bool
	ParkReason ParkReason
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
	EventSourceResult     = "result"
	EventSourceRepository = "repository"
)

// Event is one timeline entry: a control result transition or a
// repository event, by Source.
type Event struct {
	Source     string
	ID         int64
	OccurredAt time.Time

	// Result events: the control and its status transition. ToStatus is
	// nil when the result was withdrawn.
	ControlID  string
	FromStatus *string
	ToStatus   *string

	// Repository events.
	RepositoryKind string
	Detail         json.RawMessage
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

// RateSnapshot is an installation's last observed X-RateLimit-* values.
type RateSnapshot struct {
	Limit      int
	Remaining  int
	ResetAt    time.Time
	ObservedAt time.Time
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
	// Rates holds live installations' rate snapshots. The controls
	// schema stores none; IMPL-0029 8.5 rebuilds the budget component.
	Rates []RateSnapshot
	// Compliance is the fleet's result counts, from the compliance query.
	Compliance StatusCounts
	// History is fleet compliance at each UTC day's last snapshot over
	// the last 30 days, oldest first.
	History []CompliancePoint
	// CheckedLastDay counts repositories with a finished check in the
	// last 24h.
	CheckedLastDay int
	// Rollout is the current policy version's rollout; nil before any
	// version is activated.
	Rollout *Rollout
	// LastSnapshot is nil before the first compliance snapshot.
	LastSnapshot *time.Time
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
