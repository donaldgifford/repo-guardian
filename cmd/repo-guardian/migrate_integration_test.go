//go:build integration

package main

import (
	"slices"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
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

// TestMigrateControls_DryRun is IMPL-0029 4.10: --dry-run rehearses
// every controls migration on an empty database in one rolled-back
// transaction and activates nothing.
func TestMigrateControls_DryRun(t *testing.T) {
	dsns := pgtest.ControlsRoles(t, pgtest.Start(t))

	res, err := migrate(t.Context(), &migrateOptions{chain: chainControls, dsn: dsns.Owner, dryRun: true, policy: "../../examples/policy/minimal"})
	if err != nil {
		t.Fatalf("migrate --dry-run: %v", err)
	}

	want := make([]int64, 0, postgres.ControlsSchemaVersion)
	for v := int64(1); v <= postgres.ControlsSchemaVersion; v++ {
		want = append(want, v)
	}

	if !slices.Equal(res.Pending, want) || res.Activation != nil || res.Version != 0 {
		t.Errorf("dry run = pending %v, activation %+v, version %d; want %v, none, 0", res.Pending, res.Activation, res.Version, want)
	}
}
