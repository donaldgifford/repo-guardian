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
	installs []store.AppInstallation
	batches  [][]store.DiscoveredRepository
	resolved []store.ResolvedRepository
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
