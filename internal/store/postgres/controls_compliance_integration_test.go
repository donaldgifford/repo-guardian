//go:build integration

package postgres_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// perRepoResolver resolves each repository by name.
type perRepoResolver map[string][]policy.Assignment

func (m perRepoResolver) Resolve(repo policy.Repository, _ policy.AppAccess) policy.Resolution {
	res := policy.Resolution{State: policy.StateManaged, Assignments: m[repo.Name], MaxOpenPRs: 3, ReopenAfter: time.Hour}
	if !repo.Active {
		res.State, res.Reason = policy.StateParked, repo.ParkReason
	}

	return res
}

func (perRepoResolver) Version() string { return testPolicyVersion }

// TestControlsStore_ComplianceBuckets is IMPL-0029 4.7: every active
// assignment lands in exactly one bucket, exclusions are counted beside,
// and the percentage is over compliant and non-compliant only.
func TestControlsStore_ComplianceBuckets(t *testing.T) {
	s, dsns := evaluatorStore(t)
	ctx := t.Context()

	excluded := codeowners("r1")
	excluded.State, excluded.Decision, excluded.BaselineControl = policy.AssignmentExcluded, "excluded", "codeowners"

	conflict := codeowners("r1")
	conflict.State, conflict.Error = policy.AssignmentConflict, "resource_conflict: file:CODEOWNERS"

	r := perRepoResolver{
		"compliant": {codeowners("r1")}, "failing": {codeowners("r1")}, "pending": {codeowners("r1")},
		"stale": {codeowners("r1")}, "denied": {codeowners("r1")}, "excluded": {excluded}, "conflict": {conflict},
	}

	if err := s.UpsertInstallation(ctx, store.AppInstallation{App: store.AppEval, InstallationID: 1, AccountLogin: "acme", RepositorySelection: "all"}); err != nil {
		t.Fatal(err)
	}

	var repos []store.DiscoveredRepository
	for i, name := range []string{"compliant", "failing", "pending", "stale", "denied", "excluded", "conflict"} {
		repos = append(repos, store.DiscoveredRepository{Org: "acme", Name: name, ProviderRepoID: int64(100 + i), InstallationID: 1})
	}

	got, err := s.UpsertRepositories(ctx, repos, r)
	if err != nil {
		t.Fatal(err)
	}

	ids := make(map[string]int64)
	for _, g := range got {
		ids[g.Name] = g.ID
	}

	for name, status := range map[string]string{"compliant": "compliant", "failing": "non_compliant", "stale": "compliant", "denied": "compliant"} {
		stage(t, s, ids[name], "k-"+name, epochOf(t, dsns.Owner, ids[name]), "f", status)

		if _, err := s.RecordEvaluation(ctx, "k-"+name); err != nil {
			t.Fatal(err)
		}
	}

	// A new revision re-epochs the assignment: the kept result is stale.
	r["stale"] = []policy.Assignment{codeowners("r2")}
	if _, err := s.Resolve(ctx, ids["stale"], r); err != nil {
		t.Fatal(err)
	}

	if err := s.Park(ctx, ids["denied"], "access_denied"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Resolve(ctx, ids["denied"], r); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ControlCompliance(ctx, nil, store.LineageAll)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 1 {
		t.Fatalf("ControlCompliance rows = %+v, want one baseline row", rows)
	}

	p := rows[0]
	want := store.ControlPosture{
		Org: "acme", ControlID: "codeowners", Decision: "baseline", Compliant: 1, NonCompliant: 1, Pending: 1, Stale: 1,
		UnmeasurableAccessDenied: 1, UnmeasurableConflict: 1, Excluded: 1, Assigned: 5,
	}

	if p.Percent == nil || *p.Percent != 50 || p.Coverage == nil || *p.Coverage != 40 {
		t.Errorf("percent %v coverage %v, want 50 and 40 (2 measured of 5 assigned)", p.Percent, p.Coverage)
	}

	p.Percent, p.Coverage = nil, nil
	if p != want {
		t.Errorf("ControlCompliance = %+v\nwant %+v", p, want)
	}

	if rows, err := s.ControlCompliance(ctx, nil, store.LineageOrgAdded); err != nil || len(rows) != 0 {
		t.Errorf("org_added lineage = %+v, %v; want nothing (every assignment is baseline)", rows, err)
	}

	ex, err := s.ControlExclusions(ctx, []string{"ACME"})
	if err != nil || len(ex) != 2 {
		t.Errorf("ControlExclusions = %+v, %v; want the excluded and the conflicted assignment", ex, err)
	}
}

// TestControlsStore_EmptyFleetIsNoData pins DESIGN-0022's rule on the
// controls query: nothing assigned reads no rows, and a fleet of only
// pending assignments reads a NULL percentage, never 100.
func TestControlsStore_EmptyFleetIsNoData(t *testing.T) {
	s, _ := evaluatorStore(t)

	if rows, err := s.ControlCompliance(t.Context(), nil, store.LineageAll); err != nil || len(rows) != 0 {
		t.Fatalf("empty fleet = %+v, %v; want no rows", rows, err)
	}

	discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

	rows, err := s.ControlCompliance(t.Context(), nil, store.LineageAll)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pending fleet = %+v, %v", rows, err)
	}

	if rows[0].Percent != nil || rows[0].Pending != 1 {
		t.Errorf("pending fleet percent = %v, pending %d; want nil (no data) and 1 pending", rows[0].Percent, rows[0].Pending)
	}
}

