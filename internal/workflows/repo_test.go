package workflows

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

func runToEnd(t *testing.T, fakes *fakeActivities, in *RepoWorkflowInput, setup func(env testEnv)) error {
	t.Helper()

	env := newEnv(t, fakes)
	if setup != nil {
		setup(env)
	}

	env.ExecuteWorkflow(RepoWorkflowName, in)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	return env.GetWorkflowError()
}

func TestRepoWorkflow_TimerLoopJittersEachInterval(t *testing.T) {
	t.Parallel()

	fakes := &fakeActivities{script: parkAfter(5)}
	if err := runToEnd(t, fakes, input(), nil); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if len(fakes.records) != 5 || len(fakes.parks) != 1 {
		t.Fatalf("records = %d, parks = %d, want 5 and 1", len(fakes.records), len(fakes.parks))
	}

	lo, hi := time.Duration(0.9*float64(testInterval)), time.Duration(1.1*float64(testInterval))

	for i := 1; i < len(fakes.times); i++ {
		gap := fakes.times[i].Sub(fakes.times[i-1])
		if gap < lo || gap > hi {
			t.Errorf("gap %d = %v, want within ±10%% of %v", i, gap, testInterval)
		}
	}
}

func TestRepoWorkflow_TwentySignalsAtMostTwoChecks(t *testing.T) {
	t.Parallel()

	in := input()
	in.NextDue = time.Now().Add(365 * 24 * time.Hour)

	var env testEnv

	fakes := &fakeActivities{}
	fakes.script = func(n int, ci *CheckRepoInput) (*CheckRepoResult, error) {
		// Half the burst lands while the first check is running.
		if n == 0 {
			for range 10 {
				env.SignalWorkflow(RecheckSignal, Recheck{Trigger: TriggerPush, Priority: PriorityWebhook})
			}
		}

		return &CheckRepoResult{Kind: CheckChecked, CheckKey: ci.CheckKey}, nil
	}

	err := runToEnd(t, fakes, in, func(e testEnv) {
		env = e
		e.RegisterDelayedCallback(func() {
			for range 10 {
				e.SignalWorkflow(RecheckSignal, Recheck{Trigger: TriggerPush, Priority: PriorityWebhook})
			}
		}, time.Minute)
		e.RegisterDelayedCallback(func() { e.SignalWorkflow(ParkSignal, Park{Reason: ParkArchived, ClearFindings: true}) }, time.Hour)
	})
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if n := len(fakes.checks); n < 1 || n > 2 {
		t.Errorf("checks = %d, want 1 or 2 for 20 signals", n)
	}

	for i := range fakes.checks {
		if fakes.checks[i].Trigger != TriggerPush {
			t.Errorf("check %d trigger = %q, want push", i, fakes.checks[i].Trigger)
		}
	}
}

func TestRepoWorkflow_HighestPriorityWins(t *testing.T) {
	t.Parallel()

	var env testEnv

	fakes := &fakeActivities{}
	fakes.script = func(n int, ci *CheckRepoInput) (*CheckRepoResult, error) {
		switch n {
		case 0:
			// Signals arriving during a check coalesce into one follow-up.
			env.SignalWorkflow(RecheckSignal, Recheck{Trigger: TriggerPolicyRollout, Priority: PriorityRollout})
			env.SignalWorkflow(RecheckSignal, Recheck{Trigger: TriggerWebhook, Priority: PriorityWebhook})
			env.SignalWorkflow(RecheckSignal, Recheck{Trigger: TriggerSchedule, Priority: PrioritySchedule})

			return &CheckRepoResult{Kind: CheckChecked, CheckKey: ci.CheckKey}, nil
		default:
			return &CheckRepoResult{Kind: CheckParked, CheckKey: ci.CheckKey, ParkReason: ParkArchived, ClearFindings: true}, nil
		}
	}

	if err := runToEnd(t, fakes, input(), func(e testEnv) { env = e }); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if len(fakes.checks) != 2 {
		t.Fatalf("checks = %d, want the scheduled one and one follow-up", len(fakes.checks))
	}

	if got := fakes.checks[1].Trigger; got != TriggerWebhook {
		t.Errorf("follow-up trigger = %q, want the webhook's", got)
	}

	if gap := fakes.times[1].Sub(fakes.times[0]); gap >= time.Minute {
		t.Errorf("follow-up ran %v later, want immediately", gap)
	}
}

