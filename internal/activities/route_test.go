package activities

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/mocks"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// routeStore joins the generated Writer and Reader mocks.
type routeStore struct {
	*mocks.MockWriter
	*mocks.MockReader
}

// signal is one signal the router sent.
type signal struct {
	workflowID string
	name       string
	arg        any
	started    bool
	priority   int
	queue      string
}

// recordingTemporal records signals; the embedded mock panics on
// anything else. signalErr answers SignalWorkflow.
type recordingTemporal struct {
	*temporalmocks.Client

	mu        sync.Mutex
	signals   []signal
	signalErr error
}

func (c *recordingTemporal) SignalWithStartWorkflow(
	_ context.Context, id, name string, arg any, o client.StartWorkflowOptions, //nolint:gocritic // matches client.Client
	_ any, _ ...any,
) (client.WorkflowRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.signals = append(c.signals, signal{workflowID: id, name: name, arg: arg, started: true, priority: o.Priority.PriorityKey, queue: o.TaskQueue})

	return &temporalmocks.WorkflowRun{}, nil
}

// ExecuteWorkflow records a start as a signal-less entry named after the
// workflow type.
//
//nolint:gocritic // hugeParam: the signature is client.Client's.
func (c *recordingTemporal) ExecuteWorkflow(_ context.Context, o client.StartWorkflowOptions, wf any, args ...any) (client.WorkflowRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.signals = append(c.signals, signal{workflowID: o.ID, name: wf.(string), arg: args[0], started: true, priority: o.Priority.PriorityKey})

	return &temporalmocks.WorkflowRun{}, nil
}

func (c *recordingTemporal) SignalWorkflow(_ context.Context, id, _, name string, arg any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.signals = append(c.signals, signal{workflowID: id, name: name, arg: arg})

	return c.signalErr
}

func newRouter(t *testing.T) (*Router, routeStore, *recordingTemporal) {
	t.Helper()

	st := routeStore{MockWriter: mocks.NewMockWriter(t), MockReader: mocks.NewMockReader(t)}
	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}

	return NewRouter(st, tc, "repo-guardian", 24*time.Hour, 0.1, slog.New(slog.NewTextHandler(io.Discard, nil))), st, tc
}

var widgets = workflows.WebhookRepo{ID: 9, Org: "acme", Name: "widgets"}

func webhook(event, action string, repos ...workflows.WebhookRepo) *workflows.WebhookInput {
	return &workflows.WebhookInput{DeliveryID: "d1", Event: event, Action: action, InstallationID: 7, AccountLogin: "acme", Repositories: repos}
}

func wantSignals(t *testing.T, tc *recordingTemporal, want ...signal) {
	t.Helper()

	if len(tc.signals) != len(want) {
		t.Fatalf("signals = %+v, want %+v", tc.signals, want)
	}

	for i := range want {
		got := tc.signals[i]
		if got.workflowID != want[i].workflowID || got.name != want[i].name || got.started != want[i].started ||
			(want[i].priority != 0 && got.priority != want[i].priority) {
			t.Errorf("signal %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func recheckOf(t *testing.T, s *signal) workflows.Recheck {
	t.Helper()

	r, ok := s.arg.(workflows.Recheck)
	if !ok {
		t.Fatalf("signal arg = %T, want Recheck", s.arg)
	}

	return r
}

func TestRouteWebhook_PushRechecksKnownActiveRepo(t *testing.T) {
	r, st, tc := newRouter(t)
	st.MockReader.EXPECT().FindRepository(mock.Anything, "acme", "widgets", mock.Anything).
		Return(&store.Repository{ID: 42, InstallationID: 7, Active: true}, nil)

	if err := r.RouteWebhook(t.Context(), webhook("push", "", widgets)); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc, signal{workflowID: "repo/42", name: workflows.RecheckSignal, started: true, priority: int(workflows.PriorityWebhook)})

	if got := recheckOf(t, &tc.signals[0]); got.Trigger != workflows.TriggerPush {
		t.Errorf("trigger = %q", got.Trigger)
	}
}

func TestRouteWebhook_PushLeavesParkedRepoParked(t *testing.T) {
	r, st, tc := newRouter(t)
	st.MockReader.EXPECT().FindRepository(mock.Anything, "acme", "widgets", mock.Anything).
		Return(&store.Repository{ID: 42, Active: false}, nil)

	if err := r.RouteWebhook(t.Context(), webhook("push", "", widgets)); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc)
}

func TestRouteWebhook_PushDiscoversUnknownRepo(t *testing.T) {
	r, st, tc := newRouter(t)
	st.MockReader.EXPECT().FindRepository(mock.Anything, "acme", "widgets", mock.Anything).Return(nil, store.ErrNotFound)
	st.MockWriter.EXPECT().UpsertDiscovered(mock.Anything, mock.MatchedBy(func(d *store.DiscoveredRepo) bool {
		return d.Org == "acme" && d.Name == "widgets" && d.ProviderRepoID != nil && *d.ProviderRepoID == 9
	})).Return(store.UpsertResult{ID: 42, Created: true}, nil)

	if err := r.RouteWebhook(t.Context(), webhook("push", "", widgets)); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc, signal{workflowID: "repo/42", name: workflows.RecheckSignal, started: true})
}

