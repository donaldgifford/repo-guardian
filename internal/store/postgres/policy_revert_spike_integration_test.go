//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// NOTE: IMPL-0028 task 0.13 (INV-0022 spike 8). The first half pins the
// rc's revert bug; the second half prototypes DESIGN-0030 D14's
// activated_at upsert against the same rows. IMPL-0028 Phase 6 builds the
// controls schema with activated_at from the start and replaces this test.
func TestSpike_PolicyRevert(t *testing.T) {
	f := newV2Fixture(t)
	ctx := t.Context()

	record := func(version string) bool {
		t.Helper()

		firstSeen, err := f.store.RecordPolicyVersion(ctx, version, store.PolicySummary{})
		if err != nil {
			t.Fatalf("RecordPolicyVersion(%q): %v", version, err)
		}

		// first_seen_at is now(); keep the three deploys apart.
		time.Sleep(10 * time.Millisecond)

		return firstSeen
	}

	current := func() string {
		t.Helper()

		// The rc's CurrentPolicyVersion query (queries/policy.sql), verbatim.
		var v string
		if err := f.pool.QueryRow(ctx, `SELECT version FROM policy_versions ORDER BY first_seen_at DESC, version DESC LIMIT 1`).Scan(&v); err != nil {
			t.Fatalf("CurrentPolicyVersion: %v", err)
		}

		return v
	}

	// Deploy A, deploy B, revert to A.
	if !record("v2:a") || !record("v2:b") {
		t.Fatal("first deploys of A and B should both be first seen")
	}

	if record("v2:a") {
		t.Error("rc: revert to A reported first seen; the bug is gone, update INV-0022")
	}

	if got := current(); got != "v2:b" {
		t.Errorf("rc: current after revert = %q; the spike expected the bug to report v2:b", got)
	}

	// Prototype: the column and the upsert DESIGN-0030 D14 specifies. The
	// application role owns no DDL rights in production; the spike runs it
	// as the same role only because the test database lets it.
	if _, err := f.pool.Exec(ctx, `ALTER TABLE policy_versions ADD COLUMN activated_at TIMESTAMPTZ NOT NULL DEFAULT now()`); err != nil {
		t.Fatalf("add activated_at: %v", err)
	}

	const activate = `INSERT INTO policy_versions (version, summary) VALUES ($1, '{}')
ON CONFLICT (version) DO UPDATE SET activated_at = now()
RETURNING (xmax = 0) AS first_seen`

	const newest = `SELECT version FROM policy_versions ORDER BY activated_at DESC, version DESC LIMIT 1`

	steps := []struct {
		version       string
		wantFirstSeen bool
		wantCurrent   string
	}{
		{"v2:a", false, "v2:a"}, // the revert, replayed under the fix
		{"v2:b", false, "v2:b"},
		{"v2:c", true, "v2:c"},
		{"v2:a", false, "v2:a"},
	}

	for _, s := range steps {
		time.Sleep(10 * time.Millisecond)

		var firstSeen bool
		if err := f.pool.QueryRow(ctx, activate, s.version).Scan(&firstSeen); err != nil {
			t.Fatalf("activate %q: %v", s.version, err)
		}

		var got string
		if err := f.pool.QueryRow(ctx, newest).Scan(&got); err != nil {
			t.Fatalf("newest: %v", err)
		}

		if firstSeen != s.wantFirstSeen || got != s.wantCurrent {
			t.Errorf("activate(%q) = first seen %v, current %q; want %v, %q", s.version, firstSeen, got, s.wantFirstSeen, s.wantCurrent)
		}
	}

	// first_seen_at survives every reactivation: history is kept.
	var firstA, firstB time.Time
	if err := f.pool.QueryRow(ctx, `SELECT
  (SELECT first_seen_at FROM policy_versions WHERE version = 'v2:a'),
  (SELECT first_seen_at FROM policy_versions WHERE version = 'v2:b')`).Scan(&firstA, &firstB); err != nil {
		t.Fatalf("first_seen_at: %v", err)
	}

	if !firstA.Before(firstB) {
		t.Errorf("first_seen_at(a) = %v, not before first_seen_at(b) = %v", firstA, firstB)
	}
}
