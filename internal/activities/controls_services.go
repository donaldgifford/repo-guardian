package activities

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// ControlsServiceStore is the part of the controls store the evaluator's
// service activities use. *postgres.ControlsStore satisfies it.
type ControlsServiceStore interface {
	UpsertInstallation(ctx context.Context, in *store.AppInstallation) error
	UpsertRepositories(ctx context.Context, repos []store.DiscoveredRepository, r store.Resolver) ([]store.ResolvedRepository, error)
	ParkMissing(ctx context.Context, installationID int64, seen []int64) (int, error)
	RecordServiceRun(ctx context.Context, run *store.ServiceRun) error
	Resolve(ctx context.Context, repoID int64, r store.Resolver) (store.ResolutionChange, error)
	ActiveRepositories(ctx context.Context, afterID int64, limit int) ([]store.RepositoryRef, error)
	CompletePolicyRollout(ctx context.Context, version string) error
}

// ControlsServicesConfig configures the evaluator's service activities.
type ControlsServicesConfig struct {
	Store ControlsServiceStore
	// GitHub is the Evaluation App's client.
	GitHub Lister
	// Policy resolves each discovered repository inline.
	Policy store.Resolver
	// Temporal starts EvaluationWorkflows on TaskQueue, the evaluation
	// queue.
	Temporal  client.Client
	TaskQueue string
	// Interval is EVAL_INTERVAL: it seeds new EvaluationWorkflows and
	// spreads first evaluations across one interval.
	Interval time.Duration
	Logger   *slog.Logger
}

// ControlsServices are the activities behind the evaluator's discovery,
// rollout and snapshot workflows, registered on the evaluation queue.
type ControlsServices struct {
	cfg ControlsServicesConfig
}

// NewControlsServices returns the evaluator's service activities.
func NewControlsServices(cfg *ControlsServicesConfig) *ControlsServices {
	return &ControlsServices{cfg: *cfg}
}

// Register registers the service activities under their workflows
// package names.
func (s *ControlsServices) Register(r Registry) {
	for name, fn := range map[string]any{
		workflows.ControlsListInstallationsActivity: s.ListInstallations,
		workflows.ControlsListRepositoriesActivity:  s.ListRepositories,
		workflows.ControlsUpsertActivity:            s.UpsertRepositories,
		workflows.ControlsParkMissingActivity:       s.ParkMissing,
		workflows.ControlsRecordServiceRunActivity:  s.RecordServiceRun,
		workflows.ControlsResolveAllActivity:        s.ResolveAll,
		workflows.ControlsSignalAllActivity:         s.SignalAll,
		workflows.ControlsCompleteRolloutActivity:   s.CompleteRollout,
	} {
		r.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}

// ListInstallations lists the Evaluation App's installations and
// refreshes each one's app = 'eval' row.
func (s *ControlsServices) ListInstallations(ctx context.Context) ([]workflows.InstallationRef, error) {
	installs, err := s.cfg.GitHub.ListInstallations(ctx)
	metrics.DiscoveryAPICallsTotal.WithLabelValues("0", "list_installations").Inc()

	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}

	refs := make([]workflows.InstallationRef, 0, len(installs))

	for _, in := range installs {
		metrics.SetInstallationInfo(store.AppEval, in.ID, in.Account)

		row := &store.AppInstallation{
			App: store.AppEval, InstallationID: in.ID, AccountLogin: in.Account, AccountType: control.AccountOrganization,
			RepositorySelection: in.RepositorySelection,
		}
		if in.Personal {
			row.AccountType = control.AccountUser
		}

		if err := s.cfg.Store.UpsertInstallation(ctx, row); err != nil {
			return nil, fmt.Errorf("upsert installation %d: %w", in.ID, err)
		}

		refs = append(refs, workflows.InstallationRef{ID: in.ID, Account: in.Account})
	}

	return refs, nil
}

// ListRepositories lists an installation's repositories, archived and
// forked ones included: resolution parks those.
func (s *ControlsServices) ListRepositories(ctx context.Context, installationID int64) ([]workflows.DiscoveredRepo, error) {
	repos, err := s.cfg.GitHub.ListInstallationRepos(ctx, installationID)
	metrics.DiscoveryAPICallsTotal.WithLabelValues(strconv.FormatInt(installationID, 10), "list_installation_repos").Inc()

	if err != nil {
		return nil, fmt.Errorf("list repositories of installation %d: %w", installationID, err)
	}

	out := make([]workflows.DiscoveredRepo, 0, len(repos))
	for _, r := range repos {
		out = append(out, workflows.DiscoveredRepo{ID: r.ID, Org: r.Owner, Name: r.Name, Archived: r.Archived, Fork: r.Fork})
	}

	return out, nil
}

