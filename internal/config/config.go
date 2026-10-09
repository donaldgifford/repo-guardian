// Package config handles configuration loading and validation for repo-guardian.
// All configuration is read from environment variables following 12-factor principles.
package config

import (
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

	// GitHubHost is the GitHub host every v2 repository and installation
	// record is keyed under (DESIGN-0025 OQ7). Defaults to github.com.
	GitHubHost string

	// ListenAddr is the HTTP listen address for the webhook server.
	ListenAddr string

	// MetricsAddr is the HTTP listen address for the Prometheus metrics server.
	MetricsAddr string

	// TemplateDir is the directory containing template overrides (ConfigMap mount).
	TemplateDir string

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

	// GuardianConfigPath is the path to a guardian.hcl policy file or
	// directory of .hcl files. When set, operational settings are loaded
	// from the HCL config instead of environment variables.
	GuardianConfigPath string

	// StoreDSN is the Postgres connection string for the worker and api
	// roles.
	StoreDSN string

	// TemporalAddress is TEMPORAL_ADDRESS. The temporal package reads the
	// full client configuration; it is kept here so role validation can
	// require it.
	TemporalAddress string

	// CheckInterval is CHECK_INTERVAL, each repository's check cadence
	// in v2 (DESIGN-0026 OQ3). Default 24h.
	CheckInterval time.Duration

	// PolicyRolloutWindow is POLICY_ROLLOUT_WINDOW, the span a policy
	// change spreads its re-checks over. Default 24h.
	PolicyRolloutWindow time.Duration

	// API is the api role's configuration (DESIGN-0027).
	API APIConfig

	// ChecksRetention is CHECKS_RETENTION: SnapshotWorkflow prunes checks
	// older than this. Default 2160h (90 days, DESIGN-0025 OQ8).
	ChecksRetention time.Duration

	// StorePostgresMaxConns caps the postgres pool connection count.
	// Zero falls back to pgxpool's default (derived from GOMAXPROCS).
	StorePostgresMaxConns int32

	// DiscoveryEnabled gates the worker's `discovery` Temporal Schedule;
	// false removes it. Default true.
	DiscoveryEnabled bool

	// DiscoveryInterval is the `discovery` Schedule's cadence. Default
	// 1h. Lower values increase API burn on list_installations +
	// list_installation_repos; higher values delay discovering a newly
	// installed repository the webhook path missed.
	DiscoveryInterval time.Duration

	// ComplianceSnapshotInterval is the cadence between compliance
	// history rows (DESIGN-0022). Default 24h.
	//
	// This is a history cadence, not a freshness knob: it decides the
	// resolution of the quarter-over-quarter trend the report shows, so
	// the tuning question is "how finely do we want to see the past",
	// not "how current is the data". Daily is already finer than any
	// question the report answers, and the rows are permanent — there
	// is no retention machinery — so shortening it buys resolution
	// nobody asked for at a storage cost that never stops accruing.
	ComplianceSnapshotInterval time.Duration
}

// defaultComplianceSnapshotInterval is the compliance-history cadence
// when COMPLIANCE_SNAPSHOT_INTERVAL is unset (DESIGN-0022). Daily,
// which at target scale is roughly 120 rows a day.
const defaultComplianceSnapshotInterval = 24 * time.Hour

// LoadRole reads configuration for a role (IMPL-0025 Phase 12): it
// parses the environment, then validates only what the role needs.
func LoadRole(role Role) (*Config, error) {
	cfg, err := parse()
	if err != nil {
		return nil, err
	}

	if err := cfg.ValidateRole(role); err != nil {
		return nil, err
	}

	return cfg, nil
}

// parse reads configuration from environment variables and applies
// defaults, without validation.
func parse() (*Config, error) {
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
		ListenAddr:           envOrDefault("LISTEN_ADDR", defaultListenAddr),
		MetricsAddr:          envOrDefault("METRICS_ADDR", ":9090"),
		TemplateDir:          envOrDefault("TEMPLATE_DIR", "/etc/repo-guardian/templates"),
		SkipForks:            skipForks,
		SkipArchived:         skipArchived,
		DryRun:               dryRun,
		LogLevel:             envOrDefault("LOG_LEVEL", "info"),
		GitHubPrivateKeyPath: os.Getenv("GITHUB_PRIVATE_KEY_PATH"),
		GitHubPrivateKey:     os.Getenv("GITHUB_PRIVATE_KEY"),
		GitHubWebhookSecret:  os.Getenv("GITHUB_WEBHOOK_SECRET"),
		GitHubHost:           envOrDefault("GITHUB_HOST", "github.com"),
	}

	appIDStr := os.Getenv("GITHUB_APP_ID")
	if appIDStr != "" {
		appID, err := strconv.ParseInt(appIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing GITHUB_APP_ID %q: %w", appIDStr, err)
		}

		cfg.GitHubAppID = appID
	}

	rateLimitThreshold, err := envOrDefaultFloat("RATE_LIMIT_THRESHOLD", 0.10)
	if err != nil {
		return nil, err
	}

	cfg.RateLimitThreshold = rateLimitThreshold

	cfg.GuardianConfigPath = os.Getenv("GUARDIAN_CONFIG")
	cfg.TemporalAddress = os.Getenv("TEMPORAL_ADDRESS")

	cfg.StoreDSN = os.Getenv("STORE_DSN")

	maxConns, err := envOrDefaultInt("STORE_POSTGRES_MAX_CONNS", 0)
	if err != nil {
		return nil, err
	}

	cfg.StorePostgresMaxConns = int32(maxConns) //nolint:gosec // operator-supplied cap, narrow conversion is intentional

	if err := loadDiscoveryConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadDiscoveryConfig populates the discovery and snapshot schedule
// knobs, then the v2 durations and the API configuration.
func loadDiscoveryConfig(cfg *Config) error {
	discoveryEnabled, err := envOrDefaultBool("DISCOVERY_ENABLED", true)
	if err != nil {
		return err
	}

	cfg.DiscoveryEnabled = discoveryEnabled

	discoveryInterval, err := envOrDefaultDuration("DISCOVERY_INTERVAL", time.Hour)
	if err != nil {
		return err
	}

	cfg.DiscoveryInterval = discoveryInterval

	snapshotInterval, err := envOrDefaultDuration("COMPLIANCE_SNAPSHOT_INTERVAL", defaultComplianceSnapshotInterval)
	if err != nil {
		return err
	}

	cfg.ComplianceSnapshotInterval = snapshotInterval

	if err := loadV2Durations(cfg); err != nil {
		return err
	}

	return loadAPIConfig(cfg)
}

// v2 duration defaults (DESIGN-0026 § Configuration).
const (
	defaultCheckInterval       = 24 * time.Hour
	defaultPolicyRolloutWindow = 24 * time.Hour
	defaultChecksRetention     = 2160 * time.Hour
)

// loadV2Durations reads the v2 control plane's cadences.
func loadV2Durations(cfg *Config) error {
	for _, d := range []struct {
		key  string
		def  time.Duration
		dest *time.Duration
	}{
		{"CHECK_INTERVAL", defaultCheckInterval, &cfg.CheckInterval},
		{"POLICY_ROLLOUT_WINDOW", defaultPolicyRolloutWindow, &cfg.PolicyRolloutWindow},
		{"CHECKS_RETENTION", defaultChecksRetention, &cfg.ChecksRetention},
	} {
		v, err := envOrDefaultDuration(d.key, d.def)
		if err != nil {
			return err
		}

		if v <= 0 {
			return fmt.Errorf("%s must be positive, got %s", d.key, v)
		}

		*d.dest = v
	}

	return nil
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
