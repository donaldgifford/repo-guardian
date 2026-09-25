package workflows

import (
	"errors"
	"math/rand/v2"
	"strconv"
	"time"

	"go.temporal.io/sdk/workflow"
)

// maxIterationsPerRun bounds history: RepoWorkflow continues as new
// after this many checks even if the SDK has not suggested it.
const maxIterationsPerRun = 100

// budgetChangeID gates the InstallationWorkflow budget acquire
// (IMPL-0025 Phase 11). Executions started before it skip the acquire.
const budgetChangeID = "budget-v1"

// RepoWorkflow is the per-repository loop (DESIGN-0026 § RepoWorkflow):
// wait for the timer or a signal, check, record, schedule the next check.
// It runs until the repository is parked. Keep it tiny: every change is
// a determinism hazard for every running execution.
func RepoWorkflow(ctx workflow.Context, in *RepoWorkflowInput) error {
	r := newRepoLoop(ctx, in)

	for iteration := 0; ; iteration++ {
		if iteration > 0 && (iteration >= maxIterationsPerRun || workflow.GetInfo(ctx).GetContinueAsNewSuggested()) {
			r.drain()

			if r.park != nil {
				return r.parkRepo(r.park.Reason, r.park.ClearFindings, "", "")
			}

			return workflow.NewContinueAsNewError(ctx, RepoWorkflowName, &r.state)
		}

		if err := r.wait(); err != nil {
			return err
		}

		r.drain()

		if r.park != nil {
			return r.parkRepo(r.park.Reason, r.park.ClearFindings, "", "")
		}

		parked, err := r.check()
		if err != nil || parked {
			return err
		}
	}
}

// repoLoop is RepoWorkflow's state within one run.
type repoLoop struct {
	ctx   workflow.Context
	state RepoWorkflowInput
	park  *Park

	recheck, policy, parkCh workflow.ReceiveChannel
}

func newRepoLoop(ctx workflow.Context, in *RepoWorkflowInput) *repoLoop {
	return &repoLoop{
		ctx:     ctx,
		state:   *in,
		recheck: workflow.GetSignalChannel(ctx, RecheckSignal),
		policy:  workflow.GetSignalChannel(ctx, PolicyChangedSignal),
		parkCh:  workflow.GetSignalChannel(ctx, ParkSignal),
	}
}

// wait blocks until the next check is due, a re-check is pending, or the
// repository is to be parked.
func (r *repoLoop) wait() error {
	for r.state.Pending == nil && r.park == nil {
		delay := r.state.NextDue.Sub(workflow.Now(r.ctx))
		if delay <= 0 {
			return nil
		}

		timerCtx, cancel := workflow.WithCancel(r.ctx)
		fired := false
		sel := workflow.NewSelector(r.ctx)
		sel.AddFuture(workflow.NewTimer(timerCtx, delay), func(f workflow.Future) {
			fired = f.Get(r.ctx, nil) == nil
		})
		r.addSignals(sel)
		sel.Select(r.ctx)
		cancel()

		if fired {
			return nil
		}

		if err := r.ctx.Err(); err != nil {
			return err
		}
	}

	return nil
}

func (r *repoLoop) addSignals(sel workflow.Selector) {
	sel.AddReceive(r.recheck, func(c workflow.ReceiveChannel, _ bool) {
		var s Recheck
		c.Receive(r.ctx, &s)
		r.onRecheck(s)
	})
	sel.AddReceive(r.policy, func(c workflow.ReceiveChannel, _ bool) {
		var s PolicyChanged
		c.Receive(r.ctx, &s)
		r.onPolicyChanged(s)
	})
	sel.AddReceive(r.parkCh, func(c workflow.ReceiveChannel, _ bool) {
		var s Park
		c.Receive(r.ctx, &s)
		r.park = &s
	})
}

// drain applies every buffered signal without blocking, so signals that
// arrived during a check coalesce into one follow-up.
func (r *repoLoop) drain() {
	for {
		sel := workflow.NewSelector(r.ctx)
		r.addSignals(sel)

		if !sel.HasPending() {
			return
		}

		sel.Select(r.ctx)
	}
}

// onRecheck records a re-check, keeping the highest priority (the lowest
// key) when one is already pending.
func (r *repoLoop) onRecheck(s Recheck) {
	if r.state.Pending == nil || s.Priority < r.state.Pending.Priority {
		r.state.Pending = &s
	}
}

// onPolicyChanged pulls the next check in to a random point between now
// and s.By, spreading a rollout across its window.
func (r *repoLoop) onPolicyChanged(s PolicyChanged) {
	r.state.PolicyVersion = s.Version

	now := workflow.Now(r.ctx)
	at := now

	if window := s.By.Sub(now); window > 0 {
		at = now.Add(time.Duration(r.random() * float64(window)))
	}

	if at.Before(r.state.NextDue) {
		r.state.NextDue = at
	}
}

// random returns a uniform float in [0, 1), recorded in history.
func (r *repoLoop) random() float64 {
	var f float64

	draw := func(workflow.Context) any {
		return rand.Float64() //nolint:gosec // G404: jitter only; SideEffect records the value for replay
	}

	// Get cannot fail decoding a float the same SideEffect produced.
	_ = workflow.SideEffect(r.ctx, draw).Get(&f) //nolint:errcheck // see above

	return f
}

