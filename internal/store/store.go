// Package store defines the persistent per-repo state interface for
// repo-guardian. It captures the last reconcile attempt's outcome and
// the policy hash under which it ran, so a sweep can identify repos
// that need re-checking either because their state is stale or
// because the active policy has changed.
//
// Since IMPL-0023 it also holds compliance posture: RuleState records
// one rule's verdict for one repo, with actionable_since tracking how
// long a repo has been failing. That is the fact a metric cannot carry
// — Prometheus can say "40 repos lack CODEOWNERS" but never "these
// forty, since these dates" — and it is why posture lives in the
// database and is projected to Prometheus rather than the reverse.
//
// Implementations live in subpackages (`store/postgres`)
// and are selected at runtime via `STORE_BACKEND`. See DESIGN-0012 for
// the architectural rationale and DESIGN-0022 for the posture model.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound indicates the requested repo state is not in the store.
// Implementations MUST return this exact sentinel (or wrap it via
// fmt.Errorf with %w) so callers can `errors.Is(err, store.ErrNotFound)`.
var ErrNotFound = errors.New("repo state not found")

// Status values for RepoState.LastCheckStatus. Defined as constants so
// the engine and tests share the canonical set without magic strings.
const (
	StatusSuccess = "success"
	StatusError   = "error"
	StatusSkipped = "skipped"
	StatusPending = "pending"
)

// RepoState is the persistent record of repo-guardian's most recent
// interaction with a single repository. Identity is the
// (InstallationID, Owner, Repo) tuple; everything else is mutable
// across reconcile attempts.
//
// LastCheckedAt is a pointer to distinguish "never checked" (nil)
// from "checked at the zero time" (which Postgres would coerce to
// 0001-01-01). The Postgres schema uses NULL for the same purpose.
type RepoState struct {
	InstallationID  int64
	Owner           string
	Repo            string
	LastCheckedAt   *time.Time
	LastCheckStatus string
	LastError       string
	PolicyVersion   string

	// CatalogParseOK reports whether this repo's catalog-info.yaml
	// parsed into a Backstage Component on the last check. nil means no
	// catalog rule was evaluated, which is a different operator signal
	// from "evaluated and malformed" (false) — see DESIGN-0022
	// §Per-rule posture state.
	CatalogParseOK *bool
}

// RuleState is the persistent record of one rule's verdict for one
// repository (IMPL-0023 Phase 1 / DESIGN-0022 §Per-rule posture state).
// Identity is the (InstallationID, Owner, Repo, RuleName) tuple.
//
// Actionable means the repo is currently non-compliant with the rule.
// ActionableSince is the "missing since 2026-06-14" a compliance report
// needs: implementations set it on the false→true transition, clear it
// on true→false, and preserve it across true→true. Callers do NOT
// supply it — passing a value is meaningless, because only the store
// can see the prior row needed to decide the transition.
type RuleState struct {
	InstallationID int64
	Owner          string
	Repo           string
	RuleName       string
	RuleKind       string
	Actionable     bool
	PolicyVersion  string
}

// ReportFinding is one repository failing one rule, with the date it
// started failing. The rows behind the report's "which repos, failing
// which rules, since when" table — the question a metric structurally
// cannot answer, because a gauge knows how many and never which.
type ReportFinding struct {
	InstallationID  int64
	Owner           string
	Repo            string
	RuleName        string
	RuleKind        string
	ActionableSince *time.Time
}

// SnapshotRow is one historical (org, rule) compliance measurement.
type SnapshotRow struct {
	Org             string
	RuleName        string
	ActionableCount int
	TrackedCount    int
	SnapshotAt      time.Time
}

// ReportData is everything the report renderer needs for one run,
// read in a single transaction.
//
// One read rather than three keeps the narrative consistent: the
// findings table and the headline percentage are computed from the
// same instant, so a report can never say "3 repos failing" above a
// table listing four.
type ReportData struct {
	// Findings are the currently-actionable (repo, rule) pairs across
	// every active repository, ordered by org, rule, then repo.
	Findings []ReportFinding

	// Current is the live per-(org, rule) tally, computed the same way
	// InsertComplianceSnapshot computes it — so today's numbers are
	// comparable with the history even though no snapshot has been
	// taken yet today.
	Current []SnapshotRow

	// Previous is the most recent stored snapshot per (org, rule),
	// which is what Current is compared against for the trend. Empty on
	// a deployment that has never taken one.
	Previous []SnapshotRow
}

// Park reasons as they appear on the Unmeasurable label. They are
// derived from RepoState, which has no park_reason column: a park
// writes StatusError for an unreadable repo and StatusSkipped with the
// bare reason in LastError for archived and forked ones (INV-0015).
//
// Implementations MUST map to exactly this closed set. LastError is
// free text, so passing it through unmapped would make an unbounded
// Prometheus label out of an error message.
const (
	ReasonAccessDenied = "access_denied"
	ReasonArchived     = "archived"
	ReasonFork         = "fork"
	ReasonUnknown      = "unknown"
)

// RuleCount is one row of the posture aggregate: how many repositories
// in Org currently fail RuleName.
type RuleCount struct {
	Org      string
	RuleName string
	Count    int
}

// OrgCount is a per-org scalar from the posture aggregate.
type OrgCount struct {
	Org   string
	Count int
}

// ReasonCount is a per-org scalar broken down by park reason.
type ReasonCount struct {
	Org    string
	Reason string
	Count  int
}

