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

	if cfg.StoreBackend != StoreBackendMemory {
		t.Errorf("StoreBackend default = %q, want %q", cfg.StoreBackend, StoreBackendMemory)
	}

	if cfg.QueueBackend != QueueBackendMemory {
		t.Errorf("QueueBackend default = %q, want %q", cfg.QueueBackend, QueueBackendMemory)
	}

	if cfg.SchedulerBackend != SchedulerBackendTicker {
		t.Errorf("SchedulerBackend default = %q, want %q", cfg.SchedulerBackend, SchedulerBackendTicker)
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
