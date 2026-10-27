package config

import (
	"fmt"
	"time"
)

// RemediationConfig is the remediator's lifecycle configuration
// (IMPL-0030 6.6).
type RemediationConfig struct {
	// ReservationTTL is REMEDIATION_RESERVATION_TTL: how long a reserved
	// pull-request slot waits for its PR before maintenance frees it.
	// Default 1h.
	ReservationTTL time.Duration
	// SweepBatch is REMEDIATION_SWEEP_BATCH: the due remediations one
	// sweep starts. Default 500.
	SweepBatch int
	// MaintenanceBatch is REMEDIATION_MAINTENANCE_BATCH: the lifecycle
	// candidates one sweep handles. Default 200.
	MaintenanceBatch int
	// SweepSpacing is REMEDIATION_SWEEP_SPACING: the gap between two
	// remediations one sweep starts on the same installation, so a first
	// enablement does not trip GitHub's secondary rate limit (issue
	// #235). Default 30s, about 120 runs an hour against GitHub's 500
	// content-creating requests an hour; 0 starts them all at once.
	SweepSpacing time.Duration
}

// Remediation defaults.
const (
	defaultReservationTTL   = time.Hour
	defaultSweepBatch       = 500
	defaultMaintenanceBatch = 200
	defaultSweepSpacing     = 30 * time.Second
)

func parseRemediationConfig() (RemediationConfig, error) {
	ttl, err := envOrDefaultDuration("REMEDIATION_RESERVATION_TTL", defaultReservationTTL)
	if err != nil {
		return RemediationConfig{}, err
	}

	if ttl <= 0 {
		return RemediationConfig{}, fmt.Errorf("REMEDIATION_RESERVATION_TTL must be positive, got %s", ttl)
	}

	spacing, err := envOrDefaultDuration("REMEDIATION_SWEEP_SPACING", defaultSweepSpacing)
	if err != nil {
		return RemediationConfig{}, err
	}

	if spacing < 0 {
		return RemediationConfig{}, fmt.Errorf("REMEDIATION_SWEEP_SPACING must not be negative, got %s", spacing)
	}

	cfg := RemediationConfig{ReservationTTL: ttl, SweepSpacing: spacing}

	for _, b := range []struct {
		key  string
		def  int
		dest *int
	}{
		{"REMEDIATION_SWEEP_BATCH", defaultSweepBatch, &cfg.SweepBatch},
		{"REMEDIATION_MAINTENANCE_BATCH", defaultMaintenanceBatch, &cfg.MaintenanceBatch},
	} {
		v, err := envOrDefaultInt(b.key, b.def)
		if err != nil {
			return RemediationConfig{}, err
		}

		if v <= 0 {
			return RemediationConfig{}, fmt.Errorf("%s must be a positive integer, got %d", b.key, v)
		}

		*b.dest = v
	}

	return cfg, nil
}
