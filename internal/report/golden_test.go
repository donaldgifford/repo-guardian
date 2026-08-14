package report

import (
	"context"
	"errors"
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

// lastWeek dates the previous snapshot in the trend fixtures.
var lastWeek = time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)

func ptrTime(t time.Time) *time.Time { return &t }

// stubLinker stands in for the GitHub-backed resolver.
//
// It counts calls per repository because the per-repository (not
// per-finding) call budget is a real contract: a repository failing
// five rules produces five findings and must still cost one API call.
type stubLinker struct {
	urls  map[string]string
	fail  map[string]bool
	calls map[string]int

	// installations records the ID Enrich scoped each lookup with, so a
	// test can prove the finding's installation reached the linker
	// rather than a zero value that would authenticate as nobody.
	installations map[string]int64
}

func newStubLinker() *stubLinker {
	return &stubLinker{
		urls:          make(map[string]string),
		fail:          make(map[string]bool),
		calls:         make(map[string]int),
		installations: make(map[string]int64),
	}
}

func (s *stubLinker) PRURL(_ context.Context, installationID int64, owner, repo string) (string, error) {
	key := owner + "/" + repo
	s.calls[key]++
	s.installations[key] = installationID

	if s.fail[key] {
		return "", errors.New("installation 1 lacks access")
	}

	return s.urls[key], nil
}

// newRenderer builds a Renderer with the clock pinned.
func newRenderer(t *testing.T, links PRLinker) *Renderer {
	t.Helper()

	r, err := New(Options{
		Links:  links,
		Now:    func() time.Time { return fixedNow },
		Logger: testLogger(),
	})
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}

	return r
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
// rule, a rule with no history, and — critically — a rule present only
// in the history, which must NOT appear in the output.
func fullData() *store.ReportData {
	return &store.ReportData{
		Findings: []store.ReportFinding{
			{
				InstallationID:  11,
				Owner:           "acme",
				Repo:            "api",
				RuleName:        "codeowners",
				RuleKind:        "file",
				ActionableSince: ptrTime(time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)),
			},
			// No ActionableSince: the column must render an em dash
			// rather than a zero date claiming year 1.
			{
				InstallationID: 11,
				Owner:          "acme",
				Repo:           "web",
				RuleName:       "codeowners",
				RuleKind:       "file",
			},
			{
				InstallationID:  11,
				Owner:           "acme",
				Repo:            "web",
				RuleName:        "renovate",
				RuleKind:        "file",
				ActionableSince: ptrTime(time.Date(2026, 8, 9, 22, 0, 0, 0, time.UTC)),
			},
		},
		Current: []store.SnapshotRow{
			{Org: "acme", RuleName: "codeowners", ActionableCount: 2, TrackedCount: 10},
			{Org: "acme", RuleName: "dependabot", ActionableCount: 0, TrackedCount: 10},
			{Org: "acme", RuleName: "renovate", ActionableCount: 1, TrackedCount: 4},
		},
		Previous: []store.SnapshotRow{
			{Org: "acme", RuleName: "codeowners", ActionableCount: 5, TrackedCount: 10, SnapshotAt: lastWeek},
			{Org: "acme", RuleName: "dependabot", ActionableCount: 0, TrackedCount: 9, SnapshotAt: lastWeek},
			{Org: "acme", RuleName: "retired-rule", ActionableCount: 3, TrackedCount: 10, SnapshotAt: lastWeek},
		},
	}
}

