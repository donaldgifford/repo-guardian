//go:build integration

package postgres_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

// TestEndpoints_ResultsFilters is IMPL-0030 7.7: each filter and its
// total over seedTwoOrgs (widgets and c compliant, b non-compliant, d
// parked archived so outside the population).
func TestEndpoints_ResultsFilters(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	run(t, e.f.dsns.Owner, `UPDATE rule_results SET evidence = evidence || '{"reason":"missing"}'
		WHERE repository_id = `+strconv.FormatInt(e.ids["acme-web/b"], 10))

	tests := []struct {
		query string
		want  int
	}{
		{query: "", want: 3},
		{query: "bucket=non_compliant", want: 1},
		{query: "bucket=compliant", want: 2},
		{query: "bucket=pending", want: 0},
		{query: "control=codeowners", want: 3},
		{query: "control=other", want: 0},
		{query: "org=ACME-WEB", want: 1},
		{query: "decision=baseline", want: 3},
		{query: "decision=org_added", want: 0},
		{query: "rule=exists", want: 3},
		{query: "rule=other", want: 0},
		{query: "bucket=non_compliant&org=acme-pay", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			page := decodeAs[gen.ResultPage](t, e.get(t, "/results?"+tt.query, "platform"))
			if len(page.Items) != tt.want || page.Total == nil || *page.Total != tt.want {
				t.Errorf("/results?%s = %d items, total %v; want %d", tt.query, len(page.Items), page.Total, tt.want)
			}
		})
	}

	b := decodeAs[gen.ResultPage](t, e.get(t, "/results?bucket=non_compliant", "platform")).Items[0]
	if b.Repository != "b" || b.Status != "non_compliant" || len(b.FailingRules) != 1 || b.FailingRules[0] != "exists" ||
		b.Reason == nil || *b.Reason != "missing" {
		t.Errorf("b = %+v, want its failing rule and the evidence reason", b)
	}

	w := decodeAs[gen.ResultPage](t, e.get(t, "/results?org=acme&bucket=compliant", "platform")).Items[0]
	if w.Reason == nil || *w.Reason != "codeowners.exists" {
		t.Errorf("widgets reason = %v, want the evidence_kind when the evidence has no reason", w.Reason)
	}
}

// TestEndpoints_ResultsParity: with no filter but the bucket, the
// non-compliant total is the summary's non_compliant (OQ15).
func TestEndpoints_ResultsParity(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	for _, group := range []string{"platform", "team-web"} {
		page := decodeAs[gen.ResultPage](t, e.get(t, "/results?bucket=non_compliant", group))
		sum := decodeAs[gen.Summary](t, e.get(t, "/summary", group))

		if page.Total == nil || *page.Total != sum.Compliance.NonCompliant {
			t.Errorf("%s: /results total %v, summary non_compliant %d", group, page.Total, sum.Compliance.NonCompliant)
		}
	}
}

// TestEndpoints_ResultsTwoOrgAuthz: a team lists only its org's results.
func TestEndpoints_ResultsTwoOrgAuthz(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	page := decodeAs[gen.ResultPage](t, e.get(t, "/results", "team-web"))
	if len(page.Items) != 1 || page.Items[0].Org != "acme-web" || *page.Total != 1 {
		t.Errorf("team-web /results = %+v (total %v), want acme-web/b alone", page.Items, page.Total)
	}

	if p := decodeAs[gen.ResultPage](t, e.get(t, "/results?org=acme-pay", "team-web")); len(p.Items) != 0 || *p.Total != 0 {
		t.Errorf("team-web /results?org=acme-pay = %+v, want nothing", p.Items)
	}
}

// TestEndpoints_ResultsPageThroughTies: every result shares one
// last_changed_at, so the keyset falls through to repository id; both
// sorts walk every row once, in order, the total only on the first page,
// and a cursor from one sort is refused by the other.
func TestEndpoints_ResultsPageThroughTies(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)
	run(t, e.f.dsns.Owner, `UPDATE control_results SET last_changed_at = '2026-01-01T00:00:00Z'`)

	for _, sort := range []string{"since_asc", "since_desc"} {
		var ids []int64

		next := "/results?limit=1&sort=" + sort

		for i := 0; next != "" && i < 10; i++ {
			page := decodeAs[gen.ResultPage](t, e.get(t, next, "platform"))
			if (i == 0) != (page.Total != nil) {
				t.Errorf("%s page %d total = %v, want it on the first page only", sort, i, page.Total)
			}

			for _, r := range page.Items {
				ids = append(ids, r.RepositoryId)
			}

			next = ""
			if page.NextCursor != nil {
				next = "/results?limit=1&sort=" + sort + "&cursor=" + url.QueryEscape(*page.NextCursor)

				other := "/results?limit=1&sort=since_asc&cursor=" + url.QueryEscape(*page.NextCursor)
				if sort == "since_asc" {
					other = "/results?limit=1&sort=since_desc&cursor=" + url.QueryEscape(*page.NextCursor)
				}

				if r := e.get(t, other, "platform"); r.Status != http.StatusBadRequest {
					t.Errorf("cursor from %s under the other sort = %d, want 400", sort, r.Status)
				}
			}
		}

		if len(ids) != 3 {
			t.Fatalf("%s walked %v, want three results", sort, ids)
		}

		for i := 1; i < len(ids); i++ {
			if (sort == "since_asc") != (ids[i-1] < ids[i]) {
				t.Errorf("%s order = %v", sort, ids)
			}
		}
	}
}
