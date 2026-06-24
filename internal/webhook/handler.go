// Package webhook provides the HTTP handler for GitHub App webhook events.
//
// IMPL-0015 Phase 0 added the discovery write-back contract:
// installation_repositories.added and repository.created webhook
// events now call store.UpsertIfMissing for each newly-visible
// repository, seeding a `pending` row with a jittered initial
// LastCheckedAt. The jitter spreads the first cold-start sweep over
// a 2×freshness window so a fleet onboarding bulk doesn't synchronise
// every repo's next-due timestamp at install time.
//
// Push events do not seed new rows (the repo was either discovered at
// installation time or already exists). They DO update the existing
// row to LastCheckStatus=StatusPending BEFORE enqueuing, so the
// stale-sweeper doesn't redundantly enqueue the same repo if its
// sweep tick fires before the worker drains the push-triggered job.
package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"time"

	gh "github.com/google/go-github/v68/github"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Handler handles incoming GitHub webhook events and enqueues repo check jobs.
type Handler struct {
	webhookSecret []byte
	queue         queue.Queue
	logger        *slog.Logger
	watchedPaths  map[string]bool
	stateStore    store.Store
	policyVersion string
	freshness     time.Duration
	// rng is the source for discovery-jitter. Per-Handler so tests
	// can seed it deterministically; the production path receives
	// the default time-seeded source.
	rng *rand.Rand
}

// NewHandler creates a new webhook Handler.
// watchedPaths specifies file paths that trigger a re-check on push
// events. stateStore + policyVersion + freshness drive the discovery
// write-back contract added in IMPL-0015 Phase 0. Pass a nil
// stateStore in tests that don't exercise that path.
func NewHandler(
	webhookSecret string,
	q queue.Queue,
	logger *slog.Logger,
	watchedPaths map[string]bool,
	stateStore store.Store,
	policyVersion string,
	freshness time.Duration,
) *Handler {
	return &Handler{
		webhookSecret: []byte(webhookSecret),
		queue:         q,
		logger:        logger,
		watchedPaths:  watchedPaths,
		stateStore:    stateStore,
		policyVersion: policyVersion,
		freshness:     freshness,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec // non-crypto: just jitter
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
	owner := repo.GetOwner().GetLogin()
	repoName := repo.GetName()

	h.logger.Info("repository created event",
		"owner", owner,
		"repo", repoName,
		"installation_id", installID,
	)

	h.discover(ctx, installID, owner, repoName)
	h.enqueue(ctx, owner, repoName, installID)
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
		owner := extractOwner(repo.GetFullName())
		repoName := repo.GetName()

		h.discover(ctx, installID, owner, repoName)
		h.enqueue(ctx, owner, repoName, installID)
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
		owner := extractOwner(repo.GetFullName())
		repoName := repo.GetName()

		h.discover(ctx, installID, owner, repoName)
		h.enqueue(ctx, owner, repoName, installID)
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

	h.markPending(ctx, installID, owner, repoName)
	h.enqueuePush(ctx, owner, repoName, installID)
}

// discover seeds a `pending` row for a newly-visible repo. The
// LastCheckedAt is jittered uniformly across [0, 2×freshness] so a
// fleet onboarding bulk doesn't cluster every repo's next-due
// timestamp at install time — without the jitter, a stale-sweep with
// freshness=24h would fire on every repo at exactly t+24h and
// thrash the rate-limit reserve. Best-effort: a Store error here is
// logged and counted, never propagated; the next sweep will discover
// the repo via the GitHub API enumeration path.
func (h *Handler) discover(ctx context.Context, installationID int64, owner, repo string) {
	if h.stateStore == nil {
		return
	}

	jitter := time.Duration(h.rng.Int63n(int64(2 * h.freshness)))
	seedTime := time.Now().UTC().Add(-jitter)

	state := &store.RepoState{
		InstallationID:  installationID,
		Owner:           owner,
		Repo:            repo,
		LastCheckedAt:   &seedTime,
		LastCheckStatus: store.StatusPending,
		PolicyVersion:   "", // empty so the next sweep treats as drifted
	}

	created, err := h.stateStore.UpsertIfMissing(ctx, state)
	if err != nil {
		h.logger.Warn("discovery UpsertIfMissing failed; next sweep will catch the repo",
			"owner", owner,
			"repo", repo,
			"installation_id", installationID,
			"error", err,
		)

		return
	}

	if created {
		h.logger.Info("discovered new repository",
			"owner", owner,
			"repo", repo,
			"installation_id", installationID,
		)
	}
}

// markPending writes StatusPending to the existing row BEFORE the
// enqueue so the stale-sweeper doesn't redundantly enqueue the same
// repo if its tick fires before the worker processes the push job.
// Best-effort: a Store error here is logged and dropped — the worker
// write-back will still converge the row on success/error.
func (h *Handler) markPending(ctx context.Context, installationID int64, owner, repo string) {
	if h.stateStore == nil {
		return
	}

	now := time.Now().UTC()

	state := &store.RepoState{
		InstallationID:  installationID,
		Owner:           owner,
		Repo:            repo,
		LastCheckedAt:   &now,
		LastCheckStatus: store.StatusPending,
		PolicyVersion:   h.policyVersion,
	}

	if err := h.stateStore.UpdateRepoState(ctx, state); err != nil {
		h.logger.Warn("push markPending failed; worker write-back will converge",
			"owner", owner,
			"repo", repo,
			"installation_id", installationID,
			"error", err,
		)
	}
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
