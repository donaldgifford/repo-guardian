// Discoverer is the IMPL-0015 Phase 1.3 implementation of repository
// discovery. It enumerates every installation and every repo within
// each installation and persists discovery rows via
// store.Store.UpsertIfMissing — no enqueueing. The stale-sweeper then
// picks up the newly-discovered rows on its next tick (their initial
// LastCheckedAt is jittered uniformly across [-2*freshness, 0] so a
// brand-new install doesn't synchronize all repos' due-times).
//
// Conceptually replaces the pre-IMPL-0015 Sweeper (which enqueued
// every repo on every tick, burning O(installs × repos) API calls
// per sweep regardless of freshness). The Sweeper type remains in
// internal/scheduler/sweep.go pending deletion in a follow-up;
// nothing wires it up post-IMPL-0015 Phase 0.

package scheduler

import (
	"context"
	"crypto/rand"
	"log/slog"
	"math/big"
	"strconv"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Discoverer enumerates installations + repos via the GitHub API and
// upserts discovery rows into store.Store. Discovery is not
// throttle-gated: it only lists repos (one API call per
// installation), and any rate-limit pressure it does hit surfaces
// through the IMPL-0022 delayed-requeue path on the check jobs
// themselves.
type Discoverer struct {
	client       ghclient.Client
	store        store.Store
	logger       *slog.Logger
	skipForks    bool
	skipArchived bool
	freshness    time.Duration
}

// DiscovererOptions bundles Discoverer constructor inputs. Option-
// struct convention matches StaleSweeperOptions; pass-by-value is
// intentional since the caller builds a literal at the call site.
type DiscovererOptions struct {
	Client       ghclient.Client
	Store        store.Store
	Logger       *slog.Logger
	SkipForks    bool
	SkipArchived bool
	// Freshness is the operator's reconcile cadence (chart value
	// staleSweep.freshness). Discoverer jitters initial
	// LastCheckedAt uniformly across [-2*Freshness, 0] so a fleet
	// onboarding doesn't cluster every repo's due-time.
	Freshness time.Duration
}

// NewDiscoverer constructs a Discoverer. Logger defaults to
// slog.Default; Freshness defaults to 24h when zero.
func NewDiscoverer(opts DiscovererOptions) *Discoverer {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	if opts.Freshness <= 0 {
		opts.Freshness = 24 * time.Hour
	}

	return &Discoverer{
		client:       opts.Client,
		store:        opts.Store,
		logger:       opts.Logger,
		skipForks:    opts.SkipForks,
		skipArchived: opts.SkipArchived,
		freshness:    opts.Freshness,
	}
}

// randomJitter returns a uniform random duration in [0, span) using
// crypto/rand. On the practically-never reader-failure path returns 0,
// which collapses to no jitter — fail-safe because jitter is a
// load-spreading optimization, not a correctness requirement.
func randomJitter(span time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(span)))
	if err != nil {
		return 0
	}

	return time.Duration(n.Int64())
}

// Discover runs one discovery iteration. Suitable for driving via
// scheduler.Scheduler.Schedule. Returns ctx.Err() on cancellation;
// per-installation and per-repo errors are logged and skipped
// without aborting the iteration (fail-safe: a transient API or
// Store glitch never halts discovery for the rest of the fleet).
func (d *Discoverer) Discover(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	start := time.Now()

	defer func() {
		metrics.DiscoveryDurationSeconds.Observe(time.Since(start).Seconds())
	}()

	installations, err := d.client.ListInstallations(ctx)

	d.observeAPICall(0, "list_installations")

	if err != nil {
		d.logger.ErrorContext(ctx, "discovery: list installations failed", "error", err)
		// Fail-safe: a transient API error never halts the discoverer.
		// The next scheduled tick retries; per-installation iteration
		// continues below for whichever installs were already retrieved
		// (none, in this branch).
		return nil
	}

	discoveredCount := 0

	for _, install := range installations {
		discoveredCount += d.discoverInstallation(ctx, install)
	}

	d.logger.InfoContext(ctx, "discovery complete",
		"installations", len(installations),
		"discovered_new", discoveredCount,
		"duration", time.Since(start),
	)

	return nil
}

// discoverInstallation lists repos for one installation and upserts
// discovery rows for each. Returns the count of newly-created rows
// (excludes pre-existing rows). Best-effort: errors are logged and
// the installation is skipped.
func (d *Discoverer) discoverInstallation(ctx context.Context, install *ghclient.Installation) int {
	// Discovery is the authoritative source for the org↔installation
	// join (DESIGN-0022 E2): ListInstallations is the only API surface
	// that hands us both halves without an extra call. Set it before the
	// repo listing, so an installation whose repos we cannot enumerate
	// still resolves its ID on rate-limit and discovery-error panels —
	// exactly when an operator most needs to know whose org is stuck.
	metrics.SetInstallationInfo(install.ID, install.Account)

	repos, err := d.client.ListInstallationRepos(ctx, install.ID)

	d.observeAPICall(install.ID, "list_installation_repos")

	if err != nil {
		d.logger.WarnContext(ctx, "discovery: list installation repos failed",
			"installation_id", install.ID,
			"error", err,
		)

		return 0
	}

	created := 0

	for _, repo := range repos {
		if d.skipArchived && repo.Archived {
			continue
		}

		if d.skipForks && repo.Fork {
			continue
		}

		if d.upsertRepo(ctx, install.ID, repo.Owner, repo.Name) {
			created++
		}
	}

	return created
}

// upsertRepo calls Store.UpsertIfMissing for one (installation,
// repo) tuple with a jittered initial LastCheckedAt. Returns true
// when a new row was created.
func (d *Discoverer) upsertRepo(ctx context.Context, installationID int64, owner, repo string) bool {
	jitter := randomJitter(2 * d.freshness)
	seedTime := time.Now().UTC().Add(-jitter)

	state := &store.RepoState{
		InstallationID:  installationID,
		Owner:           owner,
		Repo:            repo,
		LastCheckedAt:   &seedTime,
		LastCheckStatus: store.StatusPending,
		PolicyVersion:   "", // empty → next sweep treats as drifted
	}

	created, err := d.store.UpsertIfMissing(ctx, state)
	if err != nil {
		// Fail-safe semantic: a Store error on read/upsert NEVER halts
		// the discoverer. The next sweep will catch the repo (either
		// via webhook discovery or the next Discover iteration).
		d.logger.WarnContext(ctx, "discovery: UpsertIfMissing failed",
			"installation_id", installationID,
			"owner", owner,
			"repo", repo,
			"error", err,
		)

		return false
	}

	if created {
		metrics.RepoDiscoveredTotal.
			WithLabelValues(strconv.FormatInt(installationID, 10)).
			Inc()

		d.logger.InfoContext(ctx, "discovered new repository",
			"installation_id", installationID,
			"owner", owner,
			"repo", repo,
		)
	}

	return created
}

// observeAPICall counts a GitHub API call against
// discovery_api_calls_total. installationID=0 is the
// list_installations endpoint (no installation context).
func (*Discoverer) observeAPICall(installationID int64, endpoint string) {
	label := "0"
	if installationID != 0 {
		label = strconv.FormatInt(installationID, 10)
	}

	metrics.DiscoveryAPICallsTotal.WithLabelValues(label, endpoint).Inc()
}