// UpsertRepositories upserts and resolves one batch in one transaction,
// then SignalWithStarts the EvaluationWorkflow of each new or un-parked
// managed repository with policy_changed, due at a random point in one
// interval so an onboarded fleet spreads out.
func (s *ControlsServices) UpsertRepositories(ctx context.Context, in *workflows.ControlsUpsertInput) (*workflows.UpsertRepositoriesResult, error) {
	batch := make([]store.DiscoveredRepository, 0, len(in.Repos))
	for _, r := range in.Repos {
		batch = append(batch, store.DiscoveredRepository{
			Org: r.Org, Name: r.Name, ProviderRepoID: r.ID, InstallationID: in.InstallationID, Archived: r.Archived, Fork: r.Fork,
		})
	}

	resolved, err := s.cfg.Store.UpsertRepositories(ctx, batch, s.cfg.Policy)
	if err != nil {
		return nil, fmt.Errorf("upsert installation %d batch: %w", in.InstallationID, err)
	}

	res := &workflows.UpsertRepositoriesResult{IDs: make([]int64, 0, len(resolved))}

	for i := range resolved {
		r := &resolved[i]
		res.IDs = append(res.IDs, r.ID)

		if r.Created {
			metrics.RepoDiscoveredTotal.WithLabelValues(strconv.FormatInt(in.InstallationID, 10)).Inc()
		}

		if (!r.Created && !r.Unparked) || r.State != policy.StateManaged {
			continue
		}

		due := time.Now().Add(jitter(s.cfg.Interval))
		if err := s.startEvaluation(ctx, r.ID, in.InstallationID, due, s.cfg.Policy.Version(), workflows.PrioritySchedule); err != nil {
			return nil, err
		}

		res.Started++
	}

	return res, nil
}

// startEvaluation SignalWithStarts evaluation/<id> with policy_changed
// by due, so an evaluation is never forced immediately.
func (s *ControlsServices) startEvaluation(
	ctx context.Context, repoID, installationID int64, due time.Time, version string, p workflows.Priority,
) error {
	id := workflows.EvaluationWorkflowID(repoID)

	_, err := s.cfg.Temporal.SignalWithStartWorkflow(ctx, id, workflows.PolicyChangedSignal,
		workflows.PolicyChanged{Version: version, By: due},
		client.StartWorkflowOptions{ID: id, TaskQueue: s.cfg.TaskQueue, Priority: workflows.TaskPriority(p, installationID)},
		workflows.EvaluationWorkflowName,
		&workflows.EvaluationWorkflowInput{RepositoryID: repoID, InstallationID: installationID, Interval: s.cfg.Interval, NextDue: due})
	if err != nil {
		return fmt.Errorf("signal %s: %w", id, err)
	}

	return nil
}

// ParkMissing parks removed the installation's active repositories a
// complete listing did not see, and returns how many.
func (s *ControlsServices) ParkMissing(ctx context.Context, in *workflows.ParkMissingInput) (int, error) {
	n, err := s.cfg.Store.ParkMissing(ctx, in.InstallationID, in.Seen)
	if err != nil {
		return 0, fmt.Errorf("park missing: %w", err)
	}

	return n, nil
}

// RecordServiceRun writes a service_runs row to the controls database.
func (s *ControlsServices) RecordServiceRun(ctx context.Context, run *workflows.ServiceRun) error {
	if err := s.cfg.Store.RecordServiceRun(ctx, &store.ServiceRun{
		Kind: store.ServiceRunKind(run.Kind), Success: run.Success, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Detail: run.Detail,
	}); err != nil {
		return fmt.Errorf("record service run: %w", err)
	}

	return nil
}

// ResolveAll re-resolves one page of active repositories under the
// loaded policy. A worker whose policy is not the rollout's refuses, so
// the retry lands on one that has it.
func (s *ControlsServices) ResolveAll(ctx context.Context, in *workflows.ControlsPageInput) (*workflows.ControlsPageResult, error) {
	if v := s.cfg.Policy.Version(); v != in.Version {
		return nil, fmt.Errorf("rollout of %s cannot resolve under the loaded policy %s", in.Version, v)
	}

	return s.page(ctx, in, func(r *store.RepositoryRef) (bool, error) {
		if _, err := s.cfg.Store.Resolve(ctx, r.ID, s.cfg.Policy); err != nil {
			return false, fmt.Errorf("resolve repository %d: %w", r.ID, err)
		}

		return true, nil
	})
}

// SignalAll SignalWithStarts policy_changed by in.By on one page of
// managed repositories the Evaluation App can reach.
func (s *ControlsServices) SignalAll(ctx context.Context, in *workflows.ControlsPageInput) (*workflows.ControlsPageResult, error) {
	return s.page(ctx, in, func(r *store.RepositoryRef) (bool, error) {
		if r.PolicyState != policy.StateManaged || r.InstallationID == 0 {
			return false, nil
		}

		return true, s.startEvaluation(ctx, r.ID, r.InstallationID, in.By, in.Version, workflows.PriorityRollout)
	})
}

// page applies fn to one page of active repositories and counts the
// ones it reports.
func (s *ControlsServices) page(
	ctx context.Context, in *workflows.ControlsPageInput, fn func(*store.RepositoryRef) (bool, error),
) (*workflows.ControlsPageResult, error) {
	refs, err := s.cfg.Store.ActiveRepositories(ctx, in.AfterID, in.Limit)
	if err != nil {
		return nil, fmt.Errorf("page after %d: %w", in.AfterID, err)
	}

	res := &workflows.ControlsPageResult{NextAfterID: in.AfterID, Done: len(refs) < in.Limit}

	for i := range refs {
		counted, err := fn(&refs[i])
		if err != nil {
			return nil, err
		}

		if counted {
			res.Count++
		}

		res.NextAfterID = refs[i].ID
	}

	return res, nil
}

// CompleteRollout stamps the version's rollout complete.
func (s *ControlsServices) CompleteRollout(ctx context.Context, version string) error {
	if err := s.cfg.Store.CompletePolicyRollout(ctx, version); err != nil {
		return fmt.Errorf("complete rollout: %w", err)
	}

	return nil
}
