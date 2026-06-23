// Package main is the entrypoint for the repo-guardian GitHub App.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/config"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	valkeyqueue "github.com/donaldgifford/repo-guardian/internal/queue/valkey"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	"github.com/donaldgifford/repo-guardian/internal/scheduler"
	valkeyscheduler "github.com/donaldgifford/repo-guardian/internal/scheduler/valkey"
	"github.com/donaldgifford/repo-guardian/internal/store"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/webhook"
	"github.com/donaldgifford/repo-guardian/internal/worker"
)

const (
	shutdownTimeout   = 15 * time.Second
	depthPollInterval = 15 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("repo-guardian exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// CLI flag wins over env var (standard Go convention; supports CI
	// one-off runs without touching the Deployment env).
	strictTemplates := flag.Bool(
		"strict-templates",
		strictTemplatesFromEnv(),
		"Validate every compiled PR template against a zero-value PRVars context at startup; exit non-zero on failure",
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := initLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	logger.Info("starting repo-guardian",
		"listen_addr", cfg.ListenAddr,
		"metrics_addr", cfg.MetricsAddr,
	)

	client, err := newGitHubClient(cfg, logger)
	if err != nil {
		return fmt.Errorf("create github client: %w", err)
	}

	policyCfg, engine, templates := loadPolicyAndEngine(cfg, *strictTemplates, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt, err := bringUp(ctx, cfg, policyCfg, engine, templates, client, logger)
	if err != nil {
		return err
	}

	webhookHandler := wrapWebhookAllowlist(ctx, rt.webhookHandler, &policyCfg.Guardian, logger)

	mainServer := newMainServer(ctx, cfg.ListenAddr, webhookHandler)
	metricsServer := newMetricsServer(cfg.MetricsAddr)

	startServer(logger, mainServer, "main", cfg.ListenAddr, cancel)
	startServer(logger, metricsServer, "metrics", cfg.MetricsAddr, cancel)

	awaitShutdown(ctx, logger)
	cancel()

	if err := rt.sched.Stop(); err != nil {
		logger.Warn("scheduler stop error", "error", err)
	}

	gracefulShutdown(logger, rt.jobQueue, rt.stateStore, rt.qw.rclient, rt.workerPool, mainServer, metricsServer)

	return nil
}

// closeAndLog runs the close func and logs at WARN if it returned a
// non-nil error. Used by bringUp's failure paths to release partial
// resources without producing errcheck noise.
func closeAndLog(logger *slog.Logger, what string, fn func() error) {
	if err := fn(); err != nil {
		logger.Warn(what, "error", err)
	}
}

// runtime bundles every long-lived resource constructed at startup
// so the entrypoint can finish bring-up in a single call.
type runtime struct {
	stateStore     store.Store
	qw             queueWiring
	jobQueue       queue.Queue
	sched          scheduler.Scheduler
	workerPool     *worker.Pool
	webhookHandler http.Handler
}

// bringUp constructs every long-lived resource and starts the
// background goroutines (worker pool, valkey reaper, scheduler).
// On failure it tears down anything already constructed before
// returning the error so the caller can exit cleanly.
func bringUp(
	ctx context.Context,
	cfg *config.Config,
	policyCfg *policy.PolicyConfig,
	engine *checker.Engine,
	templates *rules.TemplateStore,
	client ghclient.Client,
	logger *slog.Logger,
) (*runtime, error) {
	stateStore, err := newStore(ctx, cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}

	qw, err := newQueue(ctx, cfg, logger)
	if err != nil {
		closeAndLog(logger, "store close after queue-init failure", stateStore.Close)

		return nil, fmt.Errorf("create queue: %w", err)
	}

	sched, err := newScheduler(cfg, logger, qw.rclient)
	if err != nil {
		closeAndLog(logger, "store close after scheduler-init failure", stateStore.Close)

		return nil, fmt.Errorf("create scheduler: %w", err)
	}

	sweeper := scheduler.NewSweeper(
		client,
		qw.queue,
		policyCfg.Guardian.ParsedScheduleInterval,
		logger,
		policyCfg.Guardian.SkipForks,
		policyCfg.Guardian.SkipArchived,
	)

	if err := sched.Schedule(ctx, "sweep", policyCfg.Guardian.ParsedScheduleInterval, sweeper.ReconcileAll); err != nil {
		closeAndLog(logger, "scheduler stop after schedule-call failure", sched.Stop)
		closeAndLog(logger, "store close after schedule-call failure", stateStore.Close)

		return nil, fmt.Errorf("schedule sweep: %w", err)
	}

	if cfg.StoreBackend == config.StoreBackendPostgres {
		policyVersion, vErr := policy.Version(policyCfg, templates.AsMap())
		if vErr != nil {
			logger.Warn("policy.Version failed; stale-sweep policy_version will be empty", "error", vErr)
		}

		staleSweeper := checker.NewStaleSweeper(checker.StaleSweeperOptions{
			Store:         stateStore,
			Queue:         qw.queue,
			RateLimit:     client,
			Logger:        logger,
			Freshness:     cfg.ReconcileFreshness,
			PolicyVersion: policyVersion,
			BatchSize:     cfg.StaleSweepBatchSize,
			Reserve:       cfg.RateLimitReserve,
		})

		if err := sched.Schedule(ctx, "stale-sweep", policyCfg.Guardian.ParsedScheduleInterval, staleSweeper.SweepStale); err != nil {
			closeAndLog(logger, "scheduler stop after stale-schedule-call failure", sched.Stop)
			closeAndLog(logger, "store close after stale-schedule-call failure", stateStore.Close)

			return nil, fmt.Errorf("schedule stale-sweep: %w", err)
		}
	}

	workerPool := worker.New(qw.queue, engine, client, policyCfg.Guardian.WorkerCount, logger)
	workerPool.Start(ctx)

	if qw.reaper != nil {
		go func() {
			if err := qw.reaper.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("valkey reaper exited", "error", err)
			}
		}()
	}

	if vq, ok := qw.queue.(*valkeyqueue.Queue); ok {
		go vq.StartDepthPoller(ctx, depthPollInterval)
	}

	watchedPaths := policy.ExtractWatchedPaths(policyCfg)
	webhookHandler := webhook.NewHandler(cfg.GitHubWebhookSecret, qw.queue, logger, watchedPaths)

	return &runtime{
		stateStore:     stateStore,
		qw:             qw,
		jobQueue:       qw.queue,
		sched:          sched,
		workerPool:     workerPool,
		webhookHandler: webhookHandler,
	}, nil
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

// queueWiring bundles the constructed queue, optional Valkey reaper,
// and the redis client (nil for memory backend). The client is exposed
// so the scheduler can share the connection rather than open its own.
type queueWiring struct {
	queue   queue.Queue
	reaper  *valkeyqueue.Reaper
	rclient *redis.Client
}

// newQueue constructs the work queue from cfg. Valkey is the only
// supported backend (IMPL-0016 dropped the in-memory shim). Returns
// a Reaper that the caller should run on its own goroutine for the
// duration of ctx.
func newQueue(ctx context.Context, cfg *config.Config, logger *slog.Logger) (queueWiring, error) {
	logger.Info("queue backend", "kind", "valkey")

	parsed, err := redis.ParseURL(cfg.QueueValkeyDSN)
	if err != nil {
		return queueWiring{}, fmt.Errorf("parse QUEUE_VALKEY_DSN: %w", err)
	}

	client := redis.NewClient(parsed)
	if err := client.Ping(ctx).Err(); err != nil {
		if closeErr := client.Close(); closeErr != nil {
			logger.Warn("valkey client close failed during ping-fail cleanup", "error", closeErr)
		}

		return queueWiring{}, fmt.Errorf("valkey ping: %w", err)
	}

	q := valkeyqueue.New(client, valkeyqueue.Options{Logger: logger})
	r := valkeyqueue.NewReaper(q, valkeyqueue.ReaperOptions{
		PodID:         podID(cfg),
		Interval:      cfg.ReaperInterval,
		JobAckTimeout: cfg.JobAckTimeout,
		Logger:        logger,
	})

	return queueWiring{queue: q, reaper: r, rclient: client}, nil
}

// newScheduler constructs the scheduler.Scheduler from cfg. Valkey
// is the only supported backend (IMPL-0016 dropped the ticker
// in-process shim); reuses the queue's redis client per IMPL-0011
// Phase 4 Open Q resolution.
func newScheduler(cfg *config.Config, logger *slog.Logger, rclient *redis.Client) (scheduler.Scheduler, error) {
	if rclient == nil {
		return nil, errors.New("scheduler requires a Valkey-backed queue (set QUEUE_BACKEND=valkey)")
	}

	logger.Info("scheduler backend", "kind", "valkey")

	return valkeyscheduler.New(rclient, valkeyscheduler.Options{
		PodID:  podID(cfg),
		Logger: logger,
	}), nil
}

// podID returns the configured pod identifier or a process-time
// fallback. Used by leader-election locks to attribute holders.
func podID(cfg *config.Config) string {
	if cfg.PodID != "" {
		return cfg.PodID
	}

	return fmt.Sprintf("repo-guardian-%d", os.Getpid())
}

// newStore constructs the persistent state store from cfg. Postgres
// is the only supported backend (IMPL-0016 dropped the in-memory
// shim). The binary applies migrations before opening the pool —
// failure aborts startup so we never serve traffic against a stale
// schema.
func newStore(ctx context.Context, cfg *config.Config, logger *slog.Logger) (store.Store, error) {
	logger.Info("store backend", "kind", "postgres")

	if err := pgstore.Migrate(cfg.StoreDSN); err != nil {
		return nil, err
	}

	return pgstore.New(ctx, cfg.StoreDSN, cfg.StorePostgresMaxConns, logger)
}

func newMainServer(runCtx context.Context, addr string, webhookHandler http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/github", webhookHandler)
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz(runCtx))

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
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

func gracefulShutdown(
	logger *slog.Logger,
	jobQueue interface{ Close() error },
	stateStore interface{ Close() error },
	rclient *redis.Client,
	workerPool interface{ Stop() },
	servers ...*http.Server,
) {
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown error", "addr", srv.Addr, "error", err)
		}
	}

	workerPool.Stop()

	if err := jobQueue.Close(); err != nil {
		logger.Warn("job queue close error", "error", err)
	}

	if err := stateStore.Close(); err != nil {
		logger.Warn("store close error", "error", err)
	}

	if rclient != nil {
		if err := rclient.Close(); err != nil {
			logger.Warn("redis client close error", "error", err)
		}
	}

	logger.Info("repo-guardian stopped")
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

func initLogger(level string) *slog.Logger {
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

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})

	return slog.New(handler)
}

