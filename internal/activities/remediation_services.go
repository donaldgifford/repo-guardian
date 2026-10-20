package activities

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// RemediationServiceStore is what the remediator's service activities
// read and write; *postgres.RemediationStore satisfies it, as
// rg_remediator.
type RemediationServiceStore interface {
	RemediationStore
	DueRemediations(ctx context.Context, batch int) ([]store.DueRemediation, error)
	MaintenanceCandidates(ctx context.Context, batch int) ([]store.MaintenanceCandidate, error)
	ExpireReservations(ctx context.Context, now time.Time) ([]string, error)
	MaintenanceTarget(ctx context.Context, remediationID int64) (*store.MaintenanceTarget, error)
	RefreshRemediateAccess(ctx context.Context, inst *store.AppInstallation, providerRepoIDs []int64) ([]store.AccessChange, error)
	PruneRemediateInstallations(ctx context.Context, seen []int64) ([]store.AccessChange, error)
	RecordServiceRun(ctx context.Context, run *store.ServiceRun) error
}

// RemediationServicesConfig configures the remediator's service
// activities.
type RemediationServicesConfig struct {
	// Remediator is the remediation activities maintenance closes
	// through: the policy, the Remediation App's clients and the clock.
	Remediator *Remediator
	Store      RemediationServiceStore
	// GitHub is the Remediation App's client.
	GitHub Lister
	// Temporal starts RemediationWorkflows on RemediateQueue and hands
	// access changes to the evaluator on EvalQueue.
	Temporal       client.Client
	RemediateQueue string
	EvalQueue      string
	Logger         *slog.Logger
}

// RemediationServices are the activities behind the remediator's sweep,
// maintenance and discovery schedules (IMPL-0030 Phase 6), registered
// only on the remediation queue.
type RemediationServices struct {
	cfg     RemediationServicesConfig
	starter *RemediationStarter
}

// NewRemediationServices returns the remediator's service activities.
func NewRemediationServices(cfg *RemediationServicesConfig) *RemediationServices {
	return &RemediationServices{cfg: *cfg, starter: NewRemediationStarter(cfg.Temporal, cfg.RemediateQueue, nil)}
}

// Register registers the service activities under their workflows
// package names. RecordServiceRun shares the evaluator's name: a
// service workflow runs it on its own queue.
func (s *RemediationServices) Register(r Registry) {
	for name, fn := range map[string]any{
		workflows.RemediationSweepActivity:              s.Sweep,
		workflows.RemediationMaintainActivity:           s.Maintain,
		workflows.RemediationListInstallationsActivity:  s.ListInstallations,
		workflows.RemediationRefreshAccessActivity:      s.RefreshAccess,
		workflows.RemediationPruneInstallationsActivity: s.PruneInstallations,
		workflows.RemediationResolveAccessActivity:      s.ResolveAccess,
		workflows.ControlsRecordServiceRunActivity:      s.RecordServiceRun,
	} {
		r.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}

// Sweep starts up to batch due remediations at the rollout priority
// (IMPL-0030 6.1): the controls an evaluation left due, say because its
// start failed or the slot was full.
//
// PERF: one RemediationTarget read per due row for its installation;
// at the default batch of 500 that is one hourly round trip each.
func (s *RemediationServices) Sweep(ctx context.Context, batch int) (*workflows.SweepResult, error) {
	due, err := s.cfg.Store.DueRemediations(ctx, batch)
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}

	starts := make([]workflows.RemediationStart, 0, len(due))

	for _, d := range due {
		t, err := s.cfg.Store.RemediationTarget(ctx, d.RepositoryID, d.ControlID)
		if err != nil {
			return nil, fmt.Errorf("sweep: %w", err)
		}

		if t == nil || !remediates(t) {
			continue
		}

		starts = append(starts, workflows.RemediationStart{
			RepositoryID: d.RepositoryID, Control: d.ControlID, InstallationID: t.InstallationID, Priority: workflows.PriorityRollout,
		})
	}

	if err := s.starter.StartRemediations(ctx, starts); err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}

	return &workflows.SweepResult{Due: len(due), Started: len(starts)}, nil
}

