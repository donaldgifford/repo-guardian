package activities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// Router is the RouteWebhook activity (DESIGN-0026 § Ingest and webhook
// routing). Ingest has already validated and filtered the delivery; the
// router upserts rows as needed, resolves the repository and signals the
// workflow that owns it.
type Router struct {
	store         Store
	client        client.Client
	taskQueue     string
	checkInterval time.Duration
	threshold     float64
	logger        *slog.Logger
}

// NewRouter returns the router. checkInterval seeds the RepoWorkflows it
// starts and threshold (RATE_LIMIT_THRESHOLD) the InstallationWorkflows.
func NewRouter(st Store, c client.Client, taskQueue string, checkInterval time.Duration, threshold float64, logger *slog.Logger) *Router {
	return &Router{store: st, client: c, taskQueue: taskQueue, checkInterval: checkInterval, threshold: threshold, logger: logger}
}

// Register registers RouteWebhook under its workflows package name.
func (r *Router) Register(reg Registry) {
	reg.RegisterActivityWithOptions(r.RouteWebhook, activity.RegisterOptions{Name: workflows.RouteWebhookActivity})
}

// RouteWebhook applies one delivery, per DESIGN-0026's event table. It
// is safe to retry: every step is an upsert, a park of an already-parked
// row, or a signal that coalesces.
func (r *Router) RouteWebhook(ctx context.Context, in *workflows.WebhookInput) error {
	log := r.logger.With("delivery", in.DeliveryID, "event", in.Event, "action", in.Action, "installation_id", in.InstallationID)

	switch in.Event + "." + in.Action {
	case "push.":
		return r.each(ctx, in, r.push)
	case "repository.created":
		return r.discover(ctx, in, workflows.TriggerWebhook, workflows.PriorityWebhook)
	case "repository.unarchived", "installation.created", "installation_repositories.added":
		return r.discover(ctx, in, workflows.TriggerDiscovery, workflows.PrioritySchedule)
	case "repository.renamed", "repository.transferred":
		// UpsertDiscovered matches by provider_repo_id and updates org,
		// name and installation; the repositories.id, and so the
		// workflow ID, is unchanged.
		return r.discover(ctx, in, workflows.TriggerWebhook, workflows.PriorityWebhook)
	case "repository.archived":
		// The check's archived skip parks it, the same path as v1.
		return r.each(ctx, in, r.recheckKnown)
	case "repository.deleted", "installation_repositories.removed":
		return r.each(ctx, in, r.parkRemoved)
	case "installation.deleted":
		return r.store.MarkInstallationRemoved(ctx, in.InstallationID, time.Now())
	case "installation.suspend", "installation.unsuspend":
		return r.suspend(ctx, in, in.Action == "suspend")
	default:
		log.Warn("webhook workflow for an event ingest should have dropped")

		return nil
	}
}

func (*Router) each(
	ctx context.Context,
	in *workflows.WebhookInput,
	fn func(context.Context, *workflows.WebhookInput, workflows.WebhookRepo) error,
) error {
	var errs []error

	for _, repo := range in.Repositories {
		if err := fn(ctx, in, repo); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", repo.Org, repo.Name, err))
		}
	}

	return errors.Join(errs...)
}

// discover upserts the installation and each repository (un-parking a
// parked one: discovery is the only un-parker) and rechecks it.
//
// TODO(IMPL-0025 P13): start the single-installation DiscoveryWorkflow
// for created/added/unarchived/unsuspended instead of discovering only
// the repositories in the payload.
func (r *Router) discover(ctx context.Context, in *workflows.WebhookInput, trigger string, p workflows.Priority) error {
	if in.AccountLogin != "" {
		inst := store.Installation{InstallationID: in.InstallationID, AccountLogin: in.AccountLogin}
		if err := r.store.UpsertInstallation(ctx, inst); err != nil {
			return err
		}
	}

	return r.each(ctx, in, func(ctx context.Context, in *workflows.WebhookInput, repo workflows.WebhookRepo) error {
		res, err := r.store.UpsertDiscovered(ctx, &store.DiscoveredRepo{
			Org: repo.Org, Name: repo.Name, ProviderRepoID: providerID(repo), InstallationID: in.InstallationID,
			NextDueAt: time.Now(),
		})
		if err != nil {
			return err
		}

		return r.recheck(ctx, res.ID, in.InstallationID, trigger, p)
	})
}

