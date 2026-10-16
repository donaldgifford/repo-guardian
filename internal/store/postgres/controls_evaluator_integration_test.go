//go:build integration

package postgres_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

const testPolicyVersion = "c:1"

// fakeResolver resolves every repository to its assignments.
type fakeResolver struct{ assignments []policy.Assignment }

func (f *fakeResolver) Resolve(repo policy.Repository, _ policy.AppAccess) policy.Resolution {
	res := policy.Resolution{State: policy.StateManaged, Assignments: f.assignments, MaxOpenPRs: 3, ReopenAfter: 336 * time.Hour}
	if !repo.Active {
		res.State = policy.StateParked
	}

	return res
}

func (*fakeResolver) Version() string { return testPolicyVersion }

func codeowners(revision string) policy.Assignment {
	return policy.Assignment{
		Control: control.ID{Slug: "codeowners", Version: 1}, Revision: revision, State: policy.AssignmentActive,
		Decision: "baseline", Source: "enterprise", RequestedMode: "remediate", EffectiveMode: "remediate", ModeSource: "enterprise",
	}
}

// evaluatorStore returns a ControlsStore connected as rg_evaluator, with
// the policy version activated by the owner.
func evaluatorStore(t *testing.T) (*postgres.ControlsStore, pgtest.ControlsDSNs) {
	t.Helper()

	dsns := pgtest.ControlsDB(t)
	run(t, dsns.Owner, `INSERT INTO policy_versions (version) VALUES ('`+testPolicyVersion+`')`)

	pool, err := pgxpool.New(t.Context(), dsns.Evaluator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	return postgres.NewControlsStore(pool), dsns
}

func discover(t *testing.T, s *postgres.ControlsStore, r store.Resolver) store.ResolvedRepository {
	t.Helper()

	if err := s.UpsertInstallation(t.Context(), &store.AppInstallation{
		App: store.AppEval, InstallationID: 1, AccountLogin: "acme", RepositorySelection: "all",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.UpsertRepositories(t.Context(), []store.DiscoveredRepository{{Org: "acme", Name: "api", ProviderRepoID: 101, InstallationID: 1}}, r)
	if err != nil {
		t.Fatalf("UpsertRepositories: %v", err)
	}

	return got[0]
}

func stage(t *testing.T, s *postgres.ControlsStore, repoID int64, key string, epoch int64, fingerprint, status string) {
	t.Helper()

	if err := s.StageEvaluation(t.Context(), &store.EvaluationStage{
		CheckKey: key, RepositoryID: repoID, Trigger: "schedule", PolicyVersion: testPolicyVersion, StartedAt: time.Now(),
		Controls: []store.ControlEvaluation{{
			ControlID: "codeowners", ControlVersion: 1, Revision: "r1", Epoch: epoch, Status: status, Fingerprint: fingerprint,
			EvaluatedSHA: "sha-" + key,
			Rules: []store.RuleEvaluation{{
				RuleID: "exists", Status: "fail", Remediate: true, Evidence: json.RawMessage(`{"evidence_kind":"codeowners.exists","evidence_version":1,"present":false}`),
				EvidenceKind: "codeowners.exists", EvidenceVersion: 1,
			}},
		}},
	}); err != nil {
		t.Fatalf("StageEvaluation(%s): %v", key, err)
	}
}

// queryArgs is queryRow with query arguments.
func queryArgs(t *testing.T, dsn, sql string, dest any, args ...any) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(t.Context())

	if err := conn.QueryRow(t.Context(), sql, args...).Scan(dest); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func epochOf(t *testing.T, dsn string, repoID int64) int64 {
	t.Helper()

	var e int64
	queryArgs(t, dsn, `SELECT epoch FROM control_assignments WHERE repository_id = $1 AND control_id = 'codeowners'`, &e, repoID)

	return e
}

// TestControlsStore_EvaluationLifecycle is IMPL-0029 4.5, run as
// rg_evaluator: discovery resolves inline, a record starts at generation
// 1 and is idempotent on its key, an unchanged fingerprint keeps the
// generation and a changed one bumps it, a stale epoch is discarded, an
// unchanged resolution keeps its epoch, and withdrawal deletes the result
// with a to_status = NULL event.
func TestControlsStore_EvaluationLifecycle(t *testing.T) {
	s, dsns := evaluatorStore(t)
	ctx := t.Context()
	r := &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}}

	repo := discover(t, s, r)
	if !repo.Created || repo.State != policy.StateManaged || !slices.Equal(repo.Change.Changed, []string{"codeowners"}) {
		t.Fatalf("discovery = %+v, want a created, managed repository with codeowners assigned", repo)
	}

	epoch := epochOf(t, dsns.Owner, repo.ID)

	record := func(key string) store.RecordOutcome {
		t.Helper()

		out, err := s.RecordEvaluation(ctx, key)
		if err != nil {
			t.Fatalf("RecordEvaluation(%s): %v", key, err)
		}

		return out
	}

	generation := func() int64 {
		var g int64
		queryArgs(t, dsns.Owner, `SELECT eval_generation FROM control_results WHERE repository_id = $1`, &g, repo.ID)

		return g
	}

	stage(t, s, repo.ID, "k1", epoch, "f1", "non_compliant")

	if out := record("k1"); !slices.Equal(out.Changed, []string{"codeowners"}) || generation() != 1 {
		t.Errorf("first record = %+v, generation %d; want changed at generation 1", out, generation())
	}

	if out := record("k1"); !out.AlreadyFinal {
		t.Errorf("re-record k1 = %+v, want AlreadyFinal", out)
	}

	stage(t, s, repo.ID, "k2", epoch, "f1", "non_compliant")

	if out := record("k2"); len(out.Changed) != 0 || generation() != 1 {
		t.Errorf("unchanged fingerprint = %+v, generation %d; want no change at 1", out, generation())
	}

	var sha string
	queryArgs(t, dsns.Owner, `SELECT evaluated_sha FROM control_results WHERE repository_id = $1`, &sha, repo.ID)

	if sha != "sha-k2" {
		t.Errorf("evaluated_sha = %q, want it refreshed to sha-k2", sha)
	}

	stage(t, s, repo.ID, "k3", epoch, "f2", "compliant")

	if out := record("k3"); !slices.Equal(out.Changed, []string{"codeowners"}) || generation() != 2 {
		t.Errorf("changed fingerprint = %+v, generation %d; want a bump to 2", out, generation())
	}

	stage(t, s, repo.ID, "k4", epoch+1000, "f3", "non_compliant")

	if out := record("k4"); !slices.Equal(out.Discarded, []string{"codeowners"}) || generation() != 2 {
		t.Errorf("stale epoch = %+v, generation %d; want discarded, generation still 2", out, generation())
	}

	if change, err := s.Resolve(ctx, repo.ID, r); err != nil || len(change.Changed) != 0 || epochOf(t, dsns.Owner, repo.ID) != epoch {
		t.Errorf("unchanged re-resolve = %+v, %v, epoch %d; want no change and epoch %d kept", change, err, epochOf(t, dsns.Owner, repo.ID), epoch)
	}

	r.assignments = nil

	change, err := s.Resolve(ctx, repo.ID, r)
	if err != nil || !slices.Equal(change.Withdrawn, []string{"codeowners"}) {
		t.Fatalf("withdrawal = %+v, %v; want codeowners withdrawn", change, err)
	}

	var results, withdrawals, rules int
	queryRow(t, dsns.Owner, `SELECT count(*) FROM control_results`, &results)
	queryRow(t, dsns.Owner, `SELECT count(*) FROM rule_results`, &rules)
	queryRow(t, dsns.Owner, `SELECT count(*) FROM result_events WHERE to_status IS NULL AND rule_id IS NULL AND from_status = 'compliant'`, &withdrawals)

	if results != 0 || rules != 0 || withdrawals != 1 {
		t.Errorf("after withdrawal: %d results, %d rules, %d withdrawal events; want 0, 0, 1", results, rules, withdrawals)
	}

	r.assignments = []policy.Assignment{codeowners("r1")}

	if _, err := s.Resolve(ctx, repo.ID, r); err != nil {
		t.Fatal(err)
	}

	if again := epochOf(t, dsns.Owner, repo.ID); again == epoch {
		t.Errorf("re-added assignment kept epoch %d, want a new one from the sequence", again)
	}
}

// TestControlsStore_DiscoveryUnparks pins that UpsertRepositories is the
// un-parker and keys the repository by provider id across a rename.
func TestControlsStore_DiscoveryUnparks(t *testing.T) {
	s, dsns := evaluatorStore(t)
	r := &fakeResolver{}

	repo := discover(t, s, r)
	run(t, dsns.Owner, `UPDATE repositories SET active = false, park_reason = 'removed', parked_at = now()`)

	got, err := s.UpsertRepositories(t.Context(), []store.DiscoveredRepository{
		{Org: "acme", Name: "api-renamed", ProviderRepoID: 101, InstallationID: 1},
	}, r)
	if err != nil {
		t.Fatal(err)
	}

	if got[0].ID != repo.ID || got[0].Created || !got[0].Unparked {
		t.Errorf("rediscovery = %+v, want repository %d un-parked, not created", got[0], repo.ID)
	}

	var (
		name   string
		active bool
	)

	queryArgs(t, dsns.Owner, `SELECT name FROM repositories WHERE id = $1`, &name, repo.ID)
	queryArgs(t, dsns.Owner, `SELECT active FROM repositories WHERE id = $1`, &active, repo.ID)

	if name != "api-renamed" || !active {
		t.Errorf("repository = %q active=%v, want api-renamed active", name, active)
	}
}

// TestControlsStore_ParkResultRule is IMPL-0029 4.6 (DESIGN-0030 D6):
// archived, fork, removed and installation_removed clear results with a
// withdrawal event; access_denied and unknown keep them; suspension is
// not a park reason at all. Assignments are kept in every case.
func TestControlsStore_ParkResultRule(t *testing.T) {
	tests := []struct {
		reason    string
		wantClear bool
	}{
		{"archived", true},
		{"fork", true},
		{"removed", true},
		{"installation_removed", true},
		{"access_denied", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			s, dsns := evaluatorStore(t)
			repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

			stage(t, s, repo.ID, "k1", epochOf(t, dsns.Owner, repo.ID), "f1", "non_compliant")

			if _, err := s.RecordEvaluation(t.Context(), "k1"); err != nil {
				t.Fatal(err)
			}

			if err := s.Park(t.Context(), repo.ID, tt.reason); err != nil {
				t.Fatalf("Park(%s): %v", tt.reason, err)
			}

			var results, withdrawals, assignments int
			queryRow(t, dsns.Owner, `SELECT count(*) FROM control_results`, &results)
			queryRow(t, dsns.Owner, `SELECT count(*) FROM result_events WHERE to_status IS NULL`, &withdrawals)
			queryRow(t, dsns.Owner, `SELECT count(*) FROM control_assignments`, &assignments)

			wantResults, wantWithdrawals := 1, 0
			if tt.wantClear {
				wantResults, wantWithdrawals = 0, 1
			}

			if results != wantResults || withdrawals != wantWithdrawals || assignments != 1 {
				t.Errorf("Park(%s): %d results, %d withdrawals, %d assignments; want %d, %d, 1",
					tt.reason, results, withdrawals, assignments, wantResults, wantWithdrawals)
			}

			var active bool
			queryArgs(t, dsns.Owner, `SELECT active FROM repositories WHERE id = $1`, &active, repo.ID)

			if active {
				t.Errorf("Park(%s) left the repository active", tt.reason)
			}
		})
	}

	t.Run("suspension is not a park", func(t *testing.T) {
		s, _ := evaluatorStore(t)
		repo := discover(t, s, &fakeResolver{})

		if err := s.Park(t.Context(), repo.ID, "suspended"); err == nil {
			t.Error("Park(suspended) = nil, want an error: suspension is access state")
		}
	})
}

