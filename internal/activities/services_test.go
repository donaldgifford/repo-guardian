package activities

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	ghmocks "github.com/donaldgifford/repo-guardian/internal/github/mocks"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/mocks"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// fakeLister answers the discovery listings.
type fakeLister struct {
	repos []*ghclient.Repository
}

func (*fakeLister) ListInstallations(context.Context) ([]*ghclient.Installation, error) {
	return nil, nil
}

func (f *fakeLister) ListInstallationRepos(context.Context, int64) ([]*ghclient.Repository, error) {
	return f.repos, nil
}

func newServices(t *testing.T, cfg *ServicesConfig) (*Services, routeStore, *recordingTemporal) {
	t.Helper()

	st := routeStore{MockWriter: mocks.NewMockWriter(t), MockReader: mocks.NewMockReader(t)}
	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}

	cfg.Store, cfg.Temporal, cfg.TaskQueue = st, tc, "repo-guardian"
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 24 * time.Hour
	}

	if cfg.GitHub == nil {
		cfg.GitHub = &fakeLister{}
	}

	return NewServices(cfg), st, tc
}

// engineParks reports whether the check path parks repo: CheckRepo
// returns a durable skip. Any other path, including the engine going on
// to calls the bare mock cannot answer, is "not parked".
func engineParks(t *testing.T, skipArchived, skipForks bool, repo *ghclient.Repository) (parked bool) {
	t.Helper()

	cfg := policy.BuiltinDefaults()
	cfg.Guardian.SkipArchived, cfg.Guardian.SkipForks, cfg.Guardian.DryRun = skipArchived, skipForks, true

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatal(err)
	}

	eng, err := checker.NewEngine(cfg, ts, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}

	gh := &ghmocks.MockClient{}
	gh.EXPECT().GetRepository(mock.Anything, repo.Owner, repo.Name).Return(repo, nil)

	defer func() {
		if recover() != nil {
			parked = false
		}
	}()

	_, err = eng.CheckRepo(t.Context(), gh, repo.Owner, repo.Name)
	_, parked = checker.AsSkipped(err)

	return parked
}

// INV-0015's subset invariant: discovery is the only un-parker, so the
// repositories it filters must be exactly the ones the check path parks.
// Filtering fewer would un-park them every pass; filtering more would
// leave them unchecked.
func TestListRepositories_SubsetInvariant(t *testing.T) {
	t.Parallel()

	kinds := []*ghclient.Repository{
		{ID: 1, Owner: "acme", Name: "plain", HasBranch: true, DefaultRef: "main"},
		{ID: 2, Owner: "acme", Name: "archived", Archived: true, HasBranch: true, DefaultRef: "main"},
		{ID: 3, Owner: "acme", Name: "fork", Fork: true, HasBranch: true, DefaultRef: "main"},
		{ID: 4, Owner: "acme", Name: "both", Archived: true, Fork: true, HasBranch: true, DefaultRef: "main"},
	}

	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("archived=%v,forks=%v", flags[0], flags[1]), func(t *testing.T) {
			t.Parallel()

			s, _, _ := newServices(t, &ServicesConfig{SkipArchived: flags[0], SkipForks: flags[1], GitHub: &fakeLister{repos: kinds}})

			listed, err := s.ListRepositories(t.Context(), 7)
			if err != nil {
				t.Fatal(err)
			}

			kept := map[int64]bool{}
			for _, r := range listed {
				kept[r.ID] = true
			}

			for _, repo := range kinds {
				if parked := engineParks(t, flags[0], flags[1], repo); parked == kept[repo.ID] {
					t.Errorf("%s: check parks = %v, discovery keeps = %v; they must disagree exactly", repo.Name, parked, kept[repo.ID])
				}
			}
		})
	}
}

