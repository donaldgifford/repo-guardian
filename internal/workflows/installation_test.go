package workflows

import (
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const testLeaseTTL = 20 * time.Minute

// iwProbe drives an InstallationWorkflow through timed steps and records
// every acquire answer.
type iwProbe struct {
	env *testsuite.TestWorkflowEnvironment

	mu       sync.Mutex
	results  []*AcquireResult
	rejected []error
	n        int
}

func newIW(t *testing.T) *iwProbe {
	t.Helper()

	var suite testsuite.WorkflowTestSuite

	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))

	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(InstallationWorkflow, workflow.RegisterOptions{Name: InstallationWorkflowName})

	return &iwProbe{env: env}
}

// at runs fn d after the workflow starts.
func (p *iwProbe) at(d time.Duration, fn func()) {
	p.env.RegisterDelayedCallback(fn, d)
}

func (p *iwProbe) acquire(prio Priority) {
	p.n++
	p.env.UpdateWorkflow(AcquireUpdate, "u"+strconv.Itoa(p.n), &testsuite.TestUpdateCallback{
		OnAccept: func() {},
		OnReject: func(err error) {
			p.mu.Lock()
			defer p.mu.Unlock()

			p.rejected = append(p.rejected, err)
		},
		OnComplete: func(v any, err error) {
			p.mu.Lock()
			defer p.mu.Unlock()

			if err == nil {
				p.results = append(p.results, v.(*AcquireResult))
			}
		},
	}, &AcquireRequest{Holder: "repo/42", Priority: prio})
}

func (p *iwProbe) report(r *Report) {
	p.env.SignalWorkflow(ReportSignal, r)
}

// run executes the workflow, cancelling it after end.
func (p *iwProbe) run(t *testing.T, in *InstallationWorkflowInput, end time.Duration) error {
	t.Helper()

	p.at(end, p.env.CancelWorkflow)
	p.env.ExecuteWorkflow(InstallationWorkflowName, in)

	return p.env.GetWorkflowError()
}

func iwInput(threshold float64) *InstallationWorkflowInput {
	return &InstallationWorkflowInput{InstallationID: 7, Threshold: threshold, LeaseTTL: testLeaseTTL}
}

func (p *iwProbe) result(t *testing.T, i int) *AcquireResult {
	t.Helper()

	if i >= len(p.results) {
		t.Fatalf("acquire %d: no answer (have %d)", i, len(p.results))
	}

	return p.results[i]
}

func TestInstallationWorkflow_Grant(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	var start time.Time

	p.at(0, func() { start = p.env.Now() })
	p.at(time.Minute, func() { p.acquire(PrioritySchedule) })
	p.at(2*time.Minute, func() {
		p.report(&Report{
			LeaseID: p.results[0].LeaseID, Calls: 20, Limit: 5000, Remaining: 4000,
			Reset: start.Add(time.Hour), ObservedAt: p.env.Now(),
		})
	})
	p.at(3*time.Minute, func() { p.acquire(PrioritySchedule) })

	_ = p.run(t, iwInput(0.10), time.Hour-time.Minute)

	if r := p.result(t, 0); !r.Granted || !r.Optimistic || r.Amount != DefaultEstimate || r.LeaseID == "" {
		t.Errorf("first acquire = %+v, want an optimistic grant of %d", r, DefaultEstimate)
	}

	if r := p.result(t, 1); !r.Granted || r.Optimistic || r.LeaseID == p.results[0].LeaseID {
		t.Errorf("second acquire = %+v, want a known-budget grant under a new lease", r)
	}
}

func TestInstallationWorkflow_DenyWaitGrant(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	var reset time.Time

	p.at(time.Minute, func() {
		reset = p.env.Now().Add(30 * time.Minute)
		// 500 left is exactly the 10% reserve of 5000.
		p.report(&Report{Limit: 5000, Remaining: 500, Reset: reset, ObservedAt: p.env.Now()})
	})
	p.at(2*time.Minute, func() { p.acquire(PrioritySchedule) })
	p.at(3*time.Minute, func() { p.acquire(PriorityWebhook) })
	p.at(40*time.Minute, func() { p.acquire(PrioritySchedule) })

	_ = p.run(t, iwInput(0.10), time.Hour)

	if r := p.result(t, 0); r.Granted || r.WaitUntil.Before(reset) || r.WaitUntil.After(reset.Add(time.Minute)) {
		t.Errorf("scheduled acquire at the reserve = %+v, want a wait until the reset (+jitter) %v", r, reset)
	}

	if r := p.result(t, 1); !r.Granted {
		t.Errorf("webhook acquire at the reserve = %+v, want a grant from the half reserve it may use", r)
	}

	if r := p.result(t, 2); !r.Granted || !r.Optimistic {
		t.Errorf("acquire after the reset = %+v, want an optimistic grant", r)
	}
}

