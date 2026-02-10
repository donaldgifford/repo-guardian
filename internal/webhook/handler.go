// Package webhook provides the HTTP handler for GitHub App webhook events.
package webhook

import (
	"log/slog"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v68/github"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// Handler handles incoming GitHub webhook events and enqueues repo check jobs.
type Handler struct {
	webhookSecret []byte
	queue         *checker.Queue
	logger        *slog.Logger
}

// NewHandler creates a new webhook Handler.
func NewHandler(webhookSecret string, queue *checker.Queue, logger *slog.Logger) *Handler {
	return &Handler{
		webhookSecret: []byte(webhookSecret),
		queue:         queue,
		logger:        logger,
	}
}

// ServeHTTP implements http.Handler for GitHub webhook events.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	payload, err := gh.ValidatePayload(r, h.webhookSecret)
	if err != nil {
		h.logger.Warn("invalid webhook payload", "error", err)
		http.Error(w, "invalid payload", http.StatusUnauthorized)

		return
	}

	event, err := gh.ParseWebHook(gh.WebHookType(r), payload)
	if err != nil {
		h.logger.Error("failed to parse webhook", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	eventType := gh.WebHookType(r)
	metrics.WebhookReceivedTotal.WithLabelValues(eventType).Inc()

	switch e := event.(type) {
	case *gh.RepositoryEvent:
		h.handleRepositoryEvent(e)
	case *gh.InstallationRepositoriesEvent:
		h.handleInstallationRepositoriesEvent(e)
	case *gh.InstallationEvent:
		h.handleInstallationEvent(e)
	default:
		h.logger.Debug("ignoring unhandled event type", "type", eventType)
		w.WriteHeader(http.StatusNoContent)

		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleRepositoryEvent(e *gh.RepositoryEvent) {
	if e.GetAction() != "created" {
		h.logger.Debug("ignoring repository event", "action", e.GetAction())
		return
	}

	repo := e.GetRepo()
	installID := e.GetInstallation().GetID()

	h.logger.Info("repository created event",
		"owner", repo.GetOwner().GetLogin(),
		"repo", repo.GetName(),
		"installation_id", installID,
	)

	h.enqueue(repo.GetOwner().GetLogin(), repo.GetName(), installID)
}

func (h *Handler) handleInstallationRepositoriesEvent(e *gh.InstallationRepositoriesEvent) {
	if e.GetAction() != "added" {
		h.logger.Debug("ignoring installation_repositories event", "action", e.GetAction())
		return
	}

	installID := e.GetInstallation().GetID()

	h.logger.Info("installation repositories added",
		"count", len(e.RepositoriesAdded),
		"installation_id", installID,
	)

	for _, repo := range e.RepositoriesAdded {
		h.enqueue(extractOwner(repo.GetFullName()), repo.GetName(), installID)
	}
}

func (h *Handler) handleInstallationEvent(e *gh.InstallationEvent) {
	if e.GetAction() != "created" {
		h.logger.Debug("ignoring installation event", "action", e.GetAction())
		return
	}

	installID := e.GetInstallation().GetID()

	h.logger.Info("new installation created",
		"count", len(e.Repositories),
		"installation_id", installID,
	)

	for _, repo := range e.Repositories {
		h.enqueue(extractOwner(repo.GetFullName()), repo.GetName(), installID)
	}
}

func (h *Handler) enqueue(owner, repo string, installationID int64) {
	job := checker.RepoJob{
		Owner:          owner,
		Repo:           repo,
		InstallationID: installationID,
		Trigger:        checker.TriggerWebhook,
	}

	if err := h.queue.Enqueue(job); err != nil {
		h.logger.Error("failed to enqueue job",
			"owner", owner,
			"repo", repo,
			"error", err,
		)
	}
}

// extractOwner gets the owner from a "owner/repo" full name string.
func extractOwner(fullName string) string {
	owner, _, _ := strings.Cut(fullName, "/")
	return owner
}
