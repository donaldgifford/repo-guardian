package report

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// buckets is one compliance row's counts, in the order the table prints
// them.
type buckets struct {
	compliant, nonCompliant, pending, stale, unmeasurable, na, unknown, excluded int
}

// row builds one compliance row with its assigned count, percentage and
// coverage filled the way the SQL fills them: excluded and conflicted
// assignments are not assigned, and both percentages floor.
func row(org, control, decision string, b buckets) store.ControlPosture {
	return store.ControlPosture{
		Org: org, ControlID: control, Decision: decision,
		Compliant: b.compliant, NonCompliant: b.nonCompliant, Pending: b.pending, Stale: b.stale,
		UnmeasurableAccessDenied: b.unmeasurable, NotApplicable: b.na, Unknown: b.unknown, Excluded: b.excluded,
		Assigned: assigned(b),
		Percent:  floorPercent(b.compliant, b.compliant+b.nonCompliant),
		Coverage: floorPercent(b.compliant+b.nonCompliant+b.na, assigned(b)),
	}
}

func assigned(b buckets) int {
	return b.compliant + b.nonCompliant + b.pending + b.stale + b.unmeasurable + b.na + b.unknown
}

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

// fullData is acme with every lineage and an exclusion of each kind.
func fullData() *Data {
	return &Data{
		Posture: []store.ControlPosture{
			row("acme", "codeowners", DecisionBaseline, buckets{compliant: 40, nonCompliant: 8, pending: 2}),
			row("acme", "labels", DecisionOrgAdded, buckets{compliant: 12, na: 3}),
			row("acme", "renovate", DecisionReplaced, buckets{compliant: 30, nonCompliant: 10, excluded: 2}),
		},
		Exclusions: []store.Exclusion{
			{Org: "acme", Repository: "legacy", ControlID: "renovate", State: "excluded", Reason: "frozen until Q4", ExcludedBy: "orgs/acme.hcl"},
			{Org: "acme", Repository: "sandbox", State: "excluded", Reason: "throwaway experiments"},
		},
	}
}

// TestRender_Golden renders each fixture and compares it byte for byte.
func TestRender_Golden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data *Data
		org  string
	}{
		{
			name: "full",
			data: fullData(),
			org:  "acme",
		},
		{
			// The case the whole percent definition exists for: assigned
			// everywhere, measured nowhere. "no data", never 100%.
			name: "no_data",
			data: &Data{Posture: []store.ControlPosture{
				row("acme", "codeowners", DecisionBaseline, buckets{pending: 25}),
			}},
			org: "acme",
		},
		{
			// One measurement out of a hundred assignments reads 100% of
			// one measurement beside 1% coverage, never as a measured
			// fleet (DESIGN-0030 posture tests).
			name: "pending_heavy",
			data: &Data{Posture: []store.ControlPosture{
				row("acme", "codeowners", DecisionBaseline, buckets{compliant: 1, pending: 99}),
			}},
			org: "acme",
		},
		{
			name: "stale",
			data: &Data{Posture: []store.ControlPosture{
				row("acme", "codeowners", DecisionBaseline, buckets{compliant: 5, nonCompliant: 1, stale: 14}),
			}},
			org: "acme",
		},
		{
			// Access loss keeps the last results but measures nothing new:
			// the assignments are unmeasurable and out of the percentage.
			name: "unmeasurable",
			data: &Data{Posture: []store.ControlPosture{
				row("acme", "codeowners", DecisionBaseline, buckets{compliant: 6, nonCompliant: 2, unmeasurable: 4, unknown: 1}),
			}},
			org: "acme",
		},
		{
			name: "replaced_version",
			data: &Data{Posture: []store.ControlPosture{
				row("acme", "codeowners", DecisionBaseline, buckets{compliant: 9}),
				row("acme", "renovate", DecisionReplaced, buckets{compliant: 3, nonCompliant: 3}),
			}},
			org: "acme",
		},
		{
			// An org that excluded everything still gets a report; that it
			// measures nothing is the finding.
			name: "exclusions_only",
			data: &Data{Exclusions: []store.Exclusion{
				{Org: "acme", Repository: "archive-mirror", State: "excluded", Reason: "mirror of an upstream"},
				{Org: "acme", Repository: "widgets", ControlID: "codeowners", State: "conflict", Reason: "two controls own CODEOWNERS"},
			}},
			org: "acme",
		},
		{
			name: "second_org_acme",
			data: twoOrgs(),
			org:  "acme",
		},
		{
			name: "second_org_globex",
			data: twoOrgs(),
			org:  "globex",
		},
		{
			// Operator-authored reasons and API-supplied names reach table
			// cells; a pipe would shift every column to its right.
			name: "escaped_cells",
			data: &Data{
				Posture: []store.ControlPosture{row("ac|me", "code`owners", DecisionBaseline, buckets{compliant: 1})},
				Exclusions: []store.Exclusion{
					{Org: "ac|me", Repository: "pipe|repo", ControlID: "code`owners", State: "excluded", Reason: "line one\nline | two"},
				},
			},
			org: "ac|me",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRenderer(t)

			for _, o := range r.Build(tt.data) {
				if o.Name != tt.org {
					continue
				}

				got, err := r.Render(o)
				if err != nil {
					t.Fatalf("Render(%s) = %v, want nil", o.Name, err)
				}

				assertGolden(t, tt.name, got)

				return
			}

			t.Fatalf("Build() produced no org %q", tt.org)
		})
	}
}

// twoOrgs is two orgs sharing a control, so neither report may carry the
// other's rows.
func twoOrgs() *Data {
	return &Data{
		Posture: []store.ControlPosture{
			row("acme", "codeowners", DecisionBaseline, buckets{compliant: 10, nonCompliant: 2}),
			row("globex", "codeowners", DecisionBaseline, buckets{compliant: 1, nonCompliant: 7}),
			row("globex", "labels", DecisionOrgAdded, buckets{pending: 4}),
		},
		Exclusions: []store.Exclusion{
			{Org: "globex", Repository: "infra", ControlID: "codeowners", State: "excluded", Reason: "owned by the platform team"},
		},
	}
}
