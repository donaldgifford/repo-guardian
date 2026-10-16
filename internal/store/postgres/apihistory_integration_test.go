//go:build integration

package postgres_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// historyAt is the second snapshot the aggregate tests add, so the
// series has two points and paging has a boundary to cross.
var historyAt = t0.Add(-time.Hour)

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func newHistoryEndpoints(t *testing.T) *endpoints {
	t.Helper()

	e := newEndpoints(t)
	if _, err := e.f.store.InsertComplianceSnapshot(t.Context(), historyAt); err != nil {
		t.Fatal(err)
	}

	return e
}

// The fixture has one codeowners finding per org: compliant in acme and
// acme-pay, failing in acme-web. The fleet is 2/3 = 66.6% floored; the
// orgs are 100, 100 and 0.
func TestHistory_Aggregates(t *testing.T) {
	t.Parallel()

	e := newHistoryEndpoints(t)

	tests := []struct {
		name  string
		query string
		group string
		want  []string // org|compliant|non_compliant|percent per point
	}{
		{name: "fleet", query: "aggregate=fleet", group: "platform", want: []string{"|2|1|66.6", "|2|1|66.6"}},
		{name: "org", query: "aggregate=org", group: "platform", want: []string{
			"acme|1|0|100", "acme-pay|1|0|100", "acme-web|0|1|0", "acme|1|0|100", "acme-pay|1|0|100", "acme-web|0|1|0",
		}},
		{name: "fleet for one rule", query: "aggregate=fleet&kind=file&rule=codeowners", group: "platform", want: []string{"|2|1|66.6", "|2|1|66.6"}},
		{name: "fleet for an unknown rule", query: "aggregate=fleet&kind=file&rule=renovate", group: "platform", want: []string{}},
		{name: "fleet is scoped", query: "aggregate=fleet", group: "team-web", want: []string{"|0|1|0", "|0|1|0"}},
		{name: "org is scoped", query: "aggregate=org", group: "team-web", want: []string{"acme-web|0|1|0", "acme-web|0|1|0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			page := decodeAs[gen.HistoryPage](t, e.get(t, "/compliance/history?"+tt.query, tt.group))

			got := make([]string, 0, len(page.Items))
			for _, p := range page.Items {
				if p.Kind != nil || p.Rule != nil {
					t.Errorf("aggregated point names a rule: %+v", p)
				}

				org := ""
				if p.Org != nil {
					org = *p.Org
				}

				got = append(got, org+"|"+strconv.Itoa(p.Counts.Compliant)+"|"+strconv.Itoa(p.Counts.NonCompliant)+"|"+pct(p.CompliantPercent))
			}

			if len(got) != len(tt.want) {
				t.Fatalf("points = %v, want %v", got, tt.want)
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("point %d = %s, want %s", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// aggregate=fleet equals the unaggregated rows summed per snapshot, the
// sum the UI's fleetSeries does, with the same floor.
func TestHistory_FleetEqualsSummedRows(t *testing.T) {
	t.Parallel()

	e := newHistoryEndpoints(t)

	rows := decodeAs[gen.HistoryPage](t, e.get(t, "/compliance/history", "platform"))
	summed := map[time.Time]store.StatusCounts{}

	for _, p := range rows.Items {
		if p.Org == nil || p.Kind == nil || p.Rule == nil {
			t.Fatalf("unaggregated point lacks its key: %+v", p)
		}

		c := summed[p.SnapshotAt]
		summed[p.SnapshotAt] = c.Add(store.StatusCounts{
			Compliant: p.Counts.Compliant, NonCompliant: p.Counts.NonCompliant,
			NotApplicable: p.Counts.NotApplicable, Unknown: p.Counts.Unknown,
		})
	}

	fleet := decodeAs[gen.HistoryPage](t, e.get(t, "/compliance/history?aggregate=fleet", "platform"))
	if len(fleet.Items) != len(summed) {
		t.Fatalf("fleet points = %d, want %d", len(fleet.Items), len(summed))
	}

	for _, p := range fleet.Items {
		want := summed[p.SnapshotAt]
		if p.Counts.Compliant != want.Compliant || p.Counts.NonCompliant != want.NonCompliant ||
			p.Counts.NotApplicable != want.NotApplicable || p.Counts.Unknown != want.Unknown {
			t.Errorf("fleet %s counts = %+v, want %+v", p.SnapshotAt, p.Counts, want)
		}

		if pct(p.CompliantPercent) != pct(store.CompliantPercent(want)) {
			t.Errorf("fleet %s percent = %s, want %s", p.SnapshotAt, pct(p.CompliantPercent), pct(store.CompliantPercent(want)))
		}
	}
}

// Paging an aggregate crosses the page boundary without loss, and a
// cursor from one mode is refused under another.
func TestHistory_AggregatePagingAndCursorBinding(t *testing.T) {
	t.Parallel()

	e := newHistoryEndpoints(t)

	var orgs []string

	cursor := ""
	for range 10 {
		path := "/compliance/history?aggregate=org&limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}

		page := decodeAs[gen.HistoryPage](t, e.get(t, path, "platform"))
		for _, p := range page.Items {
			orgs = append(orgs, *p.Org)
		}

		if page.NextCursor == nil {
			break
		}

		cursor = *page.NextCursor
	}

	if len(orgs) != 6 {
		t.Fatalf("paged org points = %v, want 6", orgs)
	}

	first := decodeAs[gen.HistoryPage](t, e.get(t, "/compliance/history?aggregate=org&limit=1", "platform"))
	if first.NextCursor == nil {
		t.Fatal("no next cursor")
	}

	r := e.get(t, "/compliance/history?aggregate=fleet&limit=1&cursor="+url.QueryEscape(*first.NextCursor), "platform")
	if r.Status != http.StatusBadRequest {
		t.Errorf("cursor across modes = %d, want 400: %s", r.Status, r.Body)
	}

	if r := e.get(t, "/compliance/history?aggregate=rule", "platform"); r.Status != http.StatusBadRequest {
		t.Errorf("aggregate=rule = %d, want 400", r.Status)
	}
}

func pct(p *float64) string {
	if p == nil {
		return "unmeasured"
	}

	return fmt.Sprint(*p)
}