func TestRouteWebhook_PayloadDiscoveryEvents(t *testing.T) {
	tests := []struct {
		event, action string
		trigger       string
		priority      workflows.Priority
	}{
		{"repository", "created", workflows.TriggerWebhook, workflows.PriorityWebhook},
		{"repository", "renamed", workflows.TriggerWebhook, workflows.PriorityWebhook},
		{"repository", "transferred", workflows.TriggerWebhook, workflows.PriorityWebhook},
	}

	for _, tt := range tests {
		t.Run(tt.event+"."+tt.action, func(t *testing.T) {
			r, st, tc := newRouter(t)
			st.MockWriter.EXPECT().UpsertInstallation(mock.Anything, store.Installation{InstallationID: 7, AccountLogin: "acme"}).Return(nil)
			st.MockWriter.EXPECT().UpsertDiscovered(mock.Anything, mock.Anything).Return(store.UpsertResult{ID: 42}, nil)

			if err := r.RouteWebhook(t.Context(), webhook(tt.event, tt.action, widgets)); err != nil {
				t.Fatal(err)
			}

			wantSignals(t, tc, signal{workflowID: "repo/42", name: workflows.RecheckSignal, started: true, priority: int(tt.priority)})

			if got := recheckOf(t, &tc.signals[0]); got.Trigger != tt.trigger || got.Priority != tt.priority {
				t.Errorf("recheck = %+v", got)
			}
		})
	}
}

// Installation-level events hand the listing to a single-installation
// DiscoveryWorkflow: the listing, not the payload, decides what is
// upserted and un-parked.
func TestRouteWebhook_InstallationDiscoveryEvents(t *testing.T) {
	for _, ev := range [][2]string{
		{"repository", "unarchived"}, {"installation", "created"}, {"installation_repositories", "added"},
	} {
		t.Run(ev[0]+"."+ev[1], func(t *testing.T) {
			r, st, tc := newRouter(t)
			st.MockWriter.EXPECT().UpsertInstallation(mock.Anything, store.Installation{InstallationID: 7, AccountLogin: "acme"}).Return(nil)

			if err := r.RouteWebhook(t.Context(), webhook(ev[0], ev[1], widgets)); err != nil {
				t.Fatal(err)
			}

			wantSignals(t, tc, signal{workflowID: "discovery/installation/7/d1", name: workflows.DiscoveryWorkflowName, started: true})

			if in, ok := tc.signals[0].arg.(*workflows.DiscoveryInput); !ok || in.InstallationID != 7 {
				t.Errorf("discovery input = %+v", tc.signals[0].arg)
			}
		})
	}
}

