//go:build integration

package postgres_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

// OrgSummary.open_prs equals the number of /findings?org=&remediation=pr_open
// rows for every org, under each scope (IMPL-0031 7.5).
func TestOrgs_OpenPRsMatchFindings(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)
	withPRAt(t, e, "acme-web", "older", t0.Add(-60*24*time.Hour))
	withPRAt(t, e, "acme", "newer", t0.Add(-10*24*time.Hour))

	tests := []struct {
		group string
		want  map[string]int
	}{
		{group: "platform", want: map[string]int{"acme": 1, "acme-web": 2, "acme-pay": 0}},
		{group: "team-web", want: map[string]int{"acme-web": 2}},
	}

	for _, tt := range tests {
		t.Run(tt.group, func(t *testing.T) {
			t.Parallel()

			orgs := decodeAs[gen.OrgList](t, e.get(t, "/orgs", tt.group))
			if len(orgs.Items) != len(tt.want) {
				t.Fatalf("orgs = %+v, want %d", orgs.Items, len(tt.want))
			}

			for _, o := range orgs.Items {
				if want, ok := tt.want[o.Org]; !ok || o.OpenPrs != want {
					t.Errorf("%s open_prs = %d, want %d", o.Org, o.OpenPrs, tt.want[o.Org])
				}

				page := decodeAs[gen.FindingPage](t, e.get(t, "/findings?remediation=pr_open&limit=100&org="+url.QueryEscape(o.Org), tt.group))
				if len(page.Items) != o.OpenPrs || page.NextCursor != nil {
					t.Errorf("%s: /findings pr_open = %d rows, open_prs = %d", o.Org, len(page.Items), o.OpenPrs)
				}
			}
		})
	}
}
