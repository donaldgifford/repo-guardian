package activities

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// RemediationStarter is the StartRemediations activity (IMPL-0028 task
// 4.7). The Go SDK has no workflow-side signal-with-start, so an
// evaluation hands every due remediation to this one activity, which
// keeps the evaluation's history at one event pair however many
// controls are due (INV-0022 F4). Registered on the evaluator worker;
// IMPL-0029's EvaluationWorkflow is its caller.
type RemediationStarter struct {
	client    client.Client
	taskQueue string
}

// NewRemediationStarter returns the activity, starting workflows on
// taskQueue, the remediation queue.
func NewRemediationStarter(c client.Client, taskQueue string) *RemediationStarter {
	return &RemediationStarter{client: c, taskQueue: taskQueue}
}

// Register registers StartRemediations under its workflows package name.
func (s *RemediationStarter) Register(reg Registry) {
	reg.RegisterActivityWithOptions(s.StartRemediations, activity.RegisterOptions{Name: workflows.StartRemediationsActivity})
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
			},
			workflows.RemediationWorkflowName,
			&workflows.RemediationInput{RepositoryID: st.RepositoryID, Control: st.Control, InstallationID: st.InstallationID})
		if err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", id, err))
		}
	}

	return errors.Join(errs...)
}
