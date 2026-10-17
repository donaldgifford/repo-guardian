package activities

import (
	"context"
	"log/slog"
	"testing"
	"time"

	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// installLister lists fixed installations.
type installLister struct {
	fakeLister

	installs []*ghclient.Installation
}

func (l *installLister) ListInstallations(context.Context) ([]*ghclient.Installation, error) {
	return l.installs, nil
}

// fakeControlsStore records installations and batches and answers
// UpsertRepositories from resolved.
type fakeControlsStore struct {
	installs  []store.AppInstallation
	batches   [][]store.DiscoveredRepository
	resolved  []store.ResolvedRepository
	active    []store.RepositoryRef
	resolves  []int64
	snapshots []time.Time
}

func (s *fakeControlsStore) UpsertInstallation(_ context.Context, in *store.AppInstallation) error {
	s.installs = append(s.installs, *in)

	return nil
}

func (s *fakeControlsStore) UpsertRepositories(
	_ context.Context, repos []store.DiscoveredRepository, _ store.Resolver,
) ([]store.ResolvedRepository, error) {
	s.batches = append(s.batches, repos)

	return s.resolved, nil
}

func (*fakeControlsStore) ParkMissing(context.Context, int64, []int64) (int, error) { return 0, nil }

func (*fakeControlsStore) RecordServiceRun(context.Context, *store.ServiceRun) error { return nil }

func (s *fakeControlsStore) Resolve(_ context.Context, repoID int64, _ store.Resolver) (store.ResolutionChange, error) {
	s.resolves = append(s.resolves, repoID)

	return store.ResolutionChange{}, nil
}

func (s *fakeControlsStore) ActiveRepositories(_ context.Context, afterID int64, limit int) ([]store.RepositoryRef, error) {
	var out []store.RepositoryRef

	for _, r := range s.active {
		if r.ID > afterID && len(out) < limit {
			out = append(out, r)
		}
	}

	return out, nil
}

func (*fakeControlsStore) CompletePolicyRollout(context.Context, string) error { return nil }

func (s *fakeControlsStore) InsertComplianceSnapshot(_ context.Context, at time.Time) (int, error) {
	s.snapshots = append(s.snapshots, at)

	return 3, nil
}

// versionResolver is a store.Resolver that only names its version.
type versionResolver struct{}

func (versionResolver) Resolve(policy.Repository, policy.AppAccess) policy.Resolution {
	return policy.Resolution{}
}

func (versionResolver) Version() string { return "controls:test" }

func newControlsServices(t *testing.T, st *fakeControlsStore, gh Lister) (*ControlsServices, *recordingTemporal) {
	t.Helper()

	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}

	return NewControlsServices(&ControlsServicesConfig{
		Store: st, GitHub: gh, Policy: versionResolver{}, Temporal: tc, TaskQueue: "repo-guardian-eval",
		Interval: 24 * time.Hour, Logger: slog.New(slog.DiscardHandler),
	}), tc
}

func TestControlsServices_ListInstallationsRecordsAccountFacts(t *testing.T) {
	t.Parallel()

	st := &fakeControlsStore{}
	s, _ := newControlsServices(t, st, &installLister{installs: []*ghclient.Installation{
		{ID: 1, Account: "acme", RepositorySelection: "all"},
		{ID: 2, Account: "octocat", Personal: true, RepositorySelection: "selected"},
	}})

	refs, err := s.ListInstallations(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) != 2 || len(st.installs) != 2 {
		t.Fatalf("refs %d, installation rows %d; want 2 and 2", len(refs), len(st.installs))
	}

	org, user := st.installs[0], st.installs[1]
	if org.App != store.AppEval || org.AccountType != control.AccountOrganization || user.AccountType != control.AccountUser ||
		user.RepositorySelection != "selected" {
		t.Errorf("installation rows = %+v, want eval rows with account type and selection", st.installs)
	}
}

