package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Cannot use t.Parallel with t.Setenv.
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	// Backend DSNs are required since IMPL-0016 (no in-process
	// fallback). Provide placeholder values so Load validates.
	t.Setenv("STORE_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("QUEUE_VALKEY_DSN", "redis://localhost:6379/0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}

	if cfg.MetricsAddr != ":9090" {
		t.Errorf("MetricsAddr = %q, want :9090", cfg.MetricsAddr)
	}

	if cfg.WorkerCount != 5 {
		t.Errorf("WorkerCount = %d, want 5", cfg.WorkerCount)
	}

	if cfg.QueueSize != 1000 {
		t.Errorf("QueueSize = %d, want 1000", cfg.QueueSize)
	}

	if cfg.ScheduleInterval != 168*time.Hour {
		t.Errorf("ScheduleInterval = %v, want 168h", cfg.ScheduleInterval)
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

	if !cfg.WebhookIPAllowlist {
		t.Error("WebhookIPAllowlist should default to true")
	}

	if cfg.WebhookIPAllowlistFailOpen {
		t.Error("WebhookIPAllowlistFailOpen should default to false")
	}

	if cfg.TrustProxyHeaders {
		t.Error("TrustProxyHeaders should default to false")
	}

	if cfg.StoreBackend != StoreBackendPostgres {
		t.Errorf("StoreBackend default = %q, want %q", cfg.StoreBackend, StoreBackendPostgres)
	}

	if cfg.QueueBackend != QueueBackendValkey {
		t.Errorf("QueueBackend default = %q, want %q", cfg.QueueBackend, QueueBackendValkey)
	}

	if cfg.SchedulerBackend != SchedulerBackendValkey {
		t.Errorf("SchedulerBackend default = %q, want %q", cfg.SchedulerBackend, SchedulerBackendValkey)
	}

	if !cfg.DiscoveryEnabled {
		t.Error("DiscoveryEnabled should default to true")
	}

	if cfg.DiscoveryInterval != time.Hour {
		t.Errorf("DiscoveryInterval = %v, want 1h", cfg.DiscoveryInterval)
	}

	if cfg.PostureExportInterval != defaultPostureExportInterval {
		t.Errorf("PostureExportInterval = %v, want %v", cfg.PostureExportInterval, defaultPostureExportInterval)
	}
}

// TestLoadPostureExportInterval covers the IMPL-0023 task 2.5 knob.
//
// Unlike the sweep and discovery intervals this one costs no GitHub
// API budget — it is a few aggregates over an indexed table — so it is
// tuned against gauge staleness rather than rate limit, and an
// operator lowering it is doing something reasonable rather than
// dangerous. An unparseable value must still fail load rather than
// silently falling back, or a typo produces a fleet whose compliance
// numbers are quietly an hour stale.
func TestLoadPostureExportInterval(t *testing.T) {
	// Cannot use t.Parallel with t.Setenv.
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("STORE_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("QUEUE_VALKEY_DSN", "redis://localhost:6379/0")
	t.Setenv("POSTURE_EXPORT_INTERVAL", "15s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = _, %v, want nil", err)
	}

	if cfg.PostureExportInterval != 15*time.Second {
		t.Errorf("PostureExportInterval = %v, want 15s", cfg.PostureExportInterval)
	}

	t.Setenv("POSTURE_EXPORT_INTERVAL", "not-a-duration")

	if _, err := Load(); err == nil {
		t.Error("Load() = _, nil for an unparseable interval, want an error")
	}
}

// TestLoadMaxJobAttempts covers the IMPL-0022 retry cap: default 10,
// and < 1 rejected at load time — an uncapped queue is how the
// enterprise-migration nack-loop retried forever (INV-0012 finding K).
func TestLoadMaxJobAttempts(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("STORE_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("QUEUE_VALKEY_DSN", "redis://localhost:6379/0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MaxJobAttempts != 10 {
		t.Errorf("MaxJobAttempts = %d, want 10", cfg.MaxJobAttempts)
	}

	t.Setenv("MAX_JOB_ATTEMPTS", "0")

	if _, err := Load(); err == nil {
		t.Fatal("expected error for MAX_JOB_ATTEMPTS=0")
	}
}

// TestLoadRejects_DeprecatedStoreBackend asserts a migration-aware
// error fires when an operator sets STORE_BACKEND to a removed value.
func TestLoadRejects_DeprecatedStoreBackend(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("STORE_BACKEND", "memory")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for STORE_BACKEND=memory")
	}

	got := err.Error()
	if !strings.Contains(got, "STORE_BACKEND") {
		t.Errorf("error should reference STORE_BACKEND: %v", err)
	}

	if !strings.Contains(got, "no longer supported") {
		t.Errorf("error should say %q is no longer supported: %v", "memory", err)
	}

	if !strings.Contains(got, MigrationURL) {
		t.Errorf("error should embed the migration URL %q: %v", MigrationURL, err)
	}
}

