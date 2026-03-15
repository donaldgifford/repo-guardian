// Package main is the entrypoint for the repo-guardian GitHub App.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/config"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	"github.com/donaldgifford/repo-guardian/internal/scheduler"
	"github.com/donaldgifford/repo-guardian/internal/webhook"
)

const shutdownTimeout = 15 * time.Second

func main() {
	// Load configuration.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Initialize logger.
	logger := initLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	logger.Info("starting repo-guardian",
		"listen_addr", cfg.ListenAddr,
		"metrics_addr", cfg.MetricsAddr,
		"custom_properties_mode", cfg.CustomPropertiesMode,
	)

	// Initialize GitHub client.
	client, err := newGitHubClient(cfg, logger)
	if err != nil {
		logger.Error("failed to create GitHub client", "error", err)
		os.Exit(1)
	}

	// Load policy configuration.
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

	// Initialize template store.
	templates := rules.NewTemplateStore()
	if err := templates.Load(cfg.TemplateDir); err != nil {
		logger.Error("failed to load templates", "error", err)
		os.Exit(1)
	}

	// Initialize checker engine from policy config.
	engine, err := checker.NewEngineFromPolicy(
		policyCfg,
		templates,
		logger,
		cfg.CustomPropertiesMode,
	)
	if err != nil {
		logger.Error("failed to create checker engine", "error", err)
		os.Exit(1)
	}

	// Initialize work queue using policy guardian config.
	queue := checker.NewQueue(policyCfg.Guardian.QueueSize, logger)

	// Initialize webhook handler.
	var webhookHandler http.Handler = webhook.NewHandler(cfg.GitHubWebhookSecret, queue, logger)

	// Initialize scheduler using policy guardian config.
	sched := scheduler.NewScheduler(
		client,
		queue,
		policyCfg.Guardian.ParsedScheduleInterval,
		logger,
		policyCfg.Guardian.SkipForks,
		policyCfg.Guardian.SkipArchived,
	)

	// Set up context for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Wrap webhook handler with IP allowlist middleware if enabled.
	if policyCfg.Guardian.WebhookIPAllowlist {
		allowlist := webhook.NewGitHubIPAllowlist(
			policyCfg.Guardian.WebhookIPAllowlistFailOpen,
			policyCfg.Guardian.TrustProxyHeaders,
			logger,
		)
		allowlist.StartRefresh(ctx)
		webhookHandler = allowlist.Middleware(webhookHandler)

		logger.Info("webhook IP allowlist enabled",
			"fail_open", policyCfg.Guardian.WebhookIPAllowlistFailOpen,
			"trust_proxy", policyCfg.Guardian.TrustProxyHeaders,
		)
	} else {
		logger.Info("webhook IP allowlist disabled")
	}

	// Start work queue workers.
	queue.Start(ctx, policyCfg.Guardian.WorkerCount, engine, client)

	// Start scheduler in background.
	go sched.Start(ctx)

	// Set up and start HTTP servers.
	mainServer := newMainServer(cfg.ListenAddr, webhookHandler, queue)
	metricsServer := newMetricsServer(cfg.MetricsAddr)

	startServer(logger, mainServer, "main", cfg.ListenAddr, cancel)
	startServer(logger, metricsServer, "metrics", cfg.MetricsAddr, cancel)

	// Wait for shutdown signal.
	awaitShutdown(ctx, logger)
	cancel()

	// Graceful shutdown.
	gracefulShutdown(logger, queue, mainServer, metricsServer)
}

func newMainServer(addr string, webhookHandler http.Handler, queue *checker.Queue) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/github", webhookHandler)
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz(queue))

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

func gracefulShutdown(logger *slog.Logger, queue *checker.Queue, servers ...*http.Server) {
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown error", "addr", srv.Addr, "error", err)
		}
	}

	queue.Stop()
	logger.Info("repo-guardian stopped")
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

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok")); err != nil {
		slog.Error("failed to write healthz response", "error", err)
	}
}

func handleReadyz(queue *checker.Queue) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if !queue.Accepting() {
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
