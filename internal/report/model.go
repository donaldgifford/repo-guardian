// Package report renders per-org compliance reports from persisted
// posture state. See DESIGN-0022 §Compliance snapshots and the per-org
// report, and IMPL-0023 Phase 4.
//
// The report exists to answer the question no metric can: not "how many
// repositories are failing" but WHICH ones, failing WHICH rules, since
// WHEN. A gauge is a number; this is a list with dates on it, which is
// what somebody chasing compliance actually has to work from.
//
// # Three stages
//
// Build projects one store read onto a per-org view model, Enrich
// optionally decorates it with live GitHub links, and Render turns one
// org into markdown. The split is deliberate: Build and Render are pure
// and deterministic, so the golden-file tests exercise the real
// rendering path without a GitHub client, a network, or credentials.
// Only Enrich touches the outside world, and it is optional.
package report

import (
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// TrendState classifies a rule's movement against its last snapshot.
//
// It is an enum rather than a bare delta because the interesting cases
// are not arithmetic. A rule with no history is not "unchanged", and a
// deployment that has never taken a snapshot has no trend at all —
// rendering either as 0 would assert stability that was never measured.
type TrendState int

const (
	// TrendUnknown means no snapshot exists for this rule, so there is
	// nothing to compare against. Rendered as "new", never as 0.
	TrendUnknown TrendState = iota

	// TrendImproved means fewer repositories fail the rule than at the
	// last snapshot.
	TrendImproved

	// TrendWorsened means more do.
	TrendWorsened

	// TrendFlat means the count is identical. Note this is a count, not
	// a set: the same number of failures against a different set of
	// repositories reads as flat, which is correct for a trend line and
	// is why the findings table exists underneath it.
	TrendFlat
)

// RuleLine is one rule's compliance summary for one org.
type RuleLine struct {
	Name       string
	Kind       string
	Actionable int
	Tracked    int

	// Delta is Actionable minus the previous snapshot's actionable
	// count. Meaningless unless Trend is not TrendUnknown.
	Delta int

	Trend TrendState

	// ComparedAt dates the snapshot Delta was computed against. Rules
	// can be compared against different dates — a rule that was
	// disabled when the last run happened is compared against the last
	// run that actually measured it — so the date is per rule and the
	// renderer prints it. Zero when Trend is TrendUnknown.
	ComparedAt time.Time
}

// CompliantPercent returns the share of tracked repositories satisfying
// the rule, and whether it is defined at all.
//
// Undefined when nothing was tracked. A rule configured but evaluated
// against no repository is not 100% compliant — it is unmeasured, and
// reporting a perfect score for it is exactly the kind of comfortable
// wrong number this whole design exists to remove.
//
// The result is FLOORED to one decimal, never rounded. 999 of 1000
// repositories must read as 99.9%, not 100%: a report that says a fleet
// is fully compliant when one repository is not has told a lie that
// someone will act on.
func (r RuleLine) CompliantPercent() (float64, bool) { //nolint:gocritic // value receiver: text/template cannot address a range variable
	if r.Tracked <= 0 {
		return 0, false
	}

	compliant := float64(r.Tracked - r.Actionable)

	return float64(int(compliant/float64(r.Tracked)*1000)) / 10, true
}

// Finding is one repository failing one rule.
type Finding struct {
	Repo     string
	RuleName string
	RuleKind string

	// Since is when the repository started failing this rule. nil when
	// unknown — rendered as an em dash rather than a zero date, which
	// would claim the failure started in year 1.
	Since *time.Time

	// PRURL is the open repo-guardian PR for the repository, filled by
	// Enrich. Empty when links were not requested or the lookup failed.
	PRURL string
}

// Org is one org's whole report.
type Org struct {
	Name        string
	GeneratedAt time.Time
	Rules       []RuleLine
	Findings    []Finding

	// HasHistory reports whether any rule has a snapshot to trend
	// against. False suppresses the trend column entirely rather than
	// filling it with placeholders.
	HasHistory bool

	// ShowLinks reports whether PR links were requested. False omits
	// the column; an empty column would be indistinguishable from "no
	// repository has an open PR".
	ShowLinks bool

	// LinkFailures counts repositories whose PR lookup failed, so a
	// partially enriched report is not read as a complete one.
	LinkFailures int
}

// Compliant returns the number of (repo, rule) pairs passing, across
// every rule in the org.
func (o Org) Compliant() int { //nolint:gocritic // value receiver: text/template cannot address a range variable
	total := 0
	for _, r := range o.Rules {
		total += r.Tracked - r.Actionable
	}

	return total
}

// Evaluated returns the total number of (repo, rule) evaluations.
func (o Org) Evaluated() int { //nolint:gocritic // value receiver: text/template cannot address a range variable
	total := 0
	for _, r := range o.Rules {
		total += r.Tracked
	}

	return total
}

// snapshotKey identifies a (org, rule) pair in the history.
type snapshotKey struct {
	org  string
	rule string
}

// indexSnapshots keys rows for lookup by (org, rule).
func indexSnapshots(rows []store.SnapshotRow) map[snapshotKey]store.SnapshotRow {
	out := make(map[snapshotKey]store.SnapshotRow, len(rows))
	for _, r := range rows {
		out[snapshotKey{org: r.Org, rule: r.RuleName}] = r
	}

	return out
}