func TestRepoWorkflow_PolicyChangedPullsNextDueIn(t *testing.T) {
	t.Parallel()

	in := input()
	in.NextDue = time.Now().Add(30 * 24 * time.Hour)
	fakes := &fakeActivities{script: parkAfter(0)}

	var start time.Time

	err := runToEnd(t, fakes, in, func(e testEnv) {
		start = e.Now()
		e.RegisterDelayedCallback(func() {
			e.SignalWorkflow(PolicyChangedSignal, PolicyChanged{Version: "v2:new", By: e.Now().Add(time.Hour)})
		}, time.Minute)
	})
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if len(fakes.times) != 1 {
		t.Fatalf("checks = %d, want 1", len(fakes.times))
	}

	if at := fakes.times[0].Sub(start); at < time.Minute || at > time.Hour+time.Minute {
		t.Errorf("check ran %v after start, want within the rollout window [1m, 1h1m]", at)
	}
}

func TestRepoWorkflow_DeferredConsumesNoRetryAttempt(t *testing.T) {
	t.Parallel()

	fakes := &fakeActivities{}
	fakes.script = func(n int, ci *CheckRepoInput) (*CheckRepoResult, error) {
		switch {
		case n < 3:
			return &CheckRepoResult{Kind: CheckDeferred, CheckKey: ci.CheckKey, Until: fakes.now().Add(time.Hour)}, nil
		case n == 3:
			return &CheckRepoResult{Kind: CheckChecked, CheckKey: ci.CheckKey}, nil
		default:
			return &CheckRepoResult{Kind: CheckParked, CheckKey: ci.CheckKey, ParkReason: ParkFork, ClearFindings: true}, nil
		}
	}

	if err := runToEnd(t, fakes, input(), nil); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	for i := range 4 {
		if fakes.attempts[i] != 1 {
			t.Errorf("call %d attempt = %d, want 1: a deferral is not a retry", i, fakes.attempts[i])
		}

		if fakes.checks[i].Deferrals != i || fakes.checks[i].CheckKey != fakes.checks[0].CheckKey {
			t.Errorf("call %d = %+v, want deferrals %d on the first key", i, fakes.checks[i], i)
		}

		if i > 0 {
			if gap := fakes.times[i].Sub(fakes.times[i-1]); gap < time.Hour {
				t.Errorf("call %d ran %v after the deferral, want the durable timer's hour", i, gap)
			}
		}
	}

	if len(fakes.records) != 1 || len(fakes.errors) != 0 {
		t.Errorf("records = %d, errors = %d, want 1 and 0", len(fakes.records), len(fakes.errors))
	}
}

func TestRepoWorkflow_ParkedCompletes(t *testing.T) {
	t.Parallel()

	t.Run("result", func(t *testing.T) {
		t.Parallel()

		fakes := &fakeActivities{}
		fakes.script = func(_ int, ci *CheckRepoInput) (*CheckRepoResult, error) {
			return &CheckRepoResult{Kind: CheckParked, CheckKey: ci.CheckKey, ParkReason: ParkAccessDenied, Cause: "403"}, nil
		}

		if err := runToEnd(t, fakes, input(), nil); err != nil {
			t.Fatalf("workflow: %v", err)
		}

		want := ParkInput{
			RepositoryID: testRepoID, InstallationID: 7, CheckKey: fakes.checks[0].CheckKey,
			Trigger: TriggerSchedule, Reason: ParkAccessDenied, Cause: "403",
		}
		if len(fakes.parks) != 1 || fakes.parks[0] != want {
			t.Errorf("parks = %+v, want [%+v]", fakes.parks, want)
		}

		if len(fakes.records) != 0 {
			t.Errorf("records = %d, want none for a parked check", len(fakes.records))
		}
	})

	t.Run("signal", func(t *testing.T) {
		t.Parallel()

		in := input()
		in.NextDue = time.Now().Add(365 * 24 * time.Hour)
		fakes := &fakeActivities{}

		err := runToEnd(t, fakes, in, func(e testEnv) {
			e.RegisterDelayedCallback(func() { e.SignalWorkflow(ParkSignal, Park{Reason: ParkArchived, ClearFindings: true}) }, time.Minute)
		})
		if err != nil {
			t.Fatalf("workflow: %v", err)
		}

		if len(fakes.checks) != 0 || len(fakes.parks) != 1 || fakes.parks[0].Reason != ParkArchived || !fakes.parks[0].ClearFindings {
			t.Errorf("checks = %d, parks = %+v, want no check and one archived park", len(fakes.checks), fakes.parks)
		}
	})
}

