// Package config handles configuration loading and validation for repo-guardian.
// All configuration is read from environment variables following 12-factor principles.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration values for repo-guardian.
type Config struct {
	// GitHubAppID is the GitHub App's numeric ID.
	GitHubAppID int64

	// GitHubPrivateKeyPath is the filesystem path to the App's PEM private key.
	// Mutually exclusive with GitHubPrivateKey; one must be set.
	GitHubPrivateKeyPath string

	// GitHubPrivateKey is the raw PEM-encoded private key content.
	// Mutually exclusive with GitHubPrivateKeyPath; one must be set.
	GitHubPrivateKey string

	// GitHubWebhookSecret is the HMAC secret for validating webhook payloads.
	GitHubWebhookSecret string

	// ListenAddr is the HTTP listen address for the webhook server.
	ListenAddr string

	// MetricsAddr is the HTTP listen address for the Prometheus metrics server.
	MetricsAddr string

	// WorkerCount is the number of concurrent repo check workers.
	WorkerCount int

	// QueueSize is the work queue buffer size.
	QueueSize int

	// TemplateDir is the directory containing template overrides (ConfigMap mount).
	TemplateDir string

	// ScheduleInterval is the reconciliation interval.
	ScheduleInterval time.Duration

	// SkipForks controls whether forked repositories are skipped.
	SkipForks bool

	// SkipArchived controls whether archived repositories are skipped.
	SkipArchived bool

	// DryRun logs actions without creating PRs when true.
	DryRun bool

	// LogLevel controls log verbosity (debug, info, warn, error).
	LogLevel string

	// RateLimitThreshold is the fraction of remaining rate limit budget
	// at which pre-emptive throttling begins (e.g., 0.10 = 10%).
	RateLimitThreshold float64

	// WebhookIPAllowlist enables the GitHub webhook IP allowlist middleware.
	WebhookIPAllowlist bool

	// WebhookIPAllowlistFailOpen allows requests when the allowlist is unavailable.
	WebhookIPAllowlistFailOpen bool

	// TrustProxyHeaders reads client IP from X-Forwarded-For when true.
	TrustProxyHeaders bool

	// GuardianConfigPath is the path to a guardian.hcl policy file or
	// directory of .hcl files. When set, operational settings are loaded
	// from the HCL config instead of environment variables.
	GuardianConfigPath string

	// StoreBackend selects the persistent-state implementation. One of
	// "memory" (default — single-replica only) or "postgres". See
	// IMPL-0011 / DESIGN-0012 for the full mode matrix.
	StoreBackend string

	// QueueBackend selects the work-queue implementation. One of
	// "memory" (default — single-replica only) or "valkey".
	QueueBackend string

	// SchedulerBackend selects the scheduler implementation. One of
	// "ticker" (default — fires on every replica, single-replica only)
	// or "valkey" (Valkey-lock-coordinated; multi-replica safe).
	SchedulerBackend string

	// StoreDSN is the connection string for the postgres store backend
	// (when StoreBackend=="postgres"). Ignored otherwise.
	StoreDSN string

	// QueueValkeyDSN is the connection string for the valkey queue
	// backend (when QueueBackend=="valkey"). Same Valkey instance is
	// reused by SchedulerBackend=="valkey".
	QueueValkeyDSN string

	// StorePostgresMaxConns caps the postgres pool connection count.
	// Zero falls back to pgxpool's default (derived from GOMAXPROCS).
	StorePostgresMaxConns int32

	// JobAckTimeout is how long a Valkey-queued job may stay in-flight
	// before the reaper considers it abandoned and requeues it.
	JobAckTimeout time.Duration

	// ReaperInterval is the cadence between Valkey reaper attempts.
	ReaperInterval time.Duration

	// PodID identifies the running replica for leader-election locks
	// (Valkey reaper, Valkey scheduler). Sourced from POD_NAME via the
	// Kubernetes downward API; falls back to a process-time random
	// identifier if absent. See IMPL-0011 / DESIGN-0012.
	PodID string

	// ReconcileFreshness is the maximum age of a stored
	// last_checked_at before the StaleSweeper requeues the repo.
	// Default 24h.
	ReconcileFreshness time.Duration

	// StaleSweepBatchSize caps the number of repos returned per
	// StaleRepos query. Default 200.
	StaleSweepBatchSize int

	// RateLimitReserve is the fraction of an installation's GitHub
	// rate limit reserved against sweep enqueue. When the remaining
	// budget < (limit × reserve), the StaleSweeper skips that
	// installation's repos and increments
	// rate_limit_reserve_blocked_total. Default 0.1.
	RateLimitReserve float64
}

// Backend identifier constants. Defined as package-level strings so
// chart and binary share a single source of truth. Memory and
// ticker were removed in IMPL-0016; deprecatedBackends below carries
// the rejected values for migration-error messages.
const (
	StoreBackendPostgres   = "postgres"
	QueueBackendValkey     = "valkey"
	SchedulerBackendValkey = "valkey"
)

