package workflows

import (
	"errors"
	"math"
	"strconv"
	"time"

	"go.temporal.io/sdk/workflow"
)

// Budget defaults (DESIGN-0026 § Rate budget).
const (
	// DefaultEstimate seeds the calls-per-check EWMA.
	DefaultEstimate = 20

	// estimateWeight is the EWMA weight of the newest report.
	estimateWeight = 0.2

	// maxHandledPerRun is OQ14's ContinueAsNew bound: the SDK's
	// suggestion or this many handled Updates and Signals, whichever
	// comes first. The burst test (IMPL-0025 11.7) resizes it.
	maxHandledPerRun = 2000

	// idleSweep is how often the lease sweep runs with no lease due.
	idleSweep = time.Hour

	// waitJitterSpan spreads callers told to wait for the same reset.
	waitJitterSpan = 60

	// DefaultLeaseTTL is the CheckRepo timeout plus a margin.
	DefaultLeaseTTL = checkRepoTimeout + 5*time.Minute
)

// Lease is budget reserved for one granted check.
type Lease struct {
	Amount  int
	Expires time.Time
}

// BudgetState is an installation's rate budget. It is InstallationWorkflow's
// ContinueAsNew state, so it carries the open leases.
type BudgetState struct {
	Limit     int
	Remaining int
	Reset     time.Time
	Observed  time.Time
	Reserved  int
	Estimate  float64
	Leases    map[string]Lease
	NextLease int64
}

// InstallationWorkflowInput is InstallationWorkflow's input and its
// ContinueAsNew state.
type InstallationWorkflowInput struct {
	InstallationID int64

	// Threshold is RATE_LIMIT_THRESHOLD: the fraction of the limit held
	// back as a reserve.
	Threshold float64

	// LeaseTTL releases a grant that was never reported: the CheckRepo
	// timeout plus a margin, so a crashed check cannot leak budget.
	LeaseTTL time.Duration

	State BudgetState
}

// AcquireRequest is the acquire Update's argument.
type AcquireRequest struct {
	// Holder names the caller, for the lease id.
	Holder   string
	Priority Priority
}

// AcquireResult is the acquire Update's result: a lease, or a time to
// try again.
type AcquireResult struct {
	Granted   bool
	LeaseID   string
	Amount    int
	WaitUntil time.Time

	// Optimistic marks a grant made without a known budget.
	Optimistic bool
}

// Report is the report Signal's payload: what a check spent and the
// rate headers it last saw. A Deferred check reports Remaining 0 with
// the reset it was given, closing the gate for the whole installation.
type Report struct {
	LeaseID    string
	Calls      int
	Limit      int
	Remaining  int
	Reset      time.Time
	ObservedAt time.Time
}

// budget is InstallationWorkflow's state within one run.
type budget struct {
	in      InstallationWorkflowInput
	handled int
}

// InstallationWorkflow holds one installation's rate budget and serves
// acquire and report (DESIGN-0026 § Rate budget). It runs until the
// installation is removed, continuing as new to bound its history.
func InstallationWorkflow(ctx workflow.Context, in *InstallationWorkflowInput) error {
	b := &budget{in: *in}
	if b.in.State.Estimate <= 0 {
		b.in.State.Estimate = DefaultEstimate
	}

	if b.in.State.Leases == nil {
		b.in.State.Leases = map[string]Lease{}
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx, AcquireUpdate,
		func(ctx workflow.Context, req *AcquireRequest) (*AcquireResult, error) {
			b.handled++

			return b.acquire(workflow.Now(ctx), req), nil
		},
		workflow.UpdateHandlerOptions{Validator: validateAcquire},
	); err != nil {
		return err
	}

	reports := workflow.GetSignalChannel(ctx, ReportSignal)

	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var r Report
			if !reports.Receive(ctx, &r) {
				return
			}

			b.handled++
			b.report(&r)
		}
	})

	for {
		done := func() bool {
			return b.handled >= maxHandledPerRun || workflow.GetInfo(ctx).GetContinueAsNewSuggested()
		}

		due, err := workflow.AwaitWithTimeout(ctx, b.untilSweep(workflow.Now(ctx)), done)
		if err != nil {
			return err
		}

		b.sweep(workflow.Now(ctx))

		if due {
			return b.continueAsNew(ctx, reports)
		}
	}
}

