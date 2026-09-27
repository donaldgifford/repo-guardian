// Package temporal connects repo-guardian v2 to its Temporal cluster
// (DESIGN-0026): configuration from TEMPORAL_* env vars, a client that
// authenticates with mTLS or an OIDC bearer token, logs through slog
// and reports SDK metrics on the process's meter provider, and the
// startup checks every role runs before doing work.
package temporal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	otelcontrib "go.temporal.io/sdk/contrib/opentelemetry"
	tlog "go.temporal.io/sdk/log"
	"golang.org/x/mod/semver"
)

// MinServerVersion is the oldest Temporal server v2 runs against:
// fairness, Update-with-Start and NextRetryDelay are GA from 1.31
// (DESIGN-0026 § Temporal deployment).
const MinServerVersion = "1.31.0"

// Defaults for the optional TEMPORAL_* variables.
const (
	DefaultNamespace = "repo-guardian"
	DefaultTaskQueue = "repo-guardian"
)

// ErrServerTooOld is returned by CheckServerVersion when the server is
// below the floor.
var ErrServerTooOld = errors.New("temporal server is older than the supported minimum")

// Config is the Temporal connection, read from the environment.
type Config struct {
	// Address is the frontend host:port (TEMPORAL_ADDRESS, required).
	Address string
	// Namespace is TEMPORAL_NAMESPACE, default repo-guardian.
	Namespace string
	// TaskQueue is TEMPORAL_TASK_QUEUE, default repo-guardian.
	TaskQueue string

	// TLS files for mTLS (TEMPORAL_TLS_CERT_PATH, _KEY_PATH, _CA_PATH)
	// and the name expected on the server certificate
	// (TEMPORAL_TLS_SERVER_NAME). Unset cert and key mean plaintext,
	// which only the local dev server should use — unless OIDC is set,
	// which always verifies the server over TLS and may use the CA and
	// server name without a client certificate.
	TLSCertPath   string
	TLSKeyPath    string
	TLSCAPath     string
	TLSServerName string

	// TLSDisabled (TEMPORAL_TLS_DISABLED) forces plaintext even with
	// OIDC, whose bearer token would otherwise always travel over TLS.
	// For a cluster-internal frontend that authorizes JWTs without
	// serving TLS; Dial warns, because the token is then readable on
	// the wire.
	TLSDisabled bool

	// OIDC, when set, authenticates with a bearer token from an OAuth2
	// client-credentials grant (TEMPORAL_OIDC_*).
	OIDC *OIDCConfig
}

// ConfigFromEnv reads Config from the TEMPORAL_* variables.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Address:       os.Getenv("TEMPORAL_ADDRESS"),
		Namespace:     envOr("TEMPORAL_NAMESPACE", DefaultNamespace),
		TaskQueue:     envOr("TEMPORAL_TASK_QUEUE", DefaultTaskQueue),
		TLSCertPath:   os.Getenv("TEMPORAL_TLS_CERT_PATH"),
		TLSKeyPath:    os.Getenv("TEMPORAL_TLS_KEY_PATH"),
		TLSCAPath:     os.Getenv("TEMPORAL_TLS_CA_PATH"),
		TLSServerName: os.Getenv("TEMPORAL_TLS_SERVER_NAME"),
		OIDC:          oidcFromEnv(),
	}

	disabled, err := envBool("TEMPORAL_TLS_DISABLED")
	if err != nil {
		return Config{}, err
	}

	cfg.TLSDisabled = disabled

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	var errs []error

	if c.Address == "" {
		errs = append(errs, errors.New("TEMPORAL_ADDRESS is required"))
	}

	if (c.TLSCertPath == "") != (c.TLSKeyPath == "") {
		errs = append(errs, errors.New("TEMPORAL_TLS_CERT_PATH and TEMPORAL_TLS_KEY_PATH must be set together"))
	}

	if c.TLSCertPath == "" && c.OIDC == nil && (c.TLSCAPath != "" || c.TLSServerName != "") {
		errs = append(errs, errors.New("TEMPORAL_TLS_CA_PATH and TEMPORAL_TLS_SERVER_NAME need a client certificate or OIDC"))
	}

	if c.TLSDisabled && (c.TLSCertPath != "" || c.TLSCAPath != "" || c.TLSServerName != "") {
		errs = append(errs, errors.New("TEMPORAL_TLS_DISABLED contradicts the TEMPORAL_TLS_* files"))
	}

	if c.OIDC != nil {
		errs = append(errs, c.OIDC.validate()...)
	}

	return errors.Join(errs...)
}

// envBool reads a boolean variable; unset is false.
func envBool(name string) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return false, nil
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}

	return b, nil
}