// TestControlsStore_EvaluationTarget is IMPL-0029 5.3's read, run as
// rg_evaluator: identity, policy state, the Evaluation App's
// installation with its account type, and the assignments at their
// epoch.
func TestControlsStore_EvaluationTarget(t *testing.T) {
	s, _ := evaluatorStore(t)

	repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

	// A personal account, refreshed after discovery wrote the installation.
	if err := s.UpsertInstallation(t.Context(), &store.AppInstallation{
		App: store.AppEval, InstallationID: 1, AccountLogin: "acme", AccountType: control.AccountUser, RepositorySelection: "all",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.EvaluationTarget(t.Context(), repo.ID)
	if err != nil {
		t.Fatalf("EvaluationTarget: %v", err)
	}

	if got.Org != "acme" || got.Name != "api" || !got.Active || got.PolicyState != policy.StateManaged ||
		got.PolicyVersion != testPolicyVersion || got.InstallationID != 1 || got.AccountType != control.AccountUser {
		t.Errorf("EvaluationTarget = %+v, want acme/api, active, managed under %s, installation 1 on a user account", got, testPolicyVersion)
	}

	if len(got.Assignments) != 1 {
		t.Fatalf("assignments = %+v, want codeowners", got.Assignments)
	}

	if a := got.Assignments[0]; a.ControlID != "codeowners" || a.Revision != "r1" || a.Epoch == 0 || a.State != policy.AssignmentActive {
		t.Errorf("assignment = %+v, want active codeowners at r1 with its epoch", a)
	}
}

// TestControlsStore_StageAcceptsControlsTriggers is IMPL-0029 5.4: the
// controls triggers pr_event and manual stage like the rc's.
func TestControlsStore_StageAcceptsControlsTriggers(t *testing.T) {
	s, _ := evaluatorStore(t)
	repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

	for _, trigger := range []string{"pr_event", "manual"} {
		if err := s.StageEvaluation(t.Context(), &store.EvaluationStage{
			CheckKey: "trigger/" + trigger, RepositoryID: repo.ID, Trigger: trigger, PolicyVersion: testPolicyVersion, StartedAt: time.Now(),
		}); err != nil {
			t.Errorf("StageEvaluation(trigger=%s) = %v, want accepted", trigger, err)
		}
	}
}

// TestControlsStore_ParkMissingAndServiceRun is IMPL-0029 5.5's store
// side, run as rg_evaluator: a repository the installation's complete
// listing did not see is parked removed, one it saw is not, and the
// discovery run is recorded.
func TestControlsStore_ParkMissingAndServiceRun(t *testing.T) {
	s, dsns := evaluatorStore(t)
	repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

	if n, err := s.ParkMissing(t.Context(), 1, []int64{repo.ID}); err != nil || n != 0 {
		t.Fatalf("ParkMissing(seen) = %d, %v; want 0 parked", n, err)
	}

	if n, err := s.ParkMissing(t.Context(), 2, nil); err != nil || n != 0 {
		t.Fatalf("ParkMissing(other installation) = %d, %v; want 0 parked", n, err)
	}

	if n, err := s.ParkMissing(t.Context(), 1, nil); err != nil || n != 1 {
		t.Fatalf("ParkMissing(unseen) = %d, %v; want 1 parked", n, err)
	}

	var reason string
	queryArgs(t, dsns.Owner, `SELECT park_reason FROM repositories WHERE id = $1 AND NOT active`, &reason, repo.ID)

	if reason != string(store.ParkRemoved) {
		t.Errorf("park_reason = %q, want removed", reason)
	}

	if err := s.RecordServiceRun(t.Context(), &store.ServiceRun{
		Kind: store.ServiceRunKind("discovery"), Success: true, StartedAt: time.Now(), FinishedAt: time.Now(),
		Detail: map[string]any{"repositories": 1},
	}); err != nil {
		t.Fatalf("RecordServiceRun: %v", err)
	}

	var runs int
	queryArgs(t, dsns.Owner, `SELECT count(*) FROM service_runs WHERE kind = 'discovery' AND outcome = 'success'`, &runs)

	if runs != 1 {
		t.Errorf("service_runs = %d, want 1", runs)
	}
}

// TestControlsStore_RolloutReads is IMPL-0029 5.6's store side, run as
// rg_evaluator: the rollout pages active repositories with their eval
// installation and policy state, and may stamp rollout_completed_at.
func TestControlsStore_RolloutReads(t *testing.T) {
	s, dsns := evaluatorStore(t)
	repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

	page, err := s.ActiveRepositories(t.Context(), 0, 10)
	if err != nil || len(page) != 1 {
		t.Fatalf("ActiveRepositories = %+v, %v; want one", page, err)
	}

	if r := page[0]; r.ID != repo.ID || r.InstallationID != 1 || r.PolicyState != policy.StateManaged {
		t.Errorf("page[0] = %+v, want the discovered repository, installation 1, managed", r)
	}

	if after, err := s.ActiveRepositories(t.Context(), repo.ID, 10); err != nil || len(after) != 0 {
		t.Errorf("ActiveRepositories(after last) = %+v, %v; want an empty page", after, err)
	}

	if err := s.CompletePolicyRollout(t.Context(), testPolicyVersion); err != nil {
		t.Fatalf("CompletePolicyRollout: %v", err)
	}

	var done bool
	queryArgs(t, dsns.Owner, `SELECT rollout_completed_at IS NOT NULL FROM policy_versions WHERE version = $1`, &done, testPolicyVersion)

	if !done {
		t.Error("rollout_completed_at is NULL, want stamped")
	}
}

// TestControlsStore_DiscoveryParksArchivedAndForks: discovery parks a
// repository it lists as archived or forked, clearing its results and
// resolving it parked, and un-parks it once the listing no longer says
// so. A push or a rollout never un-parks it.
func TestControlsStore_DiscoveryParksArchivedAndForks(t *testing.T) {
	s, dsns := evaluatorStore(t)
	ctx := t.Context()
	r := &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}}

	repo := discover(t, s, r)
	stage(t, s, repo.ID, "k1", epochOf(t, dsns.Owner, repo.ID), "fp1", "non_compliant")

	if _, err := s.RecordEvaluation(ctx, "k1"); err != nil {
		t.Fatal(err)
	}

	listed := func(archived, fork bool) store.ResolvedRepository {
		t.Helper()

		got, err := s.UpsertRepositories(ctx, []store.DiscoveredRepository{{
			Org: "acme", Name: "api", ProviderRepoID: 101, InstallationID: 1, Archived: archived, Fork: fork,
		}}, r)
		if err != nil {
			t.Fatal(err)
		}

		return got[0]
	}

	if got := listed(true, false); got.State != policy.StateParked || got.Unparked {
		t.Errorf("archived listing = %+v, want parked and not un-parked", got)
	}

	var reason string
	queryArgs(t, dsns.Owner, `SELECT park_reason FROM repositories WHERE id = $1 AND NOT active`, &reason, repo.ID)

	var results int
	queryArgs(t, dsns.Owner, `SELECT count(*) FROM control_results WHERE repository_id = $1`, &results, repo.ID)

	if reason != "archived" || results != 0 {
		t.Errorf("park_reason %q, results %d; want archived with results cleared", reason, results)
	}

	if got := listed(false, true); got.State != policy.StateParked {
		t.Errorf("forked listing = %+v, want parked", got)
	}

	queryArgs(t, dsns.Owner, `SELECT park_reason FROM repositories WHERE id = $1`, &reason, repo.ID)

	if reason != "fork" {
		t.Errorf("park_reason = %q, want fork", reason)
	}

	if got := listed(false, false); got.State != policy.StateManaged || !got.Unparked {
		t.Errorf("plain listing = %+v, want un-parked and managed", got)
	}
}

