//go:build integration

// Package postgres integration tests run against a real PostgreSQL
// instance provisioned with testcontainers-go. Build them only under
// the `integration` tag — the standard `go test ./...` path stays
// hermetic and fast.
//
//	go test -tags=integration ./internal/store/postgres/...
package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// startPostgres provisions a Postgres 16 container and returns the DSN
// + a cleanup function. The container is wired with `pg_isready` wait
// strategy so the test only proceeds once the database accepts
// connections; previous attempts using port-only waits flaked at high
// host load.
func startPostgres(ctx context.Context, t *testing.T) string {
	t.Helper()

	container, err := tcpostgres.Run(
		ctx,
		"postgres:18.4-alpine",
		tcpostgres.WithDatabase("repoguardian_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	return dsn
}

// newStore migrates the schema and constructs a Store against dsn.
// Centralised so every test exercises the same startup path.
func newStore(ctx context.Context, t *testing.T, dsn string) *postgres.Store {
	t.Helper()

	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	s, err := postgres.New(ctx, dsn, 4, logger)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("store close: %v", err)
		}
	})

	return s
}

func TestPostgresStore_UpsertAndReadBack(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	rs := &store.RepoState{
		InstallationID:  42,
		Owner:           "octo",
		Repo:            "alpha",
		LastCheckedAt:   &ts,
		LastCheckStatus: store.StatusSuccess,
		LastError:       "",
		PolicyVersion:   "abc123",
	}

	if err := s.UpdateRepoState(ctx, rs); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	got, err := s.GetRepoState(ctx, 42, "octo", "alpha")
	if err != nil {
		t.Fatalf("get after insert: %v", err)
	}

	if got.PolicyVersion != "abc123" || got.LastCheckStatus != store.StatusSuccess {
		t.Fatalf("unexpected state after insert: %+v", got)
	}

	rs.PolicyVersion = "def456"
	rs.LastCheckStatus = store.StatusError
	rs.LastError = "boom"

	if err := s.UpdateRepoState(ctx, rs); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err = s.GetRepoState(ctx, 42, "octo", "alpha")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}

	if got.PolicyVersion != "def456" || got.LastError != "boom" {
		t.Fatalf("upsert did not overwrite: %+v", got)
	}
}

func TestPostgresStore_GetNotFound(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	_, err := s.GetRepoState(ctx, 1, "ghost", "repo")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPostgresStore_StaleByFreshness(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	recent := now.Add(-1 * time.Minute)

	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "old",
		LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	})
	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "fresh",
		LastCheckedAt: &recent, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	})
	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "never",
		LastCheckedAt: nil, LastCheckStatus: store.StatusPending, PolicyVersion: "v1",
	})

	stale, err := s.StaleRepos(ctx, time.Hour, "v1", 10)
	if err != nil {
		t.Fatalf("stale: %v", err)
	}

	if len(stale) != 2 {
		t.Fatalf("expected 2 stale rows (never, old), got %d (%+v)", len(stale), stale)
	}

	if stale[0].Repo != "never" {
		t.Fatalf("expected NULLS FIRST ordering, got %q first", stale[0].Repo)
	}
}

func TestPostgresStore_StaleByPolicyVersion(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	recent := time.Now().UTC().Add(-1 * time.Minute)

	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "matched",
		LastCheckedAt: &recent, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v2",
	})
	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "drifted",
		LastCheckedAt: &recent, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	})

	stale, err := s.StaleRepos(ctx, time.Hour, "v2", 10)
	if err != nil {
		t.Fatalf("stale: %v", err)
	}

	if len(stale) != 1 || stale[0].Repo != "drifted" {
		t.Fatalf("expected only 'drifted', got %+v", stale)
	}
}

