package report

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func orgNames(orgs []Org) []string {
	out := make([]string, 0, len(orgs))
	for i := range orgs {
		out = append(out, orgs[i].Name)
	}

	return out
}

// TestBuild_SplitsTheLineages pins where each decision lands: replaced
// is the org's version of a baseline control and stays in the baseline
// lineage (DESIGN-0030 D17).
func TestBuild_SplitsTheLineages(t *testing.T) {
	t.Parallel()

	orgs := newRenderer(t).Build(fullData())
	if len(orgs) != 1 {
		t.Fatalf("Build() = %v orgs, want acme alone", orgNames(orgs))
	}

	o := orgs[0]

	baseline := make([]string, 0, len(o.Baseline))
	added := make([]string, 0, len(o.OrgAdded))

	for _, l := range o.Baseline {
		baseline = append(baseline, l.Control+"/"+l.Decision)
	}

	for _, l := range o.OrgAdded {
		added = append(added, l.Control+"/"+l.Decision)
	}

	if want := []string{"codeowners/baseline", "renovate/replaced"}; !slices.Equal(baseline, want) {
		t.Errorf("baseline = %v, want %v", baseline, want)
	}

	if want := []string{"labels/org_added"}; !slices.Equal(added, want) {
		t.Errorf("org-added = %v, want %v", added, want)
	}

	if len(o.Exclusions) != 2 {
		t.Errorf("exclusions = %+v, want both acme rows", o.Exclusions)
	}
}

// TestBuild_OrgsAreSortedAndIncludeExclusionOnlyOrgs pins determinism
// and that an org with nothing assigned is still reported.
func TestBuild_OrgsAreSortedAndIncludeExclusionOnlyOrgs(t *testing.T) {
	t.Parallel()

	data := &Data{
		Posture:    []store.ControlPosture{row("zeta", "codeowners", DecisionBaseline, buckets{compliant: 1})},
		Exclusions: []store.Exclusion{{Org: "alpha", Repository: "r", State: "excluded"}},
	}

	if got := orgNames(newRenderer(t).Build(data)); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Errorf("Build() orgs = %v, want [alpha zeta]", got)
	}
}

func TestBuild_EmptyReadProducesNoOrgs(t *testing.T) {
	t.Parallel()

	if got := newRenderer(t).Build(&Data{}); len(got) != 0 {
		t.Errorf("Build(empty) = %v, want no orgs", orgNames(got))
	}
}

// TestOrg_Totals pins the summed percentages against the shared
// definition: Go totals agree with what the SQL would compute on the
// same counts, and an org measuring nothing has no percentage.
func TestOrg_Totals(t *testing.T) {
	t.Parallel()

	o := newRenderer(t).Build(fullData())[0]
	tot := o.Totals()

	// 82 of 100 measured (40+30 of 48+40, plus labels' 12 of 12).
	if tot.Compliant != 82 || tot.Measured() != 100 || tot.Percent == nil || *tot.Percent != 82 {
		t.Errorf("totals = %+v (percent %v), want 82 of 100 → 82.0", tot, tot.Percent)
	}

	// Measured 103 (100 + 3 N/A) of 105 assigned: 98.0 after flooring.
	if tot.Assigned != 105 || tot.Coverage == nil || *tot.Coverage != 98 {
		t.Errorf("coverage = %v of %d assigned, want 98.0 of 105", tot.Coverage, tot.Assigned)
	}

	empty := Org{Baseline: []ControlLine{{Control: "codeowners", Pending: 3, Assigned: 3}}}.Totals()
	if empty.Percent != nil {
		t.Errorf("an org measuring nothing has percent %v, want nil (no data)", *empty.Percent)
	}
}

// TestRenderPercent pins "no data" for an empty denominator.
func TestRenderPercent(t *testing.T) {
	t.Parallel()

	v := 99.9

	if got := renderPercent(nil); got != noData {
		t.Errorf("renderPercent(nil) = %q, want %q", got, noData)
	}

	if got := renderPercent(&v); got != "99.9%" {
		t.Errorf("renderPercent(99.9) = %q, want 99.9%%", got)
	}
}

// TestFloorPercent pins that the Go mirror floors like the SQL:
// 1999 of 2000 is 99.9, never 100.
func TestFloorPercent(t *testing.T) {
	t.Parallel()

	if got := floorPercent(1999, 2000); got == nil || *got != 99.9 {
		t.Errorf("floorPercent(1999, 2000) = %v, want 99.9", got)
	}

	if got := floorPercent(0, 0); got != nil {
		t.Errorf("floorPercent(0, 0) = %v, want nil", *got)
	}
}

// TestMdcell pins the table-hygiene rules.
func TestMdcell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: "plain", want: "plain"},
		{in: "a|b", want: `a\|b`},
		{in: "tick`name", want: "tick\\`name"},
		{in: "two\nlines", want: "two lines"},
		{in: "crlf\r\nlines", want: "crlf lines"},
		{in: "  padded  ", want: "padded"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			if got := mdcell(tt.in); got != tt.want {
				t.Errorf("mdcell(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
