//go:build integration

package postgres_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/control"
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
				RuleID: "exists", Status: "fail", Remediate: true, Evidence: json.RawMessage(`{"kind":"codeowners.exists"}`),
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
