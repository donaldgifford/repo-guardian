// Package temporal connects repo-guardian v2 to its Temporal cluster
// (DESIGN-0026): configuration from TEMPORAL_* env vars, an mTLS client
// that logs through slog and reports SDK metrics on the process's meter
// provider, and the startup checks every role runs before doing work.
package temporal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	// which only the local dev server should use.
	TLSCertPath   string
	TLSKeyPath    string
	TLSCAPath     string
	TLSServerName string
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
	}

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

	if c.TLSCertPath == "" && (c.TLSCAPath != "" || c.TLSServerName != "") {
		errs = append(errs, errors.New("TEMPORAL_TLS_CA_PATH and TEMPORAL_TLS_SERVER_NAME need a client certificate"))
	}

	return errors.Join(errs...)
}

// tlsConfig builds the mTLS client config, or nil for plaintext.
func (c *Config) tlsConfig() (*tls.Config, error) {
	if c.TLSCertPath == "" {
		return nil, nil //nolint:nilnil // nil config: plaintext (dev server)
	}

	cert, err := tls.LoadX509KeyPair(c.TLSCertPath, c.TLSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("temporal: loading client certificate: %w", err)
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName:   c.TLSServerName,
		MinVersion:   tls.VersionTLS12,
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
		ConnectionOptions: client.ConnectionOptions{TLS: tlsCfg},
	}

	if opts.MeterProvider != nil {
		co.MetricsHandler = otelcontrib.NewMetricsHandler(otelcontrib.MetricsHandlerOptions{
			Meter: opts.MeterProvider.Meter("temporal-sdk-go"),
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
func CheckServerVersion(ctx context.Context, c client.Client, minVersion string) error {
	info, err := c.WorkflowService().GetClusterInfo(ctx, &workflowservice.GetClusterInfoRequest{})
	if err != nil {
		return fmt.Errorf("temporal: reading cluster info: %w", err)
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