func TestPostgresStore_UpsertIfMissing(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	// First call inserts a fresh row.
	created, err := s.UpsertIfMissing(ctx, &store.RepoState{
		InstallationID: 7,
		Owner:          "octo",
		Repo:           "new-repo",
		PolicyVersion:  "v1",
	})
	if err != nil {
		t.Fatalf("first UpsertIfMissing: %v", err)
	}

	if !created {
		t.Fatal("expected created=true on fresh insert")
	}

	got, err := s.GetRepoState(ctx, 7, "octo", "new-repo")
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}

	if got.LastCheckedAt != nil {
		t.Fatalf("expected nil LastCheckedAt, got %v", got.LastCheckedAt)
	}

	if got.LastCheckStatus != store.StatusPending {
		t.Fatalf("expected StatusPending, got %q", got.LastCheckStatus)
	}

	// Second call on the same key is a no-op; created=false and the
	// existing row is unchanged.
	ts := time.Now().UTC()

	rs := &store.RepoState{
		InstallationID:  7,
		Owner:           "octo",
		Repo:            "new-repo",
		LastCheckedAt:   &ts,
		LastCheckStatus: store.StatusSuccess,
		PolicyVersion:   "v2",
	}
	if err := s.UpdateRepoState(ctx, rs); err != nil {
		t.Fatalf("UpdateRepoState to populate state: %v", err)
	}

	created, err = s.UpsertIfMissing(ctx, &store.RepoState{
		InstallationID: 7,
		Owner:          "octo",
		Repo:           "new-repo",
		PolicyVersion:  "v3", // intentionally different — must NOT overwrite.
	})
	if err != nil {
		t.Fatalf("second UpsertIfMissing: %v", err)
	}

	if created {
		t.Fatal("expected created=false on existing row")
	}

	got, err = s.GetRepoState(ctx, 7, "octo", "new-repo")
	if err != nil {
		t.Fatalf("get after second upsert: %v", err)
	}

	if got.PolicyVersion != "v2" {
		t.Fatalf("UpsertIfMissing overwrote PolicyVersion: %q (wanted v2)", got.PolicyVersion)
	}

	if got.LastCheckStatus != store.StatusSuccess {
		t.Fatalf("UpsertIfMissing overwrote LastCheckStatus: %q (wanted success)", got.LastCheckStatus)
	}
}

// TestPostgresStore_UpsertIfMissing_ConcurrentRace exercises the
// IMPL-0015 Testing Plan race-condition checkbox: simultaneous
// webhook + Discoverer writes for the same repo. The contract is
// that ON CONFLICT DO NOTHING returns the *atomic* "did I insert this
// row" flag — exactly one goroutine across N parallel callers must
// see created=true, all others created=false.
func TestPostgresStore_UpsertIfMissing_ConcurrentRace(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	const workers = 16

	var (
		wg       sync.WaitGroup
		startGun = make(chan struct{})
		created  atomic.Int32
		errCount atomic.Int32
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-startGun

			ok, err := s.UpsertIfMissing(ctx, &store.RepoState{
				InstallationID: 99,
				Owner:          "race",
				Repo:           "contended",
				PolicyVersion:  "v1",
			})
			if err != nil {
				errCount.Add(1)
				return
			}

			if ok {
				created.Add(1)
			}
		}()
	}

	close(startGun)
	wg.Wait()

	if got := errCount.Load(); got != 0 {
		t.Fatalf("UpsertIfMissing errored %d/%d times under contention", got, workers)
	}

	if got := created.Load(); got != 1 {
		t.Fatalf("expected exactly 1 created=true under contention, got %d/%d", got, workers)
	}
}

func TestPostgresStore_MigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)

	for i := range 3 {
		if err := postgres.Migrate(dsn); err != nil {
			t.Fatalf("migrate apply %d: %v", i, err)
		}
	}

	s := newStore(ctx, t, dsn)

	ts := time.Now().UTC()
	if err := s.UpdateRepoState(ctx, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "after-migrate",
		LastCheckedAt: &ts, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("upsert after repeated migrate: %v", err)
	}
}

// ruleStateRow is the subset of a rule_state row the posture tests
// assert on.
type ruleStateRow struct {
	Kind       string
	Actionable bool
	Since      *time.Time
}