// TestLoadRejects_DeprecatedSchedulerBackend asserts ticker is also
// rejected with the migration message.
func TestLoadRejects_DeprecatedSchedulerBackend(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("SCHEDULER_BACKEND", "ticker")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for SCHEDULER_BACKEND=ticker")
	}

	if !strings.Contains(err.Error(), "no longer supported") {
		t.Errorf("error should say %q is no longer supported: %v", "ticker", err)
	}
}

func TestLoadInvalidStoreBackend(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("STORE_BACKEND", "mysql")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "STORE_BACKEND") {
		t.Errorf("expected STORE_BACKEND validation error, got %v", err)
	}
}

func TestLoadPostgresMissingDSN(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("STORE_BACKEND", "postgres")
	t.Setenv("STORE_DSN", "")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "STORE_DSN") {
		t.Errorf("expected STORE_DSN required error, got %v", err)
	}
}

func TestLoadValkeySchedulerMissingDSN(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("SCHEDULER_BACKEND", "valkey")
	t.Setenv("QUEUE_VALKEY_DSN", "")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "QUEUE_VALKEY_DSN") {
		t.Errorf("expected QUEUE_VALKEY_DSN required error, got %v", err)
	}
}

func TestLoadValkeyBackendsAcceptDSN(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/path/to/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("STORE_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("QUEUE_BACKEND", "valkey")
	t.Setenv("SCHEDULER_BACKEND", "valkey")
	t.Setenv("QUEUE_VALKEY_DSN", "valkey://localhost:6379")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.QueueBackend != QueueBackendValkey || cfg.SchedulerBackend != SchedulerBackendValkey {
		t.Errorf("backends not accepted: queue=%q scheduler=%q", cfg.QueueBackend, cfg.SchedulerBackend)
	}
}

func TestLoadRequired_Missing(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "")
	t.Setenv("GITHUB_PRIVATE_KEY", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when required fields are missing")
	}

	errStr := err.Error()

	if !strings.Contains(errStr, "GITHUB_APP_ID") {
		t.Errorf("error should mention GITHUB_APP_ID: %v", err)
	}

	if !strings.Contains(errStr, "GITHUB_PRIVATE_KEY") {
		t.Errorf("error should mention GITHUB_PRIVATE_KEY: %v", err)
	}

	if !strings.Contains(errStr, "GITHUB_WEBHOOK_SECRET") {
		t.Errorf("error should mention GITHUB_WEBHOOK_SECRET: %v", err)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "99999")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/custom/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "mysecret")
	t.Setenv("STORE_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("QUEUE_VALKEY_DSN", "redis://localhost:6379/0")
	t.Setenv("LISTEN_ADDR", ":9999")
	t.Setenv("METRICS_ADDR", ":7777")
	t.Setenv("WORKER_COUNT", "10")
	t.Setenv("QUEUE_SIZE", "500")
	t.Setenv("TEMPLATE_DIR", "/custom/templates")
	t.Setenv("SCHEDULE_INTERVAL", "24h")
	t.Setenv("SKIP_FORKS", "false")
	t.Setenv("SKIP_ARCHIVED", "false")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("RATE_LIMIT_THRESHOLD", "0.25")
	t.Setenv("WEBHOOK_IP_ALLOWLIST", "false")
	t.Setenv("WEBHOOK_IP_ALLOWLIST_FAIL_OPEN", "true")
	t.Setenv("TRUST_PROXY_HEADERS", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.GitHubAppID != 99999 {
		t.Errorf("GitHubAppID = %d, want 99999", cfg.GitHubAppID)
	}

	if cfg.GitHubPrivateKeyPath != "/custom/key.pem" {
		t.Errorf("GitHubPrivateKeyPath = %q, want /custom/key.pem", cfg.GitHubPrivateKeyPath)
	}

	if cfg.GitHubWebhookSecret != "mysecret" {
		t.Errorf("GitHubWebhookSecret = %q, want mysecret", cfg.GitHubWebhookSecret)
	}

	if cfg.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999", cfg.ListenAddr)
	}

	if cfg.MetricsAddr != ":7777" {
		t.Errorf("MetricsAddr = %q, want :7777", cfg.MetricsAddr)
	}

	if cfg.WorkerCount != 10 {
		t.Errorf("WorkerCount = %d, want 10", cfg.WorkerCount)
	}

	if cfg.QueueSize != 500 {
		t.Errorf("QueueSize = %d, want 500", cfg.QueueSize)
	}

	if cfg.TemplateDir != "/custom/templates" {
		t.Errorf("TemplateDir = %q, want /custom/templates", cfg.TemplateDir)
	}

	if cfg.ScheduleInterval != 24*time.Hour {
		t.Errorf("ScheduleInterval = %v, want 24h", cfg.ScheduleInterval)
	}

	if cfg.SkipForks {
		t.Error("SkipForks should be false")
	}

	if cfg.SkipArchived {
		t.Error("SkipArchived should be false")
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

	if cfg.WebhookIPAllowlist {
		t.Error("WebhookIPAllowlist should be false")
	}

	if !cfg.WebhookIPAllowlistFailOpen {
		t.Error("WebhookIPAllowlistFailOpen should be true")
	}

	if !cfg.TrustProxyHeaders {
		t.Error("TrustProxyHeaders should be true")
	}
}

func TestLoadInvalidAppID(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "not-a-number")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid GITHUB_APP_ID")
	}
}

