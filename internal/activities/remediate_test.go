package activities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// fakeRemediationStore answers RemediationTarget from targets in turn
// (the last one repeats) and records holds.
type fakeRemediationStore struct {
	mu      sync.Mutex
	targets []*store.RemediationTarget
	reads   int
	holds   []string
}

func (f *fakeRemediationStore) RemediationTarget(context.Context, int64, string) (*store.RemediationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.targets) == 0 {
		return nil, nil //nolint:nilnil // a withdrawn control
	}

	t := f.targets[min(f.reads, len(f.targets)-1)]
	f.reads++

	if t == nil {
		return nil, nil //nolint:nilnil // a withdrawn control
	}

	c := *t

	return &c, nil
}

func (f *fakeRemediationStore) SetHold(_ context.Context, _ int64, _ string, _ int64, hold string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.holds = append(f.holds, hold)

	return true, nil
}

type fakeRemediationClients struct{ opened int }

func (f *fakeRemediationClients) Repo(context.Context, int64, string, string) (RemediationRepo, error) {
	f.opened++

	return fakeRemediationRepo{}, nil
}

type fakeRemediationRepo struct{}

func (fakeRemediationRepo) Writer() control.Writer       { return nil }
func (fakeRemediationRepo) Reader(string) control.Reader { return nil }
func (fakeRemediationRepo) Observer() control.PRObserver { return nil }

// remediable is a target a run acts on.
func remediable() *store.RemediationTarget {
	return &store.RemediationTarget{
		Org: "acme", Name: "svc", Active: true, Epoch: 3, State: "active", EffectiveMode: "remediate", Revision: "r1",
		InstallationID: 7, Remediable: true, HasResult: true, ResultEpoch: 3, Generation: 5, IntentRevision: "i5",
	}
}

func remediateInput() *workflows.RemediateInput {
	return &workflows.RemediateInput{RepositoryID: 9, Control: "codeowners", InstallationID: 7, OperationKey: "remediation/9/codeowners/r/0"}
}

// runRemediate runs Remediate once in a test activity environment with
// act as the run's work.
func runRemediate(t *testing.T, st *fakeRemediationStore, act remediationAct) (*workflows.RemediateResult, error) {
	t.Helper()

	var suite testsuite.WorkflowTestSuite

	r := NewRemediator(st, &fakeRemediationClients{}, ghclient.BotIdentity{}, slog.New(slog.DiscardHandler))
	r.act = act

	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(r.Remediate, activity.RegisterOptions{Name: workflows.RemediateActivity})

	val, err := env.ExecuteActivity(workflows.RemediateActivity, remediateInput())
	if err != nil {
		return nil, err
	}

	var res workflows.RemediateResult
	if err := val.Get(&res); err != nil {
		t.Fatal(err)
	}

	return &res, nil
}

// TestRemediate_OperationHeldExitsCleanly is IMPL-0030 3.3: a second
// attempt that finds the operation claimed does nothing and returns a
// result, so the retry policy never sees it.
func TestRemediate_OperationHeldExitsCleanly(t *testing.T) {
	t.Parallel()

	st := &fakeRemediationStore{targets: []*store.RemediationTarget{remediable()}}

	res, err := runRemediate(t, st, func(context.Context, *remediationRun) error {
		return store.ErrOperationHeld
	})
	if err != nil {
		t.Fatalf("Remediate = %v, want a clean result", err)
	}

	if res.Kind != workflows.RemediationBusy || len(st.holds) != 0 {
		t.Errorf("Remediate = %+v, holds %v; want busy and nothing recorded", res, st.holds)
	}

	if res.Start != (workflows.RemediationState{Generation: 5, IntentRevision: "i5"}) {
		t.Errorf("start state = %+v, want the target's", res.Start)
	}
}

