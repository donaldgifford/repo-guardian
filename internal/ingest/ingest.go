// Package ingest is the internet-facing webhook role (DESIGN-0026 §
// Ingest and webhook routing). It validates the HMAC, drops what it can
// decide without state, and hands everything else to a WebhookWorkflow.
// It holds neither the GitHub App key nor database credentials.
package ingest

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v68/github"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// reasonSignature labels WebhookRejectedTotal for a failed HMAC check,
// the only app-layer rejection (DESIGN-0023).
const reasonSignature = "signature"

// deliveryHeader carries GitHub's per-delivery id; redeliveries reuse it.
const deliveryHeader = "X-GitHub-Delivery"

// Starter starts workflows. client.Client satisfies it.
type Starter interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error)
}

// Handler is the ingest webhook handler.
type Handler struct {
	secret    []byte
	temporal  Starter
	taskQueue string
	watched   map[string]bool
	logger    *slog.Logger
}

// New returns the handler. watched is policy.ExtractWatchedPaths: a push
// to the default branch that touches none of them is dropped.
func New(secret string, starter Starter, taskQueue string, watched map[string]bool, logger *slog.Logger) *Handler {
	return &Handler{secret: []byte(secret), temporal: starter, taskQueue: taskQueue, watched: watched, logger: logger}
}

// ServeHTTP answers 401 for a bad signature, 400 for an unparseable or
// unidentified delivery, 204 for an event dropped statelessly, 503 when
// Temporal is unreachable and 202 once the workflow is started —
// including for a redelivery of one already started.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	payload, err := gh.ValidatePayload(r, h.secret)
	if err != nil {
		h.logger.Warn("invalid webhook payload", "error", err)
		metrics.WebhookRejectedTotal.WithLabelValues(reasonSignature).Inc()
		http.Error(w, "invalid payload", http.StatusUnauthorized)

		return
	}

	eventType := gh.WebHookType(r)

	event, err := gh.ParseWebHook(eventType, payload)
	if err != nil {
		h.logger.Error("failed to parse webhook", "error", err, "event", eventType)
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	metrics.WebhookReceivedTotal.WithLabelValues(eventType).Inc()

	in, ok := h.route(eventType, event)
	if !ok {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	in.DeliveryID = r.Header.Get(deliveryHeader)
	if in.DeliveryID == "" {
		http.Error(w, "missing "+deliveryHeader, http.StatusBadRequest)

		return
	}

	if err := h.start(r.Context(), in); err != nil {
		h.logger.Error("failed to start webhook workflow",
			"error", err, "delivery", in.DeliveryID, "event", in.Event, "action", in.Action,
			"installation_id", in.InstallationID)
		metrics.WebhookTemporalErrorsTotal.Inc()
		http.Error(w, "temporal unavailable", http.StatusServiceUnavailable)

		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// start starts webhook/<delivery>. A duplicate is success: GitHub
// redelivered something already handled or in progress.
func (h *Handler) start(ctx context.Context, in *workflows.WebhookInput) error {
	_, err := h.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflows.WebhookWorkflowID(in.DeliveryID),
		TaskQueue:             h.taskQueue,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		Priority:              workflows.TaskPriority(workflows.PriorityWebhook, in.InstallationID),
	}, workflows.WebhookWorkflowName, in)
	if err != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(err) {
		return err
	}

	return nil
}

// Handled actions per event (DESIGN-0026's event table).
var handled = map[string]map[string]bool{
	"repository":                {"created": true, "deleted": true, "archived": true, "unarchived": true, "renamed": true, "transferred": true},
	"installation":              {"created": true, "deleted": true, "suspend": true, "unsuspend": true},
	"installation_repositories": {"added": true, "removed": true},
}

// route decides statelessly whether an event needs a workflow and builds
// its input.
func (h *Handler) route(eventType string, event any) (*workflows.WebhookInput, bool) {
	switch e := event.(type) {
	case *gh.PushEvent:
		return h.routePush(e)
	case *gh.RepositoryEvent:
		if !handled[eventType][e.GetAction()] {
			return nil, false
		}

		return &workflows.WebhookInput{
			Event: eventType, Action: e.GetAction(),
			InstallationID: e.GetInstallation().GetID(),
			Repositories:   []workflows.WebhookRepo{repoOf(e.GetRepo())},
		}, true
	case *gh.InstallationEvent:
		if !handled[eventType][e.GetAction()] {
			return nil, false
		}

		return &workflows.WebhookInput{
			Event: eventType, Action: e.GetAction(),
			InstallationID: e.GetInstallation().GetID(),
			AccountLogin:   e.GetInstallation().GetAccount().GetLogin(),
			Repositories:   reposOf(e.Repositories),
		}, true
	case *gh.InstallationRepositoriesEvent:
		if !handled[eventType][e.GetAction()] {
			return nil, false
		}

		repos := e.RepositoriesAdded
		if e.GetAction() == "removed" {
			repos = e.RepositoriesRemoved
		}

		return &workflows.WebhookInput{
			Event: eventType, Action: e.GetAction(),
			InstallationID: e.GetInstallation().GetID(),
			AccountLogin:   e.GetInstallation().GetAccount().GetLogin(),
			Repositories:   reposOf(repos),
		}, true
	default:
		h.logger.Debug("ignoring unhandled event type", "type", eventType)

		return nil, false
	}
}

// routePush keeps only default-branch pushes that add, modify or remove
// a watched path. Removals count: a removed renovate.json closes a gate.
func (h *Handler) routePush(e *gh.PushEvent) (*workflows.WebhookInput, bool) {
	ref := e.GetRef()
	if strings.HasPrefix(ref, "refs/tags/") || ref != "refs/heads/"+e.GetRepo().GetDefaultBranch() {
		return nil, false
	}

	if !h.touchesWatched(e) {
		return nil, false
	}

	repo := e.GetRepo()

	return &workflows.WebhookInput{
		Event: "push", InstallationID: e.GetInstallation().GetID(),
		Repositories: []workflows.WebhookRepo{{ID: repo.GetID(), Org: repo.GetOwner().GetLogin(), Name: repo.GetName()}},
	}, true
}

func (h *Handler) touchesWatched(e *gh.PushEvent) bool {
	for _, c := range e.Commits {
		for _, paths := range [][]string{c.Added, c.Modified, c.Removed} {
			for _, p := range paths {
				if h.watched[p] {
					return true
				}
			}
		}
	}

	return false
}

func repoOf(r *gh.Repository) workflows.WebhookRepo {
	return workflows.WebhookRepo{ID: r.GetID(), Org: ownerOf(r), Name: r.GetName()}
}

func reposOf(rs []*gh.Repository) []workflows.WebhookRepo {
	out := make([]workflows.WebhookRepo, 0, len(rs))
	for _, r := range rs {
		out = append(out, repoOf(r))
	}

	return out
}

// ownerOf reads the owner login, falling back to full_name: the
// installation events carry only a short repository object.
func ownerOf(r *gh.Repository) string {
	if login := r.GetOwner().GetLogin(); login != "" {
		return login
	}

	owner, _, _ := strings.Cut(r.GetFullName(), "/")

	return owner
}
