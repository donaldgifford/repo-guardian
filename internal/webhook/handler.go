// Package webhook provides the HTTP handler for GitHub App webhook events.
package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	gh "github.com/google/go-github/v68/github"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
)

// Handler handles incoming GitHub webhook events and enqueues repo check jobs.
type Handler struct {
	webhookSecret []byte
	queue         queue.Queue
	logger        *slog.Logger
	watchedPaths  map[string]bool
}

// NewHandler creates a new webhook Handler.
// watchedPaths specifies file paths that trigger a re-check on push events.
func NewHandler(
	webhookSecret string,
	q queue.Queue,
	logger *slog.Logger,
	watchedPaths map[string]bool,
) *Handler {
	return &Handler{
		webhookSecret: []byte(webhookSecret),
		queue:         q,
		logger:        logger,
		watchedPaths:  watchedPaths,
	}
}

// ServeHTTP implements http.Handler for GitHub webhook events.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	ctx := r.Context()

	switch e := event.(type) {
	case *gh.RepositoryEvent:
		h.handleRepositoryEvent(ctx, e)
	case *gh.InstallationRepositoriesEvent:
		h.handleInstallationRepositoriesEvent(ctx, e)
	case *gh.InstallationEvent:
		h.handleInstallationEvent(ctx, e)
	case *gh.PushEvent:
		h.handlePushEvent(ctx, e)
	default:
		h.logger.Debug("ignoring unhandled event type", "type", eventType)
		w.WriteHeader(http.StatusNoContent)

		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) handleRepositoryEvent(ctx context.Context, e *gh.RepositoryEvent) {
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

	h.enqueue(ctx, repo.GetOwner().GetLogin(), repo.GetName(), installID)
}

func (h *Handler) handleInstallationRepositoriesEvent(ctx context.Context, e *gh.InstallationRepositoriesEvent) {
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
		h.enqueue(ctx, extractOwner(repo.GetFullName()), repo.GetName(), installID)
	}
}

func (h *Handler) handleInstallationEvent(ctx context.Context, e *gh.InstallationEvent) {
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
		h.enqueue(ctx, extractOwner(repo.GetFullName()), repo.GetName(), installID)
	}
}

func (h *Handler) handlePushEvent(ctx context.Context, e *gh.PushEvent) {
	ref := e.GetRef()

	// Ignore tag pushes.
	if strings.HasPrefix(ref, "refs/tags/") {
		h.logger.Debug("ignoring tag push", "ref", ref)
		return
	}

	// Only process pushes to the default branch.
	repo := e.GetRepo()
	defaultBranch := repo.GetDefaultBranch()

	if ref != "refs/heads/"+defaultBranch {
		h.logger.Debug("ignoring push to non-default branch",
			"ref", ref,
			"default_branch", defaultBranch,
		)

		return
	}

	if len(h.watchedPaths) == 0 {
		h.logger.Debug("no watched paths configured, ignoring push event")
		return
	}

	if !h.hasWatchedFileChanges(e) {
		return
	}

	owner := repo.GetOwner().GetLogin()
	repoName := repo.GetName()
	installID := e.GetInstallation().GetID()

	h.logger.Info("push event with watched file changes",
		"owner", owner,
		"repo", repoName,
		"ref", ref,
	)

	h.enqueuePush(ctx, owner, repoName, installID)
}

// hasWatchedFileChanges checks if any commit in the push event contains
// added or modified files that match the watched paths. Removed files
// are intentionally not checked.
func (h *Handler) hasWatchedFileChanges(e *gh.PushEvent) bool {
	for _, commit := range e.Commits {
		for _, path := range commit.Added {
			if h.watchedPaths[path] {
				return true
			}
		}

		for _, path := range commit.Modified {
			if h.watchedPaths[path] {
				return true
			}
		}
	}

	return false
}

func (h *Handler) enqueuePush(ctx context.Context, owner, repo string, installationID int64) {
	h.enqueueWith(ctx, owner, repo, installationID, queue.TriggerPush)
}

func (h *Handler) enqueue(ctx context.Context, owner, repo string, installationID int64) {
	h.enqueueWith(ctx, owner, repo, installationID, queue.TriggerWebhook)
}

func (h *Handler) enqueueWith(ctx context.Context, owner, repo string, installationID int64, trigger string) {
	job := queue.Job{
		ID:             fmt.Sprintf("%s/%s/%d", owner, repo, time.Now().UnixNano()),
		Owner:          owner,
		Repo:           repo,
		InstallationID: installationID,
		Trigger:        trigger,
		EnqueuedAt:     time.Now(),
	}

	if err := h.queue.Enqueue(ctx, job); err != nil {
		h.logger.Error("failed to enqueue job",
			"owner", owner,
			"repo", repo,
			"trigger", trigger,
			"error", err,
		)

		metrics.ErrorsTotal.WithLabelValues("enqueue", owner).Inc()

		return
	}

	metrics.QueueEnqueuedTotal.WithLabelValues(trigger).Inc()
}

// extractOwner gets the owner from a "owner/repo" full name string.
func extractOwner(fullName string) string {
	owner, _, _ := strings.Cut(fullName, "/")
	return owner
}