func TestUpsertRepositories_StartsOnlyNewOrReactivated(t *testing.T) {
	t.Parallel()

	s, st, tc := newServices(t, &ServicesConfig{PolicyVersion: "v2:abc"})

	answers := map[string]store.UpsertResult{
		"new": {ID: 1, Created: true}, "known": {ID: 2}, "back": {ID: 3, Reactivated: true},
	}
	st.MockWriter.EXPECT().UpsertDiscovered(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, d *store.DiscoveredRepo) (store.UpsertResult, error) {
			if d.InstallationID != 7 || d.NextDueAt.IsZero() {
				t.Errorf("upsert = %+v", d)
			}

			return answers[d.Name], nil
		})

	before := time.Now()

	res, err := s.UpsertRepositories(t.Context(), &workflows.UpsertRepositoriesInput{InstallationID: 7, Repos: []workflows.WebhookRepo{
		{ID: 11, Org: "acme", Name: "new"}, {ID: 12, Org: "acme", Name: "known"}, {ID: 13, Org: "acme", Name: "back"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.IDs) != 3 || res.Started != 2 {
		t.Errorf("result = %+v, want 3 ids and 2 started", res)
	}

	wantSignals(t, tc,
		signal{workflowID: "repo/1", name: workflows.PolicyChangedSignal, started: true},
		signal{workflowID: "repo/3", name: workflows.PolicyChangedSignal, started: true})

	for _, sig := range tc.signals {
		pc := sig.arg.(workflows.PolicyChanged)
		if pc.Version != "v2:abc" || pc.By.Before(before) || pc.By.After(before.Add(24*time.Hour+time.Minute)) {
			t.Errorf("policy_changed = %+v, want by within one check interval", pc)
		}
	}
}

func TestParkMissing_ParksOnlyTheInstallationsUnseen(t *testing.T) {
	t.Parallel()

	s, st, tc := newServices(t, &ServicesConfig{})
	st.MockReader.EXPECT().ListActiveRepositories(mock.Anything, int64(0), 500).Return([]store.Repository{
		{ID: 1, InstallationID: 7}, {ID: 2, InstallationID: 7}, {ID: 3, InstallationID: 8}, {ID: 4, InstallationID: 7},
	}, nil)

	n, err := s.ParkMissing(t.Context(), &workflows.ParkMissingInput{InstallationID: 7, Seen: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}

	if n != 2 {
		t.Errorf("parked = %d, want 2", n)
	}

	wantSignals(t, tc, signal{workflowID: "repo/2", name: workflows.ParkSignal}, signal{workflowID: "repo/4", name: workflows.ParkSignal})
}

func TestSignalRepositories_RolloutBootstrapAndStragglers(t *testing.T) {
	t.Parallel()

	due := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	by := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	page := []store.Repository{
		{ID: 5, InstallationID: 7, PolicyVersion: "v1:old", NextDueAt: &due},
		{ID: 6, InstallationID: 7, PolicyVersion: "v2:new"},
	}

	tests := []struct {
		name        string
		in          workflows.SignalRepositoriesInput
		wantIDs     []string
		wantVersion string
		wantBy      time.Time
	}{
		{"rollout", workflows.SignalRepositoriesInput{Limit: 2, Version: "v2:new", By: by}, []string{"repo/5", "repo/6"}, "v2:new", by},
		{"stragglers", workflows.SignalRepositoriesInput{Limit: 2, Version: "v2:new", By: by, DriftedOnly: true}, []string{"repo/5"}, "v2:new", by},
		{"bootstrap", workflows.SignalRepositoriesInput{Limit: 2, Bootstrap: true}, []string{"repo/5", "repo/6"}, "v1:old", due},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, st, tc := newServices(t, &ServicesConfig{})
			st.MockReader.EXPECT().ListActiveRepositories(mock.Anything, int64(0), 2).Return(page, nil)

			res, err := s.SignalRepositories(t.Context(), &tt.in)
			if err != nil {
				t.Fatal(err)
			}

			if res.Done || res.NextAfterID != 6 || res.Signalled != len(tt.wantIDs) {
				t.Errorf("result = %+v", res)
			}

			want := make([]signal, len(tt.wantIDs))
			for j, id := range tt.wantIDs {
				want[j] = signal{workflowID: id, name: workflows.PolicyChangedSignal, started: true, priority: int(workflows.PriorityRollout)}
			}

			wantSignals(t, tc, want...)

			if pc := tc.signals[0].arg.(workflows.PolicyChanged); pc.Version != tt.wantVersion || !pc.By.Equal(tt.wantBy) {
				t.Errorf("first policy_changed = %+v, want %s by %v", pc, tt.wantVersion, tt.wantBy)
			}
		})
	}
}

func TestSignalRepositories_ShortPageIsDone(t *testing.T) {
	t.Parallel()

	s, st, _ := newServices(t, &ServicesConfig{})
	st.MockReader.EXPECT().ListActiveRepositories(mock.Anything, int64(40), 500).Return(nil, nil)

	res, err := s.SignalRepositories(t.Context(), &workflows.SignalRepositoriesInput{AfterID: 40, Limit: 500})
	if err != nil || !res.Done {
		t.Errorf("res = %+v, err = %v; want done", res, err)
	}
}
