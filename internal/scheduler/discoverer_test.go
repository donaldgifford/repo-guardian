package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// fakeDiscoveryStore implements store.Store with just enough surface
// for Discoverer tests — UpsertIfMissing is the only method exercised;
// the other interface methods are no-op stubs.
type fakeDiscoveryStore struct {
	mu       sync.Mutex
	upserted []*store.RepoState
	keys     map[string]bool
	err      error
}

func newFakeDiscoveryStore() *fakeDiscoveryStore {
	return &fakeDiscoveryStore{keys: make(map[string]bool)}
}

func discoveryKey(installationID int64, owner, repo string) string {
	return strconv.FormatInt(installationID, 10) + "|" + owner + "|" + repo
}

func (f *fakeDiscoveryStore) UpsertIfMissing(_ context.Context, s *store.RepoState) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return false, f.err
	}

	key := discoveryKey(s.InstallationID, s.Owner, s.Repo)
	if f.keys[key] {
		return false, nil
	}

	cp := *s
	f.upserted = append(f.upserted, &cp)
	f.keys[key] = true

	return true, nil
}

func (*fakeDiscoveryStore) GetRepoState(_ context.Context, _ int64, _, _ string) (*store.RepoState, error) {
	return nil, store.ErrNotFound
}

func (*fakeDiscoveryStore) UpdateRepoState(_ context.Context, _ *store.RepoState) error {
	return nil
}

func (*fakeDiscoveryStore) StaleRepos(_ context.Context, _ time.Duration, _ string, _ int) ([]store.RepoState, error) {
	return nil, nil
}

// Discovery never writes rule states — it only seeds pending rows.
func (*fakeDiscoveryStore) UpsertRuleStates(
	_ context.Context, _ int64, _, _ string, _ []store.RuleState,
) error {
	return nil
}

func (*fakeDiscoveryStore) Close() error { return nil }

func (f *fakeDiscoveryStore) Upserted() []*store.RepoState {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*store.RepoState, len(f.upserted))
	copy(out, f.upserted)

	return out
}

func TestDiscoverer_UpsertsEveryReturnedRepo(t *testing.T) {
	// Cannot use t.Parallel() — shares the global RepoDiscoveredTotal
	// CounterVec with TestDiscoverer_Idempotent_NoDoubleCount.
	metrics.RepoDiscoveredTotal.Reset()

	client := newMockClient()
	client.installations = []*ghclient.Installation{
		{ID: 1, Account: "org1"},
		{ID: 2, Account: "org2"},
	}
	client.installRepos[1] = []*ghclient.Repository{
		{Owner: "org1", Name: "repo-a"},
		{Owner: "org1", Name: "repo-b"},
	}
	client.installRepos[2] = []*ghclient.Repository{
		{Owner: "org2", Name: "repo-c"},
	}

	st := newFakeDiscoveryStore()

	d := NewDiscoverer(DiscovererOptions{
		Client: client, Store: st, Logger: slog.Default(),
		Freshness: time.Hour,
	})

	if err := d.Discover(t.Context()); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	upserted := st.Upserted()
	if len(upserted) != 3 {
		t.Fatalf("upserted = %d, want 3 (2+1 repos)", len(upserted))
	}

	for _, rs := range upserted {
		if rs.LastCheckStatus != store.StatusPending {
			t.Errorf("LastCheckStatus = %q, want %q", rs.LastCheckStatus, store.StatusPending)
		}

		if rs.PolicyVersion != "" {
			t.Errorf("PolicyVersion = %q, want empty (treat as drifted)", rs.PolicyVersion)
		}

		if rs.LastCheckedAt == nil {
			t.Error("LastCheckedAt is nil")
			continue
		}

		// Jitter window: [-2*freshness, 0].
		age := time.Since(*rs.LastCheckedAt)
		if age < 0 || age > 2*time.Hour {
			t.Errorf("seed jitter outside [-2h, 0]: %v", age)
		}
	}

	if got := testutil.ToFloat64(metrics.RepoDiscoveredTotal.WithLabelValues("1")); got != 2 {
		t.Errorf("repo_discovered_total{inst=1} = %v, want 2", got)
	}

	if got := testutil.ToFloat64(metrics.RepoDiscoveredTotal.WithLabelValues("2")); got != 1 {
		t.Errorf("repo_discovered_total{inst=2} = %v, want 1", got)
	}
}

func TestDiscoverer_Idempotent_NoDoubleCount(t *testing.T) {
	// Cannot use t.Parallel() — shares the global RepoDiscoveredTotal
	// CounterVec with TestDiscoverer_UpsertsEveryReturnedRepo.
	metrics.RepoDiscoveredTotal.Reset()

	client := newMockClient()
	client.installations = []*ghclient.Installation{{ID: 1, Account: "org1"}}
	client.installRepos[1] = []*ghclient.Repository{{Owner: "org1", Name: "r1"}}

	st := newFakeDiscoveryStore()
	d := NewDiscoverer(DiscovererOptions{
		Client: client, Store: st, Logger: slog.Default(), Freshness: time.Hour,
	})

	for range 3 {
		if err := d.Discover(t.Context()); err != nil {
			t.Fatalf("Discover: %v", err)
		}
	}

	if got := testutil.ToFloat64(metrics.RepoDiscoveredTotal.WithLabelValues("1")); got != 1 {
		t.Errorf("repo_discovered_total = %v, want 1 (idempotent over 3 discover runs)", got)
	}

	if got := len(st.Upserted()); got != 1 {
		t.Errorf("len upserted = %d, want 1 (idempotent)", got)
	}
}

