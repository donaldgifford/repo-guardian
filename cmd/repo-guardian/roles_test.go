package main

import (
	"log/slog"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/config"
)

// The chart gives the api role no Temporal env (it never dials), so
// dialTemporal must not fail it for a missing TEMPORAL_ADDRESS.
func TestDialTemporal_APIRoleNeedsNoTemporal(t *testing.T) {
	t.Setenv("TEMPORAL_ADDRESS", "")

	_, tc, err := dialTemporal(t.Context(), config.RoleAPI, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("dialTemporal(api) = %v, want nil", err)
	}

	if tc != nil {
		t.Fatal("dialTemporal(api) dialed Temporal")
	}
}

func TestDialTemporal_OtherRolesRequireTemporal(t *testing.T) {
	t.Setenv("TEMPORAL_ADDRESS", "")

	if _, _, err := dialTemporal(t.Context(), config.RoleIngest, nil, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("dialTemporal(ingest) with no TEMPORAL_ADDRESS = nil, want an error")
	}
}
