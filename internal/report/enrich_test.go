package report

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// TestEnrich_OneCallPerRepository pins the API budget.
//
// A repository failing five rules produces five findings. Looking the
// PR up per finding would multiply the cost of the one part of this
// command that can be rate-limited, for a column that shows the same
// URL five times.
func TestEnrich_OneCallPerRepository(t *testing.T) {
	t.Parallel()

	data := &store.ReportData{
		Findings: []store.ReportFinding{
			{InstallationID: 42, Owner: "acme", Repo: "api", RuleName: "codeowners", RuleKind: "file"},
			{InstallationID: 42, Owner: "acme", Repo: "api", RuleName: "dependabot", RuleKind: "file"},
			{InstallationID: 42, Owner: "acme", Repo: "api", RuleName: "renovate", RuleKind: "file"},
		},
		Current: []store.SnapshotRow{
			{Org: "acme", RuleName: "codeowners", ActionableCount: 1, TrackedCount: 1},
			{Org: "acme", RuleName: "dependabot", ActionableCount: 1, TrackedCount: 1},
			{Org: "acme", RuleName: "renovate", ActionableCount: 1, TrackedCount: 1},
		},
	}

	links := newStubLinker()
	links.urls["acme/api"] = "https://github.example/acme/api/pull/3"

	r := newRenderer(t, links)
	orgs := r.Build(data)
	r.Enrich(t.Context(), data, orgs)

	if got := links.calls["acme/api"]; got != 1 {
		t.Errorf("PRURL called %d times for one repository with three findings, want 1", got)
	}

	for _, f := range orgs[0].Findings {
		if f.PRURL != "https://github.example/acme/api/pull/3" {
			t.Errorf("Findings[%s].PRURL = %q, want the cached URL on every finding", f.RuleName, f.PRURL)
		}
	}
}

// TestEnrich_ScopesLookupToTheFindingInstallation pins that the
// installation ID reaches the linker.
//
// A zero ID authenticates as nobody, so this failing silently would
// mean every link lookup 401s on a report that otherwise looks fine.
func TestEnrich_ScopesLookupToTheFindingInstallation(t *testing.T) {
	t.Parallel()

	data := &store.ReportData{
		Findings: []store.ReportFinding{
			{InstallationID: 11, Owner: "acme", Repo: "api", RuleName: "codeowners", RuleKind: "file"},
			{InstallationID: 22, Owner: "globex", Repo: "tools", RuleName: "codeowners", RuleKind: "file"},
		},
		Current: []store.SnapshotRow{
			{Org: "acme", RuleName: "codeowners", ActionableCount: 1, TrackedCount: 1},
			{Org: "globex", RuleName: "codeowners", ActionableCount: 1, TrackedCount: 1},
		},
	}

	links := newStubLinker()

	r := newRenderer(t, links)
	orgs := r.Build(data)
	r.Enrich(t.Context(), data, orgs)

	want := map[string]int64{"acme/api": 11, "globex/tools": 22}
	for key, id := range want {
		if got := links.installations[key]; got != id {
			t.Errorf("PRURL(%s) scoped to installation %d, want %d", key, got, id)
		}
	}
}

// TestEnrich_FailureIsCountedNotFatal pins the best-effort contract.
//
// A lookup failure costs one link, never the report — and it is
// counted, so the rendered output can say the column is incomplete
// instead of implying the repository has no open PR.
func TestEnrich_FailureIsCountedNotFatal(t *testing.T) {
	t.Parallel()

	data := fullData()

	links := newStubLinker()
	links.urls["acme/api"] = "https://github.example/acme/api/pull/7"
	links.fail["acme/web"] = true

	r := newRenderer(t, links)
	orgs := r.Build(data)
	r.Enrich(t.Context(), data, orgs)

	if orgs[0].LinkFailures != 1 {
		t.Errorf("LinkFailures = %d, want 1", orgs[0].LinkFailures)
	}

	body, err := r.Render(orgs[0])
	if err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}

	if !strings.Contains(body, "PR link lookup(s) failed") {
		t.Errorf("a report with a failed lookup does not disclose it:\n%s", body)
	}

	if !strings.Contains(body, "https://github.example/acme/api/pull/7") {
		t.Errorf("one failed lookup dropped a link that succeeded:\n%s", body)
	}
}

// TestEnrich_FailureIsNotRetriedPerFinding pins that a repository whose
// lookup failed is not asked again for its other findings.
//
// The failing case is the expensive one — a 403 or a timeout — so
// retrying it once per finding would multiply exactly the cost the
// per-repository cache exists to avoid.
func TestEnrich_FailureIsNotRetriedPerFinding(t *testing.T) {
	t.Parallel()

	data := &store.ReportData{
		Findings: []store.ReportFinding{
			{InstallationID: 11, Owner: "acme", Repo: "web", RuleName: "codeowners", RuleKind: "file"},
			{InstallationID: 11, Owner: "acme", Repo: "web", RuleName: "renovate", RuleKind: "file"},
		},
		Current: []store.SnapshotRow{
			{Org: "acme", RuleName: "codeowners", ActionableCount: 1, TrackedCount: 1},
			{Org: "acme", RuleName: "renovate", ActionableCount: 1, TrackedCount: 1},
		},
	}

	links := newStubLinker()
	links.fail["acme/web"] = true

	r := newRenderer(t, links)
	orgs := r.Build(data)
	r.Enrich(t.Context(), data, orgs)

	if got := links.calls["acme/web"]; got != 1 {
		t.Errorf("PRURL called %d times for a repository whose first lookup failed, want 1", got)
	}

	if orgs[0].LinkFailures != 1 {
		t.Errorf("LinkFailures = %d, want 1; one failing repository is one failure", orgs[0].LinkFailures)
	}
}

// TestEnrich_NilLinkerOmitsTheColumn pins the two link shapes.
//
// nil is not an empty linker. Without links the PR column is absent
// entirely; a column of dashes would be indistinguishable from "no
// repository has an open PR", which is a claim the report would not
// have checked.
func TestEnrich_NilLinkerOmitsTheColumn(t *testing.T) {
	t.Parallel()

	data := fullData()

	r := newRenderer(t, nil)
	orgs := r.Build(data)
	r.Enrich(t.Context(), data, orgs)

	if orgs[0].ShowLinks {
		t.Error("Build().ShowLinks = true with no linker, want false")
	}

	body, err := r.Render(orgs[0])
	if err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}

	if strings.Contains(body, "Open PR") {
		t.Errorf("report rendered a PR column with no linker configured:\n%s", body)
	}
}