func TestLoadInvalidWorkerCount(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("WORKER_COUNT", "abc")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid WORKER_COUNT")
	}
}

func TestLoadInvalidRateLimitThreshold(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("RATE_LIMIT_THRESHOLD", "not-a-float")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid RATE_LIMIT_THRESHOLD")
	}
}

func TestLoadInvalidBool(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("SKIP_FORKS", "yes")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid SKIP_FORKS")
	}

	if !strings.Contains(err.Error(), "SKIP_FORKS") {
		t.Errorf("error should mention SKIP_FORKS: %v", err)
	}
}

func TestLoadInvalidScheduleInterval(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("SCHEDULE_INTERVAL", "not-a-duration")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid SCHEDULE_INTERVAL")
	}
}

func TestLoadPrivateKeyFromEnvVar(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("STORE_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("QUEUE_VALKEY_DSN", "redis://localhost:6379/0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.GitHubPrivateKey == "" {
		t.Error("GitHubPrivateKey should be set")
	}

	if cfg.GitHubPrivateKeyPath != "" {
		t.Error("GitHubPrivateKeyPath should be empty")
	}
}

func TestLoadPrivateKeyBothSet(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when both GITHUB_PRIVATE_KEY_PATH and GITHUB_PRIVATE_KEY are set")
	}

	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention mutually exclusive: %v", err)
	}
}

func TestLoadInvalidWebhookIPAllowlist(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("WEBHOOK_IP_ALLOWLIST", "notabool")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid WEBHOOK_IP_ALLOWLIST")
	}

	if !strings.Contains(err.Error(), "WEBHOOK_IP_ALLOWLIST") {
		t.Errorf("error should mention WEBHOOK_IP_ALLOWLIST: %v", err)
	}
}

func TestLoadInvalidWebhookIPAllowlistFailOpen(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("WEBHOOK_IP_ALLOWLIST_FAIL_OPEN", "notabool")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid WEBHOOK_IP_ALLOWLIST_FAIL_OPEN")
	}

	if !strings.Contains(err.Error(), "WEBHOOK_IP_ALLOWLIST_FAIL_OPEN") {
		t.Errorf("error should mention WEBHOOK_IP_ALLOWLIST_FAIL_OPEN: %v", err)
	}
}

func TestLoadInvalidTrustProxyHeaders(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "/key.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("TRUST_PROXY_HEADERS", "notabool")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid TRUST_PROXY_HEADERS")
	}

	if !strings.Contains(err.Error(), "TRUST_PROXY_HEADERS") {
		t.Errorf("error should mention TRUST_PROXY_HEADERS: %v", err)
	}
}