// readRuleStates returns every rule_state row for a repo, keyed by rule
// name.
func readRuleStates(ctx context.Context, t *testing.T, dsn string, installationID int64, owner, repo string) map[string]ruleStateRow {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx,
		`SELECT rule_name, rule_kind, actionable, actionable_since
		 FROM rule_state
		 WHERE installation_id = $1 AND owner = $2 AND repo = $3`,
		installationID, owner, repo,
	)
	if err != nil {
		t.Fatalf("query rule_state: %v", err)
	}

	defer rows.Close()

	out := make(map[string]ruleStateRow)

	for rows.Next() {
		var (
			name string
			r    ruleStateRow
		)

		if err := rows.Scan(&name, &r.Kind, &r.Actionable, &r.Since); err != nil {
			t.Fatalf("scan rule_state: %v", err)
		}

		out[name] = r
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rule_state: %v", err)
	}

	return out
}

// TestPostgresStore_ActionableSinceTransitions covers IMPL-0023 task
// 1.3's whole reason for living in SQL. actionable_since is the
// "missing since 2026-06-14" a compliance report shows a human, and
// each of the four edges has a distinct failure mode if the CASE is
// wrong: a reset clock on true→true silently rewrites history to
// "noticed today", and a preserved timestamp across false→true reports
// a repo as failing since before it was.
func TestPostgresStore_ActionableSinceTransitions(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	const (
		instID = 42
		owner  = "org"
		repo   = "repo"
		rule   = "codeowners"
	)

	write := func(actionable bool) {
		t.Helper()

		if err := s.UpsertRuleStates(ctx, instID, owner, repo, []store.RuleState{
			{
				InstallationID: instID, Owner: owner, Repo: repo,
				RuleName: rule, RuleKind: "file",
				Actionable: actionable, PolicyVersion: "v1",
			},
		}); err != nil {
			t.Fatalf("UpsertRuleStates(actionable=%v): %v", actionable, err)
		}
	}

	// Edge 1: absent → false. A first-ever check of a compliant repo
	// must not invent a since-date.
	write(false)

	got := readRuleStates(ctx, t, dsn, instID, owner, repo)[rule]
	if got.Actionable {
		t.Errorf("actionable = true, want false on first compliant check")
	}

	if got.Since != nil {
		t.Errorf("actionable_since = %v, want NULL for a rule that never failed", got.Since)
	}

	// Edge 2: false → true. The clock starts now.
	write(true)

	got = readRuleStates(ctx, t, dsn, instID, owner, repo)[rule]
	if !got.Actionable {
		t.Fatal("actionable = false, want true after the repo started failing")
	}

	if got.Since == nil {
		t.Fatal("actionable_since = NULL, want a timestamp stamped on the false->true edge")
	}

	firstSince := *got.Since

	// Edge 3: true → true. The original timestamp survives. This is the
	// edge that makes the column worth having; a naive
	// `actionable_since = now()` would pass every other assertion here
	// and silently reset every repo's since-date on every sweep.
	time.Sleep(10 * time.Millisecond)
	write(true)

	got = readRuleStates(ctx, t, dsn, instID, owner, repo)[rule]
	if got.Since == nil || !got.Since.Equal(firstSince) {
		t.Errorf("actionable_since = %v after a second failing check, want it preserved at %v", got.Since, firstSince)
	}

	// Edge 4: true → false. Cleared, so the next failure starts a fresh
	// clock rather than reporting a months-old date.
	write(false)

	got = readRuleStates(ctx, t, dsn, instID, owner, repo)[rule]
	if got.Actionable {
		t.Error("actionable = true, want false after the repo became compliant")
	}

	if got.Since != nil {
		t.Errorf("actionable_since = %v, want NULL once the rule is satisfied", got.Since)
	}

	// And a fresh failure gets a new clock, not the old one.
	write(true)

	got = readRuleStates(ctx, t, dsn, instID, owner, repo)[rule]
	if got.Since == nil || !got.Since.After(firstSince) {
		t.Errorf("actionable_since = %v on re-failure, want a timestamp later than the cleared %v", got.Since, firstSince)
	}
}

