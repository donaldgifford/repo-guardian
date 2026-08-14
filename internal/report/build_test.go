package report

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// testLogger discards output. Enrich warns on every link failure and
// several tests provoke one deliberately.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ruleByName finds a rule line, failing the test if it is absent.
func ruleByName(t *testing.T, o *Org, name string) RuleLine {
	t.Helper()

	for _, r := range o.Rules {
		if r.Name == name {
			return r
		}
	}

	t.Fatalf("Build() produced no rule %q for org %s; rules are %v", name, o.Name, ruleNames(o))

	return RuleLine{}
}

func ruleNames(o *Org) []string {
	out := make([]string, 0, len(o.Rules))
	for _, r := range o.Rules {
		out = append(out, r.Name)
	}

	return out
}

// TestBuild_Trends covers the four ways a rule can relate to history.
//
// The removed-rule case is the one that matters most: a rule present
// only in the previous snapshot has stopped being evaluated, so
// reporting a percentage for it would describe a measurement nobody
// took today. It must vanish from the output, not appear at its stale
// value.
func TestBuild_Trends(t *testing.T) {
	t.Parallel()

	r := newRenderer(t, nil)
	orgs := r.Build(fullData())

	if len(orgs) != 1 {
		t.Fatalf("Build() = %d orgs, want 1", len(orgs))
	}

	org := &orgs[0]

	if !org.HasHistory {
		t.Error("Build().HasHistory = false, want true; the trend column would be suppressed")
	}

	for _, name := range ruleNames(org) {
		if name == "retired-rule" {
			t.Fatalf("Build() kept %q, which exists only in the previous snapshot; a rule nobody evaluated today has no compliance number", name)
		}
	}

	tests := []struct {
		rule       string
		wantTrend  TrendState
		wantDelta  int
		wantDated  bool
		wantKind   string
		wantRender string
	}{
		{
			rule:       "codeowners",
			wantTrend:  TrendImproved,
			wantDelta:  -3,
			wantDated:  true,
			wantKind:   "file",
			wantRender: "3 fewer since 2026-08-03",
		},
		{
			rule:      "dependabot",
			wantTrend: TrendFlat,
			wantDelta: 0,
			wantDated: true,
			// No finding carries this rule, so no finding carries its
			// kind either. Blank, not guessed.
			wantKind:   "",
			wantRender: "no change since 2026-08-03",
		},
		{
			rule:       "renovate",
			wantTrend:  TrendUnknown,
			wantDelta:  0,
			wantDated:  false,
			wantKind:   "file",
			wantRender: "new",
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			line := ruleByName(t, org, tt.rule)

			if line.Trend != tt.wantTrend {
				t.Errorf("Build().Rules[%s].Trend = %v, want %v", tt.rule, line.Trend, tt.wantTrend)
			}

			if line.Delta != tt.wantDelta {
				t.Errorf("Build().Rules[%s].Delta = %d, want %d", tt.rule, line.Delta, tt.wantDelta)
			}

			if got := !line.ComparedAt.IsZero(); got != tt.wantDated {
				t.Errorf("Build().Rules[%s].ComparedAt set = %t, want %t", tt.rule, got, tt.wantDated)
			}

			if line.Kind != tt.wantKind {
				t.Errorf("Build().Rules[%s].Kind = %q, want %q", tt.rule, line.Kind, tt.wantKind)
			}

			if got := renderTrend(line); got != tt.wantRender {
				t.Errorf("renderTrend(%s) = %q, want %q", tt.rule, got, tt.wantRender)
			}
		})
	}
}

// TestBuild_PerRuleComparisonDate pins that the baseline is per rule.
//
// A rule that was disabled when the last snapshot ran is compared
// against the last run that actually measured it, which is an older
// date than its neighbours'. Collapsing every cell onto one report-wide
// date would date some deltas to a run that never saw the rule.
func TestBuild_PerRuleComparisonDate(t *testing.T) {
	t.Parallel()

	older := lastWeek.AddDate(0, 0, -14)

	data := &store.ReportData{
		Current: []store.SnapshotRow{
			{Org: "acme", RuleName: "fresh", ActionableCount: 1, TrackedCount: 4},
			{Org: "acme", RuleName: "stale", ActionableCount: 1, TrackedCount: 4},
		},
		Previous: []store.SnapshotRow{
			{Org: "acme", RuleName: "fresh", ActionableCount: 2, TrackedCount: 4, SnapshotAt: lastWeek},
			{Org: "acme", RuleName: "stale", ActionableCount: 2, TrackedCount: 4, SnapshotAt: older},
		},
	}

	orgs := newRenderer(t, nil).Build(data)
	org := &orgs[0]

	if got := ruleByName(t, org, "fresh").ComparedAt; !got.Equal(lastWeek) {
		t.Errorf("Build().Rules[fresh].ComparedAt = %s, want %s", got, lastWeek)
	}

	if got := ruleByName(t, org, "stale").ComparedAt; !got.Equal(older) {
		t.Errorf("Build().Rules[stale].ComparedAt = %s, want %s", got, older)
	}
}

