package report

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// updateGolden rewrites the fixtures instead of asserting against them.
//
// Run `go test ./internal/report -update` after a deliberate format
// change, then READ the diff. The whole value of a golden file is that
// an unintended change to the report's wording or column layout shows
// up as a reviewable diff; regenerating without reading it converts the
// suite into a very slow way of asserting that the code equals itself.
var updateGolden = flag.Bool("update", false, "rewrite golden report fixtures")

// fixedNow pins the report header. Real time in a golden file would
// make every run a failure.
var fixedNow = time.Date(2026, 8, 10, 14, 30, 0, 0, time.UTC)

// lastWeek dates the previous snapshot in the trend fixtures.
var lastWeek = time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)

// newRenderer builds a Renderer with the clock pinned.
func newRenderer(t *testing.T) *Renderer {
	t.Helper()

	r, err := New(Options{
		Now:    func() time.Time { return fixedNow },
		Logger: testLogger(),
	})
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}

	return r
}

// percentOf is the test oracle for the shared query's percentage:
// integer division floors, so 1999 of 2000 is 999 tenths, never 1000.
func percentOf(compliant, nonCompliant int) *float64 {
	if compliant+nonCompliant == 0 {
		return nil
	}

	v := float64(compliant*1000/(compliant+nonCompliant)) / 10

	return &v
}

// count builds one shared-query row with its percentage filled in the
// way the SQL fills it.
func count(org, kind, rule string, compliant, nonCompliant, na, unknown int) store.ComplianceCount {
	return store.ComplianceCount{
		Org: org, Kind: findings.RuleKind(kind), RuleName: rule,
		Compliant: compliant, NonCompliant: nonCompliant, NotApplicable: na, Unknown: unknown,
		Percent: percentOf(compliant, nonCompliant),
	}
}

// snap builds one stored acme file-rule snapshot row.
func snap(rule string, compliant, nonCompliant int, at time.Time) store.ComplianceSnapshot {
	c := count("acme", "file", rule, compliant, nonCompliant, 0, 0)
	c.Percent = nil

	return store.ComplianceSnapshot{ComplianceCount: c, SnapshotAt: at}
}

// failing builds one non-compliant finding.
func failing(org, repo, rule string, reason findings.Reason, since time.Time) store.FailingFinding {
	return store.FailingFinding{
		InstallationID: 11, Org: org, Repo: repo, Kind: findings.RuleKindFile, RuleName: rule,
		Reason: reason, Remediation: findings.RemediationNone, Since: since,
	}
}

// assertGolden compares rendered markdown against testdata.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden.md")

	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatalf("MkdirAll(testdata) = %v, want nil", err)
		}

		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) = %v, want nil", path, err)
		}

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) = %v, want nil; run `go test ./internal/report -update` to create it", path, err)
	}

	if got != string(want) {
		t.Errorf("Render() differs from %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// fullData is the everything-at-once fixture: an improving rule, a flat
// rule, a rule with no history, a setting rule sharing nothing with the
// file rules, and — critically — a rule present only in the history,
// which must NOT appear in the output.
func fullData() *store.ComplianceReport {
	return &store.ComplianceReport{
		Findings: []store.FailingFinding{
			failing("acme", "api", "codeowners", findings.ReasonFileMissing, time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)),
			// Backfilled from v1: the reason says so and the date is v1's.
			failing("acme", "web", "codeowners", findings.ReasonMigratedFromV1, time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)),
			failing("acme", "web", "renovate", findings.ReasonAssertionFailed, time.Date(2026, 8, 9, 22, 0, 0, 0, time.UTC)),
		},
		Current: []store.ComplianceCount{
			count("acme", "file", "codeowners", 8, 2, 0, 0),
			count("acme", "file", "dependabot", 10, 0, 0, 0),
			count("acme", "file", "renovate", 3, 1, 6, 0),
			count("acme", "setting", "vuln_alerts", 9, 0, 0, 1),
		},
		Previous: []store.ComplianceSnapshot{
			snap("codeowners", 5, 5, lastWeek),
			snap("dependabot", 9, 0, lastWeek),
			snap("retired-rule", 7, 3, lastWeek),
		},
	}
}

// withPRs is fullData with every PR-cell shape: our open PR, a human PR
// the rule yields to, a dry run, and nothing.
func withPRs() *store.ComplianceReport {
	d := fullData()
	d.Findings[0].Remediation = findings.RemediationPROpen
	d.Findings[0].PRURL = "https://github.example/acme/api/pull/7"
	d.Findings[1].Remediation = findings.RemediationForeignPR
	d.Findings[1].PRURL = "https://github.example/acme/web/pull/3"
	d.Findings[2].Remediation = findings.RemediationDryRun

	return d
}

