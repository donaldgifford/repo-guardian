// Package main is the entrypoint for the repo-guardian GitHub App.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/config"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
	"github.com/donaldgifford/repo-guardian/internal/rules"
)

const (
	shutdownTimeout = 15 * time.Second

	// observabilityShutdownTimeout bounds the SDK flush. It is short
	// because the Prometheus exporter is pull-based and has nothing to
	// flush; the bound exists so a future push exporter cannot hang
	// process exit.
	observabilityShutdownTimeout = 5 * time.Second

	// webhookRoute is the ServeMux pattern for the webhook endpoint.
	// Shared between the route registration and its instrumentation so
	// the span name and the http.route attribute cannot drift apart.
	webhookRoute = "POST /webhooks/github"
)

func main() {
	if err := dispatch(os.Args); err != nil {
		slog.Error("repo-guardian exited with error", "error", err)
		os.Exit(1)
	}
}

// dispatch routes argv to a role or subcommand (DESIGN-0026 § Roles).
// No arguments, or a leading flag, means all, so `repo-guardian` and
// `repo-guardian --strict-templates` run every role in one process.
//
// Deliberately NOT flag.Parse()'d first. flag.CommandLine stops at the
// first non-flag argument, so parsing here would swallow the subcommand
// name and leave its own flags sitting in an unread tail. Every role and
// subcommand parses its own FlagSet from the tail instead.
func dispatch(argv []string) error {
	if len(argv) < 2 || strings.HasPrefix(argv[1], "-") {
		return runAll(argv[1:])
	}

	switch argv[1] {
	case cmdIngest:
		return runIngest(argv[2:])
	case cmdWorker:
		return runWorker(argv[2:])
	case cmdAPI:
		return runAPI(argv[2:])
	case cmdAll:
		return runAll(argv[2:])
	case cmdReport:
		return runReport(argv[2:])
	case cmdMonitoring:
		return runMonitoring(argv[2:])
	case cmdMigrate:
		return runMigrate(argv[2:])
	case cmdHelp:
		usage(os.Stdout)

		return nil
	default:
		usage(os.Stderr)

		return fmt.Errorf("unknown subcommand %q", argv[1])
	}
}

// usage lists the subcommands.
//
// `--help` and `-h` never reach dispatch's switch — they start with a
// dash, so dispatch hands them to the all role, whose FlagSet prints
// this banner before its own defaults, so the one thing a user is most
// likely to type still names every subcommand.
//
// The write is unchecked on purpose: this is usage text on its way to
// stdout or stderr, there is no recovery path, and the flag package
// ignores the identical error in PrintDefaults.
func usage(w io.Writer) {
	//nolint:errcheck // usage text; no recovery path, see doc comment
	fmt.Fprint(w, `repo-guardian — GitHub App for repository compliance

Usage:
  repo-guardian [all] [flags]        run every role in one process (default)
  repo-guardian ingest [flags]       webhook ingest: HMAC, filter, start workflows
  repo-guardian worker [flags]       Temporal worker: every workflow and activity
  repo-guardian api                  read-only HTTP API
  repo-guardian report [flags]       write per-org compliance reports
  repo-guardian monitoring generate  emit dashboards and alerts from the policy
  repo-guardian migrate [flags]      apply v2 schema migrations (Helm hook Job)
  repo-guardian migrate verify-shadow --v1-dsn --v2-dsn
                                     compare a v2 shadow run with v1
  repo-guardian help                 show this message

Running with no subcommand runs every role. The ingest role holds
neither the GitHub App key nor database credentials.
`)
}

// loadPolicyAndEngine loads the operator's HCL policy, runs strict
// template validation when enabled, loads the template store, and
// constructs the checker engine. Any failure exits the process.
// Extracted from main() to keep the entrypoint under the funlen
// statement budget.
func loadPolicyAndEngine(
	cfg *config.Config,
	strictTemplates bool,
	logger *slog.Logger,
) (*policy.PolicyConfig, *checker.Engine, *rules.TemplateStore) {
	policyCfg, err := policy.Load(cfg.GuardianConfigPath)
	if err != nil {
		logger.Error("failed to load policy config", "error", err)
		os.Exit(1)
	}

	if cfg.GuardianConfigPath != "" {
		logger.Info("loaded policy config", "path", cfg.GuardianConfigPath)
	} else {
		logger.Info("using built-in default policy")
	}

	runStrictTemplateValidation(strictTemplates, policyCfg, logger)

	templates := rules.NewTemplateStore()
	if err := templates.Load(cfg.TemplateDir); err != nil {
		logger.Error("failed to load templates", "error", err)
		os.Exit(1)
	}

	engine, err := checker.NewEngine(
		policyCfg,
		templates,
		logger,
		newReconcilerRegistry(templates),
	)
	if err != nil {
		logger.Error("failed to create checker engine", "error", err)
		os.Exit(1)
	}

	return policyCfg, engine, templates
}

func newMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func startServer(logger *slog.Logger, srv *http.Server, name, addr string, cancel context.CancelFunc) {
	go func() {
		logger.Info("server listening", "name", name, "addr", addr)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "name", name, "error", err)
			cancel()
		}
	}()
}

func awaitShutdown(ctx context.Context, logger *slog.Logger) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("received shutdown signal", "signal", sig)
	case <-ctx.Done():
		logger.Info("context canceled")
	}
}