// TestBuild_ZeroSnapshots covers a deployment that has never run the
// compliance-snapshot handler.
func TestBuild_ZeroSnapshots(t *testing.T) {
	t.Parallel()

	data := fullData()
	data.Previous = nil

	orgs := newRenderer(t, nil).Build(data)
	org := &orgs[0]

	if org.HasHistory {
		t.Error("Build().HasHistory = true with no stored snapshots, want false")
	}

	for _, line := range org.Rules {
		if line.Trend != TrendUnknown {
			t.Errorf("Build().Rules[%s].Trend = %v with no history, want TrendUnknown", line.Name, line.Trend)
		}
	}
}

// TestBuild_EmptyReadProducesNoOrgs pins that a fleet with nothing
// evaluated yields zero files rather than one empty file per org.
func TestBuild_EmptyReadProducesNoOrgs(t *testing.T) {
	t.Parallel()

	if orgs := newRenderer(t, nil).Build(&store.ReportData{}); len(orgs) != 0 {
		t.Errorf("Build(empty) = %d orgs, want 0", len(orgs))
	}
}

// TestBuild_FindingForUnknownOrgIsDropped pins the guard against a
// findings row whose org has no tally.
//
// It cannot happen in one consistent read, which is exactly why the
// guard needs a test: a future change that reads findings and tallies
// separately would otherwise produce a findings table under an org
// header that never says how many rules it has.
func TestBuild_FindingForUnknownOrgIsDropped(t *testing.T) {
	t.Parallel()

	data := &store.ReportData{
		Findings: []store.ReportFinding{
			{Owner: "ghost", Repo: "api", RuleName: "codeowners", RuleKind: "file"},
		},
		Current: []store.SnapshotRow{
			{Org: "acme", RuleName: "codeowners", ActionableCount: 0, TrackedCount: 1},
		},
	}

	orgs := newRenderer(t, nil).Build(data)

	if len(orgs) != 1 || orgs[0].Name != "acme" {
		t.Fatalf("Build() = %v, want exactly the acme org", orgs)
	}

	if len(orgs[0].Findings) != 0 {
		t.Errorf("Build().Findings = %v, want none; the finding belongs to an org with no tally", orgs[0].Findings)
	}
}

// TestCompliantPercent covers the floor and the undefined case.
func TestCompliantPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		line       RuleLine
		want       float64
		wantOK     bool
		wantRender string
	}{
		{
			// 1999 of 2000 is 99.95%, which ROUNDS to 100.0% and floors
			// to 99.9%. This is the case the whole floor exists for: a
			// report calling a fleet fully compliant while a repository
			// is not has told a lie somebody will act on.
			//
			// Note 999-of-1000 does NOT discriminate — it is 99.9% under
			// both rules — so a fixture built on it would assert nothing
			// about floor-versus-round despite its name.
			name:       "floors rather than rounds",
			line:       RuleLine{Actionable: 1, Tracked: 2000},
			want:       99.9,
			wantOK:     true,
			wantRender: "99.9%",
		},
		{
			// 2 of 3 is 66.66…%, floored to 66.6 rather than rounded to
			// 66.7.
			name:       "repeating decimal",
			line:       RuleLine{Actionable: 1, Tracked: 3},
			want:       66.6,
			wantOK:     true,
			wantRender: "66.6%",
		},
		{
			name:       "all passing",
			line:       RuleLine{Actionable: 0, Tracked: 4},
			want:       100,
			wantOK:     true,
			wantRender: "100.0%",
		},
		{
			name:       "none passing",
			line:       RuleLine{Actionable: 4, Tracked: 4},
			want:       0,
			wantOK:     true,
			wantRender: "0.0%",
		},
		{
			name:       "nothing tracked is unmeasured, not perfect",
			line:       RuleLine{Actionable: 0, Tracked: 0},
			want:       0,
			wantOK:     false,
			wantRender: "n/a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.line.CompliantPercent()
			if ok != tt.wantOK {
				t.Fatalf("CompliantPercent() ok = %t, want %t", ok, tt.wantOK)
			}

			if ok && got != tt.want {
				t.Errorf("CompliantPercent() = %v, want %v", got, tt.want)
			}

			if rendered := renderPercent(tt.line); rendered != tt.wantRender {
				t.Errorf("renderPercent() = %q, want %q", rendered, tt.wantRender)
			}
		})
	}
}

// TestRenderSince pins that an unknown start date is an em dash.
//
// A zero time.Time formats as year 1, which would claim the repository
// has been failing since before the organisation existed.
func TestRenderSince(t *testing.T) {
	t.Parallel()

	if got := renderSince(nil); got != "—" {
		t.Errorf("renderSince(nil) = %q, want an em dash", got)
	}

	when := time.Date(2026, 7, 1, 23, 59, 0, 0, time.UTC)
	if got := renderSince(&when); got != "2026-07-01" {
		t.Errorf("renderSince(%s) = %q, want 2026-07-01", when, got)
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
