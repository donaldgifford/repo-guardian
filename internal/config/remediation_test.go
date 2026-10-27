package config

import (
	"strings"
	"testing"
	"time"
)

// TestParse_RemediationConfig is IMPL-0030 6.6: the remediator's
// lifecycle knobs default, parse, and refuse a non-positive value (a
// negative one for the sweep spacing, where 0 turns pacing off).
func TestParse_RemediationConfig(t *testing.T) {
	cfg, err := parse()
	if err != nil {
		t.Fatalf("parse = %v", err)
	}

	if want := (RemediationConfig{ReservationTTL: time.Hour, SweepBatch: 500, MaintenanceBatch: 200, SweepSpacing: 30 * time.Second}); cfg.Remediation != want {
		t.Errorf("defaults = %+v, want %+v", cfg.Remediation, want)
	}

	t.Setenv("REMEDIATION_RESERVATION_TTL", "30m")
	t.Setenv("REMEDIATION_SWEEP_BATCH", "50")
	t.Setenv("REMEDIATION_MAINTENANCE_BATCH", "20")
	t.Setenv("REMEDIATION_SWEEP_SPACING", "0s")

	if cfg, err = parse(); err != nil {
		t.Fatalf("parse = %v", err)
	}

	if want := (RemediationConfig{ReservationTTL: 30 * time.Minute, SweepBatch: 50, MaintenanceBatch: 20}); cfg.Remediation != want {
		t.Errorf("set = %+v, want %+v", cfg.Remediation, want)
	}

	for key, bad := range map[string]string{
		"REMEDIATION_RESERVATION_TTL":   "0s",
		"REMEDIATION_SWEEP_BATCH":       "0",
		"REMEDIATION_MAINTENANCE_BATCH": "-3",
		"REMEDIATION_SWEEP_SPACING":     "-1s",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, bad)

			if _, err := parse(); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("parse with %s=%s = %v, want an error naming it", key, bad, err)
			}
		})
	}
}
