package api

import (
	"slices"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// ruleKey identifies a rule across orgs.
type ruleKey struct {
	kind findings.RuleKind
	name string
}

// ruleTotal sums one rule's shared-query rows. Percentages are computed
// from the summed counts with store.CompliantPercent, the same floor
// math the shared query runs per row.
type ruleTotal struct {
	key        ruleKey
	counts     store.StatusCounts
	previous   *store.StatusCounts
	previousAt time.Time
}

// ruleTotals sums current and previous rows per rule, ordered by kind
// then name.
func ruleTotals(current []store.ComplianceCount, previous []store.ComplianceSnapshot) []ruleTotal {
	byKey := map[ruleKey]*ruleTotal{}

	var order []ruleKey

	total := func(k ruleKey) *ruleTotal {
		t, ok := byKey[k]
		if !ok {
			t = &ruleTotal{key: k}
			byKey[k] = t
			order = append(order, k)
		}

		return t
	}

	for i := range current {
		t := total(ruleKey{current[i].Kind, current[i].RuleName})
		t.counts = t.counts.Add(current[i].Counts())
	}

	for i := range previous {
		k := ruleKey{previous[i].Kind, previous[i].RuleName}
		if _, ok := byKey[k]; !ok {
			continue // a rule no longer measured has no current line
		}

		t := byKey[k]
		sum := previous[i].Counts()

		if t.previous != nil {
			sum = sum.Add(*t.previous)
		}

		t.previous = &sum

		if previous[i].SnapshotAt.After(t.previousAt) {
			t.previousAt = previous[i].SnapshotAt
		}
	}

	slices.SortFunc(order, func(a, b ruleKey) int {
		if c := strings.Compare(string(a.kind), string(b.kind)); c != 0 {
			return c
		}

		return strings.Compare(a.name, b.name)
	})

	out := make([]ruleTotal, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}

	return out
}

func ruleComplianceOut(t *ruleTotal) gen.RuleCompliance {
	out := gen.RuleCompliance{
		Kind: string(t.key.kind), Name: t.key.name, Counts: counts(t.counts), CompliantPercent: store.CompliantPercent(t.counts),
	}

	if t.previous != nil {
		out.PreviousPercent = store.CompliantPercent(*t.previous)
		at := t.previousAt
		out.PreviousAt = &at
	}

	return out
}

func ruleCompliancesOut(current []store.ComplianceCount, previous []store.ComplianceSnapshot) []gen.RuleCompliance {
	totals := ruleTotals(current, previous)
	out := make([]gen.RuleCompliance, 0, len(totals))

	for i := range totals {
		out = append(out, ruleComplianceOut(&totals[i]))
	}

	return out
}

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

func parkedOut(in []store.ParkedCount) []gen.ParkedCount {
	out := make([]gen.ParkedCount, 0, len(in))
	for _, p := range in {
		out = append(out, gen.ParkedCount{Reason: string(p.Reason), Count: p.Count})
	}

	return out
}

func reasonsOut(in []store.FindingReasonCount) []gen.ReasonCount {
	out := make([]gen.ReasonCount, 0, len(in))
	for _, r := range in {
		out = append(out, gen.ReasonCount{Reason: string(r.Reason), Count: r.Count})
	}

	return out
}
