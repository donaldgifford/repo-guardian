package activities

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// routeFakeStore is a stateful controls store: repositories by provider
// id, an upsert that creates or matches them, and a log of writes.
type routeFakeStore struct {
	byProvider map[int64]*store.RepositoryRef
	org        []int64
	change     store.ResolutionChange

	upserted [][]store.DiscoveredRepository
	installs []store.AppInstallation
	parked   []string
	resolved []int64
}

func (s *routeFakeStore) UpsertInstallation(_ context.Context, in *store.AppInstallation) error {
	s.installs = append(s.installs, *in)

	return nil
}

func (s *routeFakeStore) UpsertRepositories(
	_ context.Context, repos []store.DiscoveredRepository, _ store.Resolver,
) ([]store.ResolvedRepository, error) {
	s.upserted = append(s.upserted, repos)

	out := make([]store.ResolvedRepository, 0, len(repos))

	for _, d := range repos {
		ref, ok := s.byProvider[d.ProviderRepoID]
		if !ok {
			ref = &store.RepositoryRef{ID: 100 + d.ProviderRepoID, Active: true, InstallationID: d.InstallationID, PolicyState: policy.StateManaged}
			s.byProvider[d.ProviderRepoID] = ref
		}

		state := policy.StateManaged
		if d.Archived || d.Fork {
			state = policy.StateParked
		}

		out = append(out, store.ResolvedRepository{ID: ref.ID, Created: !ok, State: state})
	}

	return out, nil
}

func (s *routeFakeStore) FindRepository(_ context.Context, id int64) (*store.RepositoryRef, error) {
	return s.byProvider[id], nil
}

func (s *routeFakeStore) RepositoryRef(_ context.Context, id int64) (*store.RepositoryRef, error) {
	for _, ref := range s.byProvider {
		if ref.ID == id {
			return ref, nil
		}
	}

	return nil, fmt.Errorf("repository %d not found", id)
}

func (s *routeFakeStore) OrgRepositories(context.Context, string) ([]int64, error) { return s.org, nil }

func (s *routeFakeStore) Resolve(_ context.Context, id int64, _ store.Resolver) (store.ResolutionChange, error) {
	s.resolved = append(s.resolved, id)

	return s.change, nil
}

func (s *routeFakeStore) Park(_ context.Context, id int64, reason string) error {
	s.parked = append(s.parked, fmt.Sprintf("%d:%s", id, reason))

	return nil
}

func (s *routeFakeStore) ParkInstallation(_ context.Context, id int64, reason string) (int, error) {
	s.parked = append(s.parked, fmt.Sprintf("installation %d:%s", id, reason))

	return 1, nil
}

func newControlsRouter(t *testing.T) (*ControlsRouter, *routeFakeStore, *recordingTemporal) {
	t.Helper()

	st := &routeFakeStore{byProvider: map[int64]*store.RepositoryRef{
		9:  {ID: 42, Active: true, InstallationID: 7, PolicyState: policy.StateManaged},
		10: {ID: 43, Active: false, InstallationID: 7, PolicyState: policy.StateParked},
		11: {ID: 44, Active: true, InstallationID: 7, PolicyState: policy.StateExcluded},
	}}
	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}

	return NewControlsRouter(st, versionResolver{}, tc, "repo-guardian-eval", 24*time.Hour, slog.New(slog.DiscardHandler)), st, tc
}

func evalWebhook(event, action string, repos ...workflows.WebhookRepo) *workflows.WebhookInput {
	in := webhook(event, action, repos...)
	in.App = workflows.AppEval

	return in
}

func TestControlsRouter_PushRechecksWithTheChangeSet(t *testing.T) {
	t.Parallel()

	r, _, tc := newControlsRouter(t)
	in := evalWebhook("push", "", widgets)
	in.Paths = []string{".github/CODEOWNERS"}

	if err := r.RouteWebhook(t.Context(), in); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc, signal{workflowID: "evaluation/42", name: workflows.RecheckSignal, started: true, priority: int(workflows.PriorityWebhook)})

	if got := recheckOf(t, &tc.signals[0]); got.Trigger != workflows.TriggerPush || !slices.Equal(got.Paths, in.Paths) || got.Unknown {
		t.Errorf("recheck = %+v, want a push recheck of the changed paths", got)
	}

	if tc.signals[0].queue != "repo-guardian-eval" {
		t.Errorf("queue = %q, want the evaluation queue", tc.signals[0].queue)
	}
}

func TestControlsRouter_PushLeavesParkedAndUnmanagedAlone(t *testing.T) {
	t.Parallel()

	r, st, tc := newControlsRouter(t)

	for _, repo := range []workflows.WebhookRepo{{ID: 10, Org: "acme", Name: "parked"}, {ID: 11, Org: "acme", Name: "excluded"}} {
		if err := r.RouteWebhook(t.Context(), evalWebhook("push", "", repo)); err != nil {
			t.Fatal(err)
		}
	}

	wantSignals(t, tc)

	if len(st.upserted) != 0 {
		t.Errorf("upserts = %+v, want none: a push never un-parks", st.upserted)
	}
}

func TestControlsRouter_PushDiscoversAnUnknownRepository(t *testing.T) {
	t.Parallel()

	r, st, tc := newControlsRouter(t)

	if err := r.RouteWebhook(t.Context(), evalWebhook("push", "", workflows.WebhookRepo{ID: 12, Org: "acme", Name: "new"})); err != nil {
		t.Fatal(err)
	}

	if len(st.upserted) != 1 || st.upserted[0][0].InstallationID != 7 {
		t.Fatalf("upserts = %+v, want the unknown repository discovered under installation 7", st.upserted)
	}

	wantSignals(t, tc, signal{workflowID: "evaluation/112", name: workflows.RecheckSignal, started: true})

	if got := recheckOf(t, &tc.signals[0]); !got.Unknown {
		t.Errorf("recheck = %+v, want a full evaluation of a new repository", got)
	}
}

