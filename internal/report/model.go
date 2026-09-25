// Package report renders per-org compliance reports from persisted
// posture state. See DESIGN-0022 §Compliance snapshots and the per-org
// report, and IMPL-0023 Phase 4.
//
// The report exists to answer the question no metric can: not "how many
// repositories are failing" but WHICH ones, failing WHICH rules, since
// WHEN. A gauge is a number; this is a list with dates on it, which is
// what somebody chasing compliance actually has to work from.
//
// # Two stages
//
// Build projects one store read (store.ComplianceReport, the findings
// model of DESIGN-0025) onto a per-org view model, and Render turns one
// org into markdown. Both are pure and deterministic, so the golden-file
// tests exercise the real rendering path without a database, a GitHub
// client or credentials. PR links come from the findings' evidence, so
// the report makes no API calls at all.
package report

import (
	"time"

	"github.com/donaldgifford/repo-guardian/internal/findings"
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

// RuleLine is one rule's compliance summary for one org: a row of the
// shared compliance query (queries/compliance.sql).
type RuleLine struct {
	Name          string
	Kind          string
	Compliant     int
	NonCompliant  int
	NotApplicable int
	Unknown       int

	// Percent is the shared query's compliant percentage, floored to one
	// decimal; nil when the rule applies to no repository. It is taken
	// as computed, never recomputed, so the report, the snapshots and
	// the API print the same number.
	Percent *float64

	// Delta is NonCompliant minus the previous snapshot's. Meaningless
	// unless Trend is not TrendUnknown.
	Delta int

	Trend TrendState

	// ComparedAt dates the snapshot Delta was computed against. Rules
	// can be compared against different dates — a rule that was
	// disabled when the last run happened is compared against the last
	// run that actually measured it — so the date is per rule and the
	// renderer prints it. Zero when Trend is TrendUnknown.
	ComparedAt time.Time
}

// CompliantPercent returns the share of measured repositories
// satisfying the rule, and whether it is defined at all.
//
// Undefined when nothing was measured. A rule configured but applying
// to no repository is not 100% compliant — it is unmeasured, and
// reporting a perfect score for it is exactly the kind of comfortable
// wrong number this whole design exists to remove. The value is the
// shared query's, which floors rather than rounds: 1999 of 2000 reads
// 99.9%, never 100%.
func (r RuleLine) CompliantPercent() (float64, bool) { //nolint:gocritic // value receiver: text/template cannot address a range variable
	if r.Percent == nil {
		return 0, false
	}

	return *r.Percent, true
}

// Finding is one repository failing one rule.
type Finding struct {
	Repo        string
	RuleName    string
	RuleKind    string
	Reason      findings.Reason
	Remediation findings.Remediation

	// Since is when the repository started failing this rule. nil when
	// unknown — rendered as an em dash rather than a zero date, which
	// would claim the failure started in year 1.
	Since *time.Time

	// PRURL is the PR recorded in the finding's evidence: repo-guardian's
	// own for pr_open, a human's for foreign_pr. Empty otherwise.
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
}

// Compliant returns the number of (repo, rule) pairs passing, across
// every rule in the org.
func (o Org) Compliant() int { //nolint:gocritic // value receiver: text/template cannot address a range variable
	total := 0
	for _, r := range o.Rules {
		total += r.Compliant
	}

	return total
}

// Evaluated returns the number of measured (repo, rule) pairs: compliant
// plus non-compliant. Not-applicable and unknown are not measurements.
func (o Org) Evaluated() int { //nolint:gocritic // value receiver: text/template cannot address a range variable
	total := 0
	for _, r := range o.Rules {
		total += r.Compliant + r.NonCompliant
	}

	return total
}

// ruleKey identifies an (org, kind, rule) triple. Kind is part of the
// key because a file rule and a setting rule may share a name.
type ruleKey struct {
	org  string
	kind string
	rule string
}

// indexSnapshots keys rows for lookup by (org, kind, rule).
func indexSnapshots(rows []store.ComplianceSnapshot) map[ruleKey]store.ComplianceSnapshot {
	out := make(map[ruleKey]store.ComplianceSnapshot, len(rows))
	for i := range rows {
		out[ruleKey{org: rows[i].Org, kind: string(rows[i].Kind), rule: rows[i].RuleName}] = rows[i]
	}

	return out
}
