//go:build integration

package postgres_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// withPRAt adds a failing codeowners finding with a PR created at prAt
// on a new repository.
func withPRAt(t *testing.T, e *endpoints, org, name string, prAt time.Time) {
	t.Helper()

	ctx := t.Context()

	res, err := e.f.store.UpsertDiscovered(ctx, &store.DiscoveredRepo{Org: org, Name: name, InstallationID: testInstallation})
	if err != nil {
		t.Fatal(err)
	}

	o := missing("codeowners", "CODEOWNERS")
	o.Remediation = findings.RemediationPROpen
	o.PR = &findings.PREvidence{Number: 9, URL: "https://github.com/" + org + "/" + name + "/pull/9", CreatedAt: prAt}

	if _, err := e.f.store.RecordCheck(ctx, &store.CheckRecord{
		Key: "sort-" + name, RepositoryID: res.ID, InstallationID: testInstallation, Trigger: store.TriggerSchedule,
		PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0, Outcomes: []store.Outcome{o},
	}); err != nil {
		t.Fatal(err)
	}
}

// sort=pr_age walks oldest PR first, PR-less findings last, one row per
// page so every step crosses a page boundary, and a cursor is bound to
// its sort.
func TestFindings_SortPRAge(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)
	withPRAt(t, e, "acme-web", "older", t0.Add(-60*24*time.Hour))
	withPRAt(t, e, "acme", "newer", t0.Add(-10*24*time.Hour))

	var got []string

	cursor := ""
	for range 20 {
		path := "/findings?sort=pr_age&limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}

		page := decodeAs[gen.FindingPage](t, e.get(t, path, "platform"))
		for _, f := range page.Items {
			got = append(got, f.Org+"/"+f.Repository)
		}

		if page.NextCursor == nil {
			break
		}

		cursor = *page.NextCursor
	}

	// The fixture's PR (acme-web/b) is 40 days old; acme/widgets and acme-pay/c
	// have none and follow by repository id.
	want := []string{"acme-web/older", "acme-web/b", "acme/newer", "acme/widgets", "acme-pay/c"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order = %v, want %v", got, want)

			break
		}
	}

	first := decodeAs[gen.FindingPage](t, e.get(t, "/findings?sort=pr_age&limit=1", "platform"))
	if first.NextCursor == nil {
		t.Fatal("no next cursor")
	}

	if r := e.get(t, "/findings?limit=1&cursor="+url.QueryEscape(*first.NextCursor), "platform"); r.Status != http.StatusBadRequest {
		t.Errorf("pr_age cursor under the default sort = %d, want 400", r.Status)
	}

	if r := e.get(t, "/findings?sort=age", "platform"); r.Status != http.StatusBadRequest {
		t.Errorf("sort=age = %d, want 400", r.Status)
	}
}