// check runs one check to a final disposition: recorded, recorded as an
// error, or parked. Deferred results wait on a durable timer and retry
// the same iteration.
func (r *repoLoop) check() (parked bool, err error) {
	trigger, priority := TriggerSchedule, PrioritySchedule
	if p := r.state.Pending; p != nil {
		trigger, priority = p.Trigger, p.Priority
		r.state.Pending = nil
	}

	key := checkKey(r.ctx, r.state.Iteration)
	started := workflow.Now(r.ctx)

	for deferrals := 0; ; deferrals++ {
		lease, err := r.acquire(key, priority)
		if err != nil {
			return false, err
		}

		var res CheckRepoResult

		err = workflow.ExecuteActivity(checkRepoOptions(r.ctx, priority), CheckRepoActivity, &CheckRepoInput{
			RepositoryID: r.state.RepositoryID,
			CheckKey:     key,
			Trigger:      trigger,
			Deferrals:    deferrals,
		}).Get(r.ctx, &res)
		if err != nil {
			if r.ctx.Err() != nil {
				return false, r.ctx.Err()
			}

			r.report(lease, nil)

			return false, r.recordError(key, trigger, started, err)
		}

		r.report(lease, &res)

		switch res.Kind {
		case CheckDeferred:
			if err := workflow.Sleep(r.ctx, res.Until.Sub(workflow.Now(r.ctx))); err != nil {
				return false, err
			}

			continue
		case CheckParked:
			return true, r.parkRepo(res.ParkReason, res.ClearFindings, res.Cause, trigger)
		default:
			return false, r.record(trigger, &res)
		}
	}
}

// acquire takes a lease from the installation's rate budget, waiting on
// a durable timer (which holds no worker) while it is exhausted. It
// returns "" for executions started before budget-v1.
func (r *repoLoop) acquire(key string, priority Priority) (string, error) {
	if workflow.GetVersion(r.ctx, budgetChangeID, workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return "", nil
	}

	for attempt := 0; ; attempt++ {
		var res AcquireResult

		err := workflow.ExecuteActivity(storeOptions(r.ctx), AcquireBudgetActivity, &AcquireInput{
			InstallationID: r.state.InstallationID,
			UpdateID:       key + "/acquire/" + strconv.Itoa(attempt),
			Request:        AcquireRequest{Holder: workflow.GetInfo(r.ctx).WorkflowExecution.ID, Priority: priority},
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

// report returns a lease with what the check spent. A Deferred check
// reports remaining 0 until its reset, closing the gate for every
// repository of the installation at once. res is nil for a failed
// check, which only releases the lease. Reporting is best effort: an
// unreported lease expires on its own.
func (r *repoLoop) report(lease string, res *CheckRepoResult) {
	if lease == "" {
		return
	}

	rep := Report{LeaseID: lease}

	if res != nil {
		rep.Calls = res.Calls

		switch {
		case res.Kind == CheckDeferred:
			rep.Remaining, rep.Reset, rep.ObservedAt = 0, res.Until, workflow.Now(r.ctx)
		case res.Rate != nil:
			rep.Limit, rep.Remaining, rep.Reset, rep.ObservedAt = res.Rate.Limit, res.Rate.Remaining, res.Rate.ResetAt, res.Rate.ObservedAt
		}
	}

	err := workflow.SignalExternalWorkflow(r.ctx, InstallationWorkflowID(r.state.InstallationID), "", ReportSignal, &rep).Get(r.ctx, nil)
	if err != nil {
		workflow.GetLogger(r.ctx).Warn("budget report failed; the lease will expire", "lease", lease, "error", err)
	}
}

func (r *repoLoop) record(trigger string, res *CheckRepoResult) error {
	err := workflow.ExecuteActivity(storeOptions(r.ctx), RecordCheckActivity, &RecordCheckInput{
		RepositoryID: r.state.RepositoryID,
		Trigger:      trigger,
		Result:       *res,
	}).Get(r.ctx, nil)
	if err != nil {
		return err
	}

	r.scheduleNext()

	return nil
}

// recordError records a check that still failed after its retries. The
// repository is not dropped: it is checked again next interval.
func (r *repoLoop) recordError(key, trigger string, started time.Time, cause error) error {
	var msg string
	if appErr := errors.Unwrap(cause); appErr != nil {
		msg = appErr.Error()
	} else {
		msg = cause.Error()
	}

	err := workflow.ExecuteActivity(storeOptions(r.ctx), RecordCheckErrorActivity, &RecordCheckErrorInput{
		RepositoryID: r.state.RepositoryID,
		CheckKey:     key,
		Trigger:      trigger,
		StartedAt:    started,
		FinishedAt:   workflow.Now(r.ctx),
		Error:        msg,
	}).Get(r.ctx, nil)
	if err != nil {
		return err
	}

	workflow.GetLogger(r.ctx).Warn("check failed after retries; trying again next interval", "check_key", key)
	r.scheduleNext()

	return nil
}

// scheduleNext advances the iteration and sets the next check to one
// interval from now, jittered ±10% so a fleet onboarded at once spreads
// out.
func (r *repoLoop) scheduleNext() {
	r.state.Iteration++

	interval := float64(r.state.CheckInterval) * (0.9 + 0.2*r.random())
	r.state.NextDue = workflow.Now(r.ctx).Add(time.Duration(interval))
}

// parkRepo parks the repository. The workflow completes afterwards; only
// discovery starts it again.
func (r *repoLoop) parkRepo(reason string, clearFindings bool, cause, trigger string) error {
	return workflow.ExecuteActivity(storeOptions(r.ctx), ParkActivity, &ParkInput{
		RepositoryID:   r.state.RepositoryID,
		InstallationID: r.state.InstallationID,
		CheckKey:       checkKey(r.ctx, r.state.Iteration),
		Trigger:        trigger,
		Reason:         reason,
		ClearFindings:  clearFindings,
		Cause:          cause,
	}).Get(r.ctx, nil)
}
