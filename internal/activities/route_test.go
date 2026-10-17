package activities

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"go.temporal.io/sdk/client"
	temporalmocks "go.temporal.io/sdk/mocks"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

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

// newRouter returns a router and a strict Temporal mock: the router
// itself never signals, so any call fails the test.
func newRouter(t *testing.T) (*Router, *recordingTemporal) {
	t.Helper()

	return NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil))), &recordingTemporal{Client: temporalmocks.NewClient(t)}
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

// TestRouteWebhook_UnroutedEventIsDropped: a delivery naming neither
// controls App has no routing since the switch-over (IMPL-0029 7.1).
func TestRouteWebhook_UnroutedEventIsDropped(t *testing.T) {
	r, tc := newRouter(t)

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
// drive discovery.
func TestRouteWebhook_RemediationAppAccess(t *testing.T) {
	for _, ev := range [][2]string{
		{"installation", "created"},
		{"installation", "deleted"},
		{"installation", "suspend"},
		{"installation_repositories", "added"},
		{"installation_repositories", "removed"},
	} {
		t.Run(ev[0]+"."+ev[1], func(t *testing.T) {
			r, tc := newRouter(t)
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
}

// recordingControls records the deliveries handed to the controls
// routing.
type recordingControls struct {
	got []*workflows.WebhookInput
}

func (c *recordingControls) RouteWebhook(_ context.Context, in *workflows.WebhookInput) error {
	c.got = append(c.got, in)

	return nil
}

// TestRouteWebhook_EvaluationAppTakesTheControlsRouting is IMPL-0029
// task 5.7, replacing IMPL-0028 task 9.1's drop: every Evaluation App
// delivery goes to the controls routing. The strict mock fails on any
// Temporal call the router makes itself.
func TestRouteWebhook_EvaluationAppTakesTheControlsRouting(t *testing.T) {
	for _, ev := range [][2]string{
		{"installation", "created"},
		{"installation_repositories", "added"},
		{"repository", "created"},
		{"repository", "deleted"},
		{"push", ""},
	} {
		t.Run(ev[0]+"."+ev[1], func(t *testing.T) {
			r, tc := newRouter(t)
			ctl := &recordingControls{}
			r.WithControls(ctl)

			in := webhook(ev[0], ev[1], widgets)
			in.App = workflows.AppEval

			if err := r.RouteWebhook(t.Context(), in); err != nil {
				t.Fatal(err)
			}

			wantSignals(t, tc)

			if len(ctl.got) != 1 || ctl.got[0] != in {
				t.Errorf("controls routing got %d deliveries, want this one", len(ctl.got))
			}
		})
	}
}

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