// Posture is one consistent read of fleet compliance state — the whole
// input to a single exporter tick (DESIGN-0022 §Leader-scoped posture
// exporter).
//
// The three slices are read in one transaction so a ratio computed
// across them can never straddle a concurrent write-back and report
// more actionable repos than tracked ones.
//
// Actionable and Tracked cover ACTIVE repositories only. Parked ones
// are excluded from both and surfaced separately in Unmeasurable, so a
// compliance ratio is only ever computed over repositories the fleet
// can actually measure. Letting a parked repo hold its last verdict
// would skew the ratio in a way nothing corrects — parking is exactly
// the state in which a repository is never re-checked.
type Posture struct {
	// Actionable is per (org, rule): repos currently failing that rule.
	Actionable []RuleCount

	// Tracked is per org: distinct repos that have any posture at all,
	// i.e. that have been checked at least once. It is the compliance
	// denominator. Repos discovered but not yet checked are absent,
	// because "not measured" is not "compliant".
	Tracked []OrgCount

	// Unmeasurable is per (org, reason): parked repos, excluded from
	// the two above. Its total should track the standing population
	// implied by repos_parked_total; a persistent disagreement means
	// parks that never un-parked or un-parks nobody counted.
	Unmeasurable []ReasonCount
}

// Store is the persistence boundary for per-repo reconcile state.
// All methods are context-cancellable; implementations must respect
// ctx.Done() for in-flight queries.
//
// StaleRepos drives the sweep loop. A row is "stale" when either its
// LastCheckedAt is older than freshness OR its PolicyVersion differs
// from currentPolicyVersion (handles new-rule rollouts without per-rule
// freshness tracking — see DESIGN-0012 Q2 resolution).
//
// UpsertIfMissing is the discovery write-path (DESIGN-0017): it inserts
// a row with LastCheckStatus=StatusPending and LastCheckedAt=nil iff no
// row exists for the (installationID, owner, repo) tuple. It returns
// (true, nil) when a new row was inserted and (false, nil) when a row
// already existed — callers use the boolean to drive
// "discovered_repos_total" instrumentation and to decide whether to
// emit a slog.Info on first-sight. Implementations MUST execute this
// as a single atomic statement (no read-then-write race), and MUST NOT
// overwrite any existing fields on conflict.
//
// UpsertRuleStates is the posture write-path (DESIGN-0022). It replaces
// the complete set of rule verdicts for ONE repository, identified by
// the (InstallationID, Owner, Repo) shared by every element of states:
// rows in states are upserted, and any pre-existing row for that repo
// whose rule name is absent from states is deleted. That
// delete-not-in is what makes a renamed or removed rule stop counting
// against compliance on the very next check instead of lingering
// forever (DESIGN-0022 OQ3 → a).
//
// An empty (or nil) states slice is therefore meaningful, not a no-op:
// it clears every rule row for the repo. Callers that merely failed to
// learn anything must not call this at all.
//
// Implementations MUST apply the whole batch in a single transaction,
// so a concurrent posture query never observes a repo mid-rewrite with
// some rules deleted and others not yet written.
type Store interface {
	GetRepoState(ctx context.Context, installationID int64, owner, repo string) (*RepoState, error)
	UpdateRepoState(ctx context.Context, s *RepoState) error
	UpsertIfMissing(ctx context.Context, s *RepoState) (created bool, err error)
	UpsertRuleStates(ctx context.Context, installationID int64, owner, repo string, states []RuleState) error
	StaleRepos(ctx context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]RepoState, error)

	// Posture is the read side of UpsertRuleStates: one consistent
	// snapshot of fleet compliance, sized for a single exporter tick.
	// Implementations MUST read the three aggregates in one
	// transaction — see Posture for why — and MUST restrict Actionable
	// and Tracked to rows whose repository is still active.
	Posture(ctx context.Context) (*Posture, error)

	// InsertComplianceSnapshot appends one dated row per (org, rule) to
	// the history table and returns how many rows it wrote.
	//
	// Written as a single INSERT ... SELECT so the whole snapshot is one
	// statement against one MVCC view: reading the counts into Go and
	// writing them back would let a worker's write-back land between the
	// read and the insert, dating a mixture of two states as one moment.
	//
	// The caller supplies the timestamp rather than the database, so
	// every row of one snapshot shares an instant exactly and tests can
	// seed a history without sleeping.
	//
	// Idempotent on (org, rule, at): re-running with the same timestamp
	// inserts nothing rather than erroring, so a retry after a partial
	// failure is safe.
	InsertComplianceSnapshot(ctx context.Context, at time.Time) (rows int, err error)

	// ReportData reads everything the report CLI renders, in one
	// transaction. See ReportData for why it is one call.
	//
	// Restricted to active repositories, consistently with Posture and
	// InsertComplianceSnapshot: a parked repository is one nobody can
	// measure, so naming it in a compliance report as failing would be
	// asserting something we did not check.
	ReportData(ctx context.Context) (*ReportData, error)

	// Deactivate marks a repository inactive so StaleRepos stops
	// returning it. It is deliberately one-way: nothing here can set a
	// row active again, because only discovery observes the
	// installation's real repository set and is therefore the only
	// component entitled to say a repository is reachable (INV-0015).
	// Reactivation happens in UpsertIfMissing.
	//
	// Idempotent: deactivating an already-inactive or absent row is not
	// an error.
	Deactivate(ctx context.Context, installationID int64, owner, repo string) error

	Close() error
}