// tlsConfig builds the client TLS config: mTLS with a client
// certificate, server-verified TLS for OIDC, or nil for plaintext.
func (c *Config) tlsConfig() (*tls.Config, error) {
	if c.TLSDisabled || (c.TLSCertPath == "" && c.OIDC == nil) {
		return nil, nil //nolint:nilnil // nil config: plaintext (dev server, or TLS disabled)
	}

	cfg := &tls.Config{
		ServerName: c.TLSServerName,
		MinVersion: tls.VersionTLS12,
	}

	if c.TLSCertPath != "" {
		cert, err := tls.LoadX509KeyPair(c.TLSCertPath, c.TLSKeyPath)
		if err != nil {
			return nil, fmt.Errorf("temporal: loading client certificate: %w", err)
		}

		cfg.Certificates = []tls.Certificate{cert}
	}

	if c.TLSCAPath != "" {
		pem, err := os.ReadFile(c.TLSCAPath)
		if err != nil {
			return nil, fmt.Errorf("temporal: reading CA: %w", err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("temporal: %s holds no PEM certificate", c.TLSCAPath)
		}

		cfg.RootCAs = pool
	}

	return cfg, nil
}

// DialOptions are the process-wide dependencies Dial wires in.
type DialOptions struct {
	// Logger receives the SDK's logs. nil means slog.Default().
	Logger *slog.Logger
	// MeterProvider receives the SDK's metrics, so they land on the
	// existing /metrics registry. nil disables SDK metrics.
	MeterProvider metric.MeterProvider
}

// Dial connects to the frontend. The caller must Close the client.
func Dial(ctx context.Context, cfg *Config, opts DialOptions) (client.Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("temporal: %w", err)
	}

	tlsCfg, err := cfg.tlsConfig()
	if err != nil {
		return nil, err
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	co := client.Options{
		HostPort:          cfg.Address,
		Namespace:         cfg.Namespace,
		Logger:            tlog.NewStructuredLogger(logger.With("component", "temporal-sdk")),
		ConnectionOptions: client.ConnectionOptions{TLS: tlsCfg, TLSDisabled: cfg.TLSDisabled},
	}

	if cfg.OIDC != nil {
		ts, err := cfg.OIDC.tokenSource(ctx)
		if err != nil {
			return nil, err
		}

		if cfg.TLSDisabled {
			logger.Warn("temporal: TLS is disabled, so the OIDC bearer token is sent in plaintext", "address", cfg.Address)
		}

		co.Credentials = client.NewAPIKeyDynamicCredentials(tokenCallback(ts, logger))
	}

	if opts.MeterProvider != nil {
		co.MetricsHandler = otelcontrib.NewMetricsHandler(otelcontrib.MetricsHandlerOptions{
			Meter: opts.MeterProvider.Meter(SDKMeterName),
			// UseMonotonicCounters stays off on purpose: Temporal's own
			// dashboards (temporalio/dashboards sdk/temporal-go-sdk-otel)
			// query the SDK counters by their default names, without
			// _total, and rate() handles resets on these series either way.
			// The contrib default panics on a meter error; a metrics
			// fault must never take a worker down.
			OnError: func(err error) { logger.Warn("temporal: SDK metric error", "error", err) },
		})
	}

	c, err := client.DialContext(ctx, co)
	if err != nil {
		return nil, fmt.Errorf("temporal: dial %s: %w", cfg.Address, err)
	}

	return c, nil
}

// CheckServerVersion fails with ErrServerTooOld when the cluster runs a
// server older than minVersion ("1.31.0" form).
//
// It reads GetSystemInfo, not GetClusterInfo. Both carry the server
// version, but a JWT-authorizing frontend treats GetClusterInfo as
// cluster-scoped and admits only a temporal-system role, which a
// namespace-scoped worker never holds: rc.2 crash-looped on "Request
// unauthorized" there. GetSystemInfo is on the authorizer's
// always-allowed list, and the SDK already calls it to connect.
func CheckServerVersion(ctx context.Context, c client.Client, minVersion string) error {
	info, err := c.WorkflowService().GetSystemInfo(ctx, &workflowservice.GetSystemInfoRequest{})
	if err != nil {
		return fmt.Errorf("temporal: reading system info: %w", err)
	}

	got := info.GetServerVersion()

	have, want := "v"+strings.TrimPrefix(got, "v"), "v"+strings.TrimPrefix(minVersion, "v")
	if !semver.IsValid(have) {
		return fmt.Errorf("temporal: unparseable server version %q", got)
	}

	if semver.Compare(have, want) < 0 {
		return fmt.Errorf("%w: server %s, need %s or later", ErrServerTooOld, got, minVersion)
	}

	return nil
}

// Ping checks the frontend answers health checks.
func Ping(ctx context.Context, c client.Client) error {
	if _, err := c.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
		return fmt.Errorf("temporal: health check: %w", err)
	}

	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return def
}
