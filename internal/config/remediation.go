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
}

// Remediation defaults.
const (
	defaultReservationTTL   = time.Hour
	defaultSweepBatch       = 500
	defaultMaintenanceBatch = 200
)

func parseRemediationConfig() (RemediationConfig, error) {
	ttl, err := envOrDefaultDuration("REMEDIATION_RESERVATION_TTL", defaultReservationTTL)
	if err != nil {
		return RemediationConfig{}, err
	}

	if ttl <= 0 {
		return RemediationConfig{}, fmt.Errorf("REMEDIATION_RESERVATION_TTL must be positive, got %s", ttl)
	}

	cfg := RemediationConfig{ReservationTTL: ttl}

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
