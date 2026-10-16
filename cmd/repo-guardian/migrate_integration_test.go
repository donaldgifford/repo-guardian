//go:build integration

package main

import (
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// TestMigrateControls_ActivatesThePolicy is IMPL-0029 4.5's activation
// step: migrate --chain controls applies the chain and activates the
// policy root's version as the owner, and a second run (an Argo CD
// re-sync) activates nothing new.
func TestMigrateControls_ActivatesThePolicy(t *testing.T) {
	dsns := pgtest.ControlsRoles(t, pgtest.Start(t))
	opts := migrateOptions{chain: chainControls, dsn: dsns.Owner, policy: "../../examples/policy/minimal"}

	first, err := migrate(t.Context(), &opts)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if first.Activation == nil || !first.Activation.FirstSeen || first.Activation.Version == "" {
		t.Fatalf("first migrate activation = %+v, want a first-seen version", first.Activation)
	}

	again, err := migrate(t.Context(), &opts)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	if again.Activation == nil || again.Activation.FirstSeen || again.Activation.Version != first.Activation.Version {
		t.Errorf("second migrate activation = %+v, want %s again, not first seen", again.Activation, first.Activation.Version)
	}
}
