package activities

import (
	"context"
	"errors"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// DueReader reads one repository's remediation_due rows.
// *postgres.ControlsStore satisfies it.
type DueReader interface {
	DueRemediationsFor(ctx context.Context, repoID int64) ([]store.DueRemediation, error)
}

// RemediationStarter is the StartRemediations activity (IMPL-0028 task
// 4.7). The Go SDK has no workflow-side signal-with-start, so an
// evaluation hands every due remediation to this one activity, which
// keeps the evaluation's history at one event pair however many
// controls are due (INV-0022 F4). Registered on the evaluator worker;
// IMPL-0029's EvaluationWorkflow is its caller.
type RemediationStarter struct {
	client    client.Client
	taskQueue string
	due       DueReader
}

// NewRemediationStarter returns the activities, starting workflows on
// taskQueue, the remediation queue, and reading due controls from due.
func NewRemediationStarter(c client.Client, taskQueue string, due DueReader) *RemediationStarter {
	return &RemediationStarter{client: c, taskQueue: taskQueue, due: due}
}

// Register registers StartRemediations and StartDueRemediations under
// their workflows package names.
func (s *RemediationStarter) Register(reg Registry) {
	reg.RegisterActivityWithOptions(s.StartRemediations, activity.RegisterOptions{Name: workflows.StartRemediationsActivity})
	reg.RegisterActivityWithOptions(s.StartDueRemediations, activity.RegisterOptions{Name: workflows.StartDueRemediationsActivity})
}

// StartDueRemediations reads remediation_due for the repository an
// evaluation just recorded and starts each due control at priority 3,
// in one activity however many are due (DESIGN-0032 A1). A no-change
// evaluation finds nothing due and starts nothing.
func (s *RemediationStarter) StartDueRemediations(
	ctx context.Context, in *workflows.StartDueRemediationsInput,
) (*workflows.StartDueRemediationsResult, error) {
	due, err := s.due.DueRemediationsFor(ctx, in.RepositoryID)
	if err != nil {
		return nil, fmt.Errorf("start due remediations: %w", err)
	}

	starts := make([]workflows.RemediationStart, 0, len(due))
	for _, d := range due {
		starts = append(starts, workflows.RemediationStart{
			RepositoryID: d.RepositoryID, Control: d.ControlID, InstallationID: in.InstallationID, Priority: workflows.PrioritySchedule,
		})
	}

	if err := s.StartRemediations(ctx, starts); err != nil {
		return nil, err
	}

	return &workflows.StartDueRemediationsResult{Started: len(starts)}, nil
}

// StartRemediations signal-with-starts remediation/<repository>/<control>
// for each entry. A running remediation takes the signal as "loop once
// more"; a finished one starts afresh. Every entry is attempted; the
// failures are joined, and a retry re-signals the ones that succeeded,
// which the workflow coalesces.
func (s *RemediationStarter) StartRemediations(ctx context.Context, starts []workflows.RemediationStart) error {
	var errs []error

	for _, st := range starts {
		id := workflows.RemediationWorkflowID(st.RepositoryID, st.Control)

		_, err := s.client.SignalWithStartWorkflow(ctx, id, workflows.RemediateSignal, workflows.Remediate{},
			client.StartWorkflowOptions{
				ID:        id,
				TaskQueue: s.taskQueue,
				Priority:  workflows.TaskPriority(st.Priority, st.InstallationID),
				// A finished remediation starts afresh on the next signal.
				WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
			},
			workflows.RemediationWorkflowName,
			&workflows.RemediationInput{RepositoryID: st.RepositoryID, Control: st.Control, InstallationID: st.InstallationID})
		if err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", id, err))
		}
	}

	return errors.Join(errs...)
}
