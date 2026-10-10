//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// NOTE: IMPL-0028 task 0.13 (INV-0022 spike 8) pins the rc's revert bug.
// Its fix lives on the controls chain: activated_at and
// ControlsStore.ActivatePolicyVersion, tested by
// TestControlsStore_ActivationHonoursARevert.
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
}