// wrapWebhookAllowlist optionally wraps next with the GitHub IP
// allowlist middleware. When the allowlist is enabled the refresher
// is started against ctx so it terminates with shutdown. Returns the
// original handler when disabled.
func wrapWebhookAllowlist(
	ctx context.Context,
	next http.Handler,
	g *policy.GuardianConfig,
	logger *slog.Logger,
) http.Handler {
	if !g.WebhookIPAllowlist {
		logger.Info("webhook IP allowlist disabled")
		return next
	}

	allowlist := webhook.NewGitHubIPAllowlist(
		g.WebhookIPAllowlistFailOpen,
		g.TrustProxyHeaders,
		logger,
	)
	allowlist.StartRefresh(ctx)

	logger.Info("webhook IP allowlist enabled",
		"fail_open", g.WebhookIPAllowlistFailOpen,
		"trust_proxy", g.TrustProxyHeaders,
	)

	return allowlist.Middleware(next)
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

func handleReadyz(runCtx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if runCtx.Err() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)

			if _, err := w.Write([]byte("not ready")); err != nil {
				slog.Error("failed to write readyz response", "error", err)
			}

			return
		}

		w.WriteHeader(http.StatusOK)

		if _, err := w.Write([]byte("ok")); err != nil {
			slog.Error("failed to write readyz response", "error", err)
		}
	}
}
