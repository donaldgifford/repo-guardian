package budget_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/budget"
)

// fakeRateLimit is a test RateLimitClient. Captures the call count
// so tests can assert the tracker isn't over-fetching.
type fakeRateLimit struct {
	remaining int
	limit     int
	resetAt   time.Time
	err       error

	calls atomic.Int32
}

func (f *fakeRateLimit) RateLimitRemaining(_ context.Context, _ int64) (int, int, time.Time, error) {
	f.calls.Add(1)
	return f.remaining, f.limit, f.resetAt, f.err
}

func TestTracker_SpendableForEnqueue_NoSnapshot_ReturnsErrNoSnapshot(t *testing.T) {
	t.Parallel()

	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	got, err := tr.SpendableForEnqueue(42)
	if !errors.Is(err, budget.ErrNoSnapshot) {
		t.Errorf("err = %v, want ErrNoSnapshot", err)
	}

	if got != 0 {
		t.Errorf("got = %d, want 0", got)
	}
}

func TestTracker_RefreshThenSpendable(t *testing.T) {
	t.Parallel()

	rl := &fakeRateLimit{remaining: 5000, limit: 5000, resetAt: time.Now().Add(time.Hour)}
	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	if err := tr.RefreshFromAPI(context.Background(), rl, 42); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// reserve = 1000 (20% of 5000); usable = 4000; spendable = 400.
	got, err := tr.SpendableForEnqueue(42)
	if err != nil {
		t.Fatalf("Spendable: %v", err)
	}

	if got != 400 {
		t.Errorf("got = %d, want 400 (reserve 20%% of 5000 = 1000 floor, (5000-1000)/10 = 400)", got)
	}
}

func TestTracker_BudgetExhaustedGate(t *testing.T) {
	t.Parallel()

	rl := &fakeRateLimit{remaining: 100, limit: 5000, resetAt: time.Now().Add(time.Hour)}
	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	if err := tr.RefreshFromAPI(context.Background(), rl, 42); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// remaining=100 is below reserve floor (1000); spendable=0.
	got, err := tr.SpendableForEnqueue(42)
	if err != nil {
		t.Fatalf("Spendable: %v", err)
	}

	if got != 0 {
		t.Errorf("got = %d, want 0 (remaining below reserve floor)", got)
	}
}

func TestTracker_Decrement_RoundsDown(t *testing.T) {
	t.Parallel()

	rl := &fakeRateLimit{remaining: 2000, limit: 5000, resetAt: time.Now().Add(time.Hour)}
	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	if err := tr.RefreshFromAPI(context.Background(), rl, 42); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// usable = 2000 - 1000 = 1000; spendable = 100.
	pre, _ := tr.SpendableForEnqueue(42)
	if pre != 100 {
		t.Fatalf("pre-decrement spendable = %d, want 100", pre)
	}

	// Decrement 50 times = 500 budget burned.
	for range 50 {
		tr.Decrement(42)
	}

	// remaining = 2000 - 500 = 1500; usable = 500; spendable = 50.
	post, _ := tr.SpendableForEnqueue(42)
	if post != 50 {
		t.Errorf("post-decrement spendable = %d, want 50", post)
	}
}

func TestTracker_ResetAtElapsed_RequiresRefresh(t *testing.T) {
	t.Parallel()

	rl := &fakeRateLimit{remaining: 5000, limit: 5000, resetAt: time.Now().Add(-time.Minute)}
	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	if err := tr.RefreshFromAPI(context.Background(), rl, 42); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, err := tr.SpendableForEnqueue(42)
	if !errors.Is(err, budget.ErrNoSnapshot) {
		t.Errorf("err = %v, want ErrNoSnapshot (resetAt elapsed)", err)
	}

	if got != 0 {
		t.Errorf("got = %d, want 0", got)
	}
}

func TestTracker_MultiInstallationIsolation(t *testing.T) {
	t.Parallel()

	rl := &fakeRateLimit{remaining: 5000, limit: 5000, resetAt: time.Now().Add(time.Hour)}
	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	if err := tr.RefreshFromAPI(context.Background(), rl, 1); err != nil {
		t.Fatalf("Refresh inst 1: %v", err)
	}

	if err := tr.RefreshFromAPI(context.Background(), rl, 2); err != nil {
		t.Fatalf("Refresh inst 2: %v", err)
	}

	// Burn budget only on inst 1.
	for range 50 {
		tr.Decrement(1)
	}

	got1, _ := tr.SpendableForEnqueue(1)
	got2, _ := tr.SpendableForEnqueue(2)

	if got1 == got2 {
		t.Errorf("installations not isolated: inst1=%d, inst2=%d (want inst1<inst2)", got1, got2)
	}

	if got2 != 400 {
		t.Errorf("inst 2 spendable = %d, want 400 (unaffected by inst 1 Decrement)", got2)
	}
}

func TestTracker_RefreshError_Propagates(t *testing.T) {
	t.Parallel()

	rl := &fakeRateLimit{err: errors.New("upstream broken")}
	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	err := tr.RefreshFromAPI(context.Background(), rl, 42)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	got, gErr := tr.SpendableForEnqueue(42)
	if !errors.Is(gErr, budget.ErrNoSnapshot) {
		t.Errorf("err = %v, want ErrNoSnapshot (no snapshot was cached due to refresh error)", gErr)
	}

	if got != 0 {
		t.Errorf("got = %d, want 0", got)
	}
}

func TestTracker_UnknownLimit_FallsOpen(t *testing.T) {
	t.Parallel()

	rl := &fakeRateLimit{remaining: 0, limit: 0, resetAt: time.Now().Add(time.Hour)}
	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	if err := tr.RefreshFromAPI(context.Background(), rl, 42); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Limit ≤ 0 → unknown → fall open via ErrNoSnapshot.
	got, err := tr.SpendableForEnqueue(42)
	if !errors.Is(err, budget.ErrNoSnapshot) {
		t.Errorf("err = %v, want ErrNoSnapshot (unknown limit must fall open)", err)
	}

	if got != 0 {
		t.Errorf("got = %d, want 0", got)
	}
}

func TestTracker_Decrement_NoSnapshot_NoOp(t *testing.T) {
	t.Parallel()

	tr := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})

	// Must not panic on Decrement before any snapshot is cached.
	tr.Decrement(42)
}

func TestTracker_InvalidOptions_Panic(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		opts budget.Options
	}{
		{"negative reserve fraction", budget.Options{ReserveFraction: -0.1, CostPerRepo: 10}},
		{"reserve fraction > 1", budget.Options{ReserveFraction: 1.5, CostPerRepo: 10}},
		{"zero cost per repo", budget.Options{ReserveFraction: 0.20, CostPerRepo: 0}},
		{"negative cost per repo", budget.Options{ReserveFraction: 0.20, CostPerRepo: -1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if r := recover(); r == nil {
					t.Error("expected panic, got none")
				}
			}()

			budget.New(tc.opts)
		})
	}
}