func TestControlsRouter_RepositoryEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action      string
		repo        workflows.WebhookRepo
		wantSignals int
		wantParked  []string
	}{
		{action: "created", repo: workflows.WebhookRepo{ID: 12, Org: "acme", Name: "new"}, wantSignals: 1},
		{action: "renamed", repo: workflows.WebhookRepo{ID: 9, Org: "acme", Name: "gadgets"}, wantSignals: 1},
		{action: "archived", repo: workflows.WebhookRepo{ID: 9, Org: "acme", Name: "widgets", Archived: true}},
		{action: "unarchived", repo: workflows.WebhookRepo{ID: 9, Org: "acme", Name: "widgets"}, wantSignals: 1},
		{action: "deleted", repo: widgets, wantParked: []string{"42:removed"}},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()

			r, st, tc := newControlsRouter(t)

			if err := r.RouteWebhook(t.Context(), evalWebhook("repository", tt.action, tt.repo)); err != nil {
				t.Fatal(err)
			}

			if len(tc.signals) != tt.wantSignals || !slices.Equal(st.parked, tt.wantParked) {
				t.Errorf("signals %+v, parked %v; want %d signals, parked %v", tc.signals, st.parked, tt.wantSignals, tt.wantParked)
			}

			if tt.action == "archived" && (len(st.upserted) != 1 || !st.upserted[0][0].Archived) {
				t.Errorf("upserts = %+v, want the archived flag passed to the store, which parks", st.upserted)
			}
		})
	}
}

func TestControlsRouter_InstallationEvents(t *testing.T) {
	t.Parallel()

	r, st, tc := newControlsRouter(t)

	created := evalWebhook("installation", "created")
	created.AccountType, created.RepositorySelection = control.AccountUser, "selected"

	if err := r.RouteWebhook(t.Context(), created); err != nil {
		t.Fatal(err)
	}

	if len(st.installs) != 1 || st.installs[0].AccountType != control.AccountUser || st.installs[0].App != store.AppEval {
		t.Errorf("installations = %+v, want the eval row on a user account", st.installs)
	}

	wantSignals(t, tc, signal{workflowID: "controls-discovery/installation/7/d1", name: workflows.ControlsDiscoveryWorkflowName, started: true})

	if err := r.RouteWebhook(t.Context(), evalWebhook("installation", "deleted")); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(st.parked, []string{"installation 7:installation_removed"}) {
		t.Errorf("parked = %v, want the installation's repositories parked installation_removed", st.parked)
	}

	added := evalWebhook("installation_repositories", "added", workflows.WebhookRepo{ID: 12, Org: "acme", Name: "new"})
	added.RepositorySelection = selectionAll

	if err := r.RouteWebhook(t.Context(), added); err != nil {
		t.Fatal(err)
	}

	if len(st.upserted) != 1 {
		t.Errorf("upserts = %+v, want a small all-repositories addition discovered from its payload", st.upserted)
	}
}

func TestControlsRouter_RemediationAccessReResolves(t *testing.T) {
	t.Parallel()

	r, st, tc := newControlsRouter(t)
	st.change = store.ResolutionChange{Changed: []string{"codeowners@1"}}

	if err := r.RecordAppAccess(t.Context(), &AppAccess{
		App: workflows.AppRemediate, InstallationID: 70, Event: "installation_repositories", Action: "added",
		Repositories: []workflows.WebhookRepo{widgets, {ID: 10, Name: "parked"}},
	}); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(st.resolved, []int64{42}) {
		t.Errorf("resolved = %v, want the one active repository named", st.resolved)
	}

	wantSignals(t, tc, signal{workflowID: "evaluation/42", name: workflows.PolicyChangedSignal, started: true})

	st.org, st.resolved, st.change = []int64{42}, nil, store.ResolutionChange{}

	created := &AppAccess{App: workflows.AppRemediate, Event: "installation", Action: "created", AccountLogin: "acme"}
	if err := r.RecordAppAccess(t.Context(), created); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(st.resolved, []int64{42}) || len(tc.signals) != 1 {
		t.Errorf("resolved %v, signals %d; want the account's repositories resolved and no signal without a change", st.resolved, len(tc.signals))
	}
}

// TestControlsRouter_PullRequestEventObserves is IMPL-0030 2.3: a
// remediation PR event rechecks a managed repository with pr_event at
// webhook priority, and never discovers or un-parks.
func TestControlsRouter_PullRequestEventObserves(t *testing.T) {
	t.Parallel()

	r, st, tc := newControlsRouter(t)

	for _, repo := range []workflows.WebhookRepo{widgets, {ID: 10, Org: "acme", Name: "parked"}, {ID: 12, Org: "acme", Name: "new"}} {
		if err := r.RouteWebhook(t.Context(), evalWebhook("pull_request", "closed", repo)); err != nil {
			t.Fatal(err)
		}
	}

	wantSignals(t, tc, signal{workflowID: "evaluation/42", name: workflows.RecheckSignal, started: true, priority: int(workflows.PriorityWebhook)})

	if got := recheckOf(t, &tc.signals[0]); got.Trigger != workflows.TriggerPREvent || len(got.Paths) != 0 || got.Unknown {
		t.Errorf("recheck = %+v, want a bare pr_event", got)
	}

	if len(st.upserted) != 0 {
		t.Errorf("upserts = %+v, want none: a PR event never discovers", st.upserted)
	}
}