// TestRender_Golden pins the rendered markdown for the shapes an
// operator will actually receive.
func TestRender_Golden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data func() *store.ComplianceReport
	}{
		{
			name: "full_with_history",
			data: fullData,
		},
		{
			// The first run a deployment ever makes. No trend column at
			// all, plus the note explaining why, rather than a column of
			// zeroes claiming stability nobody measured.
			name: "no_history",
			data: func() *store.ComplianceReport {
				d := fullData()
				d.Previous = nil

				return d
			},
		},
		{
			// The happy fleet. Rules evaluated, nothing failing.
			name: "all_passing",
			data: func() *store.ComplianceReport {
				return &store.ComplianceReport{
					Current:  []store.ComplianceCount{count("acme", "file", "codeowners", 12, 0, 0, 0)},
					Previous: []store.ComplianceSnapshot{snap("codeowners", 8, 4, lastWeek)},
				}
			},
		},
		{
			// A configured rule that applies nowhere. Must read "n/a",
			// never 100% — an unmeasured rule scoring perfectly is the
			// most misleading cell this format could produce.
			name: "unmeasured_rule",
			data: func() *store.ComplianceReport {
				return &store.ComplianceReport{
					Current: []store.ComplianceCount{count("acme", "branch_protection", "main", 0, 0, 5, 0)},
				}
			},
		},
		{
			// 1999 of 2000 is 99.95%: it must read 99.9%, not the 100.0%
			// a rounding rule would print. 2000 rather than 1000 because
			// 999-of-1000 floors and rounds to the same 99.9%.
			name: "floored_percent",
			data: func() *store.ComplianceReport {
				return &store.ComplianceReport{
					Findings: []store.FailingFinding{
						failing("acme", "straggler", "codeowners", findings.ReasonFileMissing, lastWeek),
					},
					Current: []store.ComplianceCount{count("acme", "file", "codeowners", 1999, 1, 0, 0)},
				}
			},
		},
		{
			// Two orgs in one read must render as two independent
			// documents; nothing from acme may leak into globex.
			name: "second_org",
			data: func() *store.ComplianceReport {
				d := fullData()
				d.Findings = append(d.Findings,
					failing("globex", "tools", "dependabot", findings.ReasonFileMissing, lastWeek))
				d.Current = append(d.Current, count("globex", "file", "dependabot", 2, 1, 0, 0))

				return d
			},
		},
		{
			// A pipe would silently shift every column to its right; a
			// backtick would open a code span swallowing the row.
			name: "escaped_cells",
			data: func() *store.ComplianceReport {
				return &store.ComplianceReport{
					Findings: []store.FailingFinding{
						failing("acme", "a|b", "pipe|rule", findings.ReasonFileMissing, lastWeek),
						failing("acme", "tick`repo", "pipe|rule", findings.ReasonFileMissing, lastWeek),
					},
					Current: []store.ComplianceCount{count("acme", "file", "pipe|rule", 0, 2, 0, 0)},
				}
			},
		},
		{
			// PR links come from evidence, never from a live lookup.
			name: "with_pr_links",
			data: withPRs,
		},
		{
			// A rule yielding to a human PR is non-compliant with a link
			// to that PR, not to ours.
			name: "foreign_pr",
			data: func() *store.ComplianceReport {
				d := withPRs()
				d.Findings = d.Findings[1:2]

				return d
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRenderer(t)

			orgs := r.Build(tt.data())
			if len(orgs) == 0 {
				t.Fatalf("Build() returned no orgs; the fixture renders nothing")
			}

			for _, o := range orgs {
				body, err := r.Render(o)
				if err != nil {
					t.Fatalf("Render(%s) = %v, want nil", o.Name, err)
				}

				name := tt.name
				if len(orgs) > 1 {
					name = tt.name + "_" + o.Name
				}

				assertGolden(t, name, body)
			}
		})
	}
}

// TestRender_NoRulesEvaluated covers the defensive branch Build cannot
// reach.
//
// Build derives orgs from the Current tallies, so an org with zero
// rules never comes out of it. The template still handles the case, and
// this pins what it says — an empty document with no explanation would
// look like a bug in the tool rather than an absence of data.
func TestRender_NoRulesEvaluated(t *testing.T) {
	t.Parallel()

	r := newRenderer(t)

	body, err := r.Render(Org{Name: "acme", GeneratedAt: fixedNow})
	if err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}

	assertGolden(t, "no_rules_evaluated", body)
}
