package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func probe(t *testing.T, h http.HandlerFunc) (int, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", http.NoBody))

	return rec.Code, rec.Body.String()
}

func TestReadiness_CachesTheLastEvaluation(t *testing.T) {
	t.Parallel()

	var (
		down  atomic.Bool
		calls atomic.Int32
	)

	r := newReadiness(slog.New(slog.NewTextHandler(io.Discard, nil)), readinessCheck{name: "temporal", fn: func(context.Context) error {
		calls.Add(1)

		if down.Load() {
			return errors.New("unreachable")
		}

		return nil
	}})
	h := r.handler(context.Background())

	if code, _ := probe(t, h); code != http.StatusServiceUnavailable {
		t.Errorf("before the first evaluation = %d, want 503", code)
	}

	r.evaluate(context.Background())

	for range 3 {
		if code, _ := probe(t, h); code != http.StatusOK {
			t.Errorf("after a passing evaluation = %d, want 200", code)
		}
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("checks ran %d times for 3 probes, want 1: probes must read the cache", n)
	}

	down.Store(true)
	r.evaluate(context.Background())

	if code, body := probe(t, h); code != http.StatusServiceUnavailable || !strings.Contains(body, "temporal: unreachable") {
		t.Errorf("after a failing evaluation = %d %q, want 503 naming the check", code, body)
	}
}

func TestReadiness_NotReadyWhileShuttingDown(t *testing.T) {
	t.Parallel()

	r := newReadiness(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.evaluate(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if code, _ := probe(t, r.handler(ctx)); code != http.StatusServiceUnavailable {
		t.Errorf("readyz during shutdown = %d, want 503", code)
	}
}