// TestPostgresStore_UpsertRuleStates_DeleteNotIn locks the
// reconciliation half (DESIGN-0022 OQ3 → a): a rule that leaves the
// evaluated set loses its row on the very next check. Without it a
// renamed rule keeps its last verdict forever and goes on failing a
// compliance report nobody can fix, because the rule it names no longer
// exists to be satisfied.
func TestPostgresStore_UpsertRuleStates_DeleteNotIn(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	const (
		instID = 7
		owner  = "org"
		repo   = "repo"
	)

	rs := func(name string, actionable bool) store.RuleState {
		return store.RuleState{
			InstallationID: instID, Owner: owner, Repo: repo,
			RuleName: name, RuleKind: "file",
			Actionable: actionable, PolicyVersion: "v1",
		}
	}

	if err := s.UpsertRuleStates(ctx, instID, owner, repo, []store.RuleState{
		rs("codeowners", true), rs("dependabot", false), rs("renovate", true),
	}); err != nil {
		t.Fatalf("UpsertRuleStates initial: %v", err)
	}

	if got := readRuleStates(ctx, t, dsn, instID, owner, repo); len(got) != 3 {
		t.Fatalf("rule rows = %d, want 3 after the initial write", len(got))
	}

	// "renovate" was renamed to "renovate_config"; "dependabot" was
	// removed from the policy entirely.
	if err := s.UpsertRuleStates(ctx, instID, owner, repo, []store.RuleState{
		rs("codeowners", true), rs("renovate_config", true),
	}); err != nil {
		t.Fatalf("UpsertRuleStates after policy change: %v", err)
	}

	got := readRuleStates(ctx, t, dsn, instID, owner, repo)
	if len(got) != 2 {
		t.Fatalf("rule rows = %v, want exactly codeowners + renovate_config", got)
	}

	for _, gone := range []string{"dependabot", "renovate"} {
		if _, ok := got[gone]; ok {
			t.Errorf("rule %q still has a row after leaving the evaluated set", gone)
		}
	}

	// An empty set is a legitimate call, not a no-op: it is how a repo
	// that left policy scope stops counting against compliance.
	if err := s.UpsertRuleStates(ctx, instID, owner, repo, nil); err != nil {
		t.Fatalf("UpsertRuleStates(nil): %v", err)
	}

	if got := readRuleStates(ctx, t, dsn, instID, owner, repo); len(got) != 0 {
		t.Errorf("rule rows = %v after an empty evaluated set, want none", got)
	}
}

// TestPostgresStore_UpsertRuleStates_ScopedToRepo guards the blast
// radius of delete-not-in: it must clear rows for the named repo only.
// A missing repo predicate in the DELETE would wipe the fleet's posture
// on every single check and still pass every other test in this file.
func TestPostgresStore_UpsertRuleStates_ScopedToRepo(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	const owner = "org"

	seed := func(instID int64, repo string) {
		t.Helper()

		if err := s.UpsertRuleStates(ctx, instID, owner, repo, []store.RuleState{
			{
				InstallationID: instID, Owner: owner, Repo: repo,
				RuleName: "codeowners", RuleKind: "file",
				Actionable: true, PolicyVersion: "v1",
			},
		}); err != nil {
			t.Fatalf("seed %d/%s: %v", instID, repo, err)
		}
	}

	seed(1, "alpha")
	seed(1, "beta")
	seed(2, "alpha") // same repo name, different installation

	// Clear only 1/org/alpha.
	if err := s.UpsertRuleStates(ctx, 1, owner, "alpha", nil); err != nil {
		t.Fatalf("clear alpha: %v", err)
	}

	if got := readRuleStates(ctx, t, dsn, 1, owner, "alpha"); len(got) != 0 {
		t.Errorf("1/org/alpha rows = %v, want cleared", got)
	}

	if got := readRuleStates(ctx, t, dsn, 1, owner, "beta"); len(got) != 1 {
		t.Errorf("1/org/beta rows = %v, want untouched", got)
	}

	if got := readRuleStates(ctx, t, dsn, 2, owner, "alpha"); len(got) != 1 {
		t.Errorf("2/org/alpha rows = %v, want untouched (different installation)", got)
	}
}

