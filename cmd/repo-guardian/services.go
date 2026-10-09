package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// serviceStore is what worker startup reads and writes.
type serviceStore interface {
	RecordPolicyVersion(ctx context.Context, version string, summary store.PolicySummary) (bool, error)
	BootstrapPending(ctx context.Context) (bool, error)
}

// serviceStarter brings up the worker's service workflows at startup
// (IMPL-0025 Phase 13). Every worker pod runs it: each step is
// idempotent, and Temporal rejects the duplicate starts.
type serviceStarter struct {
	cfg       *config.Config
	client    client.Client
	store     serviceStore
	taskQueue string
	logger    *slog.Logger
}

func (s *serviceStarter) start(ctx context.Context, policyVersion string, summary store.PolicySummary) error {
	if err := s.ensureSchedules(ctx, config.RoleWorker); err != nil {
		return err
	}

	if err := s.startRollout(ctx, policyVersion, summary); err != nil {
		return err
	}

	return s.startBootstrap(ctx)
}

// ensureSchedules reconciles role's Schedules with the configuration
// and touches no other role's, so pods of one role never create the
// other's (IMPL-0028 task 4.4).
func (s *serviceStarter) ensureSchedules(ctx context.Context, role config.Role) error {
	ensure, remove := s.schedules(role)

	for _, id := range remove {
		if err := temporal.RemoveSchedule(ctx, s.client, id); err != nil {
			return err
		}
	}

	for _, sc := range ensure {
		if err := temporal.EnsureSchedule(ctx, s.client, sc); err != nil {
			return err
		}
	}

	return nil
}

// schedules returns the Schedules role owns: those to ensure and those
// to remove. The rc worker owns discovery (removed when
// DISCOVERY_ENABLED=false) and snapshot. The evaluator will own
// controls discovery and snapshot (IMPL-0029) and the remediator sweep
// and maintenance (IMPL-0030); neither has a workflow to schedule yet.
func (s *serviceStarter) schedules(role config.Role) (ensure []*temporal.Schedule, remove []string) {
	if role != config.RoleWorker {
		return nil, nil
	}

	if s.cfg.DiscoveryEnabled {
		ensure = append(ensure, &temporal.Schedule{
			ID: workflows.DiscoveryScheduleID, Every: s.cfg.DiscoveryInterval, Workflow: workflows.DiscoveryWorkflowName,
			Args: []any{&workflows.DiscoveryInput{}}, TaskQueue: s.taskQueue,
			Priority: workflows.TaskPriority(workflows.PrioritySchedule, 0),
		})
	} else {
		remove = append(remove, workflows.DiscoveryScheduleID)
	}

	ensure = append(ensure, &temporal.Schedule{
		ID: workflows.SnapshotScheduleID, Every: s.cfg.ComplianceSnapshotInterval, Workflow: workflows.SnapshotWorkflowName,
		Args: []any{&workflows.SnapshotInput{Retention: s.cfg.ChecksRetention}}, TaskQueue: s.taskQueue,
		Priority: workflows.TaskPriority(workflows.PrioritySchedule, 0),
	})

	return ensure, remove
}

// worker sees it, starts policy-rollout/<version>.
func (s *serviceStarter) startRollout(ctx context.Context, version string, summary store.PolicySummary) error {
	first, err := s.store.RecordPolicyVersion(ctx, version, summary)
	if err != nil {
		return fmt.Errorf("record policy version: %w", err)
	}

	if !first {
		return nil
	}

	s.logger.Info("new policy version: starting rollout", "policy_version", version, "window", s.cfg.PolicyRolloutWindow)

	return s.startOnce(ctx, workflows.PolicyRolloutWorkflowID(version), enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		workflows.PolicyRolloutWorkflowName, &workflows.PolicyRolloutInput{Version: version, Window: s.cfg.PolicyRolloutWindow})
}

// startBootstrap starts bootstrap/v1 while migrate's flag is set
// (IMPL-0025 OQ15). The workflow clears the flag when it completes.
func (s *serviceStarter) startBootstrap(ctx context.Context) error {
	pending, err := s.store.BootstrapPending(ctx)
	if err != nil {
		return fmt.Errorf("read bootstrap flag: %w", err)
	}

	if !pending {
		return nil
	}

	s.logger.Info("v1 backfill found: starting bootstrap")

	return s.startOnce(ctx, workflows.BootstrapWorkflowID, enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		workflows.BootstrapWorkflowName)
}

// startOnce starts a workflow; one already running is success.
func (s *serviceStarter) startOnce(ctx context.Context, id string, reuse enumspb.WorkflowIdReusePolicy, name string, args ...any) error {
	_, err := s.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: id, TaskQueue: s.taskQueue, WorkflowIDReusePolicy: reuse,
		Priority: workflows.TaskPriority(workflows.PriorityRollout, 0),
	}, name, args...)

	var started *serviceerror.WorkflowExecutionAlreadyStarted
	if err != nil && !errors.As(err, &started) {
		return fmt.Errorf("start %s: %w", id, err)
	}

	return nil
}