// TestCompliantPercent_SQLAgreesWithGo feeds the shared golden counts to
// floor_percent and to store.CompliantPercent and asserts they agree, so
// the Go mirror used for cross-org totals can never drift from the SQL.
func TestCompliantPercent_SQLAgreesWithGo(t *testing.T) {
	s, _ := evaluatorStore(t)

	b, err := os.ReadFile("../../../api/testdata/compliant_percent.json")
	if err != nil {
		t.Fatal(err)
	}

	var cases []struct {
		Compliant    int `json:"compliant"`
		NonCompliant int `json:"non_compliant"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		sql, err := s.FloorPercent(t.Context(), int64(c.Compliant), int64(c.Compliant+c.NonCompliant))
		if err != nil {
			t.Fatal(err)
		}

		goVal := store.CompliantPercent(store.StatusCounts{Compliant: c.Compliant, NonCompliant: c.NonCompliant})

		if (sql == nil) != (goVal == nil) || (sql != nil && *sql != *goVal) {
			t.Errorf("counts %d/%d: SQL %v, Go %v", c.Compliant, c.NonCompliant, show(sql), show(goVal))
		}
	}
}

func show(p *float64) any {
	if p == nil {
		return nil
	}

	return *p
}

// versionedResolver is fakeResolver under a chosen policy version.
type versionedResolver struct {
	fakeResolver
	version string
}

func (v *versionedResolver) Version() string { return v.version }

// TestControlsStore_EpochFencingAcrossRecreate is IMPL-0029 4.9: a
// result staged before its assignment was deleted and recreated is
// discarded on the record retry, and the recreated assignment's first
// result starts again at generation 1.
func TestControlsStore_EpochFencingAcrossRecreate(t *testing.T) {
	s, dsns := evaluatorStore(t)
	ctx := t.Context()
	r := &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}}
	repo := discover(t, s, r)
	old := epochOf(t, dsns.Owner, repo.ID)

	for i, fp := range []string{"f1", "f2"} {
		key := "k" + fp
		stage(t, s, repo.ID, key, old, fp, []string{"compliant", "non_compliant"}[i])

		if _, err := s.RecordEvaluation(ctx, key); err != nil {
			t.Fatal(err)
		}
	}

	stage(t, s, repo.ID, "retry", old, "f3", "compliant")

	r.assignments = nil
	if _, err := s.Resolve(ctx, repo.ID, r); err != nil {
		t.Fatal(err)
	}

	r.assignments = []policy.Assignment{codeowners("r1")}
	if _, err := s.Resolve(ctx, repo.ID, r); err != nil {
		t.Fatal(err)
	}

	if out, err := s.RecordEvaluation(ctx, "retry"); err != nil || len(out.Discarded) != 1 {
		t.Fatalf("record retry across recreate = %+v, %v; want the result discarded", out, err)
	}

	stage(t, s, repo.ID, "fresh", epochOf(t, dsns.Owner, repo.ID), "f4", "compliant")

	if _, err := s.RecordEvaluation(ctx, "fresh"); err != nil {
		t.Fatal(err)
	}

	var generation int64
	queryArgs(t, dsns.Owner, `SELECT eval_generation FROM control_results WHERE repository_id = $1`, &generation, repo.ID)

	if generation != 1 {
		t.Errorf("recreated assignment generation = %d, want 1", generation)
	}
}

// TestControlsStore_PolicyRevertResolvesAgain is IMPL-0029 4.9 (D14):
// activating A, B, then A again makes A current, and resolving under it
// returns the repository to A's assignments.
func TestControlsStore_PolicyRevertResolvesAgain(t *testing.T) {
	_, dsns := evaluatorStore(t)
	ctx := t.Context()

	ownerPool, err := pgxpool.New(ctx, dsns.Owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ownerPool.Close)

	owner := postgres.NewControlsStore(ownerPool)

	evalPool, err := pgxpool.New(ctx, dsns.Evaluator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(evalPool.Close)

	s := postgres.NewControlsStore(evalPool)
	under := map[string]*versionedResolver{
		"c:A": {fakeResolver{assignments: []policy.Assignment{codeowners("rA")}}, "c:A"},
		"c:B": {fakeResolver{assignments: []policy.Assignment{codeowners("rB")}}, "c:B"},
	}

	var repoID int64

	for i, v := range []string{"c:A", "c:B", "c:A"} {
		time.Sleep(10 * time.Millisecond) // activated_at is now(); keep the deploys apart

		if _, err := owner.ActivatePolicyVersion(ctx, v, nil); err != nil {
			t.Fatal(err)
		}

		current, _, err := s.CurrentActivation(ctx)
		if err != nil || current != v {
			t.Fatalf("deploy %d: CurrentActivation = %q, %v; want %q", i, current, err, v)
		}

		if i == 0 {
			repoID = discover(t, s, under[current]).ID

			continue
		}

		if _, err := s.Resolve(ctx, repoID, under[current]); err != nil {
			t.Fatal(err)
		}
	}

	var version, revision string
	queryArgs(t, dsns.Owner, `SELECT policy_version FROM repository_policy_state WHERE repository_id = $1`, &version, repoID)
	queryArgs(t, dsns.Owner, `SELECT revision FROM control_assignments WHERE repository_id = $1`, &revision, repoID)

	if version != "c:A" || revision != "rA" {
		t.Errorf("after A → B → A: policy %q revision %q, want c:A and rA", version, revision)
	}
}

// TestControlsStore_PostureOfAMostlyPendingFleet is IMPL-0029 4.9: 100
// assigned and one result reads 99 pending, coverage 1% and a percentage
// from that one row, so one measurement is never read as a measured
// fleet.
func TestControlsStore_PostureOfAMostlyPendingFleet(t *testing.T) {
	s, dsns := evaluatorStore(t)
	ctx := t.Context()
	r := &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}}

	if err := s.UpsertInstallation(ctx, store.AppInstallation{App: store.AppEval, InstallationID: 1, AccountLogin: "acme", RepositorySelection: "all"}); err != nil {
		t.Fatal(err)
	}

	repos := make([]store.DiscoveredRepository, 0, 100)
	for i := range 100 {
		repos = append(repos, store.DiscoveredRepository{Org: "acme", Name: fmt.Sprintf("r%03d", i), ProviderRepoID: int64(i + 1), InstallationID: 1})
	}

	got, err := s.UpsertRepositories(ctx, repos, r)
	if err != nil {
		t.Fatal(err)
	}

	stage(t, s, got[0].ID, "one", epochOf(t, dsns.Owner, got[0].ID), "f", "non_compliant")

	if _, err := s.RecordEvaluation(ctx, "one"); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ControlCompliance(ctx, nil, store.LineageAll)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ControlCompliance = %+v, %v", rows, err)
	}

	p := rows[0]
	if p.Assigned != 100 || p.Pending != 99 || p.NonCompliant != 1 || show(p.Coverage) != 1.0 || show(p.Percent) != 0.0 {
		t.Errorf("posture = assigned %d pending %d non-compliant %d coverage %v percent %v; want 100, 99, 1, 1, 0",
			p.Assigned, p.Pending, p.NonCompliant, show(p.Coverage), show(p.Percent))
	}
}
