//go:build integration

// Worker integration tests run the real engine and the real Postgres
// store end to end, so the posture rows a sweep produces are checked
// against a live database rather than a recorder.
//
//	go test -tags=integration ./internal/worker/...
package worker_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/mock"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	ghmocks "github.com/donaldgifford/repo-guardian/internal/github/mocks"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/worker"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// startPostgresFor provisions a Postgres container, migrates it, and
// returns the DSN.
func startPostgresFor(ctx context.Context, t *testing.T) string {
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

	if err := pgstore.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return dsn
}

// postureCounts runs the exact aggregate from DESIGN-0022's posture
// query, so this test fails if the rows the worker writes cannot answer
// the question the Phase 2 exporter will ask of them.
func postureCounts(ctx context.Context, t *testing.T, dsn string) map[string][2]int {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx,
		`SELECT owner, rule_name,
		        count(*) FILTER (WHERE actionable) AS actionable,
		        count(*)                            AS tracked
		 FROM rule_state
		 GROUP BY 1, 2`)
	if err != nil {
		t.Fatalf("posture query: %v", err)
	}

	defer rows.Close()

	out := make(map[string][2]int)

	for rows.Next() {
		var (
			org, rule           string
			actionable, tracked int
		)

		if err := rows.Scan(&org, &rule, &actionable, &tracked); err != nil {
			t.Fatalf("scan posture: %v", err)
		}

		out[org+"/"+rule] = [2]int{actionable, tracked}
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate posture: %v", err)
	}

	return out
}

// mixedEngine builds a policy with one rule of each kind, in dry-run so
// no remediation happens and every verdict reflects the repo as found.
func mixedEngine(t *testing.T) *checker.Engine {
	t.Helper()

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("template store load: %v", err)
	}

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{Name: "codeowners", Type: "file", Paths: []string{"CODEOWNERS"}, Template: "codeowners.tmpl"},
		},
		SettingRules: []policy.SettingRuleConfig{
			{Name: "enable_issues", Property: "has_issues", Expected: true},
		},
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{Name: "protect_main", Branch: "main", RequirePR: true},
		},
	}
	cfg.Guardian.DryRun = true

	e, err := checker.NewEngine(cfg, ts, discardLogger(), nil)
	if err != nil {
		t.Fatalf("checker.NewEngine: %v", err)
	}

	return e
}

// clientForRepo wires a mock GitHub client for one repo.
// codeownersPresent drives the file rule's verdict.
func clientForRepo(t *testing.T, owner, repo string, codeownersPresent bool) ghclient.Client {
	t.Helper()

	install := &ghmocks.MockClient{}
	install.On("GetRepository", mock.Anything, owner, repo).
		Return(&ghclient.Repository{Owner: owner, Name: repo, HasBranch: true, DefaultRef: "main"}, nil)
	install.On("ListOpenPullRequests", mock.Anything, owner, repo).
		Return([]*ghclient.PullRequest(nil), nil)
	install.On("GetContents", mock.Anything, owner, repo, "CODEOWNERS").
		Return(codeownersPresent, nil)
	// has_issues false -> setting rule actionable (remediate defaults off).
	install.On("GetRepoSettings", mock.Anything, owner, repo).
		Return(&ghclient.RepoSettings{HasIssues: false}, nil)
	// Branch exists but has no ruleset -> BP actionable.
	install.On("GetBranchSHA", mock.Anything, owner, repo, "main").Return("abc123", nil)
	install.On("ListRepositoryRulesets", mock.Anything, owner, repo).
		Return([]*ghclient.Ruleset(nil), nil)

	root := &ghmocks.MockClient{}
	root.On("CreateInstallationClient", mock.Anything, mock.Anything).
		Return(ghclient.Client(install), nil)

	return root
}

