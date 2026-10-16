package api

import (
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// orgTotal is one org's repositories and summed finding counts.
type orgTotal struct {
	org     string
	tracked int
	parked  []store.ParkedCount
	counts  store.StatusCounts
}

// orgTotals joins repository counts and compliance rows per org,
// ordered case-insensitively by org.
func orgTotals(repos []store.OrgRepositoryCount, current []store.ComplianceCount) []orgTotal {
	byOrg := map[string]*orgTotal{}

	var order []string

	total := func(org string) *orgTotal {
		k := strings.ToLower(org)

		t, ok := byOrg[k]
		if !ok {
			t = &orgTotal{org: org, parked: []store.ParkedCount{}}
			byOrg[k] = t
			order = append(order, k)
		}

		return t
	}

	for _, r := range repos {
		t := total(r.Org)
		if r.Active {
			t.tracked += r.Count

			continue
		}

		t.parked = append(t.parked, store.ParkedCount{Reason: r.ParkReason, Count: r.Count})
	}

	for i := range current {
		t := total(current[i].Org)
		t.counts = t.counts.Add(current[i].Counts())
	}

	slices.Sort(order)

	out := make([]orgTotal, 0, len(order))
	for _, k := range order {
		out = append(out, *byOrg[k])
	}

	return out
}