// TestRemediate_Classification is IMPL-0030 3.6, in order: a throttle
// defers without recording (a secondary-limit 403 is a throttle first),
// access loss holds permission, then the run's own outcomes.
func TestRemediate_Classification(t *testing.T) {
	t.Parallel()

	forbidden := &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusForbidden}}

	for _, tt := range []struct {
		name  string
		err   error
		kind  workflows.RemediationKind
		holds []string
	}{
		{name: "done", kind: workflows.RemediationDone},
		{name: "throttle", err: fmt.Errorf("commit: %w", &ghclient.ThrottledError{ResetAt: time.Now().Add(time.Hour)}), kind: workflows.RemediationDeferred},
		{
			name: "secondary limit 403",
			err:  fmt.Errorf("commit: %w", &gh.AbuseRateLimitError{Response: &http.Response{StatusCode: http.StatusForbidden}}),
			kind: workflows.RemediationDeferred,
		},
		{name: "access loss", err: fmt.Errorf("commit: %w", forbidden), kind: workflows.RemediationHeld, holds: []string{store.HoldPermission}},
		{name: "stale epoch", err: ErrStaleEpoch, kind: workflows.RemediationStale},
		{name: "hold", err: &holdError{hold: store.HoldForeignBranch}, kind: workflows.RemediationHeld, holds: []string{store.HoldForeignBranch}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := &fakeRemediationStore{targets: []*store.RemediationTarget{remediable()}}

			res, err := runRemediate(t, st, func(context.Context, *remediationRun) error { return tt.err })
			if err != nil {
				t.Fatalf("Remediate = %v, want a result", err)
			}

			if res.Kind != tt.kind || !slices.Equal(st.holds, tt.holds) {
				t.Errorf("Remediate = %s, holds %v; want %s, %v", res.Kind, st.holds, tt.kind, tt.holds)
			}

			if (res.Kind == workflows.RemediationDeferred) != !res.Until.IsZero() {
				t.Errorf("Until = %v for %s", res.Until, res.Kind)
			}
		})
	}
}

// TestRemediate_OperationalErrorRetries: anything unclassified is an
// error, which the retry policy retries.
func TestRemediate_OperationalErrorRetries(t *testing.T) {
	t.Parallel()

	st := &fakeRemediationStore{targets: []*store.RemediationTarget{remediable()}}

	if _, err := runRemediate(t, st, func(context.Context, *remediationRun) error { return errors.New("boom") }); err == nil {
		t.Fatal("Remediate = nil, want the error for the retry policy")
	}
}

// TestRemediate_ActsOnlyInRemediateMode: a target the run must not touch
// completes with nothing done.
func TestRemediate_ActsOnlyInRemediateMode(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(*store.RemediationTarget){
		"evaluate mode": func(t *store.RemediationTarget) { t.EffectiveMode = "evaluate" },
		"excluded":      func(t *store.RemediationTarget) { t.State = "excluded" },
		"parked":        func(t *store.RemediationTarget) { t.Active = false },
		"unreachable":   func(t *store.RemediationTarget) { t.Remediable = false },
	} {
		target := remediable()
		change(target)

		acted := false
		st := &fakeRemediationStore{targets: []*store.RemediationTarget{target}}

		res, err := runRemediate(t, st, func(context.Context, *remediationRun) error { acted = true; return nil })
		if err != nil || acted || res.Kind != workflows.RemediationDone {
			t.Errorf("%s: Remediate = %+v, %v, acted %v; want done without acting", name, res, err, acted)
		}
	}
}

// claimOnce models ClaimOperation's row lock: the first attempt holds
// it until release; a concurrent attempt gets ErrOperationHeld.
type claimOnce struct {
	mu      sync.Mutex
	held    bool
	acted   int
	release chan struct{}
}

func (c *claimOnce) act(context.Context, *remediationRun) error {
	c.mu.Lock()
	if c.held {
		c.mu.Unlock()

		return store.ErrOperationHeld
	}

	c.held = true
	c.acted++
	c.mu.Unlock()

	<-c.release

	return nil
}

// TestRemediate_OverlappingAttemptsActOnce is IMPL-0030 3.10: a retried
// attempt that overlaps one still running (its StartToClose passed, the
// worker did not die) finds the operation claimed and returns busy; the
// remediation acts once.
func TestRemediate_OverlappingAttemptsActOnce(t *testing.T) {
	t.Parallel()

	claim := &claimOnce{release: make(chan struct{})}
	st := &fakeRemediationStore{targets: []*store.RemediationTarget{remediable()}}

	type outcome struct {
		res *workflows.RemediateResult
		err error
	}

	first := make(chan outcome, 1)

	go func() {
		res, err := runRemediate(t, st, claim.act)
		first <- outcome{res, err}
	}()

	// Wait for the first attempt to hold the claim.
	for {
		claim.mu.Lock()
		held := claim.held
		claim.mu.Unlock()

		if held {
			break
		}

		time.Sleep(time.Millisecond)
	}

	second, err := runRemediate(t, st, claim.act)
	if err != nil || second.Kind != workflows.RemediationBusy {
		t.Fatalf("overlapping attempt = %+v, %v; want busy", second, err)
	}

	close(claim.release)

	if got := <-first; got.err != nil || got.res.Kind != workflows.RemediationDone {
		t.Errorf("first attempt = %+v, %v; want done", got.res, got.err)
	}

	if claim.acted != 1 {
		t.Errorf("acted %d times, want once", claim.acted)
	}
}
