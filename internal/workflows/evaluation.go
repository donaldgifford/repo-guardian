package workflows

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// maxIterationsPerRun bounds history: EvaluationWorkflow continues as
// new after this many evaluations even if the SDK has not suggested it.
const maxIterationsPerRun = 100

// remediationChange is the one version marker IMPL-0030 adds to
// EvaluationWorkflow: version 1 starts the due remediations after a
// record (2.6) and persists next due after every schedule (2.9). The
// DefaultVersion branch keeps IMPL-0029's command order exactly, so a run
// begun before the upgrade replays unchanged.
const remediationChange = "remediation-v1"

// EvaluationWorkflow is the per-repository controls evaluation loop
// (DESIGN-0032 § Evaluation), on the evaluation queue as the Evaluation
// App: wait for the jittered timer or a signal, evaluate the selected
// controls, record, schedule the next. It runs until the repository is
// parked.
func EvaluationWorkflow(ctx workflow.Context, in *EvaluationWorkflowInput) error {
	e := newEvalLoop(ctx, in)

	for iteration := 0; ; iteration++ {
		if iteration > 0 && (iteration >= maxIterationsPerRun || workflow.GetInfo(ctx).GetContinueAsNewSuggested()) {
			// Unread signals are folded into the carried state, never lost.
			e.drain()

			return workflow.NewContinueAsNewError(ctx, EvaluationWorkflowName, &e.state)
		}

		if err := e.wait(); err != nil {
			return err
		}

		e.drain()

		done, err := e.evaluate()
		if err != nil || done {
			return err
		}
	}
}

// evalLoop is EvaluationWorkflow's state within one run.
type evalLoop struct {
	ctx      workflow.Context
	state    EvaluationWorkflowInput
	priority Priority

	// remediation is remediationChange at version 1, read once per run.
	remediation bool

	recheck, policy workflow.ReceiveChannel
}

func newEvalLoop(ctx workflow.Context, in *EvaluationWorkflowInput) *evalLoop {
	return &evalLoop{
		ctx:         ctx,
		state:       *in,
		remediation: workflow.GetVersion(ctx, remediationChange, workflow.DefaultVersion, 1) == 1,
		recheck:     workflow.GetSignalChannel(ctx, RecheckSignal),
		policy:      workflow.GetSignalChannel(ctx, PolicyChangedSignal),
	}
}

func (e *evalLoop) taskPriority() temporal.Priority {
	p := e.priority
	if p == 0 {
		p = PrioritySchedule
	}

	return TaskPriority(p, e.state.InstallationID)
}

// wait blocks until the next evaluation is due or a re-check is pending.
func (e *evalLoop) wait() error {
	for e.state.Pending == nil {
		delay := e.state.NextDue.Sub(workflow.Now(e.ctx))
		if delay <= 0 {
			return nil
		}

		timerCtx, cancel := workflow.WithCancel(e.ctx)
		fired := false
		sel := workflow.NewSelector(e.ctx)
		sel.AddFuture(workflow.NewTimer(timerCtx, delay), func(f workflow.Future) {
			fired = f.Get(e.ctx, nil) == nil
		})
		e.addSignals(sel)
		sel.Select(e.ctx)
		cancel()

		if fired {
			return nil
		}

		if err := e.ctx.Err(); err != nil {
			return err
		}
	}

	return nil
}

func (e *evalLoop) addSignals(sel workflow.Selector) {
	sel.AddReceive(e.recheck, func(c workflow.ReceiveChannel, _ bool) {
		var s Recheck
		c.Receive(e.ctx, &s)
		e.state.Pending = coalesce(e.state.Pending, &s)
	})
	sel.AddReceive(e.policy, func(c workflow.ReceiveChannel, _ bool) {
		var s PolicyChanged
		c.Receive(e.ctx, &s)
		e.onPolicyChanged(s)
	})
}

// drain applies every buffered signal without blocking.
func (e *evalLoop) drain() {
	for {
		sel := workflow.NewSelector(e.ctx)
		e.addSignals(sel)

		if !sel.HasPending() {
			return
		}

		sel.Select(e.ctx)
	}
}