func TestInstallationWorkflow_LeaseExpiry(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	p.at(time.Minute, func() {
		p.report(&Report{Limit: 5000, Remaining: 30, Reset: p.env.Now().Add(24 * time.Hour), ObservedAt: p.env.Now()})
	})
	p.at(2*time.Minute, func() { p.acquire(PrioritySchedule) })
	p.at(3*time.Minute, func() { p.acquire(PrioritySchedule) })
	p.at(2*time.Minute+testLeaseTTL+time.Minute, func() { p.acquire(PrioritySchedule) })

	_ = p.run(t, iwInput(0), 2*time.Hour)

	if !p.result(t, 0).Granted || p.result(t, 1).Granted {
		t.Fatalf("acquires = %+v %+v, want grant then wait while the lease holds 20 of 30", p.results[0], p.results[1])
	}

	if r := p.result(t, 2); !r.Granted {
		t.Errorf("acquire after the lease expired = %+v, want a grant: an unreported lease must not leak", r)
	}
}

func TestInstallationWorkflow_DeferredReportClosesTheGate(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	var reset time.Time

	p.at(time.Minute, func() {
		p.report(&Report{Limit: 5000, Remaining: 4000, Reset: p.env.Now().Add(time.Hour), ObservedAt: p.env.Now()})
	})
	p.at(2*time.Minute, func() {
		reset = p.env.Now().Add(45 * time.Minute)
		p.report(&Report{Remaining: 0, Reset: reset, ObservedAt: p.env.Now()})
	})
	p.at(3*time.Minute, func() { p.acquire(PriorityWebhook) })

	_ = p.run(t, iwInput(0.10), 30*time.Minute)

	if r := p.result(t, 0); r.Granted || r.WaitUntil.Before(reset) {
		t.Errorf("acquire after a deferral = %+v, want a wait until %v", r, reset)
	}
}

func TestInstallationWorkflow_EstimateConverges(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	for i := range 40 {
		p.at(time.Duration(i+1)*time.Second, func() { p.report(&Report{Calls: 5}) })
	}

	p.at(time.Minute, func() { p.acquire(PrioritySchedule) })

	_ = p.run(t, iwInput(0.10), time.Hour)

	if r := p.result(t, 0); r.Amount < 5 || r.Amount > 6 {
		t.Errorf("lease amount = %d after 40 reports of 5 calls, want the EWMA near 5", r.Amount)
	}
}

func TestInstallationWorkflow_ContinueAsNewKeepsLeases(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	const maxHandled = 20

	p.at(time.Second, func() { p.acquire(PrioritySchedule) })

	for i := range maxHandled - 1 {
		p.at(time.Duration(i+2)*time.Second, func() { p.report(&Report{LeaseID: "unknown"}) })
	}

	in := iwInput(0.10)
	in.MaxHandled = maxHandled

	err := p.run(t, in, time.Hour)

	var can *workflow.ContinueAsNewError
	if !errors.As(err, &can) {
		t.Fatalf("workflow error = %v, want ContinueAsNew after %d handled", err, maxHandled)
	}

	var next InstallationWorkflowInput
	if err := converter.GetDefaultDataConverter().FromPayloads(can.Input, &next); err != nil {
		t.Fatalf("decode: %v", err)
	}

	lease := p.result(t, 0).LeaseID
	if _, ok := next.State.Leases[lease]; !ok || next.State.Reserved != DefaultEstimate {
		t.Errorf("carried state = %+v, want lease %s with %d reserved", next.State, lease, DefaultEstimate)
	}

	if next.InstallationID != 7 || next.LeaseTTL != testLeaseTTL || next.Threshold != 0.10 || next.MaxHandled != maxHandled {
		t.Errorf("carried config = %+v", next)
	}
}

func TestInstallationWorkflow_ValidatorRejectsBadRequests(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	p.at(time.Second, func() {
		p.env.UpdateWorkflow(AcquireUpdate, "bad", &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) {
				p.mu.Lock()
				defer p.mu.Unlock()

				p.rejected = append(p.rejected, err)
			},
			OnComplete: func(any, error) {},
		}, &AcquireRequest{Holder: "repo/42", Priority: 9})
	})

	_ = p.run(t, iwInput(0.10), time.Minute)

	if len(p.rejected) != 1 || len(p.results) != 0 {
		t.Errorf("rejected = %v, results = %v; want the bad priority rejected before any lease", p.rejected, p.results)
	}
}

func TestInstallationWorkflow_SuspendedWaits(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	p.at(time.Minute, func() { p.env.SignalWorkflow(SuspendSignal, Suspend{Suspended: true}) })
	p.at(2*time.Minute, func() { p.acquire(PriorityWebhook) })
	p.at(3*time.Minute, func() { p.env.SignalWorkflow(SuspendSignal, Suspend{Suspended: false}) })
	p.at(4*time.Minute, func() { p.acquire(PriorityWebhook) })

	_ = p.run(t, iwInput(0.10), time.Hour)

	if r := p.result(t, 0); r.Granted || r.WaitUntil.IsZero() {
		t.Errorf("acquire while suspended = %+v, want a wait", r)
	}

	if r := p.result(t, 1); !r.Granted {
		t.Errorf("acquire after unsuspend = %+v, want a grant", r)
	}
}
