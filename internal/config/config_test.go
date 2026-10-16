package config

import (
	"strings"
	"testing"
	"time"
)

// Tests here use t.Setenv, so none is parallel (Go 1.25+ panics on the
// combination).

func TestLoadRole_Defaults(t *testing.T) {
	setWorkerEnv(t)
	setAppEnv(t, envPrefixEval)

	cfg, err := LoadRole(RoleEvaluator)
	if err != nil {
		t.Fatalf("LoadRole(evaluator) = _, %v, want nil", err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}

	if cfg.GitHubHost != "github.com" {
		t.Errorf("GitHubHost = %q, want github.com", cfg.GitHubHost)
	}

	if cfg.MetricsAddr != ":9090" {
		t.Errorf("MetricsAddr = %q, want :9090", cfg.MetricsAddr)
	}

	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}

	if cfg.RateLimitThreshold != 0.10 {
		t.Errorf("RateLimitThreshold = %f, want 0.10", cfg.RateLimitThreshold)
	}

	if !cfg.DiscoveryEnabled {
		t.Error("DiscoveryEnabled should default to true")
	}

	if cfg.DiscoveryInterval != time.Hour {
		t.Errorf("DiscoveryInterval = %v, want 1h", cfg.DiscoveryInterval)
	}

	if cfg.ComplianceSnapshotInterval != defaultComplianceSnapshotInterval {
		t.Errorf("ComplianceSnapshotInterval = %v, want %v", cfg.ComplianceSnapshotInterval, defaultComplianceSnapshotInterval)
	}
}

// TestLoadRole_IgnoresRemovedVars pins that the v1 runtime's knobs and
// the rule engine's (IMPL-0029 7.8) no longer reach config: an
// unparseable value used to fail load, and now the binary only warns
// about it (cmd/repo-guardian warnRemovedEnvVars).
func TestLoadRole_IgnoresRemovedVars(t *testing.T) {
	setWorkerEnv(t)
	setAppEnv(t, envPrefixEval)

	for _, name := range []string{
		"STORE_BACKEND", "QUEUE_BACKEND", "SCHEDULER_BACKEND", "QUEUE_VALKEY_DSN",
		"JOB_ACK_TIMEOUT", "REAPER_INTERVAL", "MAX_JOB_ATTEMPTS", "POD_NAME",
		"STALE_SWEEP_BATCH_SIZE", "POSTURE_EXPORT_INTERVAL", "WORKER_COUNT",
		"QUEUE_SIZE", "SCHEDULE_INTERVAL",
		"GITHUB_APP_ID", "SKIP_FORKS", "SKIP_ARCHIVED", "DRY_RUN", "AUTO_CLOSE_PR", "ORPHAN_CLEANUP",
		"RECONCILE_FRESHNESS", "PR_STALE_AFTER", "CHECK_INTERVAL", "STORE_POSTGRES_MAX_CONNS",
		"WORKER_ACTIVITY_CONCURRENCY", "STRICT_TEMPLATES",
	} {
		t.Setenv(name, "not-a-valid-value")
	}

	if _, err := LoadRole(RoleEvaluator); err != nil {
		t.Fatalf("LoadRole(evaluator) with removed v1 vars set = %v, want nil", err)
	}
}

func TestLoadRole_Overrides(t *testing.T) {
	setWorkerEnv(t)
	setAppEnv(t, envPrefixEval)
	t.Setenv("LISTEN_ADDR", ":9999")
	t.Setenv("METRICS_ADDR", ":7777")
	t.Setenv("EVAL_INTERVAL", "6h")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("RATE_LIMIT_THRESHOLD", "0.25")

	cfg, err := LoadRole(RoleEvaluator)
	if err != nil {
		t.Fatalf("LoadRole(evaluator) = _, %v, want nil", err)
	}

	if cfg.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999", cfg.ListenAddr)
	}

	if cfg.MetricsAddr != ":7777" {
		t.Errorf("MetricsAddr = %q, want :7777", cfg.MetricsAddr)
	}

	if cfg.EvalInterval != 6*time.Hour {
		t.Errorf("EvalInterval = %v, want 6h", cfg.EvalInterval)
	}

	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}

	if cfg.RateLimitThreshold != 0.25 {
		t.Errorf("RateLimitThreshold = %f, want 0.25", cfg.RateLimitThreshold)
	}
}

func TestLoadRole_InvalidValues(t *testing.T) {
	for _, tt := range []struct{ env, value string }{
		{"RATE_LIMIT_THRESHOLD", "not-a-float"},
		{"DISCOVERY_ENABLED", "yes"},
		{"EVAL_INTERVAL", "not-a-duration"},
		{"DISCOVERY_INTERVAL", "not-a-duration"},
		{"COMPLIANCE_SNAPSHOT_INTERVAL", "not-a-duration"},
	} {
		t.Run(tt.env, func(t *testing.T) {
			setWorkerEnv(t)
			setAppEnv(t, envPrefixEval)
			t.Setenv(tt.env, tt.value)

			_, err := LoadRole(RoleEvaluator)
			if err == nil {
				t.Fatalf("LoadRole(evaluator) with %s=%q = _, nil, want an error", tt.env, tt.value)
			}

			if !strings.Contains(err.Error(), tt.env) {
				t.Errorf("LoadRole(evaluator) error = %v, want it to name %s", err, tt.env)
			}
		})
	}
}