// coalesce folds a re-check into the pending one: the union of the
// changed paths, Unknown dominating (it selects every control, so the
// paths no longer matter), at the highest priority (the lowest key),
// whose trigger is kept; on a tie a pr_event gives way, since it alone
// evaluates nothing. A union past MaxChangedPaths becomes unknown,
// so the carried state stays bounded however many signals arrive.
func coalesce(pending, s *Recheck) *Recheck {
	if pending == nil {
		out := *s
		if out.Unknown || len(out.Paths) > MaxChangedPaths {
			out.Unknown, out.Paths = true, nil
		}

		return &out
	}

	out := *pending
	if s.Priority < out.Priority || (s.Priority == out.Priority && out.Trigger == TriggerPREvent) {
		out.Priority, out.Trigger = s.Priority, s.Trigger
	}

	if out.Unknown = out.Unknown || s.Unknown; out.Unknown {
		out.Paths = nil

		return &out
	}

	for _, p := range s.Paths {
		if !slices.Contains(out.Paths, p) {
			out.Paths = append(out.Paths, p)
		}
	}

	if len(out.Paths) > MaxChangedPaths {
		out.Unknown, out.Paths = true, nil

		return &out
	}

	slices.Sort(out.Paths)

	return &out
}

// onPolicyChanged marks the next evaluation as a full one and pulls it in
// to a random point between now and s.By, spreading the rollout.
func (e *evalLoop) onPolicyChanged(s PolicyChanged) {
	e.state.PolicyVersion, e.state.PolicyPending = s.Version, true

	now := workflow.Now(e.ctx)
	at := now

	if window := s.By.Sub(now); window > 0 {
		at = now.Add(time.Duration(e.random() * float64(window)))
	}

	if at.Before(e.state.NextDue) {
		e.state.NextDue = at
	}
}

func (e *evalLoop) random() float64 {
	var f float64

	draw := func(workflow.Context) any {
		return rand.Float64() //nolint:gosec // G404: jitter only; SideEffect records the value for replay
	}

	_ = workflow.SideEffect(e.ctx, draw).Get(&f) //nolint:errcheck // decoding the float SideEffect produced cannot fail

	return f
}

// input builds this iteration's Evaluate input and consumes the pending
// re-check. A signalled re-check selects by its paths unless it is
// unknown or a policy change is pending; the timer selects everything,
// API resources included.
func (e *evalLoop) input(key string) EvaluateInput {
	in := EvaluateInput{RepositoryID: e.state.RepositoryID, InstallationID: e.state.InstallationID, CheckKey: key}
	e.priority = PrioritySchedule

	switch p := e.state.Pending; {
	case p != nil && observeOnly(p) && !e.state.PolicyPending:
		in.Trigger, in.Observe = p.Trigger, true
		e.priority = p.Priority
	case p != nil:
		in.Trigger, in.Paths, in.All = p.Trigger, p.Paths, p.Unknown || e.state.PolicyPending
		e.priority = p.Priority
	case e.state.PolicyPending:
		in.Trigger, in.All, e.priority = TriggerPolicyRollout, true, PriorityRollout
	default:
		in.Trigger, in.All, in.Scheduled = TriggerSchedule, true, true
	}

	if in.All {
		in.Paths = nil
	}

	e.state.Pending, e.state.PolicyPending = nil, false

	return in
}

// observeOnly reports whether a pending re-check is a bare pr_event:
// one that coalesced with a push or an unknown change selects controls.
func observeOnly(p *Recheck) bool {
	return p.Trigger == TriggerPREvent && !p.Unknown && len(p.Paths) == 0
}

// evaluate runs one evaluation to a disposition. A deferral waits on a
// durable timer and retries the same iteration under the same key,
// recording nothing; access loss parks and ends the workflow.
func (e *evalLoop) evaluate() (done bool, err error) {
	key := checkKey(e.ctx, e.state.Iteration)
	in := e.input(key)

	for ; ; in.Deferrals++ {
		lease, err := e.acquire(key)
		if err != nil {
			return false, err
		}

		var res EvaluateResult

		err = workflow.ExecuteActivity(checkRepoOptions(e.ctx, e.taskPriority()), EvaluateActivity, &in).Get(e.ctx, &res)
		if err != nil {
			if e.ctx.Err() != nil {
				return false, e.ctx.Err()
			}

			e.report(lease, nil)
			workflow.GetLogger(e.ctx).Warn("evaluation failed after retries; trying again next interval", "check_key", key, "error", err)

			// A failed observation leaves the schedule where it was.
			if !in.Observe {
				e.scheduleNext()
			}

			return false, nil
		}

		e.report(lease, &res)

		switch res.Kind {
		case CheckDeferred:
			if err := workflow.Sleep(e.ctx, res.Until.Sub(workflow.Now(e.ctx))); err != nil {
				return false, err
			}

			continue
		case CheckParked:
			return true, e.park(key, &res)
		case CheckSkipped:
			e.scheduleNext()

			return false, nil
		case CheckObserved:
			return false, nil
		default:
			return false, e.record(key)
		}
	}
}

