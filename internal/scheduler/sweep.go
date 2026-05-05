// Package scheduler implements a periodic reconciliation loop that
// enqueues all installed repositories for compliance checks.
//
// Two shapes coexist during the IMPL-0011 migration:
//   - Sweeper: the legacy concrete loop that ListInstallations →
//     ListInstallationRepos → checker.Queue.Enqueue. Still wired into
//     main.go as of Phase 1.
//   - Scheduler interface (scheduler.go) + ticker implementation
//     (ticker/ticker.go): the new abstraction that replaces Sweeper's
//     for-select tick logic and is multi-replica-coordination-ready.
//
// Phase 1 introduces the new shape alongside the old; later phases
// move main.go onto the interface and retire Sweeper's ticker loop.
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// Sweeper periodically reconciles all repositories across all GitHub
// App installations. Renamed from Scheduler in IMPL-0011 Phase 1 to
// avoid colliding with the new Scheduler interface; behavior is
// otherwise unchanged.
type Sweeper struct {
	client       ghclient.Client
	queue        *checker.Queue
	interval     time.Duration
	logger       *slog.Logger
	skipForks    bool
	skipArchived bool
}

// NewSweeper creates a new Sweeper.
func NewSweeper(
	client ghclient.Client,
	queue *checker.Queue,
	interval time.Duration,
	logger *slog.Logger,
	skipForks, skipArchived bool,
) *Sweeper {
	return &Sweeper{
		client:       client,
		queue:        queue,
		interval:     interval,
		logger:       logger,
		skipForks:    skipForks,
		skipArchived: skipArchived,
	}
}

// Start begins the reconciliation loop. It runs reconcileAll immediately
// on startup, then repeats at the configured interval. It blocks until
// the context is canceled.
func (s *Sweeper) Start(ctx context.Context) {
	s.logger.Info("sweeper starting", "interval", s.interval)

	// Run once on startup.
	s.reconcileAll(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("sweeper stopped")
			return
		case <-ticker.C:
			s.reconcileAll(ctx)
		}
	}
}

// reconcileAll lists all installations and their repos, enqueuing each for checking.
func (s *Sweeper) reconcileAll(ctx context.Context) {
	start := time.Now()
	s.logger.Info("starting reconciliation")

	installations, err := s.client.ListInstallations(ctx)
	if err != nil {
		s.logger.Error("failed to list installations", "error", err)
		return
	}

	var enqueued int

	for _, install := range installations {
		repos, err := s.client.ListInstallationRepos(ctx, install.ID)
		if err != nil {
			s.logger.Error("failed to list repos for installation",
				"installation_id", install.ID,
				"error", err,
			)

			continue
		}

		for _, repo := range repos {
			// Pre-filter archived and forked repos to avoid enqueuing work
			// that the engine would skip anyway. The engine performs the
			// authoritative check — this is an optimization to reduce
			// unnecessary GitHub API calls during reconciliation.
			if s.skipArchived && repo.Archived {
				continue
			}

			if s.skipForks && repo.Fork {
				continue
			}

			job := checker.RepoJob{
				Owner:          repo.Owner,
				Repo:           repo.Name,
				InstallationID: install.ID,
				Trigger:        checker.TriggerScheduler,
			}

			if err := s.queue.Enqueue(job); err != nil {
				s.logger.Error("failed to enqueue repo",
					"owner", repo.Owner,
					"repo", repo.Name,
					"error", err,
				)

				continue
			}

			enqueued++
		}
	}

	s.logger.Info("reconciliation complete",
		"enqueued", enqueued,
		"duration", time.Since(start),
	)
}