// ListInstallations lists the Remediation App's installations.
func (s *RemediationServices) ListInstallations(ctx context.Context) ([]workflows.RemediateInstallation, error) {
	installs, err := s.cfg.GitHub.ListInstallations(ctx)
	metrics.DiscoveryAPICallsTotal.WithLabelValues("0", "list_installations").Inc()

	if err != nil {
		return nil, fmt.Errorf("list remediation app installations: %w", err)
	}

	out := make([]workflows.RemediateInstallation, 0, len(installs))

	for _, in := range installs {
		metrics.SetInstallationInfo(store.AppRemediate, in.ID, in.Account)

		inst := workflows.RemediateInstallation{
			ID: in.ID, Account: in.Account, AccountType: control.AccountOrganization, RepositorySelection: in.RepositorySelection,
		}
		if in.Personal {
			inst.AccountType = control.AccountUser
		}

		out = append(out, inst)
	}

	return out, nil
}

// RefreshAccess lists one installation's repositories, sets its
// app = 'remediate' rows to exactly those (IMPL-0030 6.4) and returns
// the repositories whose access appeared or went.
func (s *RemediationServices) RefreshAccess(ctx context.Context, in *workflows.RemediateInstallation) ([]workflows.WebhookRepo, error) {
	repos, err := s.cfg.GitHub.ListInstallationRepos(ctx, in.ID)
	metrics.DiscoveryAPICallsTotal.WithLabelValues(strconv.FormatInt(in.ID, 10), "list_installation_repos").Inc()

	if err != nil {
		return nil, fmt.Errorf("list repositories of remediation installation %d: %w", in.ID, err)
	}

	ids := make([]int64, 0, len(repos))
	for _, r := range repos {
		ids = append(ids, r.ID)
	}

	changes, err := s.cfg.Store.RefreshRemediateAccess(ctx, &store.AppInstallation{
		App: store.AppRemediate, InstallationID: in.ID, AccountLogin: in.Account, AccountType: in.AccountType,
		RepositorySelection: in.RepositorySelection,
	}, ids)
	if err != nil {
		return nil, fmt.Errorf("refresh remediation access of installation %d: %w", in.ID, err)
	}

	return accessRepos(changes), nil
}

// PruneInstallations deletes the Remediation App installations a
// complete listing did not see and returns the repositories that lost
// access with them.
func (s *RemediationServices) PruneInstallations(ctx context.Context, seen []int64) ([]workflows.WebhookRepo, error) {
	changes, err := s.cfg.Store.PruneRemediateInstallations(ctx, seen)
	if err != nil {
		return nil, fmt.Errorf("prune remediation installations: %w", err)
	}

	return accessRepos(changes), nil
}

// ResolveAccess hands changed repositories to the evaluator, which owns
// resolution: a WebhookWorkflow on the evaluation queue carrying the
// Remediation App access event RouteWebhook already re-resolves on
// (IMPL-0029's resolution trigger). The id is the calling run's, so a
// retry is the same start.
func (s *RemediationServices) ResolveAccess(ctx context.Context, in *workflows.ResolveAccessInput) error {
	id := "remediation-access/" + activity.GetInfo(ctx).WorkflowExecution.ID + "/" + strconv.FormatInt(in.InstallationID, 10)

	_, err := s.cfg.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: id, TaskQueue: s.cfg.EvalQueue,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		Priority:              workflows.TaskPriority(workflows.PrioritySchedule, in.InstallationID),
	}, workflows.WebhookWorkflowName, &workflows.WebhookInput{
		App: workflows.AppRemediate, DeliveryID: id, Event: eventInstallationRepositories, Action: actionAdded,
		InstallationID: in.InstallationID, AccountLogin: in.AccountLogin, Repositories: in.Repos,
	})
	if err != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(err) {
		return fmt.Errorf("start %s: %w", id, err)
	}

	return nil
}

// RecordServiceRun writes a service_runs row.
func (s *RemediationServices) RecordServiceRun(ctx context.Context, run *workflows.ServiceRun) error {
	if err := s.cfg.Store.RecordServiceRun(ctx, &store.ServiceRun{
		Kind: store.ServiceRunKind(run.Kind), Success: run.Success, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Detail: run.Detail,
	}); err != nil {
		return fmt.Errorf("record service run: %w", err)
	}

	return nil
}

func accessRepos(changes []store.AccessChange) []workflows.WebhookRepo {
	out := make([]workflows.WebhookRepo, 0, len(changes))
	for _, c := range changes {
		out = append(out, workflows.WebhookRepo{ID: c.ProviderRepoID, Org: c.Org, Name: c.Name})
	}

	return out
}
