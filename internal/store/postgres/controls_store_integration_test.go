//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// TestControlsStore_ActivationHonoursARevert is IMPL-0028 task 6.8
// (DESIGN-0030 D14): deploying A, B, then A again makes A current, and
// both first_seen_at values survive the reactivation.
func TestControlsStore_ActivationHonoursARevert(t *testing.T) {
	dsns := pgtest.ControlsDB(t)
	ctx := t.Context()

	// Activation is the migrate Job's, as the owner.
	pool, err := pgxpool.New(ctx, dsns.Owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	s := postgres.NewControlsStore(pool)

	if v, _, err := s.CurrentActivation(ctx); err != nil || v != "" {
		t.Fatalf("CurrentActivation on an empty table = %q, %v; want \"\", nil", v, err)
	}

	steps := []struct {
		version       string
		wantFirstSeen bool
	}{
		{"c:a", true},
		{"c:b", true},
		{"c:a", false},
	}

	for _, st := range steps {
		// activated_at is now(); keep the deploys apart.
		time.Sleep(10 * time.Millisecond)

		firstSeen, err := s.ActivatePolicyVersion(ctx, st.version, nil)
		if err != nil {
			t.Fatalf("ActivatePolicyVersion(%q): %v", st.version, err)
		}

		if firstSeen != st.wantFirstSeen {
			t.Errorf("ActivatePolicyVersion(%q) first seen = %v, want %v", st.version, firstSeen, st.wantFirstSeen)
		}

		if v, _, err := s.CurrentActivation(ctx); err != nil || v != st.version {
			t.Errorf("CurrentActivation after %q = %q, %v", st.version, v, err)
		}
	}

	var firstA, firstB time.Time
	if err := pool.QueryRow(ctx, `SELECT
  (SELECT first_seen_at FROM policy_versions WHERE version = 'c:a'),
  (SELECT first_seen_at FROM policy_versions WHERE version = 'c:b')`).Scan(&firstA, &firstB); err != nil {
		t.Fatal(err)
	}

	if !firstA.Before(firstB) {
		t.Errorf("first_seen_at(a) = %v, not before first_seen_at(b) = %v: reactivation rewrote history", firstA, firstB)
	}
}