// acquire takes a lease from the Evaluation App's budget for the
// installation, waiting on a durable timer while it is exhausted.
func (e *evalLoop) acquire(key string) (string, error) {
	holder := BudgetHolder(AppEval, workflow.GetInfo(e.ctx).WorkflowExecution.ID)

	for attempt := 0; ; attempt++ {
		var res AcquireResult

		err := workflow.ExecuteActivity(storeOptions(e.ctx, e.taskPriority()), AcquireBudgetActivity, &AcquireInput{
			App:            AppEval,
			InstallationID: e.state.InstallationID,
			UpdateID:       key + "/acquire/" + strconv.Itoa(attempt),
			Request:        AcquireRequest{Holder: holder, Priority: e.priority},
		}).Get(e.ctx, &res)
		if err != nil {
			return "", err
		}

		if res.Granted {
			return res.LeaseID, nil
		}

		if err := workflow.Sleep(e.ctx, res.WaitUntil.Sub(workflow.Now(e.ctx))); err != nil {
			return "", err
		}
	}
}

// report returns a lease with what the evaluation spent; a deferral
// reports remaining 0 until its reset. Best effort.
func (e *evalLoop) report(lease string, res *EvaluateResult) {
	if lease == "" {
		return
	}

	rep := Report{LeaseID: lease}

	if res != nil {
		rep.Calls = res.Calls

		switch {
		case res.Kind == CheckDeferred:
			rep.Remaining, rep.Reset, rep.ObservedAt = 0, res.Until, workflow.Now(e.ctx)
		case res.Rate != nil:
			rep.Limit, rep.Remaining, rep.Reset, rep.ObservedAt = res.Rate.Limit, res.Rate.Remaining, res.Rate.ResetAt, res.Rate.ObservedAt
		}
	}

	err := workflow.SignalExternalWorkflow(e.ctx, InstallationWorkflowID(AppEval, e.state.InstallationID), "", ReportSignal, &rep).Get(e.ctx, nil)
	if err != nil {
		workflow.GetLogger(e.ctx).Warn("budget report failed; the lease will expire", "lease", lease, "error", err)
	}
}

func (e *evalLoop) record(key string) error {
	var rec RecordEvaluationResult

	err := workflow.ExecuteActivity(storeOptions(e.ctx, e.taskPriority()), RecordEvaluationActivity,
		&RecordEvaluationInput{CheckKey: key}).Get(e.ctx, &rec)
	if err != nil {
		return err
	}

	if e.remediation && (rec.AlreadyFinal || rec.Recorded > 0) {
		e.startDueRemediations(key)
	}

	e.scheduleNext()

	return nil
}

// startDueRemediations hands the repository's due controls to one
// activity (DESIGN-0032 A1). A discarded evaluation never gets here; a
// failure is logged, and the next evaluation or the sweep starts them.
func (e *evalLoop) startDueRemediations(key string) {
	err := workflow.ExecuteActivity(hookOptions(e.ctx, TaskPriority(PrioritySchedule, e.state.InstallationID)), StartDueRemediationsActivity,
		&StartDueRemediationsInput{RepositoryID: e.state.RepositoryID, InstallationID: e.state.InstallationID}).Get(e.ctx, nil)
	if err != nil {
		workflow.GetLogger(e.ctx).Warn("starting due remediations failed", "check_key", key, "error", err)
	}
}

// park parks the repository on access loss; an already parked
// repository just ends the loop. Only discovery starts it again.
func (e *evalLoop) park(key string, res *EvaluateResult) error {
	if res.ParkReason == "" {
		return nil
	}

	return workflow.ExecuteActivity(storeOptions(e.ctx, e.taskPriority()), ParkRepositoryActivity, &ParkRepositoryInput{
		RepositoryID: e.state.RepositoryID, CheckKey: key, Reason: res.ParkReason, Cause: res.Cause,
	}).Get(e.ctx, nil)
}

// scheduleNext advances the iteration and sets the next evaluation one
// interval out, jittered ±10%.
func (e *evalLoop) scheduleNext() {
	e.state.Iteration++

	interval := float64(e.state.Interval) * (0.9 + 0.2*e.random())
	e.state.NextDue = workflow.Now(e.ctx).Add(time.Duration(interval))

	if e.remediation {
		e.persistNextDue()
	}
}

// persistNextDue writes NextDue to repositories.next_due_at through the
// SetNextDue local activity (OQ17, INV-0023 F3). Best effort: the API
// shows a stale time until the next schedule rather than block the loop.
func (e *evalLoop) persistNextDue() {
	err := workflow.ExecuteLocalActivity(localHookOptions(e.ctx), SetNextDueActivity,
		&SetNextDueInput{RepositoryID: e.state.RepositoryID, At: e.state.NextDue}).Get(e.ctx, nil)
	if err != nil {
		workflow.GetLogger(e.ctx).Warn("persisting next due failed", "next_due", e.state.NextDue, "error", err)
	}
}
