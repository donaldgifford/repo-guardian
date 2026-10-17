// Package report renders per-org compliance reports from the controls
// compliance query. See DESIGN-0030 § Compliance queries, D5 and D16,
// and IMPL-0029 task 6.9.
//
// The report answers what a gauge cannot: for each organisation, which
// controls are assigned, how many assignments sit in each bucket, how
// much of the org is actually measured, and which repositories and
// controls are excluded and why.
//
// # Two stages
//
// Build projects one store read (Data: the ControlCompliance rows and
// the ControlExclusions rows) onto a per-org view model, and Render
// turns one org into markdown. Both are pure and deterministic, so the
// golden-file tests exercise the real rendering path without a
// database, a GitHub client or credentials.
package report

import (
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Data is one store read: the compliance rows for every lineage and the
// exclusions report, both scoped to the same orgs.
type Data struct {
	Posture    []store.ControlPosture
	Exclusions []store.Exclusion
}

// The decisions a compliance row carries (DESIGN-0030 D17): the
// baseline lineage is a baseline control, or the org's replacement for
// one; org_added is a control the org assigned on its own.
const (
	DecisionBaseline = "baseline"
	DecisionReplaced = "replaced"
	DecisionOrgAdded = "org_added"
)

// ControlLine is one (control, decision) row of the compliance query
// for one org.
type ControlLine struct {
	Control  string
	Decision string

	Compliant     int
	NonCompliant  int
	Pending       int
	Stale         int
	Unmeasurable  int
	NotApplicable int
	Unknown       int
	Excluded      int
	Assigned      int

	// Percent is compliant ÷ (compliant + non_compliant), floored to one
	// decimal in SQL; nil, "no data", when nothing is measured. Taken as
	// computed, never recomputed, so the report, snapshots, the API and
	// the gauges print the same number.
	Percent *float64

	// Coverage is measured ÷ assigned from the same query; nil when
	// nothing is assigned.
	Coverage *float64
}

// Exclusion is one excluded or conflicted assignment, or one excluded
// repository (Control empty).
type Exclusion struct {
	Repository string
	Control    string
	State      string
	Reason     string
	ExcludedBy string
}

// Org is one org's whole report.
type Org struct {
	Name        string
	GeneratedAt time.Time

	// Baseline holds the baseline lineage: baseline controls and the
	// org's replacements for them.
	Baseline []ControlLine

	// OrgAdded holds the controls the org assigned on its own.
	OrgAdded []ControlLine

	Exclusions []Exclusion
}

// Totals sums an org's buckets across every control.
type Totals struct {
	Compliant     int
	NonCompliant  int
	Pending       int
	Stale         int
	Unmeasurable  int
	NotApplicable int
	Unknown       int
	Excluded      int
	Assigned      int

	// Percent is store.CompliantPercent over the summed counts: the Go
	// mirror of the SQL percentage, so a total agrees with its rows.
	Percent *float64

	// Coverage is the summed measured ÷ assigned, floored the same way.
	Coverage *float64
}

// Measured is the percentage's denominator.
func (t Totals) Measured() int { //nolint:gocritic // value receiver: text/template cannot address a range variable
	return t.Compliant + t.NonCompliant
}

// HasControls reports whether any control is assigned in the org.
func (o Org) HasControls() bool { //nolint:gocritic // value receiver: text/template cannot address a range variable
	return len(o.Baseline)+len(o.OrgAdded) > 0
}

// Totals sums the org's control lines.
func (o Org) Totals() Totals { //nolint:gocritic // value receiver: text/template cannot address a range variable
	var t Totals

	for _, lines := range [][]ControlLine{o.Baseline, o.OrgAdded} {
		for i := range lines {
			l := &lines[i]
			t.Compliant += l.Compliant
			t.NonCompliant += l.NonCompliant
			t.Pending += l.Pending
			t.Stale += l.Stale
			t.Unmeasurable += l.Unmeasurable
			t.NotApplicable += l.NotApplicable
			t.Unknown += l.Unknown
			t.Excluded += l.Excluded
			t.Assigned += l.Assigned
		}
	}

	t.Percent = store.CompliantPercent(store.StatusCounts{Compliant: t.Compliant, NonCompliant: t.NonCompliant})
	t.Coverage = floorPercent(t.Compliant+t.NonCompliant+t.NotApplicable, t.Assigned)

	return t
}

// floorPercent is num ÷ den as a percentage floored to one decimal, nil
// on an empty denominator: the SQL floor_percent function, for totals
// summed in Go.
func floorPercent(num, den int) *float64 {
	if den == 0 {
		return nil
	}

	p := float64(num*1000/den) / 10

	return &p
}