// MigrationURL is the canonical operator documentation for the
// memory-backend removal. Embedded in startup validation errors so
// operators land on a runbook with overlay→values recipes.
const MigrationURL = "https://github.com/donaldgifford/repo-guardian/blob/main/docs/operations/migrations.md#removing-memory-backend"

// deprecatedBackends maps removed backend identifiers to the env var
// that referenced them. Used by validateBackends to emit a single
// targeted error per misconfigured env var.
var deprecatedBackends = map[string]string{
	"memory": "memory backend removed in IMPL-0016 (chart 1.0.0)",
	"ticker": "ticker scheduler removed in IMPL-0016 (chart 1.0.0)",
}

// Load reads configuration from environment variables and applies defaults.
func Load() (*Config, error) {
	skipForks, err := envOrDefaultBool("SKIP_FORKS", true)
	if err != nil {
		return nil, err
	}

	skipArchived, err := envOrDefaultBool("SKIP_ARCHIVED", true)
	if err != nil {
		return nil, err
	}

	dryRun, err := envOrDefaultBool("DRY_RUN", false)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		ListenAddr:           envOrDefault("LISTEN_ADDR", ":8080"),
		MetricsAddr:          envOrDefault("METRICS_ADDR", ":9090"),
		TemplateDir:          envOrDefault("TEMPLATE_DIR", "/etc/repo-guardian/templates"),
		SkipForks:            skipForks,
		SkipArchived:         skipArchived,
		DryRun:               dryRun,
		LogLevel:             envOrDefault("LOG_LEVEL", "info"),
		GitHubPrivateKeyPath: os.Getenv("GITHUB_PRIVATE_KEY_PATH"),
		GitHubPrivateKey:     os.Getenv("GITHUB_PRIVATE_KEY"),
		GitHubWebhookSecret:  os.Getenv("GITHUB_WEBHOOK_SECRET"),
	}

	appIDStr := os.Getenv("GITHUB_APP_ID")
	if appIDStr != "" {
		appID, err := strconv.ParseInt(appIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing GITHUB_APP_ID %q: %w", appIDStr, err)
		}

		cfg.GitHubAppID = appID
	}

	workerCount, err := envOrDefaultInt("WORKER_COUNT", 5)
	if err != nil {
		return nil, err
	}

	cfg.WorkerCount = workerCount

	queueSize, err := envOrDefaultInt("QUEUE_SIZE", 1000)
	if err != nil {
		return nil, err
	}

	cfg.QueueSize = queueSize

	interval, err := envOrDefaultDuration("SCHEDULE_INTERVAL", 168*time.Hour)
	if err != nil {
		return nil, err
	}

	cfg.ScheduleInterval = interval

	rateLimitThreshold, err := envOrDefaultFloat("RATE_LIMIT_THRESHOLD", 0.10)
	if err != nil {
		return nil, err
	}

	cfg.RateLimitThreshold = rateLimitThreshold

	webhookIPAllowlist, err := envOrDefaultBool("WEBHOOK_IP_ALLOWLIST", true)
	if err != nil {
		return nil, err
	}

	cfg.WebhookIPAllowlist = webhookIPAllowlist

	webhookIPAllowlistFailOpen, err := envOrDefaultBool("WEBHOOK_IP_ALLOWLIST_FAIL_OPEN", false)
	if err != nil {
		return nil, err
	}

	cfg.WebhookIPAllowlistFailOpen = webhookIPAllowlistFailOpen

	trustProxyHeaders, err := envOrDefaultBool("TRUST_PROXY_HEADERS", false)
	if err != nil {
		return nil, err
	}

	cfg.TrustProxyHeaders = trustProxyHeaders
	cfg.GuardianConfigPath = os.Getenv("GUARDIAN_CONFIG")

	if err := loadBackendConfig(cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func loadBackendConfig(cfg *Config) error {
	// Backends are required since IMPL-0016 — there is no longer a
	// safe in-process default. Empty values fall through to
	// validateBackends, which emits a targeted error.
	cfg.StoreBackend = envOrDefault("STORE_BACKEND", StoreBackendPostgres)
	cfg.QueueBackend = envOrDefault("QUEUE_BACKEND", QueueBackendValkey)
	cfg.SchedulerBackend = envOrDefault("SCHEDULER_BACKEND", SchedulerBackendValkey)
	cfg.StoreDSN = os.Getenv("STORE_DSN")
	cfg.QueueValkeyDSN = os.Getenv("QUEUE_VALKEY_DSN")

	maxConns, err := envOrDefaultInt("STORE_POSTGRES_MAX_CONNS", 0)
	if err != nil {
		return err
	}

	cfg.StorePostgresMaxConns = int32(maxConns) //nolint:gosec // operator-supplied cap, narrow conversion is intentional

	ackTimeout, err := envOrDefaultDuration("JOB_ACK_TIMEOUT", 5*time.Minute)
	if err != nil {
		return err
	}

	cfg.JobAckTimeout = ackTimeout

	reaperInterval, err := envOrDefaultDuration("REAPER_INTERVAL", time.Minute)
	if err != nil {
		return err
	}

	cfg.ReaperInterval = reaperInterval
	cfg.PodID = os.Getenv("POD_NAME")

	freshness, err := envOrDefaultDuration("RECONCILE_FRESHNESS", 24*time.Hour)
	if err != nil {
		return err
	}

	cfg.ReconcileFreshness = freshness

	batchSize, err := envOrDefaultInt("STALE_SWEEP_BATCH_SIZE", 200)
	if err != nil {
		return err
	}

	cfg.StaleSweepBatchSize = batchSize

	reserve, err := envOrDefaultFloat("RATE_LIMIT_RESERVE", 0.1)
	if err != nil {
		return err
	}

	cfg.RateLimitReserve = reserve

	return nil
}

// Validate checks that required configuration fields are set.
func (c *Config) Validate() error {
	var errs []error

	if c.GitHubAppID == 0 {
		errs = append(errs, errors.New("GITHUB_APP_ID is required"))
	}

	if c.GitHubPrivateKeyPath == "" && c.GitHubPrivateKey == "" {
		errs = append(errs, errors.New("one of GITHUB_PRIVATE_KEY_PATH or GITHUB_PRIVATE_KEY is required"))
	}

	if c.GitHubPrivateKeyPath != "" && c.GitHubPrivateKey != "" {
		errs = append(errs, errors.New("GITHUB_PRIVATE_KEY_PATH and GITHUB_PRIVATE_KEY are mutually exclusive"))
	}

	if c.GitHubWebhookSecret == "" {
		errs = append(errs, errors.New("GITHUB_WEBHOOK_SECRET is required"))
	}

	errs = append(errs, c.validateBackends()...)

	return errors.Join(errs...)
}

func (c *Config) validateBackends() []error {
	var errs []error

	if err := validateBackend("STORE_BACKEND", c.StoreBackend, StoreBackendPostgres); err != nil {
		errs = append(errs, err)
	}

	if c.StoreBackend == StoreBackendPostgres && c.StoreDSN == "" {
		errs = append(errs, errors.New("STORE_DSN is required when STORE_BACKEND=postgres"))
	}

	if err := validateBackend("QUEUE_BACKEND", c.QueueBackend, QueueBackendValkey); err != nil {
		errs = append(errs, err)
	}

	if c.QueueBackend == QueueBackendValkey && c.QueueValkeyDSN == "" {
		errs = append(errs, errors.New("QUEUE_VALKEY_DSN is required when QUEUE_BACKEND=valkey"))
	}

	if err := validateBackend("SCHEDULER_BACKEND", c.SchedulerBackend, SchedulerBackendValkey); err != nil {
		errs = append(errs, err)
	}

	if c.SchedulerBackend == SchedulerBackendValkey && c.QueueValkeyDSN == "" {
		errs = append(errs, errors.New("QUEUE_VALKEY_DSN is required when SCHEDULER_BACKEND=valkey (shared Valkey instance)"))
	}

	return errs
}

// validateBackend returns nil when value matches the expected
// backend, a migration-aware error when value is a removed backend
// (memory, ticker), or a generic "must be X" error otherwise.
func validateBackend(envVar, value, expected string) error {
	if value == expected {
		return nil
	}

	if reason, deprecated := deprecatedBackends[value]; deprecated {
		return fmt.Errorf("%s=%q is no longer supported (%s). Migration runbook: %s", envVar, value, reason, MigrationURL)
	}

	return fmt.Errorf("%s %q must be %q. See %s", envVar, value, expected, MigrationURL)
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}

	return defaultVal
}

func envOrDefaultBool(key string, defaultVal bool) (bool, error) {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal, nil
	}

	b, err := strconv.ParseBool(val)
	if err != nil {
		return false, fmt.Errorf("parsing %s %q: %w", key, val, err)
	}

	return b, nil
}

func envOrDefaultInt(key string, defaultVal int) (int, error) {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal, nil
	}

	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("parsing %s %q: %w", key, val, err)
	}

	return n, nil
}

func envOrDefaultFloat(key string, defaultVal float64) (float64, error) {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal, nil
	}

	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing %s %q: %w", key, val, err)
	}

	return f, nil
}

func envOrDefaultDuration(key string, defaultVal time.Duration) (time.Duration, error) {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal, nil
	}

	d, err := time.ParseDuration(val)
	if err != nil {
		return 0, fmt.Errorf("parsing %s %q: %w", key, val, err)
	}

	return d, nil
}