// TestControlsStore_RoutingReads is IMPL-0029 task 5.7: the reads and the
// installation park behind the controls webhook routing, as rg_evaluator.
func TestControlsStore_RoutingReads(t *testing.T) {
	s, _ := evaluatorStore(t)
	ctx := t.Context()
	repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

	if got, err := s.FindRepository(ctx, 999); err != nil || got != nil {
		t.Errorf("FindRepository(unknown) = %+v, %v; want nil, nil", got, err)
	}

	found, err := s.FindRepository(ctx, 101)
	if err != nil || found == nil || found.ID != repo.ID || !found.Active || found.InstallationID != 1 || found.PolicyState != policy.StateManaged {
		t.Fatalf("FindRepository(101) = %+v, %v; want the active managed repository on installation 1", found, err)
	}

	if ref, err := s.RepositoryRef(ctx, repo.ID); err != nil || *ref != *found {
		t.Errorf("RepositoryRef = %+v, %v; want %+v", ref, err, found)
	}

	if ids, err := s.OrgRepositories(ctx, "ACME"); err != nil || len(ids) != 1 || ids[0] != repo.ID {
		t.Errorf("OrgRepositories(ACME) = %v, %v; want the repository, case-insensitively", ids, err)
	}

	if n, err := s.ParkInstallation(ctx, 1, string(store.ParkInstallationRemoved)); err != nil || n != 1 {
		t.Fatalf("ParkInstallation = %d, %v; want 1 parked", n, err)
	}

	if ref, err := s.RepositoryRef(ctx, repo.ID); err != nil || ref.Active {
		t.Errorf("RepositoryRef after ParkInstallation = %+v, %v; want parked", ref, err)
	}

	if ids, err := s.OrgRepositories(ctx, "acme"); err != nil || len(ids) != 0 {
		t.Errorf("OrgRepositories after park = %v, %v; want only active repositories", ids, err)
	}
}