// runOneJob pushes a single job through a real Pool and waits for the
// handler to return.
func runOneJob(t *testing.T, dsn string, gh ghclient.Client, j queue.Job) error {
	t.Helper()

	ctx := context.Background()

	st, err := pgstore.New(ctx, dsn, 4, discardLogger())
	if err != nil {
		t.Fatalf("pgstore.New: %v", err)
	}

	defer func() { _ = st.Close() }()

	q := &deliverOnceQueue{job: j, result: make(chan error, 1)}
	p := worker.New(q, mixedEngine(t), gh, st, "pv1", 10, 1, discardLogger())

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	p.Start(runCtx)

	var res error
	select {
	case res = <-q.result:
	case <-time.After(30 * time.Second):
		t.Fatal("handler never invoked")
	}

	cancel()
	p.Stop()

	return res
}

// TestWorker_PostureMatchesEngineVerdicts is Phase 1's headline success
// criterion (IMPL-0023): after a check, the DESIGN-0022 posture query
// returns exactly the engine's in-memory verdicts, for every rule kind.
//
// The unit tests prove each hop in isolation — the engine produces
// outcomes, writeBack translates them, the store's SQL upserts them.
// This is the only test that would catch a mismatch *between* those
// hops: a kind string that does not round-trip, an identity column
// filled from the wrong side, or an aggregate the rows cannot answer.
func TestWorker_PostureMatchesEngineVerdicts(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgresFor(ctx, t)

	if err := runOneJob(t, dsn, clientForRepo(t, "octo", "alpha", false), queue.Job{
		ID: "j1", InstallationID: 42, Owner: "octo", Repo: "alpha",
		Trigger: queue.TriggerScheduler,
	}); err != nil {
		t.Fatalf("processJob: %v", err)
	}

	got := postureCounts(ctx, t, dsn)

	// CODEOWNERS missing, has_issues wrong, no ruleset — all three
	// actionable, all three tracked.
	want := map[string][2]int{
		"octo/codeowners":    {1, 1},
		"octo/enable_issues": {1, 1},
		"octo/protect_main":  {1, 1},
	}

	for key, w := range want {
		if got[key] != w {
			t.Errorf("posture[%s] = {actionable:%d tracked:%d}, want {actionable:%d tracked:%d}",
				key, got[key][0], got[key][1], w[0], w[1])
		}
	}

	if len(got) != len(want) {
		t.Errorf("posture has %d (org, rule) groups, want %d: %v", len(got), len(want), got)
	}
}

// TestWorker_SatisfiedRuleClearsActionableSince walks Phase 1's second
// success criterion through the whole stack: a repo that fixes a rule
// reads as compliant on its next check, with the since-clock cleared —
// not merely as "no longer counted", which a delete-not-in bug would
// also produce.
func TestWorker_SatisfiedRuleClearsActionableSince(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgresFor(ctx, t)

	job := queue.Job{
		ID: "j1", InstallationID: 42, Owner: "octo", Repo: "alpha",
		Trigger: queue.TriggerScheduler,
	}

	// Check 1: CODEOWNERS missing.
	if err := runOneJob(t, dsn, clientForRepo(t, "octo", "alpha", false), job); err != nil {
		t.Fatalf("first check: %v", err)
	}

	if got := postureCounts(ctx, t, dsn)["octo/codeowners"]; got != [2]int{1, 1} {
		t.Fatalf("posture after first check = %v, want {1, 1}", got)
	}

	// Check 2: someone merged a CODEOWNERS file.
	if err := runOneJob(t, dsn, clientForRepo(t, "octo", "alpha", true), job); err != nil {
		t.Fatalf("second check: %v", err)
	}

	// Still tracked (the rule applies), no longer actionable.
	if got := postureCounts(ctx, t, dsn)["octo/codeowners"]; got != [2]int{0, 1} {
		t.Errorf("posture after the fix = {actionable:%d tracked:%d}, want {actionable:0 tracked:1}", got[0], got[1])
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() { _ = conn.Close(ctx) }()

	var since *time.Time
	if err := conn.QueryRow(ctx,
		`SELECT actionable_since FROM rule_state
		 WHERE installation_id = 42 AND owner = 'octo' AND repo = 'alpha'
		   AND rule_name = 'codeowners'`).Scan(&since); err != nil {
		t.Fatalf("read actionable_since: %v", err)
	}

	if since != nil {
		t.Errorf("actionable_since = %v after the rule became satisfied, want NULL", since)
	}
}