// TestPostgresStore_UpsertRuleStates_ConcurrentRace mirrors
// TestPostgresStore_UpsertIfMissing_ConcurrentRace for the posture
// write path. Sixteen workers racing on the same repo is not
// hypothetical — a push event and a stale sweep can dispatch the same
// repo concurrently. The transition CASE reads rule_state.actionable
// under the row lock ON CONFLICT already holds, so there is no
// read-then-write window; this proves it under -race and under real
// contention.
func TestPostgresStore_UpsertRuleStates_ConcurrentRace(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	const (
		instID  = 99
		owner   = "org"
		repo    = "hot"
		workers = 16
	)

	var (
		wg       sync.WaitGroup
		failures atomic.Int32
	)

	wg.Add(workers)

	for i := range workers {
		go func(i int) {
			defer wg.Done()

			if err := s.UpsertRuleStates(ctx, instID, owner, repo, []store.RuleState{
				{
					InstallationID: instID, Owner: owner, Repo: repo,
					RuleName: "codeowners", RuleKind: "file",
					Actionable: i%2 == 0, PolicyVersion: "v1",
				},
			}); err != nil {
				t.Logf("worker %d: %v", i, err)
				failures.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if got := failures.Load(); got != 0 {
		t.Errorf("UpsertRuleStates failures under contention = %d, want 0", got)
	}

	// Exactly one row survives regardless of interleaving — the primary
	// key guarantees it, and a batch that partially applied would show
	// up here as zero rows.
	if got := readRuleStates(ctx, t, dsn, instID, owner, repo); len(got) != 1 {
		t.Errorf("rule rows after %d concurrent writers = %v, want exactly 1", workers, got)
	}
}

// TestPostgresStore_CatalogParseOK_NilPreservesPriorVerdict locks the
// COALESCE in UpdateRepoState. A check where no catalog rule ran
// reports nil, which means "learned nothing" — not "the catalog is
// fine". A plain EXCLUDED assignment would erase a real parse failure
// the moment a sweep ran with the catalog rule scoped away, and the
// operator would never see the broken file again.
func TestPostgresStore_CatalogParseOK_NilPreservesPriorVerdict(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	ts := time.Now().UTC()
	base := func(parseOK *bool) *store.RepoState {
		return &store.RepoState{
			InstallationID: 5, Owner: "org", Repo: "repo",
			LastCheckedAt: &ts, LastCheckStatus: store.StatusSuccess,
			PolicyVersion: "v1", CatalogParseOK: parseOK,
		}
	}

	no := false
	mustUpdate(ctx, t, s, base(&no))

	got, err := s.GetRepoState(ctx, 5, "org", "repo")
	if err != nil {
		t.Fatalf("GetRepoState: %v", err)
	}

	if got.CatalogParseOK == nil || *got.CatalogParseOK {
		t.Fatalf("catalog_parse_ok = %v, want false", got.CatalogParseOK)
	}

	// A later check that evaluated no catalog rule must leave it alone.
	mustUpdate(ctx, t, s, base(nil))

	got, err = s.GetRepoState(ctx, 5, "org", "repo")
	if err != nil {
		t.Fatalf("GetRepoState after nil write: %v", err)
	}

	if got.CatalogParseOK == nil || *got.CatalogParseOK {
		t.Errorf("catalog_parse_ok = %v after a nil write, want the prior false preserved", got.CatalogParseOK)
	}

	// An explicit true does overwrite — the catalog got fixed.
	yes := true
	mustUpdate(ctx, t, s, base(&yes))

	got, err = s.GetRepoState(ctx, 5, "org", "repo")
	if err != nil {
		t.Fatalf("GetRepoState after true write: %v", err)
	}

	if got.CatalogParseOK == nil || !*got.CatalogParseOK {
		t.Errorf("catalog_parse_ok = %v, want true once the catalog parsed", got.CatalogParseOK)
	}
}

// TestPostgresStore_MigrateUpDownUp locks IMPL-0023 task 1.1: every
// .down.sql is the true inverse of its .up.sql. A drifted down file is
// worse than none — it fails partway and strands a half-dropped schema
// — and nothing else in the suite would catch it, since the normal
// startup path only ever migrates up.
//
// The final Up is the load-bearing assertion: it only succeeds if Down
// left the database genuinely empty rather than merely renumbered in
// schema_migrations.
func TestPostgresStore_MigrateUpDownUp(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)

	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	if err := postgres.MigrateDown(dsn); err != nil {
		t.Fatalf("migrate down: %v", err)
	}

	assertRelationAbsent(ctx, t, dsn, "repo_state")
	assertRelationAbsent(ctx, t, dsn, "rule_state")
	assertRelationAbsent(ctx, t, dsn, "compliance_snapshot")

	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("migrate up after down: %v", err)
	}

	// catalog_parse_ok is added by 0002 as an ALTER on a table 0001
	// owns. Dropping the column and re-adding it is the edge a
	// table-level up/down check would miss entirely.
	assertColumnPresent(ctx, t, dsn, "repo_state", "catalog_parse_ok")

	s := newStore(ctx, t, dsn)

	ts := time.Now().UTC()
	if err := s.UpdateRepoState(ctx, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "after-round-trip",
		LastCheckedAt: &ts, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("upsert after up/down/up: %v", err)
	}
}

// assertRelationAbsent fails the test if relation exists in the public
// schema.
func assertRelationAbsent(ctx context.Context, t *testing.T, dsn, relation string) {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() { _ = conn.Close(ctx) }()

	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = $1)`,
		relation,
	).Scan(&exists); err != nil {
		t.Fatalf("query relation %q: %v", relation, err)
	}

	if exists {
		t.Errorf("relation %q still exists after migrate down, want dropped", relation)
	}
}

// assertColumnPresent fails the test if table lacks column.
func assertColumnPresent(ctx context.Context, t *testing.T, dsn, table, column string) {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() { _ = conn.Close(ctx) }()

	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2)`,
		table, column,
	).Scan(&exists); err != nil {
		t.Fatalf("query column %s.%s: %v", table, column, err)
	}

	if !exists {
		t.Errorf("column %s.%s missing after re-migrate, want present", table, column)
	}
}