// On an all-repositories installation, installation_repositories.added
// is discovered from its payload like repository.created; a selected
// installation, or a payload past one listing page, is listed.
func TestRouteWebhook_InstallationRepositoriesAdded(t *testing.T) {
	many := make([]workflows.WebhookRepo, payloadDiscoveryMax+1)
	for i := range many {
		many[i] = workflows.WebhookRepo{ID: int64(100 + i), Org: "acme", Name: fmt.Sprintf("r%d", i)}
	}

	tests := []struct {
		name      string
		selection string
		repos     []workflows.WebhookRepo
		listing   bool
	}{
		{name: "all", selection: "all", repos: []workflows.WebhookRepo{widgets}},
		{name: "selected", selection: "selected", repos: []workflows.WebhookRepo{widgets}, listing: true},
		{name: "all past one page", selection: "all", repos: many, listing: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, st, tc := newRouter(t)
			st.MockWriter.EXPECT().UpsertInstallation(mock.Anything, store.Installation{InstallationID: 7, AccountLogin: "acme"}).Return(nil)

			if !tt.listing {
				st.MockWriter.EXPECT().UpsertDiscovered(mock.Anything, mock.MatchedBy(func(d *store.DiscoveredRepo) bool {
					return d.Name == "widgets" && d.InstallationID == 7
				})).Return(store.UpsertResult{ID: 42}, nil)
			}

			in := webhook("installation_repositories", "added", tt.repos...)
			in.RepositorySelection = tt.selection

			if err := r.RouteWebhook(t.Context(), in); err != nil {
				t.Fatal(err)
			}

			if tt.listing {
				wantSignals(t, tc, signal{workflowID: "discovery/installation/7/d1", name: workflows.DiscoveryWorkflowName, started: true})

				return
			}

			wantSignals(t, tc, signal{workflowID: "repo/42", name: workflows.RecheckSignal, started: true})
		})
	}
}

// A rename matches the row by provider id, so the repository id, and
// with it the workflow ID, survives the new name.
func TestRouteWebhook_RenameKeepsWorkflowID(t *testing.T) {
	r, st, tc := newRouter(t)
	st.MockWriter.EXPECT().UpsertInstallation(mock.Anything, mock.Anything).Return(nil)
	st.MockWriter.EXPECT().UpsertDiscovered(mock.Anything, mock.MatchedBy(func(d *store.DiscoveredRepo) bool {
		return d.Name == "gadgets" && *d.ProviderRepoID == 9
	})).Return(store.UpsertResult{ID: 42, Renamed: true}, nil)

	renamed := workflows.WebhookRepo{ID: 9, Org: "acme", Name: "gadgets"}
	if err := r.RouteWebhook(t.Context(), webhook("repository", "renamed", renamed)); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc, signal{workflowID: "repo/42", name: workflows.RecheckSignal, started: true})
}

func TestRouteWebhook_ArchivedRechecksKnownRepo(t *testing.T) {
	r, st, tc := newRouter(t)
	st.MockReader.EXPECT().FindRepository(mock.Anything, "acme", "widgets", mock.Anything).
		Return(&store.Repository{ID: 42, InstallationID: 7, Active: true}, nil)

	if err := r.RouteWebhook(t.Context(), webhook("repository", "archived", widgets)); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc, signal{workflowID: "repo/42", name: workflows.RecheckSignal, started: true})
}

func TestRouteWebhook_ArchivedUnknownRepoIsIgnored(t *testing.T) {
	r, st, tc := newRouter(t)
	st.MockReader.EXPECT().FindRepository(mock.Anything, "acme", "widgets", mock.Anything).Return(nil, store.ErrNotFound)

	if err := r.RouteWebhook(t.Context(), webhook("repository", "archived", widgets)); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc)
}

func TestRouteWebhook_RemovedParksThroughWorkflow(t *testing.T) {
	for _, ev := range [][2]string{{"repository", "deleted"}, {"installation_repositories", "removed"}} {
		t.Run(ev[0]+"."+ev[1], func(t *testing.T) {
			r, st, tc := newRouter(t)
			st.MockReader.EXPECT().FindRepository(mock.Anything, "acme", "widgets", mock.Anything).
				Return(&store.Repository{ID: 42, Active: true}, nil)

			if err := r.RouteWebhook(t.Context(), webhook(ev[0], ev[1], widgets)); err != nil {
				t.Fatal(err)
			}

			wantSignals(t, tc, signal{workflowID: "repo/42", name: workflows.ParkSignal})

			if p, ok := tc.signals[0].arg.(workflows.Park); !ok || p.Reason != string(store.ParkRemoved) {
				t.Errorf("park arg = %+v", tc.signals[0].arg)
			}
		})
	}
}

func TestRouteWebhook_RemovedWithoutWorkflowParksRow(t *testing.T) {
	r, st, tc := newRouter(t)
	tc.signalErr = serviceerror.NewNotFound("workflow not found")
	st.MockReader.EXPECT().FindRepository(mock.Anything, "acme", "widgets", mock.Anything).
		Return(&store.Repository{ID: 42, Active: true}, nil)
	st.MockWriter.EXPECT().Park(mock.Anything, int64(42), store.ParkRemoved, false).Return(nil)

	if err := r.RouteWebhook(t.Context(), webhook("repository", "deleted", widgets)); err != nil {
		t.Fatal(err)
	}
}

