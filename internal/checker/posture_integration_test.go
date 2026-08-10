//go:build integration

// Integration tests for the posture exporter (IMPL-0023 task 2.6).
// Two things the unit tests structurally cannot cover:
//
//  1. Leader gating against real Valkey — the unit tests call Export
//     directly, so they say nothing about whether the handler is
//     actually serialisable under the SETNX election.
//
//  2. Gauge values against real SQL — the unit tests assert the
//     exporter faithfully publishes whatever the store hands it, which
//     is true by construction of the fake. Whether the store hands it
//     the right numbers is a question only Postgres can answer.
//
//     go test -tags=integration ./internal/checker/...
package checker_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/scheduler/valkey"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// startPostgresForPosture and newStoreForPosture mirror the helpers in
// internal/store/postgres. Inlined rather than imported because those
// live in a test-only package — the same reason the Valkey scheduler
// contract test duplicates its own harness.
func startPostgresForPosture(ctx context.Context, t *testing.T) string {
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

func newStoreForPosture(ctx context.Context, t *testing.T, dsn string) *postgres.Store {
	t.Helper()

	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	s, err := postgres.New(ctx, dsn, 4, logger)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("close store: %v", err)
		}
	})

	return s
}

func startValkeyForPosture(ctx context.Context, t *testing.T) *redis.Client {
	t.Helper()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "valkey/valkey:9.1-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start valkey: %v", err)
	}

	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate valkey: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}

	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", host, port.Port())})
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// countingPostureStore records how many times its pod was asked for
// posture. Reads are the observable proxy for "this pod ran the
// handler" — cheaper and less flaky than inspecting gauges, which are
// process-global and shared by both pods in-process.
type countingPostureStore struct {
	store.Store

	reads atomic.Int32
}

func (s *countingPostureStore) Posture(context.Context) (*store.Posture, error) {
	s.reads.Add(1)

	return &store.Posture{
		Tracked: []store.OrgCount{{Org: "acme", Count: 1}},
	}, nil
}

// TestPostureExport_OnlyTheLeaderRuns is the posture half of the
// leader-gating requirement.
//
// TestLeaderElection_TwoPods already proves the Valkey scheduler
// serialises an arbitrary handler. What it cannot prove is that the
// posture handler is *compatible* with that: a handler that read the
// store outside the scheduled callback, or kicked off its own ticker,
// would pass the generic test and still have every pod hammering
// Postgres and publishing competing series.
//
// Two exporters, two stores, one Valkey. Only one store should be
// read, because only one pod should hold the lock.
func TestPostureExport_OnlyTheLeaderRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := startValkeyForPosture(ctx, t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	prefix := "test:" + t.Name() + ":"

	storeA, storeB := &countingPostureStore{}, &countingPostureStore{}

	schedule := func(pod string, st store.Store) *valkey.Scheduler {
		t.Helper()

		sched := valkey.New(client, valkey.Options{
			PodID:         pod,
			LockKeyPrefix: prefix,
			LockTTL:       2 * time.Second,
			Logger:        logger,
		})

		exporter := checker.NewPostureExporter(checker.PostureExporterOptions{Store: st, Logger: logger})

		if err := sched.Schedule(ctx, "posture-export", 200*time.Millisecond, exporter.Export); err != nil {
			t.Fatalf("schedule %s: %v", pod, err)
		}

		return sched
	}

	a := schedule("pod-a", storeA)
	b := schedule("pod-b", storeB)

	defer func() { _ = a.Stop() }()
	defer func() { _ = b.Stop() }()

	// ~7 ticks at 200ms. LockTTL of 2s exceeds the interval, so
	// whichever pod captures the lock keeps it for the run.
	time.Sleep(1500 * time.Millisecond)

	readsA, readsB := int(storeA.reads.Load()), int(storeB.reads.Load())

	total := readsA + readsB
	if total == 0 {
		t.Fatal("neither pod exported; the handler never ran at all")
	}

	// Ungated, both pods would fire on every tick — 14 reads. Any
	// count near the single-pod rate means the lock held.
	if total > 5 {
		t.Errorf("posture reads = %d across both pods (a=%d, b=%d), want <= 5; "+
			"the exporter is running on non-leaders", total, readsA, readsB)
	}

	if readsA > 0 && readsB > 0 {
		t.Errorf("both pods exported (a=%d, b=%d); with LockTTL > interval the lock should not have changed hands",
			readsA, readsB)
	}
}

