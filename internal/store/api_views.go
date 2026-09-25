package store

import (
	"encoding/json"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/findings"
)

// Page is one keyset page of an API list. More reports that a further
// page exists; the caller builds the next cursor from the last item.
type Page[T any] struct {
	Items []T
	More  bool
}

// CompliantPercent is the shared compliance math (see compliance.sql):
// Compliant / (Compliant + NonCompliant), floored to one decimal, nil
// when nothing is measured. It exists for totals summed across the
// shared query's rows, so a fleet total agrees with the per-row values
// the query computes in SQL.
func CompliantPercent(c StatusCounts) *float64 {
	measured := c.Compliant + c.NonCompliant
	if measured == 0 {
		return nil
	}

	p := float64(c.Compliant*1000/measured) / 10

	return &p
}

// Add returns the sum of two counts.
func (c StatusCounts) Add(o StatusCounts) StatusCounts {
	return StatusCounts{
		Compliant:     c.Compliant + o.Compliant,
		NonCompliant:  c.NonCompliant + o.NonCompliant,
		NotApplicable: c.NotApplicable + o.NotApplicable,
		Unknown:       c.Unknown + o.Unknown,
	}
}

// Counts returns the row's status counts.
func (c *ComplianceCount) Counts() StatusCounts {
	return StatusCounts{Compliant: c.Compliant, NonCompliant: c.NonCompliant, NotApplicable: c.NotApplicable, Unknown: c.Unknown}
}

// FindingReasonCount is the number of findings with one reason.
type FindingReasonCount struct {
	Reason findings.Reason
	Count  int
}

// OrgRepositoryCount counts one org's repositories in one state.
type OrgRepositoryCount struct {
	Org        string
	Active     bool
	ParkReason ParkReason
	Count      int
}

// RuleView is one rule across the visible orgs.
type RuleView struct {
	// Current holds the rule's shared-query rows, one per org.
	Current  []ComplianceCount
	Reasons  []FindingReasonCount
	Oldest   []APIFinding
	Previous []ComplianceSnapshot
}

// OrgsView is every visible org's repositories and compliance.
type OrgsView struct {
	Current      []ComplianceCount
	Repositories []OrgRepositoryCount
}

// OrgView is one org's repositories, rules and not-applicable reasons.
type OrgView struct {
	Org           string
	Current       []ComplianceCount
	Previous      []ComplianceSnapshot
	Repositories  []OrgRepositoryCount
	NotApplicable []FindingReasonCount
}

// APIFinding is a finding as the API lists it.
type APIFinding struct {
	RepositoryID    int64
	Org             string
	Repository      string
	Kind            findings.RuleKind
	RuleName        string
	Status          findings.Status
	Reason          findings.Reason
	Remediation     findings.Remediation
	StatusSince     time.Time
	LastEvaluatedAt time.Time
	// PRStale is computed in SQL against FindingFilter.StaleBefore.
	PRStale bool
	// Evidence is the stored object, with any PR under "pr".
	Evidence json.RawMessage
}

// FindingKey orders findings for paging.
type FindingKey struct {
	RepositoryID int64
	Kind         findings.RuleKind
	RuleName     string
}

// FindingFilter selects findings. Zero fields do not filter.
type FindingFilter struct {
	Status      findings.Status
	Reason      findings.Reason
	Remediation findings.Remediation
	Org         string
	Kind        findings.RuleKind
	RuleName    string
	PRStale     *bool
	SinceBefore *time.Time
	// StaleBefore is now minus stale_after: an open PR created before it
	// is stale.
	StaleBefore time.Time
	After       *FindingKey
	Limit       int
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

// InstallationStatus is an installation and its last rate snapshot.
type InstallationStatus struct {
	InstallationID int64
	Account        string
	SuspendedAt    *time.Time
	RemovedAt      *time.Time
	RateLimit      *int
	RateRemaining  *int
	RateResetAt    *time.Time
	RateObservedAt *time.Time
}

// Check is one check of a repository.
type Check struct {
	ID            int64
	Trigger       Trigger
	Outcome       string
	PolicyVersion string
	StartedAt     time.Time
	FinishedAt    *time.Time
	Error         string
}

// RepositoryDetail is a repository with its installation, last check and
// findings.
type RepositoryDetail struct {
	Repository   Repository
	Installation InstallationStatus
	LastCheck    *Check
	Findings     []APIFinding
}

// Event sources in the merged timeline.
const (
	EventSourceFinding    = "finding"
	EventSourceRepository = "repository"
)

// Event is one timeline entry: a finding transition or a repository
// event, by Source.
type Event struct {
	Source     string
	ID         int64
	OccurredAt time.Time

	// Finding events.
	Kind            findings.RuleKind
	RuleName        string
	FromStatus      *findings.Status
	ToStatus        *findings.Status
	FromReason      *findings.Reason
	ToReason        *findings.Reason
	FromRemediation *findings.Remediation
	ToRemediation   *findings.Remediation

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

// HistoryKey orders snapshots for paging.
type HistoryKey struct {
	SnapshotAt time.Time
	Org        string
	Kind       findings.RuleKind
	RuleName   string
}

// HistoryFilter selects stored snapshots. Zero fields do not filter.
type HistoryFilter struct {
	Org      string
	Kind     findings.RuleKind
	RuleName string
	From     *time.Time
	To       *time.Time
	After    *HistoryKey
	Limit    int
}

// CurrentPolicy is the newest policy version and its rollout.
type CurrentPolicy struct {
	Version            string
	FirstSeenAt        time.Time
	RolloutCompletedAt *time.Time
	Summary            PolicySummary
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
	// Rates holds live installations' rate snapshots.
	Rates []RateSnapshot
	// Compliance is the fleet's finding counts, from the shared query.
	Compliance StatusCounts
}
