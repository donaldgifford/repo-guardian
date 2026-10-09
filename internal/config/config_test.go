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

	cfg, err := LoadRole(RoleWorker)
	if err != nil {
		t.Fatalf("LoadRole(worker) = _, %v, want nil", err)
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

	if !cfg.SkipForks {
		t.Error("SkipForks should default to true")
	}

	if !cfg.SkipArchived {
		t.Error("SkipArchived should default to true")
	}

	if cfg.DryRun {
		t.Error("DryRun should default to false")
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

// TestLoadRole_IgnoresRemovedV1Vars pins that the v1 runtime's knobs no
// longer reach config: an unparseable value used to fail load, and now
// the binary only warns about it (cmd/repo-guardian warnRemovedEnvVars).
func TestLoadRole_IgnoresRemovedV1Vars(t *testing.T) {
	setWorkerEnv(t)

	for _, name := range []string{
		"STORE_BACKEND", "QUEUE_BACKEND", "SCHEDULER_BACKEND", "QUEUE_VALKEY_DSN",
		"JOB_ACK_TIMEOUT", "REAPER_INTERVAL", "MAX_JOB_ATTEMPTS", "POD_NAME",
		"STALE_SWEEP_BATCH_SIZE", "POSTURE_EXPORT_INTERVAL", "WORKER_COUNT",
		"QUEUE_SIZE", "SCHEDULE_INTERVAL",
	} {
		t.Setenv(name, "not-a-valid-value")
	}

	if _, err := LoadRole(RoleWorker); err != nil {
		t.Fatalf("LoadRole(worker) with removed v1 vars set = %v, want nil", err)
	}
}

func TestLoadRole_Overrides(t *testing.T) {
	setWorkerEnv(t)
	t.Setenv("GITHUB_APP_ID", "99999")
	t.Setenv("LISTEN_ADDR", ":9999")
	t.Setenv("METRICS_ADDR", ":7777")
	t.Setenv("TEMPLATE_DIR", "/custom/templates")
	t.Setenv("SKIP_FORKS", "false")
	t.Setenv("SKIP_ARCHIVED", "false")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("RATE_LIMIT_THRESHOLD", "0.25")

	cfg, err := LoadRole(RoleWorker)
	if err != nil {
		t.Fatalf("LoadRole(worker) = _, %v, want nil", err)
	}

	if cfg.GitHubAppID != 99999 {
		t.Errorf("GitHubAppID = %d, want 99999", cfg.GitHubAppID)
	}

	if cfg.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999", cfg.ListenAddr)
	}

	if cfg.MetricsAddr != ":7777" {
		t.Errorf("MetricsAddr = %q, want :7777", cfg.MetricsAddr)
	}

	if cfg.TemplateDir != "/custom/templates" {
		t.Errorf("TemplateDir = %q, want /custom/templates", cfg.TemplateDir)
	}

	if cfg.SkipForks || cfg.SkipArchived {
		t.Error("SkipForks and SkipArchived should be false")
	}

	if !cfg.DryRun {
		t.Error("DryRun should be true")
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
		{"GITHUB_APP_ID", "not-a-number"},
		{"RATE_LIMIT_THRESHOLD", "not-a-float"},
		{"SKIP_FORKS", "yes"},
		{"DISCOVERY_INTERVAL", "not-a-duration"},
		{"COMPLIANCE_SNAPSHOT_INTERVAL", "not-a-duration"},
	} {
		t.Run(tt.env, func(t *testing.T) {
			setWorkerEnv(t)
			t.Setenv(tt.env, tt.value)

			_, err := LoadRole(RoleWorker)
			if err == nil {
				t.Fatalf("LoadRole(worker) with %s=%q = _, nil, want an error", tt.env, tt.value)
			}

			if !strings.Contains(err.Error(), tt.env) {
				t.Errorf("LoadRole(worker) error = %v, want it to name %s", err, tt.env)
			}
		})
	}
}

func TestLoadRole_PrivateKeyFromEnvVar(t *testing.T) {
	setWorkerEnv(t)
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----")

	cfg, err := LoadRole(RoleWorker)
	if err != nil {
		t.Fatalf("LoadRole(worker) = _, %v, want nil", err)
	}

	if cfg.GitHubPrivateKey == "" || cfg.GitHubPrivateKeyPath != "" {
		t.Errorf("GitHubPrivateKey set = %v, GitHubPrivateKeyPath = %q; want the env key only",
			cfg.GitHubPrivateKey != "", cfg.GitHubPrivateKeyPath)
	}
}

func TestLoadRole_PrivateKeyBothSet(t *testing.T) {
	setWorkerEnv(t)
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")

	_, err := LoadRole(RoleWorker)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("LoadRole(worker) with both keys set = %v, want a mutually exclusive error", err)
	}
}
