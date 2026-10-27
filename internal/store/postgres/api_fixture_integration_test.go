//go:build integration

package postgres_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// apiFixture is a controls database seen through rg_evaluator, with the
// test policy version activated and acme/widgets discovered. Every
// repository is assigned codeowners at revision r1.
type apiFixture struct {
	dsns   pgtest.ControlsDSNs
	store  *postgres.ControlsStore
	res    *fakeResolver
	repoID int64
	next   int64
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()

	s, dsns := evaluatorStore(t)
	if err := s.UpsertInstallation(t.Context(), &store.AppInstallation{
		App: store.AppEval, InstallationID: 1, AccountLogin: "acme", RepositorySelection: "all",
	}); err != nil {
		t.Fatal(err)
	}

	f := &apiFixture{dsns: dsns, store: s, res: &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}}, next: 100}
	f.repoID = f.discover(t, "acme", "widgets")

	return f
}

// discover adds org/name and resolves its assignment.
func (f *apiFixture) discover(t *testing.T, org, name string) int64 {
	t.Helper()

	f.next++

	got, err := f.store.UpsertRepositories(t.Context(), []store.DiscoveredRepository{
		{Org: org, Name: name, ProviderRepoID: f.next, InstallationID: 1},
	}, f.res)
	if err != nil {
		t.Fatalf("UpsertRepositories(%s/%s): %v", org, name, err)
	}

	return got[0].ID
}

// evaluate records one evaluation of codeowners with status under key.
func (f *apiFixture) evaluate(t *testing.T, repoID int64, key, status string) {
	t.Helper()

	stage(t, f.store, repoID, key, epochOf(t, f.dsns.Owner, repoID), "f-"+key, status)

	if _, err := f.store.RecordEvaluation(t.Context(), key); err != nil {
		t.Fatalf("RecordEvaluation(%s): %v", key, err)
	}
}

// park parks a repository and re-resolves it, as ParkRepository does.
func (f *apiFixture) park(t *testing.T, repoID int64, reason string) {
	t.Helper()

	if err := f.store.Park(t.Context(), repoID, reason); err != nil {
		t.Fatal(err)
	}

	if _, err := f.store.Resolve(t.Context(), repoID, f.res); err != nil {
		t.Fatal(err)
	}
}

// openPR records an open repo-guardian pull request, as the owner: the
// remediator's journal is not the evaluator's to write.
func (f *apiFixture) openPR(t *testing.T, repoID int64, number int) {
	t.Helper()

	run(t, f.dsns.Owner, `INSERT INTO remediations (repository_id, control_id, assignment_epoch, operation_key, kind, state,
		intent_revision, pr_number, remediated_generation, created_at, last_run_at)
		VALUES (`+strconv.FormatInt(repoID, 10)+`, 'codeowners', 1, 'op-`+strconv.Itoa(number)+`', 'pull_request', 'open',
		'i', `+strconv.Itoa(number)+`, 1, now(), now())`)
}

// seedTwoOrgs adds acme-web (b, failing with an open PR) and acme-pay (c
// compliant, d archived) beside acme/widgets (compliant), and returns
// every repository's id by org/name.
func seedTwoOrgs(t *testing.T, f *apiFixture) map[string]int64 {
	t.Helper()

	ids := map[string]int64{"acme/widgets": f.repoID}
	for _, name := range []string{"acme-web/b", "acme-pay/c", "Acme-Pay/d"} {
		org, repo := splitName(name)
		ids[name] = f.discover(t, org, repo)
	}

	f.evaluate(t, f.repoID, "w1", "compliant")
	f.evaluate(t, ids["acme-web/b"], "w2", "non_compliant")
	f.evaluate(t, ids["acme-pay/c"], "p1", "compliant")
	f.openPR(t, ids["acme-web/b"], 7)
	f.park(t, ids["Acme-Pay/d"], "archived")

	return ids
}

func splitName(name string) (org, repo string) {
	for i := range name {
		if name[i] == '/' {
			return name[:i], name[i+1:]
		}
	}

	return "", name
}

// t0 is a fixed clock for the API handlers; the store stamps real time.
var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
