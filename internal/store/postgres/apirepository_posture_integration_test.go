//go:build integration

package postgres_test

import (
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// TestAPIReader_RepositoryCompliance is IMPL-0030 7.8: one repository
// with a non-compliant, a pending and an excluded assignment reads the
// same Posture on the list and the detail, the excluded one beside and
// never in a denominator.
func TestAPIReader_RepositoryCompliance(t *testing.T) {
	t.Parallel()

	f := newAPIFixture(t)

	labels := codeowners("r1")
	labels.Control = control.ID{Slug: "labels", Version: 1}
	excluded := codeowners("r1")
	excluded.Control, excluded.State, excluded.Decision = control.ID{Slug: "branch-ruleset", Version: 1}, policy.AssignmentExcluded, "excluded"
	f.res.assignments = []policy.Assignment{codeowners("r1"), labels, excluded}

	id := f.discover(t, "acme", "mixed")
	f.evaluate(t, id, "m1", "non_compliant")

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsns.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	r := postgres.NewAPIReader(pool)

	page, err := r.Repositories(t.Context(), store.ScopeAll(), &store.RepositoryFilter{NamePrefix: "mixed", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("Repositories(mixed) = %+v, %v", page.Items, err)
	}

	d, err := r.Repository(t.Context(), store.ScopeAll(), id)
	if err != nil {
		t.Fatal(err)
	}

	for name, got := range map[string]store.ControlPosture{"list": page.Items[0].Compliance, "detail": d.Repository.Compliance} {
		if got.NonCompliant != 1 || got.Pending != 1 || got.Excluded != 1 || got.Assigned != 2 ||
			got.Percent == nil || *got.Percent != 0 || got.Coverage == nil || *got.Coverage != 50 {
			t.Errorf("%s compliance = %+v, want non-compliant, pending and excluded with 0%% over 50%% coverage", name, got)
		}
	}

	widgets, err := r.Repository(t.Context(), store.ScopeAll(), f.repoID)
	if err != nil {
		t.Fatal(err)
	}

	if c := widgets.Repository.Compliance; c.Pending != 1 || c.Assigned != 1 || c.Percent != nil {
		t.Errorf("widgets compliance = %+v, want its one pending assignment and no percent", c)
	}
}
