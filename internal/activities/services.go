package activities

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// Lister lists the App's installations and an installation's
// repositories. The app client (ghclient.Client) satisfies it.
type Lister interface {
	ListInstallations(ctx context.Context) ([]*ghclient.Installation, error)
	ListInstallationRepos(ctx context.Context, installationID int64) ([]*ghclient.Repository, error)
}

// ServicesConfig configures the service activities.
type ServicesConfig struct {
	Store     Store
	GitHub    Lister
	Temporal  client.Client
	TaskQueue string

	// CheckInterval seeds new RepoWorkflows and spreads discovered
	// repositories' first checks across one interval.
	CheckInterval time.Duration
	// PolicyVersion is the worker's v2 policy version.
	PolicyVersion string

	// SkipArchived and SkipForks are the policy's guardian skip_archived
	// and skip_forks. The check path parks exactly these, and discovery
	// must filter the same fields or it would un-park what the check
	// parks every pass (INV-0015's subset invariant).
	SkipArchived bool
	SkipForks    bool

	Logger *slog.Logger
}

// Services are the activities behind the discovery, snapshot, policy
// rollout and bootstrap workflows (DESIGN-0026 § Discovery onwards).
type Services struct {
	cfg ServicesConfig
}

// NewServices returns the service activities.
func NewServices(cfg *ServicesConfig) *Services {
	return &Services{cfg: *cfg}
}

// Register registers the service activities under their workflows
// package names.
func (s *Services) Register(r Registry) {
	for name, fn := range map[string]any{
		workflows.ListInstallationsActivity:     s.ListInstallations,
		workflows.ListRepositoriesActivity:      s.ListRepositories,
		workflows.UpsertRepositoriesActivity:    s.UpsertRepositories,
		workflows.ParkMissingActivity:           s.ParkMissing,
		workflows.SignalRepositoriesActivity:    s.SignalRepositories,
		workflows.SnapshotActivity:              s.InsertComplianceSnapshot,
		workflows.PruneChecksActivity:           s.PruneChecks,
		workflows.CompletePolicyRolloutActivity: s.CompletePolicyRollout,
		workflows.ClearBootstrapActivity:        s.ClearBootstrapPending,
		workflows.RecordServiceRunActivity:      s.RecordServiceRun,
	} {
		r.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}

// ListInstallations lists the App's installations and upserts each.
func (s *Services) ListInstallations(ctx context.Context) ([]workflows.InstallationRef, error) {
	installs, err := s.cfg.GitHub.ListInstallations(ctx)
	metrics.DiscoveryAPICallsTotal.WithLabelValues("0", "list_installations").Inc()

	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}

	refs := make([]workflows.InstallationRef, 0, len(installs))

	for _, in := range installs {
		metrics.SetInstallationInfo(in.ID, in.Account)

		if err := s.cfg.Store.UpsertInstallation(ctx, store.Installation{InstallationID: in.ID, AccountLogin: in.Account}); err != nil {
			return nil, err
		}

		refs = append(refs, workflows.InstallationRef{ID: in.ID, Account: in.Account})
	}

	return refs, nil
}

// ListRepositories lists an installation's repositories without the
// archived and forked ones the policy skips.
func (s *Services) ListRepositories(ctx context.Context, installationID int64) ([]workflows.WebhookRepo, error) {
	repos, err := s.cfg.GitHub.ListInstallationRepos(ctx, installationID)
	metrics.DiscoveryAPICallsTotal.WithLabelValues(strconv.FormatInt(installationID, 10), "list_installation_repos").Inc()

	if err != nil {
		return nil, fmt.Errorf("list repositories of installation %d: %w", installationID, err)
	}

	out := make([]workflows.WebhookRepo, 0, len(repos))

	for _, r := range repos {
		if s.skip(r) {
			continue
		}

		out = append(out, workflows.WebhookRepo{ID: r.ID, Org: r.Owner, Name: r.Name})
	}

	return out, nil
}

// skip is discovery's filter. It must stay a superset of the check
// path's durable skips (checker.skipReason): see ServicesConfig.
func (s *Services) skip(r *ghclient.Repository) bool {
	return (s.cfg.SkipArchived && r.Archived) || (s.cfg.SkipForks && r.Fork)
}