func TestRouteWebhook_InstallationDeletedMarksRemoved(t *testing.T) {
	r, st, tc := newRouter(t)
	st.MockWriter.EXPECT().MarkInstallationRemoved(mock.Anything, int64(7), mock.Anything).Return(nil)

	if err := r.RouteWebhook(t.Context(), webhook("installation", "deleted")); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc)
}

func TestRouteWebhook_SuspendSignalsBudget(t *testing.T) {
	for _, action := range []string{"suspend", "unsuspend"} {
		t.Run(action, func(t *testing.T) {
			r, st, tc := newRouter(t)
			st.MockWriter.EXPECT().UpsertInstallation(mock.Anything, mock.MatchedBy(func(in store.Installation) bool {
				return in.InstallationID == 7 && (in.SuspendedAt != nil) == (action == "suspend")
			})).Return(nil).Once()

			if action == "unsuspend" {
				st.MockWriter.EXPECT().UpsertInstallation(mock.Anything, store.Installation{InstallationID: 7, AccountLogin: "acme"}).
					Return(nil).Once()
			}

			if err := r.RouteWebhook(t.Context(), webhook("installation", action)); err != nil {
				t.Fatal(err)
			}

			want := []signal{{workflowID: "installation/7", name: workflows.SuspendSignal, started: true}}
			if action == "unsuspend" {
				want = append(want, signal{workflowID: "discovery/installation/7/d1", name: workflows.DiscoveryWorkflowName, started: true})
			}

			wantSignals(t, tc, want...)

			if s, ok := tc.signals[0].arg.(workflows.Suspend); !ok || s.Suspended != (action == "suspend") {
				t.Errorf("suspend arg = %+v", tc.signals[0].arg)
			}
		})
	}
}

func TestRouteWebhook_UnroutedEventIsDropped(t *testing.T) {
	r, _, tc := newRouter(t)

	if err := r.RouteWebhook(t.Context(), webhook("star", "created")); err != nil {
		t.Fatal(err)
	}

	wantSignals(t, tc)
}

// recordingAccess records the access changes the router hands it.
type recordingAccess struct{ got []AppAccess }

func (r *recordingAccess) RecordAppAccess(_ context.Context, a *AppAccess) error {
	r.got = append(r.got, *a)

	return nil
}

// TestRouteWebhook_RemediationAppAccess is IMPL-0028 task 3.3: the
// Remediation App's installation events update its access and never
// drive discovery; the Evaluation App's route the same event as the rc.
func TestRouteWebhook_RemediationAppAccess(t *testing.T) {
	for _, ev := range [][2]string{
		{"installation", "created"},
		{"installation", "deleted"},
		{"installation", "suspend"},
		{"installation_repositories", "added"},
		{"installation_repositories", "removed"},
	} {
		t.Run(ev[0]+"."+ev[1], func(t *testing.T) {
			r, _, tc := newRouter(t)
			rec := &recordingAccess{}
			r.WithAccessRecorder(rec)

			in := webhook(ev[0], ev[1], widgets)
			in.App = workflows.AppRemediate

			if err := r.RouteWebhook(t.Context(), in); err != nil {
				t.Fatal(err)
			}

			wantSignals(t, tc)

			if len(rec.got) != 1 || rec.got[0].App != workflows.AppRemediate || rec.got[0].Action != ev[1] || len(rec.got[0].Repositories) != 1 {
				t.Errorf("recorded = %+v, want one %s access change", rec.got, ev[1])
			}
		})
	}

	t.Run("evaluation App discovers", func(t *testing.T) {
		r, st, tc := newRouter(t)
		rec := &recordingAccess{}
		r.WithAccessRecorder(rec)
		st.MockWriter.EXPECT().UpsertInstallation(mock.Anything, store.Installation{InstallationID: 7, AccountLogin: "acme"}).Return(nil)

		in := webhook("installation", "created", widgets)
		in.App = workflows.AppEval

		if err := r.RouteWebhook(t.Context(), in); err != nil {
			t.Fatal(err)
		}

		wantSignals(t, tc, signal{workflowID: "discovery/installation/7/d1", name: workflows.DiscoveryWorkflowName, started: true})

		if len(rec.got) != 0 {
			t.Errorf("recorded = %+v, want nothing for the Evaluation App", rec.got)
		}
	})
}