func newReconcilerRegistry(templates *rules.TemplateStore) *reconciler.Registry {
	reg := reconciler.NewRegistry()
	reg.Register("custom_properties", func(cfg policy.ReconcilerConfig) (reconciler.Reconciler, error) {
		return reconciler.NewCustomPropertiesReconciler(cfg, templates)
	})
	reg.Register("label_sync", reconciler.NewLabelSyncReconciler)
	reg.Register("branch_protection", reconciler.NewBranchProtectionReconciler)
	reg.Register("workflow_sync", reconciler.NewWorkflowSyncReconciler)

	return reg
}

func newGitHubClient(cfg *config.Config, logger *slog.Logger) (*ghclient.GitHubClient, error) {
	if cfg.GitHubPrivateKey != "" {
		logger.Info("using private key from environment variable")
		return ghclient.NewClientFromKeyBytes(cfg.GitHubAppID, []byte(cfg.GitHubPrivateKey), logger, cfg.RateLimitThreshold)
	}

	logger.Info("using private key from file", "path", cfg.GitHubPrivateKeyPath)

	return ghclient.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKeyPath, logger, cfg.RateLimitThreshold)
}

// initLogger builds the server's logger, on stdout.
//
// Stdout is deliberate for the server and load-bearing for whoever
// collects its logs; do not "fix" it to stderr. The report subcommand
// writes its path list to stdout and therefore needs the other stream —
// it calls initLoggerTo(os.Stderr, ...) instead.
func initLogger(level string) *slog.Logger {
	return initLoggerTo(os.Stdout, level)
}

// initLoggerTo builds a logger writing to w.
func initLoggerTo(w io.Writer, level string) *slog.Logger {
	var logLevel slog.Level

	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: logLevel,
	})

	return slog.New(handler)
}

// runStrictTemplateValidation invokes ValidatePRTemplates when enabled
// is true and exits non-zero on failure. Extracted from main() to keep
// the entrypoint under the funlen statement budget.
func runStrictTemplateValidation(enabled bool, policyCfg *policy.PolicyConfig, logger *slog.Logger) {
	if !enabled {
		return
	}

	if err := policy.ValidatePRTemplates(policyCfg); err != nil {
		logger.Error("strict template validation failed", "error", err)
		os.Exit(1)
	}

	logger.Info("strict template validation passed")
}

// strictTemplatesFromEnv reads STRICT_TEMPLATES from the environment
// and returns the parsed boolean. Invalid or unset values default to
// false. The CLI flag overrides this default at parse time.
func strictTemplatesFromEnv() bool {
	v := os.Getenv("STRICT_TEMPLATES")
	if v == "" {
		return false
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}

	return b
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok")); err != nil {
		slog.Error("failed to write healthz response", "error", err)
	}
}

// Runbooks a removed env var's warning points at.
const (
	runbookIngress = "docs/operations/ingress.md"
	runbookV2      = "docs/operations/v2-migration.md#environment-variables"
)

// removedEnvVars are configuration knobs the binary no longer reads,
// each with the runbook that says what replaced it. The IMPL-0024 trio
// went with the webhook IP-allowlist middleware (source-IP enforcement
// moved to the operator's edge, DESIGN-0023); the rest went with the v1
// runtime (IMPL-0028 Phase 1), which Temporal replaced. The binary
// ignores every one of them; the warning is a migration breadcrumb, not
// behavior. Remove the check in a future major.
var removedEnvVars = []struct{ name, runbook string }{
	{"WEBHOOK_IP_ALLOWLIST", runbookIngress},
	{"WEBHOOK_IP_ALLOWLIST_FAIL_OPEN", runbookIngress},
	{"TRUST_PROXY_HEADERS", runbookIngress},
	{"STORE_BACKEND", runbookV2},
	{"QUEUE_BACKEND", runbookV2},
	{"SCHEDULER_BACKEND", runbookV2},
	{"QUEUE_VALKEY_DSN", runbookV2},
	{"JOB_ACK_TIMEOUT", runbookV2},
	{"REAPER_INTERVAL", runbookV2},
	{"MAX_JOB_ATTEMPTS", runbookV2},
	{"POD_NAME", runbookV2},
	{"STALE_SWEEP_BATCH_SIZE", runbookV2},
	{"POSTURE_EXPORT_INTERVAL", runbookV2},
	{"WORKER_COUNT", runbookV2},
	{"QUEUE_SIZE", runbookV2},
	{"SCHEDULE_INTERVAL", runbookV2},
}

// warnRemovedEnvVars logs once per runbook at startup when removed knobs
// are still set in the environment, so a stale Deployment patch is a
// logged fact instead of a silent no-op.
func warnRemovedEnvVars(logger *slog.Logger) {
	stale := map[string][]string{}

	var runbooks []string

	for _, v := range removedEnvVars {
		if _, ok := os.LookupEnv(v.name); !ok {
			continue
		}

		if _, seen := stale[v.runbook]; !seen {
			runbooks = append(runbooks, v.runbook)
		}

		stale[v.runbook] = append(stale[v.runbook], v.name)
	}

	for _, rb := range runbooks {
		logger.Warn("removed configuration env vars are set and ignored",
			"vars", stale[rb],
			"migration", rb,
		)
	}
}