func TestRepoWorkflow_FailureRecordsErrorAndContinues(t *testing.T) {
	t.Parallel()

	fakes := &fakeActivities{}
	fakes.script = func(n int, ci *CheckRepoInput) (*CheckRepoResult, error) {
		switch {
		case n < checkRepoMaxAttempts:
			return nil, errors.New("github: 502 bad gateway")
		case n == checkRepoMaxAttempts:
			return &CheckRepoResult{Kind: CheckChecked, CheckKey: ci.CheckKey}, nil
		default:
			return &CheckRepoResult{Kind: CheckParked, CheckKey: ci.CheckKey, ParkReason: ParkArchived, ClearFindings: true}, nil
		}
	}

	if err := runToEnd(t, fakes, input(), nil); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if len(fakes.errors) != 1 {
		t.Fatalf("errors = %d, want 1 after %d attempts", len(fakes.errors), checkRepoMaxAttempts)
	}

	if got := fakes.errors[0]; got.CheckKey != fakes.checks[0].CheckKey || got.Error != "github: 502 bad gateway" {
		t.Errorf("error record = %+v", got)
	}

	next := fakes.checks[checkRepoMaxAttempts]
	if next.CheckKey == fakes.checks[0].CheckKey || len(fakes.records) != 1 {
		t.Errorf("after the failure: key %q, records %d; want a new iteration that records", next.CheckKey, len(fakes.records))
	}
}

func TestRepoWorkflow_ContinueAsNewCarriesState(t *testing.T) {
	t.Parallel()

	in := input()
	in.PolicyVersion = "v2:old"
	fakes := &fakeActivities{}

	err := runToEnd(t, fakes, in, func(e testEnv) {
		e.RegisterDelayedCallback(func() {
			e.SignalWorkflow(PolicyChangedSignal, PolicyChanged{Version: "v2:new", By: e.Now()})
		}, time.Hour)
	})

	var can *workflow.ContinueAsNewError
	if !errors.As(err, &can) {
		t.Fatalf("workflow error = %v, want ContinueAsNew", err)
	}

	var next RepoWorkflowInput
	if err := converter.GetDefaultDataConverter().FromPayloads(can.Input, &next); err != nil {
		t.Fatalf("decode ContinueAsNew input: %v", err)
	}

	if next.RepositoryID != testRepoID || next.InstallationID != 7 || next.CheckInterval != testInterval {
		t.Errorf("identity not carried: %+v", next)
	}

	if next.Iteration != maxIterationsPerRun || next.PolicyVersion != "v2:new" || next.NextDue.IsZero() {
		t.Errorf("state = iteration %d, policy %q, next_due %v; want %d, v2:new, set",
			next.Iteration, next.PolicyVersion, next.NextDue, maxIterationsPerRun)
	}

	if len(fakes.records) != maxIterationsPerRun {
		t.Errorf("records = %d, want %d", len(fakes.records), maxIterationsPerRun)
	}
}

func TestRepoWorkflow_BudgetWaitThenGrant(t *testing.T) {
	t.Parallel()

	var waitUntil time.Time

	fakes := &fakeActivities{script: parkAfter(1)}
	fakes.grant = func(n int) *AcquireResult {
		if n == 0 {
			waitUntil = fakes.now().Add(40 * time.Minute)

			return &AcquireResult{WaitUntil: waitUntil}
		}

		return &AcquireResult{Granted: true, LeaseID: "lease-" + strconv.Itoa(n)}
	}

	if err := runToEnd(t, fakes, input(), nil); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if len(fakes.acquire) < 2 || fakes.acquire[0].UpdateID == fakes.acquire[1].UpdateID {
		t.Fatalf("acquires = %+v, want a retry under a new update id", fakes.acquire)
	}

	if fakes.acquire[0].Request.Priority != PrioritySchedule || fakes.acquire[0].InstallationID != 7 {
		t.Errorf("acquire = %+v, want the scheduled priority for installation 7", fakes.acquire[0])
	}

	if fakes.times[0].Before(waitUntil) {
		t.Errorf("check ran at %v, before the wait ended at %v", fakes.times[0], waitUntil)
	}

	if len(fakes.reports) == 0 || fakes.reports[0].LeaseID != "lease-1" {
		t.Errorf("reports = %+v, want the granted lease returned", fakes.reports)
	}
}

func TestRepoWorkflow_DeferredReportClosesTheGate(t *testing.T) {
	t.Parallel()

	var until time.Time

	fakes := &fakeActivities{}
	fakes.script = func(n int, ci *CheckRepoInput) (*CheckRepoResult, error) {
		if n == 0 {
			until = fakes.now().Add(time.Hour)

			return &CheckRepoResult{Kind: CheckDeferred, CheckKey: ci.CheckKey, Calls: 3, Until: until}, nil
		}

		return &CheckRepoResult{Kind: CheckParked, CheckKey: ci.CheckKey, ParkReason: ParkArchived, ClearFindings: true}, nil
	}

	if err := runToEnd(t, fakes, input(), nil); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	got := fakes.reports[0]
	if got.Remaining != 0 || !got.Reset.Equal(until) || got.Calls != 3 || got.ObservedAt.IsZero() {
		t.Errorf("deferred report = %+v, want remaining 0 until %v with 3 calls", got, until)
	}
}
