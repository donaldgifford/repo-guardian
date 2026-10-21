package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// serviceStarter brings up a controls role's service workflows at
// startup (IMPL-0025 Phase 13, IMPL-0029 task 7.1, IMPL-0030 Phase 6).
// Every pod of the role runs it: each step is idempotent, and Temporal
// rejects the duplicate starts.
type serviceStarter struct {
	cfg       *config.Config
	client    client.Client
	taskQueue string
	logger    *slog.Logger
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

// legacyScheduleIDs are the rc's Schedules. Their workflows left the
// binary in IMPL-0029 Phase 7, so a namespace upgraded from an rc kept
// them firing runs no worker can take; the evaluator removes them.
var legacyScheduleIDs = []string{"discovery", "snapshot"}

// schedules returns the Schedules role owns: those to ensure and those
// to remove. The evaluator owns controls-discovery (removed when
// DISCOVERY_ENABLED=false), controls-snapshot and the removal of the
// rc's legacyScheduleIDs; the remediator owns
// remediation-sweep and controls-discovery-remediate (IMPL-0030 6.1,
// 6.4), the latter under the same DISCOVERY_ENABLED gate.
func (s *serviceStarter) schedules(role config.Role) (ensure []*temporal.Schedule, remove []string) {
	var discovery temporal.Schedule

	switch role {
	case config.RoleEvaluator:
		remove = append(remove, legacyScheduleIDs...)
		discovery = temporal.Schedule{
			ID: workflows.ControlsDiscoveryScheduleID, Workflow: workflows.ControlsDiscoveryWorkflowName,
			Args: []any{&workflows.DiscoveryInput{}},
		}
		ensure = append(ensure, s.schedule(&temporal.Schedule{
			ID: workflows.ControlsSnapshotScheduleID, Every: s.cfg.ComplianceSnapshotInterval, Workflow: workflows.ControlsSnapshotWorkflowName,
			Args: []any{&workflows.SnapshotInput{Retention: s.cfg.ChecksRetention}},
		}))
	case config.RoleRemediator:
		discovery = temporal.Schedule{ID: workflows.RemediationDiscoveryScheduleID, Workflow: workflows.RemediationDiscoveryWorkflowName}
		ensure = append(ensure, s.schedule(&temporal.Schedule{
			ID: workflows.RemediationSweepScheduleID, Every: remediationSweepInterval, Workflow: workflows.RemediationSweepWorkflowName,
			Args: []any{&workflows.RemediationSweepInput{
				SweepBatch: s.cfg.Remediation.SweepBatch, MaintenanceBatch: s.cfg.Remediation.MaintenanceBatch,
			}},
		}))
	default:
		return nil, nil
	}

	if !s.cfg.DiscoveryEnabled {
		return ensure, append(remove, discovery.ID)
	}

	discovery.Every = s.cfg.DiscoveryInterval

	return append([]*temporal.Schedule{s.schedule(&discovery)}, ensure...), remove
}

// remediationSweepInterval is the remediation-sweep cadence (IMPL-0030
// 6.1): a withdrawn control's PR closes, and an expired reservation's
// slot frees, within one interval.
const remediationSweepInterval = time.Hour

// schedule puts sc on the role's queue at the schedule priority.
func (s *serviceStarter) schedule(sc *temporal.Schedule) *temporal.Schedule {
	sc.TaskQueue = s.taskQueue
	sc.Priority = workflows.TaskPriority(workflows.PrioritySchedule, 0)

	return sc
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

// controlsActivation is what the evaluator's startup reads.
type controlsActivation interface {
	CurrentActivation(ctx context.Context) (version string, activatedAt time.Time, err error)
}

// startControlsRollout is the evaluator's bootstrap beside
// ensureSchedules(RoleEvaluator) (IMPL-0029 5.6). It
// starts controls-rollout/<version>/<activated_at> with
// REJECT_DUPLICATE, so however many pods start there is one rollout per
// activation, and a revert (a new activated_at) rolls out again. A
// pod whose loaded policy is not the activated one starts nothing: its
// resolution would be the wrong policy's, and a pod that has it will.
func (s *serviceStarter) startControlsRollout(ctx context.Context, st controlsActivation, policyVersion string) error {
	version, activatedAt, err := st.CurrentActivation(ctx)
	if err != nil {
		return fmt.Errorf("read current activation: %w", err)
	}

	if version != policyVersion {
		s.logger.Warn("loaded policy is not the activated one; not starting its rollout",
			"policy_version", policyVersion, "activated_version", version)

		return nil
	}

	s.logger.Info("starting controls rollout", "policy_version", version, "activated_at", activatedAt, "window", s.cfg.PolicyRolloutWindow)

	return s.startOnce(ctx, workflows.ControlsRolloutWorkflowID(version, activatedAt), enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		workflows.ControlsRolloutWorkflowName, &workflows.ControlsRolloutInput{Version: version, Window: s.cfg.PolicyRolloutWindow})
}
