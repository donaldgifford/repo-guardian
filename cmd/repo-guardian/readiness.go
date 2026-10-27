package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Readiness cadence (IMPL-0025 Phase 12). Probes read the cached
// verdict, so a kubelet probe never waits on Temporal or Postgres.
const (
	readinessInterval = 10 * time.Second
	readinessTimeout  = 5 * time.Second
)

// readinessCheck is one dependency a role needs to serve.
type readinessCheck struct {
	name string
	fn   func(context.Context) error
}

// readiness evaluates its checks in the background and caches the
// result. It starts not ready until the first evaluation passes.
type readiness struct {
	checks []readinessCheck
	logger *slog.Logger

	mu  sync.RWMutex
	err error
}

func newReadiness(logger *slog.Logger, checks ...readinessCheck) *readiness {
	return &readiness{checks: checks, logger: logger, err: errors.New("not evaluated yet")}
}

// run evaluates now, then every interval until ctx ends.
func (r *readiness) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		r.evaluate(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *readiness) evaluate(ctx context.Context) {
	var errs []error

	for _, c := range r.checks {
		checkCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
		if err := c.fn(checkCtx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.name, err))
		}

		cancel()
	}

	err := errors.Join(errs...)

	r.mu.Lock()
	changed := (err == nil) != (r.err == nil)
	r.err = err
	r.mu.Unlock()

	if changed && err != nil {
		r.logger.Warn("not ready", "error", err)
	} else if changed {
		r.logger.Info("ready")
	}
}

// handler answers 200 when the last evaluation passed and the process is
// not shutting down, 503 otherwise.
func (r *readiness) handler(runCtx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		r.mu.RLock()
		err := r.err
		r.mu.RUnlock()

		if err == nil {
			err = runCtx.Err()
		}

		status, body := http.StatusOK, "ok"
		if err != nil {
			status, body = http.StatusServiceUnavailable, "not ready: "+err.Error()
		}

		w.WriteHeader(status)

		if _, werr := w.Write([]byte(body)); werr != nil {
			r.logger.Error("failed to write readyz response", "error", werr)
		}
	}
}
