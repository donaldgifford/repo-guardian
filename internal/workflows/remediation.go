package workflows

import (
	"strconv"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// maxRemediationLoops bounds history: RemediationWorkflow continues as
// new after this many loops even if the SDK has not suggested it.
const maxRemediationLoops = 100

// RemediationWorkflow remediates one control on one repository
// (DESIGN-0032 § Remediation), on the remediation queue as the
// Remediation App, at remediation/<repository id>/<control>. A run loops:
// one remediation, then drain the signals and re-read the control's
// state; it loops again when a signal arrived or the state moved, and
// completes otherwise. A signal during a loop is one more loop, never a
// second run: the workflow id admits one run at a time.
func RemediationWorkflow(ctx workflow.Context, in *RemediationInput) error {
	r := &remediationLoop{ctx: ctx, in: *in, remediate: workflow.GetSignalChannel(ctx, RemediateSignal)}

	for loop := 0; ; loop++ {
		if loop >= maxRemediationLoops || (loop > 0 && workflow.GetInfo(ctx).GetContinueAsNewSuggested()) {
			// A signal still buffered is carried as "loop at least once".
			r.drain()

			return workflow.NewContinueAsNewError(ctx, RemediationWorkflowName, &r.in)
		}

		r.recheckPending = false

		res, err := r.run(loop)
		if err != nil {
			return err
		}

		r.drain()

		if r.recheckPending {
			continue
		}

		moved, err := r.moved(res.Start)
		if err != nil || !moved {
			return err
		}
	}
}

// remediationLoop is RemediationWorkflow's state within one run.
type remediationLoop struct {
	ctx       workflow.Context
	in        RemediationInput
	remediate workflow.ReceiveChannel

	// recheckPending is set by a signal that arrived since the loop began.
	recheckPending bool
}

func (r *remediationLoop) taskPriority() temporal.Priority {
	p := r.in.Priority
	if p == 0 {
		p = PrioritySchedule
	}

	return TaskPriority(p, r.in.InstallationID)
}

// drain consumes every buffered signal without blocking. The payload
// carries nothing: the run re-reads the database.
func (r *remediationLoop) drain() {
	for {
		var s Remediate
		if !r.remediate.ReceiveAsync(&s) {
			return
		}

		r.recheckPending = true
	}
}

// run runs one loop's remediation to a disposition. A throttle waits on a
// durable timer (never a sleep in the activity) and retries the same
// loop under the same operation key, having recorded nothing.
func (r *remediationLoop) run(loop int) (*RemediateResult, error) {
	in := RemediateInput{
		RepositoryID:   r.in.RepositoryID,
		Control:        r.in.Control,
		InstallationID: r.in.InstallationID,
		OperationKey:   checkKey(r.ctx, loop),
	}

	for ; ; in.Deferrals++ {
		lease, err := r.acquire(in.OperationKey)
		if err != nil {
			return nil, err
		}

		var res RemediateResult

		err = workflow.ExecuteActivity(remediateOptions(r.ctx, r.taskPriority()), RemediateActivity, &in).Get(r.ctx, &res)
		if err != nil {
			r.report(lease, nil)

			return nil, err
		}

		r.report(lease, &res)

		if res.Kind != RemediationDeferred {
			workflow.GetLogger(r.ctx).Info("remediation loop finished",
				"operation_key", in.OperationKey, "kind", string(res.Kind), "hold", res.Hold)

			return &res, nil
		}

		if err := workflow.Sleep(r.ctx, res.Until.Sub(workflow.Now(r.ctx))); err != nil {
			return nil, err
		}
	}
}

// moved re-reads the control's state and reports whether it differs from
// what the loop started from.
func (r *remediationLoop) moved(start RemediationState) (bool, error) {
	var now RemediationState

	err := workflow.ExecuteActivity(storeOptions(r.ctx, r.taskPriority()), ReadRemediationStateActivity,
		&ReadRemediationStateInput{RepositoryID: r.in.RepositoryID, Control: r.in.Control}).Get(r.ctx, &now)
	if err != nil {
		return false, err
	}

	return now != start, nil
}

// acquire takes a lease from the Remediation App's budget for the
// installation (installation/remediate/<id>), waiting on a durable timer
// while it is exhausted.
func (r *remediationLoop) acquire(key string) (string, error) {
	holder := BudgetHolder(AppRemediate, workflow.GetInfo(r.ctx).WorkflowExecution.ID)

	for attempt := 0; ; attempt++ {
		var res AcquireResult

		err := workflow.ExecuteActivity(storeOptions(r.ctx, r.taskPriority()), AcquireBudgetActivity, &AcquireInput{
			App:            AppRemediate,
			InstallationID: r.in.InstallationID,
			UpdateID:       key + "/acquire/" + strconv.Itoa(attempt),
			Request:        AcquireRequest{Holder: holder, Priority: r.in.Priority},
		}).Get(r.ctx, &res)
		if err != nil {
			return "", err
		}

		if res.Granted {
			return res.LeaseID, nil
		}

		if err := workflow.Sleep(r.ctx, res.WaitUntil.Sub(workflow.Now(r.ctx))); err != nil {
			return "", err
		}
	}
}

// report returns a lease with what the loop spent; a deferral reports
// remaining 0 until its reset. Best effort.
func (r *remediationLoop) report(lease string, res *RemediateResult) {
	if lease == "" {
		return
	}

	rep := Report{LeaseID: lease}

	if res != nil {
		rep.Calls = res.Calls

		switch {
		case res.Kind == RemediationDeferred:
			rep.Remaining, rep.Reset, rep.ObservedAt = 0, res.Until, workflow.Now(r.ctx)
		case res.Rate != nil:
			rep.Limit, rep.Remaining, rep.Reset, rep.ObservedAt = res.Rate.Limit, res.Rate.Remaining, res.Rate.ResetAt, res.Rate.ObservedAt
		}
	}

	err := workflow.SignalExternalWorkflow(r.ctx, InstallationWorkflowID(AppRemediate, r.in.InstallationID), "", ReportSignal, &rep).Get(r.ctx, nil)
	if err != nil {
		workflow.GetLogger(r.ctx).Warn("budget report failed; the lease will expire", "lease", lease, "error", err)
	}
}