// TestPostureExport_GaugesEqualSQLTruth closes the loop the unit tests
// leave open.
//
// The unit tests use a fake store, so they prove only that the
// exporter publishes whatever it is handed — true by construction.
// This one seeds real rows, runs the real query through the real
// exporter, and compares the published gauges against counts computed
// independently in the test. If the SQL and the gauge ever disagree,
// every compliance number on every dashboard is wrong, and nothing
// else in the suite would notice.
func TestPostureExport_GaugesEqualSQLTruth(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgresForPosture(ctx, t)
	st := newStoreForPosture(ctx, t, dsn)

	metrics.ResetPosture()

	ts := time.Now().UTC()

	// acme: 3 repos, codeowners actionable on 2, dependabot on 1.
	// globex: 1 repo, nothing actionable.
	// acme/parked: actionable but access-denied, so excluded entirely.
	seed := func(org, repo string, rules map[string]bool) {
		t.Helper()

		if err := st.UpdateRepoState(ctx, &store.RepoState{
			InstallationID: 1, Owner: org, Repo: repo,
			LastCheckedAt: &ts, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
		}); err != nil {
			t.Fatalf("seed repo_state %s/%s: %v", org, repo, err)
		}

		states := make([]store.RuleState, 0, len(rules))
		for name, actionable := range rules {
			states = append(states, store.RuleState{
				InstallationID: 1, Owner: org, Repo: repo,
				RuleName: name, RuleKind: "file",
				Actionable: actionable, PolicyVersion: "v1",
			})
		}

		if err := st.UpsertRuleStates(ctx, 1, org, repo, states); err != nil {
			t.Fatalf("seed rule_state %s/%s: %v", org, repo, err)
		}
	}

	seed("acme", "one", map[string]bool{"codeowners": true, "dependabot": true})
	seed("acme", "two", map[string]bool{"codeowners": true, "dependabot": false})
	seed("acme", "three", map[string]bool{"codeowners": false, "dependabot": false})
	seed("globex", "solo", map[string]bool{"codeowners": false})
	seed("acme", "parked", map[string]bool{"codeowners": true, "dependabot": true})

	if err := st.UpdateRepoState(ctx, &store.RepoState{
		InstallationID: 1, Owner: "acme", Repo: "parked",
		LastCheckedAt: &ts, LastCheckStatus: store.StatusError,
		LastError: "repository not accessible to installation 1: 404", PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("park write-back: %v", err)
	}

	if err := st.Deactivate(ctx, 1, "acme", "parked"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	exporter := checker.NewPostureExporter(checker.PostureExporterOptions{
		Store:  st,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})

	if err := exporter.Export(ctx); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Counted BEFORE any value assertion. GaugeVec.WithLabelValues
	// INSTANTIATES the child at zero as a side effect, so reading an
	// absent series to prove it is absent creates it — and a series
	// count taken afterwards measures the test's own footprint rather
	// than the exporter's output. This bit here: the count read 3
	// because asserting globex/codeowners == 0 had just conjured it.
	if n := testutil.CollectAndCount(metrics.ReposActionable); n != 2 {
		t.Errorf("repos_actionable series = %d, want 2; the aggregate only emits rules with a failing repo", n)
	}

	for _, tc := range []struct {
		rule, org string
		want      float64
		why       string
	}{
		{"codeowners", "acme", 2, "one and two, not the parked repo"},
		{"dependabot", "acme", 1, "one only"},
		{"codeowners", "globex", 0, "solo is compliant, so the GROUP BY emits no row"},
	} {
		got := testutil.ToFloat64(metrics.ReposActionable.WithLabelValues(tc.rule, tc.org))
		if got != tc.want {
			t.Errorf("repos_actionable{rule_name=%s, org=%s} = %v, want %v (%s)", tc.rule, tc.org, got, tc.want, tc.why)
		}
	}

	if got := testutil.ToFloat64(metrics.ReposTracked.WithLabelValues("acme")); got != 3 {
		t.Errorf("repos_tracked{org=acme} = %v, want 3 (parked repo excluded from the denominator)", got)
	}

	if got := testutil.ToFloat64(metrics.ReposTracked.WithLabelValues("globex")); got != 1 {
		t.Errorf("repos_tracked{org=globex} = %v, want 1", got)
	}

	if got := testutil.ToFloat64(metrics.ReposUnmeasurable.WithLabelValues("acme", store.ReasonAccessDenied)); got != 1 {
		t.Errorf("repos_unmeasurable{org=acme, reason=access_denied} = %v, want 1", got)
	}
}