// push rechecks a known active repository; an unknown one is discovered
// first. A parked repository stays parked: a push is not discovery.
func (r *Router) push(ctx context.Context, in *workflows.WebhookInput, repo workflows.WebhookRepo) error {
	found, err := r.store.FindRepository(ctx, repo.Org, repo.Name, providerID(repo))

	switch {
	case errors.Is(err, store.ErrNotFound):
		one := &workflows.WebhookInput{InstallationID: in.InstallationID, Repositories: []workflows.WebhookRepo{repo}}

		return r.discover(ctx, one, workflows.TriggerPush, workflows.PriorityWebhook)
	case err != nil:
		return err
	case !found.Active:
		return nil
	default:
		return r.recheck(ctx, found.ID, found.InstallationID, workflows.TriggerPush, workflows.PriorityWebhook)
	}
}

// recheckKnown rechecks a known active repository and ignores the rest.
func (r *Router) recheckKnown(ctx context.Context, _ *workflows.WebhookInput, repo workflows.WebhookRepo) error {
	found, err := r.store.FindRepository(ctx, repo.Org, repo.Name, providerID(repo))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	if !found.Active {
		return nil
	}

	return r.recheck(ctx, found.ID, found.InstallationID, workflows.TriggerWebhook, workflows.PriorityWebhook)
}

// parkRemoved parks a deleted or removed repository through its
// workflow, which completes; with no running workflow it parks the row
// directly. Findings are kept: the repository is inactive, so it no
// longer counts, and its history stays readable.
func (r *Router) parkRemoved(ctx context.Context, _ *workflows.WebhookInput, repo workflows.WebhookRepo) error {
	found, err := r.store.FindRepository(ctx, repo.Org, repo.Name, providerID(repo))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	err = r.client.SignalWorkflow(ctx, workflows.RepoWorkflowID(found.ID), "", workflows.ParkSignal,
		workflows.Park{Reason: string(store.ParkRemoved)})

	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return r.store.Park(ctx, found.ID, store.ParkRemoved, false)
	}

	return err
}

// suspend records the suspension and tells the installation's budget, so
// every acquire waits until it lifts.
//
// TODO(IMPL-0025 P13): an unsuspend also starts single-installation
// discovery.
func (r *Router) suspend(ctx context.Context, in *workflows.WebhookInput, suspended bool) error {
	inst := store.Installation{InstallationID: in.InstallationID, AccountLogin: in.AccountLogin}
	if suspended {
		now := time.Now()
		inst.SuspendedAt = &now
	}

	if err := r.store.UpsertInstallation(ctx, inst); err != nil {
		return err
	}

	_, err := r.client.SignalWithStartWorkflow(ctx, workflows.InstallationWorkflowID(in.InstallationID),
		workflows.SuspendSignal, workflows.Suspend{Suspended: suspended},
		client.StartWorkflowOptions{
			ID:        workflows.InstallationWorkflowID(in.InstallationID),
			TaskQueue: r.taskQueue,
			Priority:  workflows.TaskPriority(workflows.PriorityWebhook, in.InstallationID),
		},
		workflows.InstallationWorkflowName,
		&workflows.InstallationWorkflowInput{InstallationID: in.InstallationID, Threshold: r.threshold, LeaseTTL: workflows.DefaultLeaseTTL})

	return err
}

// recheck signals repo/<id>, starting its RepoWorkflow if none runs.
func (r *Router) recheck(ctx context.Context, repoID, installationID int64, trigger string, p workflows.Priority) error {
	_, err := r.client.SignalWithStartWorkflow(ctx, workflows.RepoWorkflowID(repoID),
		workflows.RecheckSignal, workflows.Recheck{Trigger: trigger, Priority: p},
		client.StartWorkflowOptions{
			ID:        workflows.RepoWorkflowID(repoID),
			TaskQueue: r.taskQueue,
			Priority:  workflows.TaskPriority(p, installationID),
		},
		workflows.RepoWorkflowName, &workflows.RepoWorkflowInput{
			RepositoryID: repoID, InstallationID: installationID, CheckInterval: r.checkInterval,
			NextDue: time.Now().Add(r.checkInterval),
		})
	if err != nil {
		return fmt.Errorf("signal %s: %w", workflows.RepoWorkflowID(repoID), err)
	}

	return nil
}

func providerID(repo workflows.WebhookRepo) *int64 {
	if repo.ID == 0 {
		return nil
	}

	id := repo.ID

	return &id
}