func TestDiscoverer_SkipsArchivedAndForked(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	client.installations = []*ghclient.Installation{{ID: 1, Account: "org1"}}
	client.installRepos[1] = []*ghclient.Repository{
		{Owner: "org1", Name: "active"},
		{Owner: "org1", Name: "archived", Archived: true},
		{Owner: "org1", Name: "forked", Fork: true},
	}

	st := newFakeDiscoveryStore()
	d := NewDiscoverer(DiscovererOptions{
		Client: client, Store: st, Logger: slog.Default(),
		Freshness: time.Hour, SkipArchived: true, SkipForks: true,
	})

	if err := d.Discover(t.Context()); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if got := len(st.Upserted()); got != 1 {
		t.Errorf("len upserted = %d, want 1 (archived+fork skipped)", got)
	}

	if st.upserted[0].Repo != "active" {
		t.Errorf("upserted repo = %q, want active", st.upserted[0].Repo)
	}
}

func TestDiscoverer_ListInstallationsError_NoEnqueue_NoError(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	client.listInstallErr = errors.New("API down")

	st := newFakeDiscoveryStore()
	d := NewDiscoverer(DiscovererOptions{
		Client: client, Store: st, Logger: slog.Default(), Freshness: time.Hour,
	})

	// Discover MUST NOT propagate the error — the next tick retries.
	if err := d.Discover(t.Context()); err != nil {
		t.Fatalf("Discover propagated upstream error: %v", err)
	}

	if got := len(st.Upserted()); got != 0 {
		t.Errorf("len upserted = %d, want 0 on listInstallations error", got)
	}
}

func TestDiscoverer_ListInstallationReposError_SkipsInstallation(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	client.installations = []*ghclient.Installation{
		{ID: 1, Account: "org1"},
		{ID: 2, Account: "org2"},
	}
	client.installRepos[2] = []*ghclient.Repository{{Owner: "org2", Name: "r1"}}
	client.listReposErr = errors.New("partial fail")

	st := newFakeDiscoveryStore()
	d := NewDiscoverer(DiscovererOptions{
		Client: client, Store: st, Logger: slog.Default(), Freshness: time.Hour,
	})

	if err := d.Discover(t.Context()); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// listReposErr is set globally on the mock; both installations
	// will fail their list — so 0 upserts.
	if got := len(st.Upserted()); got != 0 {
		t.Errorf("len upserted = %d, want 0", got)
	}
}

func TestDiscoverer_StoreError_FailSafe(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	client.installations = []*ghclient.Installation{{ID: 1, Account: "org1"}}
	client.installRepos[1] = []*ghclient.Repository{{Owner: "org1", Name: "r1"}}

	st := newFakeDiscoveryStore()
	st.err = errors.New("DB unavailable")

	d := NewDiscoverer(DiscovererOptions{
		Client: client, Store: st, Logger: slog.Default(), Freshness: time.Hour,
	})

	// Store errors MUST NOT halt the iteration.
	if err := d.Discover(t.Context()); err != nil {
		t.Fatalf("Discover propagated Store error: %v", err)
	}
}

func TestDiscoverer_ContextCancelled_ReturnsErr(t *testing.T) {
	t.Parallel()

	st := newFakeDiscoveryStore()
	d := NewDiscoverer(DiscovererOptions{
		Client: newMockClient(), Store: st, Logger: slog.Default(), Freshness: time.Hour,
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := d.Discover(ctx); err == nil {
		t.Error("expected ctx.Err(), got nil")
	}
}

// TestDiscoverer_SetsInstallationInfo pins the org↔installation join
// label (IMPL-0023 task 2.1). Every repo listing fails here, and the
// assertion that both installations still get a join series is the
// point: the rate-limit and discovery-error panels an operator opens
// *because* an installation is failing are the ones that need the org
// label most, so the emission must not sit behind the failing call.
func TestDiscoverer_SetsInstallationInfo(t *testing.T) {
	// Cannot use t.Parallel() — resets the global InstallationInfo gauge.
	metrics.InstallationInfo.Reset()

	client := newMockClient()
	client.installations = []*ghclient.Installation{
		{ID: 1, Account: "org1"},
		{ID: 2, Account: "org2"},
	}
	client.listReposErr = errors.New("rate limited")

	d := NewDiscoverer(DiscovererOptions{
		Client: client, Store: newFakeDiscoveryStore(),
		Logger: slog.Default(), Freshness: time.Hour,
	})

	if err := d.Discover(t.Context()); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	for _, want := range []struct {
		installationID string
		org            string
	}{
		{"1", "org1"},
		{"2", "org2"},
	} {
		got := testutil.ToFloat64(metrics.InstallationInfo.WithLabelValues(want.installationID, want.org))
		if got != 1 {
			t.Errorf("installation_info{installation_id=%q, org=%q} = %v, want 1",
				want.installationID, want.org, got)
		}
	}

	if n := testutil.CollectAndCount(metrics.InstallationInfo); n != 2 {
		t.Errorf("installation_info has %d series, want 2 (one per installation)", n)
	}
}

func (*fakeDiscoveryStore) Deactivate(context.Context, int64, string, string) error { return nil }

func (*fakeDiscoveryStore) ReportData(context.Context) (*store.ReportData, error) {
	return nil, errors.New("fakeDiscoveryStore: ReportData not used by these tests")
}

func (*fakeDiscoveryStore) InsertComplianceSnapshot(context.Context, time.Time) (int, error) {
	return 0, errors.New("fakeDiscoveryStore: InsertComplianceSnapshot not used by these tests")
}

func (*fakeDiscoveryStore) Posture(context.Context) (*store.Posture, error) {
	return nil, errors.New("fakeDiscoveryStore: Posture not used by these tests")
}
