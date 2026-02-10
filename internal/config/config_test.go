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
}

func TestLoadRequired_Missing(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when required fields are missing")
	}

	errStr := err.Error()

	if !strings.Contains(errStr, "GITHUB_APP_ID") {
		t.Errorf("error should mention GITHUB_APP_ID: %v", err)
	}

	if !strings.Contains(errStr, "GITHUB_PRIVATE_KEY_PATH") {
		t.Errorf("error should mention GITHUB_PRIVATE_KEY_PATH: %v", err)
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