func mustUpdate(ctx context.Context, t *testing.T, s *postgres.Store, rs *store.RepoState) {
	t.Helper()

	if err := s.UpdateRepoState(ctx, rs); err != nil {
		t.Fatalf("UpdateRepoState %s/%s: %v", rs.Owner, rs.Repo, err)
	}
}

// TestPostgresStore_DeactivateExcludesFromSweep pins the INV-0015
// circuit breaker's store half: a parked repository must stop being
// handed back by the sweep, and must stay parked across sweeps.
func TestPostgresStore_DeactivateExcludesFromSweep(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	old := time.Now().UTC().Add(-2 * time.Hour)

	for _, repo := range []string{"reachable", "denied"} {
		mustUpdate(ctx, t, s, &store.RepoState{
			InstallationID: 1, Owner: "o", Repo: repo,
			LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
		})
	}

	if err := s.Deactivate(ctx, 1, "o", "denied"); err != nil {
		t.Fatalf("Deactivate() = %v, want nil error", err)
	}

	stale, err := s.StaleRepos(ctx, time.Hour, "v1", 10)
	if err != nil {
		t.Fatalf("StaleRepos() = _, %v, want nil error", err)
	}

	if len(stale) != 1 || stale[0].Repo != "reachable" {
		t.Fatalf("StaleRepos() returned %+v, want only [reachable]; a parked repo must not be re-enqueued", stale)
	}

	// A policy-version bump must not resurrect it either: the freshness
	// clause is a disjunction, so `active` has to gate the whole group
	// rather than binding to the last OR arm.
	stale, err = s.StaleRepos(ctx, time.Hour, "v2", 10)
	if err != nil {
		t.Fatalf("StaleRepos() = _, %v, want nil error", err)
	}

	for _, rs := range stale {
		if rs.Repo == "denied" {
			t.Errorf("a policy-version change resurrected a parked repo; `active` is binding to one OR arm instead of the group")
		}
	}

	// Idempotent, and harmless on a row that does not exist.
	if err := s.Deactivate(ctx, 1, "o", "denied"); err != nil {
		t.Errorf("second Deactivate() = %v, want nil error", err)
	}

	if err := s.Deactivate(ctx, 1, "o", "ghost"); err != nil {
		t.Errorf("Deactivate() on an absent row = %v, want nil error", err)
	}
}