// TestRender_Golden pins the rendered markdown for the shapes an
// operator will actually receive.
func TestRender_Golden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data func() *store.ReportData

		// links non-nil turns on the PR column. nil is not the same as
		// an empty linker: nil omits the column entirely, because a
		// column of dashes is indistinguishable from "no repository has
		// an open PR".
		links func() *stubLinker
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
			data: func() *store.ReportData {
				d := fullData()
				d.Previous = nil

				return d
			},
		},
		{
			// The happy fleet. Rules evaluated, nothing failing.
			name: "all_passing",
			data: func() *store.ReportData {
				return &store.ReportData{
					Current: []store.SnapshotRow{
						{Org: "acme", RuleName: "codeowners", ActionableCount: 0, TrackedCount: 12},
					},
					Previous: []store.SnapshotRow{
						{Org: "acme", RuleName: "codeowners", ActionableCount: 4, TrackedCount: 12, SnapshotAt: lastWeek},
					},
				}
			},
		},
		{
			// A configured rule nothing matched. Must read "n/a", never
			// 100% — an unmeasured rule scoring perfectly is the most
			// misleading cell this format could produce.
			name: "unmeasured_rule",
			data: func() *store.ReportData {
				return &store.ReportData{
					Current: []store.SnapshotRow{
						{Org: "acme", RuleName: "branch-protection", ActionableCount: 0, TrackedCount: 0},
					},
				}
			},
		},
		{
			// 1999 of 2000 is 99.95%: it must read 99.9%, not the 100.0%
			// a rounding rule would print. A report calling a fleet fully
			// compliant while one repository is not has told a lie
			// somebody will act on.
			//
			// 2000 rather than the more obvious 1000 because 999-of-1000
			// floors and rounds to the same 99.9% — a fixture built on it
			// would look like it pinned this and pin nothing.
			name: "floored_percent",
			data: func() *store.ReportData {
				return &store.ReportData{
					Findings: []store.ReportFinding{
						{InstallationID: 11, Owner: "acme", Repo: "straggler", RuleName: "codeowners", RuleKind: "file"},
					},
					Current: []store.SnapshotRow{
						{Org: "acme", RuleName: "codeowners", ActionableCount: 1, TrackedCount: 2000},
					},
				}
			},
		},
		{
			// Two orgs in one read must render as two independent
			// documents; nothing from acme may leak into globex.
			name: "second_org",
			data: func() *store.ReportData {
				d := fullData()
				d.Findings = append(d.Findings, store.ReportFinding{
					InstallationID: 22, Owner: "globex", Repo: "tools", RuleName: "dependabot", RuleKind: "file",
				})
				d.Current = append(d.Current, store.SnapshotRow{
					Org: "globex", RuleName: "dependabot", ActionableCount: 1, TrackedCount: 3,
				})

				return d
			},
		},
		{
			// Names carrying a pipe would silently shift every column to
			// their right, turning a compliance report into a misleading
			// one rather than an obviously broken one.
			name: "escaped_cells",
			data: func() *store.ReportData {
				return &store.ReportData{
					Findings: []store.ReportFinding{
						{InstallationID: 11, Owner: "acme", Repo: "a|b", RuleName: "pipe|rule", RuleKind: "file"},
					},
					Current: []store.SnapshotRow{
						{Org: "acme", RuleName: "pipe|rule", ActionableCount: 1, TrackedCount: 2},
					},
				}
			},
		},
		{
			name: "with_pr_links",
			data: fullData,
			links: func() *stubLinker {
				l := newStubLinker()
				l.urls["acme/api"] = "https://github.example/acme/api/pull/7"

				return l
			},
		},
		{
			// A partially enriched report must say so. A short list of
			// links otherwise reads as "these repositories have no open
			// PR", which is a different and wrong statement.
			name: "pr_link_failure",
			data: fullData,
			links: func() *stubLinker {
				l := newStubLinker()
				l.urls["acme/api"] = "https://github.example/acme/api/pull/7"
				l.fail["acme/web"] = true

				return l
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var links PRLinker
			if tt.links != nil {
				links = tt.links()
			}

			r := newRenderer(t, links)
			data := tt.data()

			orgs := r.Build(data)
			r.Enrich(t.Context(), data, orgs)

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

	r := newRenderer(t, nil)

	body, err := r.Render(Org{Name: "acme", GeneratedAt: fixedNow})
	if err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}

	assertGolden(t, "no_rules_evaluated", body)
}
