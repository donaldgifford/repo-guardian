//go:build integration

package postgres_test

import (
	"strconv"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

// TestEndpoints_SummaryFindingAge is IMPL-0030 7.10: one non-compliant
// result in each of the six age buckets, so every boundary is crossed,
// and the buckets sum to the summary's non_compliant.
func TestEndpoints_SummaryFindingAge(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	ages := []string{"1 hour", "3 days", "10 days", "45 days", "120 days", "200 days"}
	repos := []int64{e.ids["acme-web/b"]}

	for i := 1; i < len(ages); i++ {
		id := e.f.discover(t, "acme-web", "nc"+strconv.Itoa(i))
		e.f.evaluate(t, id, "nc"+strconv.Itoa(i), "non_compliant")
		repos = append(repos, id)
	}

	for i, id := range repos {
		run(t, e.f.dsns.Owner, `UPDATE control_results SET last_changed_at = now() - interval '`+ages[i]+`'
			WHERE repository_id = `+strconv.FormatInt(id, 10))
	}

	s := decodeAs[gen.Summary](t, e.get(t, "/summary", "platform"))

	want := []string{"<1d", "1-7d", "7-30d", "30-90d", "90-180d", "180d+"}
	if len(s.FindingAge) != len(want) {
		t.Fatalf("finding_age = %+v, want the six buckets", s.FindingAge)
	}

	sum := 0

	for i, b := range s.FindingAge {
		if b.Bucket != want[i] || b.Count != 1 {
			t.Errorf("finding_age[%d] = %+v, want {%s 1}", i, b, want[i])
		}

		sum += b.Count
	}

	if sum != s.Compliance.NonCompliant {
		t.Errorf("finding_age sums to %d, want compliance.non_compliant %d", sum, s.Compliance.NonCompliant)
	}

	team := decodeAs[gen.Summary](t, e.get(t, "/summary", "team-web"))
	if team.FindingAge[0].Count != 1 {
		t.Errorf("team-web finding_age = %+v, want its own findings", team.FindingAge)
	}
}

// TestEndpoints_SummaryOpenPRsByAge is IMPL-0030 7.11: one open pull
// request in each of the four age buckets, summing to open_prs.
func TestEndpoints_SummaryOpenPRsByAge(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	ages := []string{"1 hour", "3 days", "10 days", "45 days"}
	repos := []int64{e.ids["acme-web/b"]}

	for i := 1; i < len(ages); i++ {
		id := e.f.discover(t, "acme-web", "pr"+strconv.Itoa(i))
		e.f.evaluate(t, id, "pr"+strconv.Itoa(i), "non_compliant")
		e.f.openPR(t, id, 10+i)
		repos = append(repos, id)
	}

	for i, id := range repos {
		run(t, e.f.dsns.Owner, `UPDATE remediations SET created_at = now() - interval '`+ages[i]+`'
			WHERE repository_id = `+strconv.FormatInt(id, 10))
	}

	s := decodeAs[gen.Summary](t, e.get(t, "/summary", "platform"))

	want := []string{"<1d", "1-7d", "7-30d", "30d+"}
	if len(s.OpenPrsByAge) != len(want) {
		t.Fatalf("open_prs_by_age = %+v, want the four buckets", s.OpenPrsByAge)
	}

	sum := 0

	for i, b := range s.OpenPrsByAge {
		if b.Bucket != want[i] || b.Count != 1 {
			t.Errorf("open_prs_by_age[%d] = %+v, want {%s 1}", i, b, want[i])
		}

		sum += b.Count
	}

	if sum != s.OpenPrs {
		t.Errorf("open_prs_by_age sums to %d, want open_prs %d", sum, s.OpenPrs)
	}
}
