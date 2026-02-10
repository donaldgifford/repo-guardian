package github

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// withRateLimitHeaders sets standard rate limit response headers.
func withRateLimitHeaders(w http.ResponseWriter, remaining, limit int, resetAt time.Time) {
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
}

func TestRateLimitTransport_NormalRequest(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		withRateLimitHeaders(w, 900, 1000, time.Now().Add(time.Hour))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok": true}`)
	}))
	defer server.Close()

	transport := newRateLimitTransport(
		http.DefaultTransport,
		slog.Default(),
		0.10,
	)

	client := &http.Client{Transport: transport}

	start := time.Now()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Should complete near-instantly (no throttling with 4500/5000 remaining).
	if elapsed > 2*time.Second {
		t.Errorf("request took too long (%v), expected near-instant", elapsed)
	}
}

func TestRateLimitTransport_PreemptiveThrottle(t *testing.T) {
	t.Parallel()

	callCount := 0
	resetAt := time.Now().Add(10 * time.Second)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		// Low remaining budget: 20 of 5000 (0.4% < 10% threshold).
		withRateLimitHeaders(w, 20, 5000, resetAt)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := newRateLimitTransport(
		http.DefaultTransport,
		slog.Default(),
		0.10,
	)

	client := &http.Client{Transport: transport}

	// First request primes the rate limit state.
	req1, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)

	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	resp1.Body.Close()

	// Second request should trigger pre-emptive throttle.
	start := time.Now()

	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)

	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	resp2.Body.Close()

	elapsed := time.Since(start)

	if elapsed < 400*time.Millisecond {
		t.Errorf("expected pre-emptive delay, but request completed in %v", elapsed)
	}

	if callCount != 2 {
		t.Errorf("expected 2 server calls, got %d", callCount)
	}
}

func TestRateLimitTransport_PrimaryRateLimit(t *testing.T) {
	t.Parallel()

	callCount := 0
	resetAt := time.Now().Add(2 * time.Second)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount == 1 {
			// First call: rate limited.
			withRateLimitHeaders(w, 0, 5000, resetAt)
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintln(w, `{"message": "API rate limit exceeded"}`)

			return
		}

		// Retry: success.
		withRateLimitHeaders(w, 4999, 5000, time.Now().Add(time.Hour))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok": true}`)
	}))
	defer server.Close()

	transport := newRateLimitTransport(
		http.DefaultTransport,
		slog.Default(),
		0.10,
	)

	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after retry, got %d", resp.StatusCode)
	}

	if callCount != 2 {
		t.Errorf("expected 2 server calls (original + retry), got %d", callCount)
	}
}

func TestRateLimitTransport_SecondaryRateLimit(t *testing.T) {
	t.Parallel()

	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount == 1 {
			// First call: secondary rate limit with Retry-After.
			w.Header().Set("Retry-After", "1")
			withRateLimitHeaders(w, 100, 5000, time.Now().Add(time.Hour))
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintln(w, `{"message": "secondary rate limit"}`)

			return
		}

		// Retry: success.
		withRateLimitHeaders(w, 4999, 5000, time.Now().Add(time.Hour))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok": true}`)
	}))
	defer server.Close()

	transport := newRateLimitTransport(
		http.DefaultTransport,
		slog.Default(),
		0.10,
	)

	client := &http.Client{Transport: transport}

	start := time.Now()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after retry, got %d", resp.StatusCode)
	}

	if callCount != 2 {
		t.Errorf("expected 2 server calls, got %d", callCount)
	}

	// Should have waited ~1 second for Retry-After.
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected ~1s delay for Retry-After, got %v", elapsed)
	}
}

func TestRateLimitTransport_ContextCancellation(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().Add(10 * time.Minute)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Rate limited — would need to wait until reset.
		withRateLimitHeaders(w, 0, 5000, resetAt)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintln(w, `{"message": "API rate limit exceeded"}`)
	}))
	defer server.Close()

	transport := newRateLimitTransport(
		http.DefaultTransport,
		slog.Default(),
		0.10,
	)

	client := &http.Client{Transport: transport}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context after a short delay.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)

	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from context cancellation")
	}

	// Should return quickly after context cancellation, not wait for reset.
	if elapsed > 2*time.Second {
		t.Errorf("expected quick return on context cancel, took %v", elapsed)
	}
}

func TestRateLimitTransport_RetryExhausted(t *testing.T) {
	t.Parallel()

	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		// Always rate limited.
		w.Header().Set("Retry-After", "1")
		withRateLimitHeaders(w, 0, 5000, time.Now().Add(2*time.Second))
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintln(w, `{"message": "API rate limit exceeded"}`)
	}))
	defer server.Close()

	transport := newRateLimitTransport(
		http.DefaultTransport,
		slog.Default(),
		0.10,
	)

	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected response (not error) when retry exhausted: %v", err)
	}
	defer resp.Body.Close()

	// After one retry, the 403 should be returned to the caller.
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when retry exhausted, got %d", resp.StatusCode)
	}

	// Original + 1 retry = 2 calls.
	if callCount != 2 {
		t.Errorf("expected exactly 2 server calls, got %d", callCount)
	}
}

// trackingReadCloser wraps an io.ReadCloser and records whether Close was called.
type trackingReadCloser struct {
	io.ReadCloser
	closed atomic.Bool
}

func (t *trackingReadCloser) Close() error {
	t.closed.Store(true)
	return t.ReadCloser.Close()
}

func TestRateLimitTransport_ResponseBodyClosed(t *testing.T) {
	t.Parallel()

	var firstBody *trackingReadCloser
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Retry-After", "1")
			withRateLimitHeaders(w, 100, 5000, time.Now().Add(time.Hour))
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintln(w, `{"message": "secondary rate limit"}`)

			return
		}

		withRateLimitHeaders(w, 4999, 5000, time.Now().Add(time.Hour))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok": true}`)
	}))
	defer server.Close()

	// Wrap the default transport to intercept and track the first response body.
	wrapping := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			return nil, err
		}

		if firstBody == nil {
			tracker := &trackingReadCloser{ReadCloser: resp.Body}
			firstBody = tracker
			resp.Body = tracker
		}

		return resp, err
	})

	transport := newRateLimitTransport(wrapping, slog.Default(), 0.10)

	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if firstBody == nil {
		t.Fatal("expected first response body to be tracked")
	}

	if !firstBody.closed.Load() {
		t.Error("first response body was not closed before retry")
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after retry, got %d", resp.StatusCode)
	}
}

// roundTripFunc adapts a function to the http.RoundTripper interface.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
