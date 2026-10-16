package api

import (
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// orgTotal is one org's repositories and summed result counts.
type orgTotal struct {
	org     string
	tracked int
	parked  int
	counts  store.StatusCounts
}

// orgTotals joins repository counts and compliance rows per org,
// ordered case-insensitively by org. Percentages are computed from the
// summed counts with store.CompliantPercent, the floor the compliance
// query runs per row.
func orgTotals(repos []store.OrgRepositoryCount, compliance []store.ControlPosture) []orgTotal {
	byOrg := map[string]*orgTotal{}

	var order []string

	total := func(org string) *orgTotal {
		k := strings.ToLower(org)

		t, ok := byOrg[k]
		if !ok {
			t = &orgTotal{org: org}
			byOrg[k] = t
			order = append(order, k)
		}

		return t
	}

	for _, r := range repos {
		t := total(r.Org)
		if r.Active {
			t.tracked += r.Count
		} else {
			t.parked += r.Count
		}
	}

	for i := range compliance {
		t := total(compliance[i].Org)
		t.counts = t.counts.Add(compliance[i].Counts())
	}

	slices.Sort(order)

	out := make([]orgTotal, 0, len(order))
	for _, k := range order {
		out = append(out, *byOrg[k])
	}

	return out
}