// continueAsNew drains pending reports and in-flight Updates first, so
// no grant or report is lost across runs.
func (b *budget) continueAsNew(ctx workflow.Context, reports workflow.ReceiveChannel) error {
	if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
		return err
	}

	for {
		var r Report
		if !reports.ReceiveAsync(&r) {
			break
		}

		b.report(&r)
	}

	return workflow.NewContinueAsNewError(ctx, InstallationWorkflowName, &b.in)
}

func validateAcquire(_ workflow.Context, req *AcquireRequest) error {
	if req == nil || req.Holder == "" {
		return errors.New("acquire: holder is required")
	}

	if req.Priority < 1 || req.Priority > 5 {
		return errors.New("acquire: priority must be 1 to 5")
	}

	return nil
}

// known reports whether the budget reflects the current window.
func (b *budget) known(now time.Time) bool {
	s := &b.in.State

	return s.Limit > 0 && now.Before(s.Reset)
}

// reserve is the budget priority p must leave untouched. Webhook and
// push checks may dip into half of it, so when the budget is scarce the
// checks a human is waiting on go first.
func (b *budget) reserve(p Priority) int {
	r := float64(b.in.State.Limit) * b.in.Threshold
	if p <= PriorityWebhook {
		r /= 2
	}

	return int(math.Ceil(r))
}

func (b *budget) acquire(now time.Time, req *AcquireRequest) *AcquireResult {
	b.sweep(now)

	s := &b.in.State
	amount := int(math.Ceil(s.Estimate))

	if !b.known(now) {
		return b.grant(now, req.Holder, amount, true)
	}

	if s.Remaining-s.Reserved-b.reserve(req.Priority) >= amount {
		return b.grant(now, req.Holder, amount, false)
	}

	// Spread the waiters over a minute past the reset, deterministically.
	jitter := time.Duration(s.NextLease%waitJitterSpan) * time.Second
	s.NextLease++

	return &AcquireResult{WaitUntil: s.Reset.Add(jitter)}
}

func (b *budget) grant(now time.Time, holder string, amount int, optimistic bool) *AcquireResult {
	s := &b.in.State
	id := holder + "#" + strconv.FormatInt(s.NextLease, 10)
	s.NextLease++

	s.Leases[id] = Lease{Amount: amount, Expires: now.Add(b.in.LeaseTTL)}
	s.Reserved += amount

	return &AcquireResult{Granted: true, LeaseID: id, Amount: amount, Optimistic: optimistic}
}

func (b *budget) report(r *Report) {
	s := &b.in.State

	if l, ok := s.Leases[r.LeaseID]; ok {
		delete(s.Leases, r.LeaseID)
		s.Reserved -= l.Amount
	}

	if r.Calls > 0 {
		s.Estimate = estimateWeight*float64(r.Calls) + (1-estimateWeight)*s.Estimate
	}

	// Keep the newest observation; reports arrive out of order.
	if r.ObservedAt.IsZero() || r.ObservedAt.Before(s.Observed) {
		return
	}

	s.Observed = r.ObservedAt
	s.Remaining = r.Remaining
	s.Reset = r.Reset

	if r.Limit > 0 {
		s.Limit = r.Limit
	}
}

// sweep releases leases past their expiry.
func (b *budget) sweep(now time.Time) {
	s := &b.in.State

	for id, l := range s.Leases {
		if !now.Before(l.Expires) {
			delete(s.Leases, id)
			s.Reserved -= l.Amount
		}
	}
}

// untilSweep is the time to the next lease expiry, or idleSweep.
func (b *budget) untilSweep(now time.Time) time.Duration {
	next := idleSweep

	for _, l := range b.in.State.Leases {
		if d := l.Expires.Sub(now); d < next {
			next = max(d, time.Second)
		}
	}

	return next
}