// UpsertRepositories upserts one batch and starts a RepoWorkflow for
// each new or reactivated repository, due at a random point in one
// CHECK_INTERVAL so an onboarded fleet spreads out.
func (s *Services) UpsertRepositories(ctx context.Context, in *workflows.UpsertRepositoriesInput) (*workflows.UpsertRepositoriesResult, error) {
	res := &workflows.UpsertRepositoriesResult{IDs: make([]int64, 0, len(in.Repos))}

	for _, repo := range in.Repos {
		due := time.Now().Add(jitter(s.cfg.CheckInterval))

		up, err := s.cfg.Store.UpsertDiscovered(ctx, &store.DiscoveredRepo{
			Org: repo.Org, Name: repo.Name, ProviderRepoID: providerID(repo), InstallationID: in.InstallationID, NextDueAt: due,
		})
		if err != nil {
			return nil, fmt.Errorf("upsert %s/%s: %w", repo.Org, repo.Name, err)
		}

		res.IDs = append(res.IDs, up.ID)

		if up.Created {
			metrics.RepoDiscoveredTotal.WithLabelValues(strconv.FormatInt(in.InstallationID, 10)).Inc()
		}

		if !up.Created && !up.Reactivated {
			continue
		}

		err = startRepo(ctx, s.cfg.Temporal, s.cfg.TaskQueue, &workflows.RepoWorkflowInput{
			RepositoryID: up.ID, InstallationID: in.InstallationID, CheckInterval: s.cfg.CheckInterval, NextDue: due,
		}, workflows.PolicyChanged{Version: s.cfg.PolicyVersion, By: due}, workflows.PrioritySchedule)
		if err != nil {
			return nil, err
		}

		res.Started++
	}

	return res, nil
}

// ParkMissing parks removed every active repository of the installation
// that a complete listing did not see. It returns how many it parked.
func (s *Services) ParkMissing(ctx context.Context, in *workflows.ParkMissingInput) (int, error) {
	seen := make(map[int64]bool, len(in.Seen))
	for _, id := range in.Seen {
		seen[id] = true
	}

	parked := 0

	err := s.eachActive(ctx, 0, func(r *store.Repository) error {
		if r.InstallationID != in.InstallationID || seen[r.ID] {
			return nil
		}

		parked++

		return parkRepository(ctx, s.cfg.Temporal, s.cfg.Store, r.ID, store.ParkRemoved)
	})

	return parked, err
}

// SignalRepositories SignalWithStarts policy_changed on one page of
// active repositories (see workflows.SignalRepositoriesInput).
func (s *Services) SignalRepositories(ctx context.Context, in *workflows.SignalRepositoriesInput) (*workflows.SignalRepositoriesResult, error) {
	repos, err := s.cfg.Store.ListActiveRepositories(ctx, in.AfterID, in.Limit)
	if err != nil {
		return nil, err
	}

	res := &workflows.SignalRepositoriesResult{Done: len(repos) < in.Limit}

	for i := range repos {
		r := &repos[i]
		res.NextAfterID = r.ID

		if in.DriftedOnly && r.PolicyVersion == in.Version {
			continue
		}

		due, signal := in.By, workflows.PolicyChanged{Version: in.Version, By: in.By}
		if r.NextDueAt != nil {
			due = *r.NextDueAt
		}

		if in.Bootstrap {
			signal = workflows.PolicyChanged{Version: r.PolicyVersion, By: due}
		}

		if err := startRepo(ctx, s.cfg.Temporal, s.cfg.TaskQueue, &workflows.RepoWorkflowInput{
			RepositoryID: r.ID, InstallationID: r.InstallationID, CheckInterval: s.cfg.CheckInterval,
			NextDue: due, PolicyVersion: r.PolicyVersion,
		}, signal, workflows.PriorityRollout); err != nil {
			return nil, err
		}

		res.Signalled++
	}

	return res, nil
}

// InsertComplianceSnapshot writes the snapshot at at.
func (s *Services) InsertComplianceSnapshot(ctx context.Context, at time.Time) (int, error) {
	return s.cfg.Store.InsertComplianceSnapshot(ctx, at)
}

// PruneChecks deletes checks that finished before before.
func (s *Services) PruneChecks(ctx context.Context, before time.Time) (int64, error) {
	return s.cfg.Store.PruneChecks(ctx, before)
}

// CompletePolicyRollout marks version's rollout complete.
func (s *Services) CompletePolicyRollout(ctx context.Context, version string) error {
	return s.cfg.Store.CompletePolicyRollout(ctx, version)
}

// ClearBootstrapPending clears migrate's bootstrap flag.
func (s *Services) ClearBootstrapPending(ctx context.Context) error {
	return s.cfg.Store.ClearBootstrapPending(ctx)
}

// RecordServiceRun writes a service_runs row.
func (s *Services) RecordServiceRun(ctx context.Context, run *workflows.ServiceRun) error {
	return s.cfg.Store.RecordServiceRun(ctx, &store.ServiceRun{
		Kind: store.ServiceRunKind(run.Kind), Success: run.Success, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Detail: run.Detail,
	})
}

// eachActive calls fn for every active repository after afterID.
func (s *Services) eachActive(ctx context.Context, afterID int64, fn func(*store.Repository) error) error {
	const page = 500

	for {
		repos, err := s.cfg.Store.ListActiveRepositories(ctx, afterID, page)
		if err != nil {
			return err
		}

		for i := range repos {
			if err := fn(&repos[i]); err != nil {
				return err
			}

			afterID = repos[i].ID
		}

		if len(repos) < page {
			return nil
		}
	}
}