func TestControlsServices_ListRepositoriesKeepsArchivedAndForks(t *testing.T) {
	t.Parallel()

	s, _ := newControlsServices(t, &fakeControlsStore{}, &fakeLister{repos: []*ghclient.Repository{
		{ID: 1, Owner: "acme", Name: "old", Archived: true},
		{ID: 2, Owner: "acme", Name: "copy", Fork: true},
	}})

	got, err := s.ListRepositories(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || !got[0].Archived || !got[1].Fork {
		t.Errorf("ListRepositories() = %+v, want both kept with their flags for resolution to park", got)
	}
}

func TestControlsServices_UpsertStartsNewManagedRepositories(t *testing.T) {
	t.Parallel()

	st := &fakeControlsStore{resolved: []store.ResolvedRepository{
		{ID: 10, Created: true, State: policy.StateManaged},
		{ID: 11, State: policy.StateManaged},
		{ID: 12, Unparked: true, State: policy.StateManaged},
		{ID: 13, Created: true, State: policy.StateParked},
	}}
	s, tc := newControlsServices(t, st, &fakeLister{})

	res, err := s.UpsertRepositories(t.Context(), &workflows.ControlsUpsertInput{
		InstallationID: 7, Repos: []workflows.DiscoveredRepo{{ID: 100, Org: "acme", Name: "a", Archived: true}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Started != 2 || len(res.IDs) != 4 {
		t.Fatalf("UpsertRepositories() = %+v, want 4 ids and 2 started", res)
	}

	if b := st.batches[0][0]; b.ProviderRepoID != 100 || b.InstallationID != 7 || !b.Archived {
		t.Errorf("batch = %+v, want the provider id, installation and archived flag", b)
	}

	for i, want := range []string{"evaluation/10", "evaluation/12"} {
		sig := tc.signals[i]
		pc, _ := sig.arg.(workflows.PolicyChanged)

		if sig.workflowID != want || sig.name != workflows.PolicyChangedSignal || sig.queue != "repo-guardian-eval" || pc.Version != "controls:test" {
			t.Errorf("signal %d = %+v, want policy_changed SignalWithStart of %s on the eval queue", i, sig, want)
		}
	}
}

func TestControlsServices_RolloutPasses(t *testing.T) {
	t.Parallel()

	st := &fakeControlsStore{active: []store.RepositoryRef{
		{ID: 1, InstallationID: 7, PolicyState: policy.StateManaged},
		{ID: 2, InstallationID: 7, PolicyState: policy.StateExcluded},
		{ID: 3, PolicyState: policy.StateManaged},
	}}
	s, tc := newControlsServices(t, st, &fakeLister{})
	by := time.Unix(1_790_000_000, 0)

	if _, err := s.ResolveAll(t.Context(), &workflows.ControlsPageInput{Version: "controls:old", Limit: 10}); err == nil {
		t.Error("ResolveAll under another policy version = nil error, want a refusal")
	}

	res, err := s.ResolveAll(t.Context(), &workflows.ControlsPageInput{Version: "controls:test", Limit: 2})
	if err != nil || res.Count != 2 || res.Done || res.NextAfterID != 2 || len(st.resolves) != 2 {
		t.Fatalf("ResolveAll(page 1) = %+v, %v; want 2 resolved, not done, next after 2", res, err)
	}

	res, err = s.SignalAll(t.Context(), &workflows.ControlsPageInput{Version: "controls:test", By: by, Limit: 10})
	if err != nil || res.Count != 1 || !res.Done {
		t.Fatalf("SignalAll() = %+v, %v; want 1 signalled on the last page", res, err)
	}

	pc, _ := tc.signals[0].arg.(workflows.PolicyChanged)
	if len(tc.signals) != 1 || tc.signals[0].workflowID != "evaluation/1" || !pc.By.Equal(by) ||
		tc.signals[0].priority != int(workflows.PriorityRollout) {
		t.Errorf("signals = %+v, want only the managed, reachable repository at rollout priority by the deadline", tc.signals)
	}
}

func TestControlsServices_SnapshotStampsTheRun(t *testing.T) {
	t.Parallel()

	st := &fakeControlsStore{}
	s, _ := newControlsServices(t, st, &fakeLister{})
	at := time.Unix(1_790_000_000, 0)

	if n, err := s.Snapshot(t.Context(), at); err != nil || n != 3 || len(st.snapshots) != 1 || !st.snapshots[0].Equal(at) {
		t.Errorf("Snapshot() = %d, %v (stamps %v); want 3 rows stamped at the workflow's start", n, err, st.snapshots)
	}
}
