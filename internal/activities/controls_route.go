package activities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// ControlsRouteStore is the part of the controls store the controls
// routing uses. *postgres.ControlsStore satisfies it.
type ControlsRouteStore interface {
	UpsertInstallation(ctx context.Context, in *store.AppInstallation) error
	UpsertRepositories(ctx context.Context, repos []store.DiscoveredRepository, r store.Resolver) ([]store.ResolvedRepository, error)
	FindRepository(ctx context.Context, providerRepoID int64) (*store.RepositoryRef, error)
	RepositoryRef(ctx context.Context, repoID int64) (*store.RepositoryRef, error)
	OrgRepositories(ctx context.Context, org string) ([]int64, error)
	Resolve(ctx context.Context, repoID int64, r store.Resolver) (store.ResolutionChange, error)
	Park(ctx context.Context, repoID int64, reason string) error
	ParkInstallation(ctx context.Context, installationID int64, reason string) (int, error)
}

// ControlsRouter routes webhook deliveries on the controls line
// (DESIGN-0032 § Evaluation, IMPL-0029 task 5.7): the Evaluation App's
// deliveries drive discovery, resolution, parking and rechecks, and
// either App's installation changes re-resolve the repositories they
// touch, because a Remediation App change moves the effective mode.
type ControlsRouter struct {
	store     ControlsRouteStore
	policy    store.Resolver
	client    client.Client
	taskQueue string
	interval  time.Duration
	logger    *slog.Logger
}

var (
	_ ControlsRouting   = (*ControlsRouter)(nil)
	_ AppAccessRecorder = (*ControlsRouter)(nil)
)

// NewControlsRouter returns the controls routing. taskQueue is the
// evaluation queue and interval EVAL_INTERVAL.
func NewControlsRouter(
	st ControlsRouteStore, pol store.Resolver, c client.Client, taskQueue string, interval time.Duration, logger *slog.Logger,
) *ControlsRouter {
	return &ControlsRouter{store: st, policy: pol, client: c, taskQueue: taskQueue, interval: interval, logger: logger}
}

// RouteWebhook applies one Evaluation App delivery. Like Router's, every
// step is an upsert, a park of a parked row, or a signal that coalesces,
// so it is safe to retry.
func (r *ControlsRouter) RouteWebhook(ctx context.Context, in *workflows.WebhookInput) error {
	switch in.Event + "." + in.Action {
	case "push.":
		return r.each(ctx, in, r.push)
	case "repository.created", "repository.renamed", "repository.transferred", "repository.archived", "repository.unarchived":
		return r.discover(ctx, in)
	case "repository.deleted", "installation_repositories.removed":
		return r.each(ctx, in, r.parkRemoved)
	case "installation_repositories.added":
		if in.RepositorySelection == selectionAll && len(in.Repositories) <= payloadDiscoveryMax {
			return r.discover(ctx, in)
		}

		return r.startDiscovery(ctx, in)
	case "installation.created", "installation.unsuspend":
		if err := r.upsertInstallation(ctx, in, nil); err != nil {
			return err
		}

		return r.startDiscovery(ctx, in)
	case "installation.suspend":
		now := time.Now()

		return r.upsertInstallation(ctx, in, &now)
	case "installation.deleted":
		_, err := r.store.ParkInstallation(ctx, in.InstallationID, string(store.ParkInstallationRemoved))

		return err
	default:
		r.logger.Warn("controls webhook for an event ingest should have dropped", "event", in.Event, "action", in.Action)

		return nil
	}
}

// RecordAppAccess re-resolves the repositories a Remediation App
// installation change touches: the ones it names, or every repository
// of the account when it names none (an all-repositories installation).
// The Remediation App's own access rows are the remediator's to write
// (IMPL-0030); a repository whose effective mode moved is re-evaluated
// so its intent revision follows.
func (r *ControlsRouter) RecordAppAccess(ctx context.Context, a *AppAccess) error {
	ids, err := r.affected(ctx, a)
	if err != nil {
		return err
	}

	var errs []error

	for _, id := range ids {
		change, err := r.store.Resolve(ctx, id, r.policy)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		if len(change.Changed) == 0 && len(change.Withdrawn) == 0 {
			continue
		}

		ref, err := r.store.RepositoryRef(ctx, id)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		errs = append(errs, r.signalManaged(ctx, ref, workflows.PolicyChanged{Version: r.policy.Version(), By: time.Now()}))
	}

	return errors.Join(errs...)
}

func (r *ControlsRouter) affected(ctx context.Context, a *AppAccess) ([]int64, error) {
	if len(a.Repositories) == 0 {
		return r.store.OrgRepositories(ctx, a.AccountLogin)
	}

	var ids []int64

	for _, repo := range a.Repositories {
		ref, err := r.store.FindRepository(ctx, repo.ID)
		if err != nil {
			return nil, err
		}

		if ref != nil && ref.Active {
			ids = append(ids, ref.ID)
		}
	}

	return ids, nil
}

func (*ControlsRouter) each(
	ctx context.Context, in *workflows.WebhookInput, fn func(context.Context, *workflows.WebhookInput, workflows.WebhookRepo) error,
) error {
	var errs []error

	for _, repo := range in.Repositories {
		if err := fn(ctx, in, repo); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", repo.Org, repo.Name, err))
		}
	}

	return errors.Join(errs...)
}