// TestPostgresStore_DiscoveryReactivates pins the other half: discovery
// is the only thing that may un-park a repository, and doing so must not
// disturb the freshness gate.
func TestPostgresStore_DiscoveryReactivates(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	checked := time.Now().UTC().Add(-2 * time.Hour)

	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "r",
		LastCheckedAt: &checked, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	})

	if err := s.Deactivate(ctx, 1, "o", "r"); err != nil {
		t.Fatalf("Deactivate() = %v, want nil error", err)
	}

	// Discovery seeing the repo again is proof the App can reach it.
	created, err := s.UpsertIfMissing(ctx, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "r",
		LastCheckStatus: store.StatusPending,
	})
	if err != nil {
		t.Fatalf("UpsertIfMissing() = _, %v, want nil error", err)
	}

	if created {
		t.Errorf("UpsertIfMissing() = true, want false — reactivating an existing row is not a creation")
	}

	stale, err := s.StaleRepos(ctx, time.Hour, "v1", 10)
	if err != nil {
		t.Fatalf("StaleRepos() = _, %v, want nil error", err)
	}

	if len(stale) != 1 || stale[0].Repo != "r" {
		t.Fatalf("StaleRepos() returned %+v, want [r] — discovery must return a parked repo to the sweep", stale)
	}

	// Reactivation must not reset the freshness gate. Overwriting
	// last_checked_at or policy_version here would re-check the entire
	// fleet on every discovery pass.
	got, err := s.GetRepoState(ctx, 1, "o", "r")
	if err != nil {
		t.Fatalf("GetRepoState() = _, %v, want nil error", err)
	}

	if got.LastCheckedAt == nil || !got.LastCheckedAt.Equal(checked) {
		t.Errorf("LastCheckedAt = %v, want %v unchanged", got.LastCheckedAt, checked)
	}

	if got.PolicyVersion != "v1" {
		t.Errorf("PolicyVersion = %q, want %q unchanged", got.PolicyVersion, "v1")
	}
}

// TestPostgresStore_UpdateRepoStateDoesNotUnpark pins the boundary
// between the two writers.
//
// UpdateRepoState is called on every processed job (worker write-back)
// and on every push webhook, so if it reset active the parking would be
// undone within one cycle by the very path that set it. The column is
// deliberately absent from its ON CONFLICT list; this test fails if
// someone adds it, or switches the statement to a whole-row upsert.
func TestPostgresStore_UpdateRepoStateDoesNotUnpark(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	old := time.Now().UTC().Add(-2 * time.Hour)

	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "r",
		LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	})

	if err := s.Deactivate(ctx, 1, "o", "r"); err != nil {
		t.Fatalf("Deactivate() = %v, want nil error", err)
	}

	// Exactly what the worker write-back and the push webhook do.
	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "r",
		LastCheckedAt: &old, LastCheckStatus: store.StatusError, PolicyVersion: "v1",
	})

	stale, err := s.StaleRepos(ctx, time.Hour, "v1", 10)
	if err != nil {
		t.Fatalf("StaleRepos() = _, %v, want nil error", err)
	}

	if len(stale) != 0 {
		t.Errorf("StaleRepos() returned %+v, want none — UpdateRepoState un-parked the row it was told nothing about", stale)
	}
}
