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
		"dry_run", cfg.DryRun,
		"worker_count", cfg.WorkerCount,
	)

	// Initialize GitHub client.
	client, err := ghclient.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKeyPath, logger, cfg.RateLimitThreshold)
	if err != nil {
		logger.Error("failed to create GitHub client", "error", err)
		os.Exit(1)
	}

	// Initialize rule registry and template store.
	registry := rules.NewRegistry(rules.DefaultRules)

	templates := rules.NewTemplateStore()
	if err := templates.Load(cfg.TemplateDir); err != nil {
		logger.Error("failed to load templates", "error", err)
		os.Exit(1)
	}

	// Initialize checker engine.
	engine := checker.NewEngine(
		registry,
		templates,
		logger,
		cfg.SkipForks,
		cfg.SkipArchived,
		cfg.DryRun,
	)

	// Initialize work queue.
	queue := checker.NewQueue(cfg.QueueSize, logger)

	// Initialize webhook handler.
	webhookHandler := webhook.NewHandler(cfg.GitHubWebhookSecret, queue, logger)

	// Initialize scheduler.
	sched := scheduler.NewScheduler(
		client,
		queue,
		cfg.ScheduleInterval,
		logger,
		cfg.SkipForks,
		cfg.SkipArchived,
	)

	// Set up context for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start work queue workers.
	queue.Start(ctx, cfg.WorkerCount, engine, client)

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