// TestControlsStore_ChangeDetection is IMPL-0029 task 5.11's store table,
// as rg_evaluator: the first evaluation and each fingerprint change bump
// the generation, a repeat does not, and a control an evaluation did not
// select keeps its evaluated_sha.
// freshnessSamples reads evaluation_freshness_seconds{org=acme}'s count.
func freshnessSamples(t *testing.T) uint64 {
	t.Helper()

	var m dto.Metric
	if err := metrics.EvaluationFreshnessSeconds.WithLabelValues("acme").(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}

	return m.GetHistogram().GetSampleCount()
}

func TestControlsStore_ChangeDetection(t *testing.T) {
	s, dsns := evaluatorStore(t)
	ctx := t.Context()

	renovate := codeowners("r1")
	renovate.Control.Slug = "renovate"
	repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1"), renovate}})
	epoch := epochOf(t, dsns.Owner, repo.ID)

	var renovateEpoch int64
	queryArgs(t, dsns.Owner, `SELECT epoch FROM control_assignments WHERE repository_id = $1 AND control_id = 'renovate'`, &renovateEpoch, repo.ID)

	evaluation := func(id string, e int64, fp, sha string) store.ControlEvaluation {
		return store.ControlEvaluation{
			ControlID: id, ControlVersion: 1, Revision: "r1", Epoch: e, Status: "compliant", Fingerprint: fp, EvaluatedSHA: sha,
			Rules: []store.RuleEvaluation{{RuleID: "exists", Status: "pass", Evidence: json.RawMessage(`{}`), EvidenceKind: "k", EvidenceVersion: 1}},
		}
	}

	steps := []struct {
		key      string
		controls []store.ControlEvaluation
		want     []string // controls whose generation is bumped
	}{
		{key: "k1", controls: []store.ControlEvaluation{evaluation("codeowners", epoch, "f1", "sha1"), evaluation("renovate", renovateEpoch, "g1", "sha1")},
			want: []string{"codeowners", "renovate"}},
		{key: "k2", controls: []store.ControlEvaluation{evaluation("codeowners", epoch, "f1", "sha2")}},
		{key: "k3", controls: []store.ControlEvaluation{evaluation("codeowners", epoch, "f2", "sha3")}, want: []string{"codeowners"}},
	}

	changes := metrics.EvaluationChangesTotal.WithLabelValues("acme", "codeowners")
	beforeChanges, beforeFresh := testutil.ToFloat64(changes), freshnessSamples(t)

	for _, st := range steps {
		if err := s.StageEvaluation(ctx, &store.EvaluationStage{
			CheckKey: st.key, RepositoryID: repo.ID, Trigger: "push", PolicyVersion: testPolicyVersion, StartedAt: time.Now(), Controls: st.controls,
		}); err != nil {
			t.Fatal(err)
		}

		out, err := s.RecordEvaluation(ctx, st.key)
		if got := slices.Sorted(slices.Values(out.Changed)); err != nil || !slices.Equal(got, st.want) {
			t.Errorf("record %s changed %v, %v; want %v", st.key, got, err, st.want)
		}
	}

	// codeowners moved at k1 (first of its epoch) and k3; it was
	// re-evaluated, with a previous evaluated_at, at k2 and k3.
	if got := testutil.ToFloat64(changes) - beforeChanges; got != 2 {
		t.Errorf("evaluation_changes_total{control=codeowners} moved by %v, want 2", got)
	}

	if got := freshnessSamples(t) - beforeFresh; got != 2 {
		t.Errorf("evaluation_freshness_seconds samples moved by %d, want 2", got)
	}

	var codeownersSHA, renovateSHA string
	queryArgs(t, dsns.Owner, `SELECT evaluated_sha FROM control_results WHERE repository_id = $1 AND control_id = 'codeowners'`, &codeownersSHA, repo.ID)
	queryArgs(t, dsns.Owner, `SELECT evaluated_sha FROM control_results WHERE repository_id = $1 AND control_id = 'renovate'`, &renovateSHA, repo.ID)

	if codeownersSHA != "sha3" || renovateSHA != "sha1" {
		t.Errorf("evaluated_sha codeowners %q, renovate %q; want sha3 and the unselected renovate's sha1 kept", codeownersSHA, renovateSHA)
	}
}