// push rechecks a known, active, managed repository with the push's
// change set at webhook priority. An unknown repository is discovered;
// a parked one stays parked, because only discovery un-parks.
func (r *ControlsRouter) push(ctx context.Context, in *workflows.WebhookInput, repo workflows.WebhookRepo) error {
	ref, err := r.store.FindRepository(ctx, repo.ID)
	if err != nil {
		return err
	}

	if ref == nil {
		return r.discover(ctx, &workflows.WebhookInput{InstallationID: in.InstallationID, Repositories: []workflows.WebhookRepo{repo}})
	}

	if !ref.Active {
		return nil
	}

	return r.signalManaged(ctx, ref, workflows.Recheck{
		Trigger: workflows.TriggerPush, Priority: workflows.PriorityWebhook, Paths: in.Paths, Unknown: in.Unknown,
	})
}

// discover upserts the delivery's repositories, which matches renames
// and transfers by provider id, parks an archived or forked one (from
// the payload's flags) and resolves each, then rechecks every managed
// one in full: its assignments may be new.
func (r *ControlsRouter) discover(ctx context.Context, in *workflows.WebhookInput) error {
	batch := make([]store.DiscoveredRepository, 0, len(in.Repositories))
	for _, repo := range in.Repositories {
		batch = append(batch, store.DiscoveredRepository{
			Org: repo.Org, Name: repo.Name, ProviderRepoID: repo.ID, InstallationID: in.InstallationID, Archived: repo.Archived, Fork: repo.Fork,
		})
	}

	resolved, err := r.store.UpsertRepositories(ctx, batch, r.policy)
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}

	var errs []error

	for i := range resolved {
		ref := &store.RepositoryRef{ID: resolved[i].ID, Active: true, InstallationID: in.InstallationID, PolicyState: resolved[i].State}
		errs = append(errs, r.signalManaged(ctx, ref, workflows.Recheck{
			Trigger: workflows.TriggerWebhook, Priority: workflows.PriorityWebhook, Unknown: true,
		}))
	}

	return errors.Join(errs...)
}

func (r *ControlsRouter) parkRemoved(ctx context.Context, _ *workflows.WebhookInput, repo workflows.WebhookRepo) error {
	ref, err := r.store.FindRepository(ctx, repo.ID)
	if err != nil || ref == nil {
		return err
	}

	return r.store.Park(ctx, ref.ID, string(store.ParkRemoved))
}

// upsertInstallation refreshes the Evaluation App's installation row.
func (r *ControlsRouter) upsertInstallation(ctx context.Context, in *workflows.WebhookInput, suspendedAt *time.Time) error {
	row := &store.AppInstallation{
		App: store.AppEval, InstallationID: in.InstallationID, AccountLogin: in.AccountLogin,
		AccountType: in.AccountType, RepositorySelection: in.RepositorySelection, SuspendedAt: suspendedAt,
	}
	if row.AccountType == "" {
		row.AccountType = control.AccountOrganization
	}

	if row.RepositorySelection == "" {
		row.RepositorySelection = selectionAll
	}

	return r.store.UpsertInstallation(ctx, row)
}

// startDiscovery runs ControlsDiscoveryWorkflow for one installation,
// once per delivery.
func (r *ControlsRouter) startDiscovery(ctx context.Context, in *workflows.WebhookInput) error {
	id := workflows.ControlsDiscoveryWorkflowID(in.InstallationID, in.DeliveryID)

	_, err := r.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: id, TaskQueue: r.taskQueue, Priority: workflows.TaskPriority(workflows.PriorityWebhook, in.InstallationID),
	}, workflows.ControlsDiscoveryWorkflowName, &workflows.DiscoveryInput{InstallationID: in.InstallationID})
	if err != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(err) {
		return fmt.Errorf("start %s: %w", id, err)
	}

	return nil
}

// signalManaged SignalWithStarts the repository's EvaluationWorkflow
// when it is active, managed and reachable by the Evaluation App.
func (r *ControlsRouter) signalManaged(ctx context.Context, ref *store.RepositoryRef, signal any) error {
	if !ref.Active || ref.PolicyState != policy.StateManaged || ref.InstallationID == 0 {
		return nil
	}

	name, p := workflows.RecheckSignal, workflows.PriorityWebhook
	if _, ok := signal.(workflows.PolicyChanged); ok {
		name, p = workflows.PolicyChangedSignal, workflows.PriorityRollout
	}

	id := workflows.EvaluationWorkflowID(ref.ID)
	due := time.Now().Add(jitter(r.interval))

	_, err := r.client.SignalWithStartWorkflow(ctx, id, name, signal,
		client.StartWorkflowOptions{ID: id, TaskQueue: r.taskQueue, Priority: workflows.TaskPriority(p, ref.InstallationID)},
		workflows.EvaluationWorkflowName,
		&workflows.EvaluationWorkflowInput{RepositoryID: ref.ID, InstallationID: ref.InstallationID, Interval: r.interval, NextDue: due})
	if err != nil {
		return fmt.Errorf("signal %s: %w", id, err)
	}

	return nil
}
