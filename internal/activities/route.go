package activities

import (
	"context"
	"log/slog"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// payloadDiscoveryMax is the most repositories an all-repositories
// installation_repositories.added delivery may name and still be
// discovered from its payload; above it (a selected → all switch) the
// batched, retried ControlsDiscoveryWorkflow does the work. One listing
// page.
const payloadDiscoveryMax = 100

// selectionAll is the repository_selection of an installation on every
// repository in its account.
const selectionAll = "all"

// The installation-level webhook events.
const (
	eventInstallation             = "installation"
	eventInstallationRepositories = "installation_repositories"

	// actionAdded is the installation_repositories action the remediator's
	// discovery stamps on an access change it hands to the evaluator.
	actionAdded = "added"
)

// Router is the RouteWebhook activity (DESIGN-0026 § Ingest and webhook
// routing). Ingest has already validated and filtered the delivery; the
// router hands an Evaluation App delivery to the controls routing and a
// Remediation App installation change to the access recorder.
type Router struct {
	access   AppAccessRecorder
	controls ControlsRouting
	logger   *slog.Logger
}

// NewRouter returns a router that records and routes nothing until
// WithAccessRecorder and WithControls wire it.
func NewRouter(logger *slog.Logger) *Router {
	return &Router{access: noopAccessRecorder{logger: logger}, controls: unwiredControls{logger: logger}, logger: logger}
}

// AppAccess is one change to a controls App's access: an installation
// created, deleted, suspended or unsuspended, or repositories added to
// or removed from it.
type AppAccess struct {
	App            string
	InstallationID int64
	AccountLogin   string
	Event          string
	Action         string
	Repositories   []workflows.WebhookRepo
}

// AppAccessRecorder records a controls App's access. *ControlsRouter
// satisfies it; the router's default recorder only logs.
type AppAccessRecorder interface {
	RecordAppAccess(ctx context.Context, a *AppAccess) error
}

// WithAccessRecorder replaces the router's no-op access recorder.
func (r *Router) WithAccessRecorder(rec AppAccessRecorder) *Router {
	r.access = rec

	return r
}

// ControlsRouting routes the Evaluation App's deliveries.
// *ControlsRouter satisfies it.
type ControlsRouting interface {
	RouteWebhook(ctx context.Context, in *workflows.WebhookInput) error
}

// WithControls hands the Evaluation App's deliveries to c.
func (r *Router) WithControls(c ControlsRouting) *Router {
	r.controls = c

	return r
}

// unwiredControls is the controls routing of a router nothing wired.
type unwiredControls struct{ logger *slog.Logger }

func (u unwiredControls) RouteWebhook(_ context.Context, in *workflows.WebhookInput) error {
	u.logger.Info("dropping Evaluation App delivery: no controls routing in this process",
		"delivery", in.DeliveryID, "event", in.Event, "action", in.Action)

	return nil
}

// noopAccessRecorder is the recorder of a router nothing wired.
type noopAccessRecorder struct{ logger *slog.Logger }

// RecordAppAccess logs the change and records nothing.
func (n noopAccessRecorder) RecordAppAccess(_ context.Context, a *AppAccess) error {
	n.logger.Debug("app access change not recorded: no access recorder in this process",
		"app", a.App, "installation_id", a.InstallationID, "event", a.Event, "action", a.Action, "repositories", len(a.Repositories))

	return nil
}

// isAccessEvent reports whether event is an installation-level change.
func isAccessEvent(event string) bool {
	return event == eventInstallation || event == eventInstallationRepositories
}

// Register registers RouteWebhook under its workflows package name.
func (r *Router) Register(reg Registry) {
	reg.RegisterActivityWithOptions(r.RouteWebhook, activity.RegisterOptions{Name: workflows.RouteWebhookActivity})
}

// RouteWebhook applies one delivery. It is safe to retry: every step it
// hands off is an upsert, a park of an already-parked row, or a signal
// that coalesces.
func (r *Router) RouteWebhook(ctx context.Context, in *workflows.WebhookInput) error {
	// The Remediation App's installation events describe where it may
	// write, not which repositories exist: they update its access and
	// never drive discovery (IMPL-0028 task 3.3).
	if in.App == workflows.AppRemediate && isAccessEvent(in.Event) {
		return r.access.RecordAppAccess(ctx, &AppAccess{
			App: in.App, InstallationID: in.InstallationID, AccountLogin: in.AccountLogin,
			Event: in.Event, Action: in.Action, Repositories: in.Repositories,
		})
	}

	// The Evaluation App's deliveries take the controls routing
	// (IMPL-0029 task 5.7).
	if in.App == workflows.AppEval {
		return r.controls.RouteWebhook(ctx, in)
	}

	r.logger.Warn("webhook workflow for a delivery ingest should have dropped",
		"delivery", in.DeliveryID, "app", in.App, "event", in.Event, "action", in.Action, "installation_id", in.InstallationID)

	return nil
}
