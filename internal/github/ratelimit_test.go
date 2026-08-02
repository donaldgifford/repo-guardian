package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

	// Second request must defer with a ThrottledError instead of
	// sleeping — it should never reach the server.
	start := time.Now()

	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)

	resp2, err := client.Do(req2)
	if resp2 != nil {
		resp2.Body.Close()
	}

	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("second request = nil error, want ThrottledError deferral")
	}

	var thr *ThrottledError
	if !errors.As(err, &thr) {
		t.Fatalf("second request error = %v, want *ThrottledError recoverable via errors.As", err)
	}

	if thr.Remaining != 20 || thr.Limit != 5000 {
		t.Errorf("ThrottledError = %d/%d, want 20/5000", thr.Remaining, thr.Limit)
	}

	if elapsed > 2*time.Second {
		t.Errorf("deferral took %v, want near-instant (no sleeping)", elapsed)
	}

	if callCount != 1 {
		t.Errorf("expected 1 server call (throttled request never sent), got %d", callCount)
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

// recordingSleeper replaces the transport's sleep func, accumulating
// requested delays as virtual time instead of actually waiting.
type recordingSleeper struct {
	delays []time.Duration
}

func (s *recordingSleeper) sleep(_ context.Context, d time.Duration) error {
	s.delays = append(s.delays, d)
	return nil
}

func (s *recordingSleeper) virtualBlocked() time.Duration {
	var total time.Duration
	for _, d := range s.delays {
		total += d
	}

	return total
}

// primeRateLimitState seeds the transport's rate-limit view as if a
// prior response had carried these headers.
func primeRateLimitState(t *testing.T, tr *rateLimitTransport, remaining, limit int, resetAt time.Time) {
	t.Helper()

	tr.mu.Lock()
	tr.remaining = remaining
	tr.limit = limit
	tr.resetAt = resetAt
	tr.mu.Unlock()
}

// TestRateLimitTransport_PreemptiveNeverSleeps locks the DESIGN-0021
// Phase 3 contract: the pre-emptive path either proceeds or returns a
// ThrottledError immediately — it never sleeps and never sends the
// throttled request, regardless of how far away the reset is.
func TestRateLimitTransport_PreemptiveNeverSleeps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		remaining     int
		resetIn       time.Duration
		wantThrottled bool
	}{
		{
			name:          "exhausted budget defers until distant reset",
			remaining:     0,
			resetIn:       50 * time.Minute,
			wantThrottled: true,
		},
		{
			name:          "below threshold defers even with budget left",
			remaining:     20,
			resetIn:       10 * time.Second,
			wantThrottled: true,
		},
		{
			name:      "above threshold proceeds",
			remaining: 900,
			resetIn:   time.Hour,
		},
		{
			name:      "elapsed reset proceeds on stale snapshot",
			remaining: 0,
			resetIn:   -time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sleeper := &recordingSleeper{}
			nextCalls := 0
			next := roundTripFunc(func(*http.Request) (*http.Response, error) {
				nextCalls++

				resp := httptest.NewRecorder()
				withRateLimitHeaders(resp, 4000, 5000, time.Now().Add(time.Hour))
				resp.WriteHeader(http.StatusOK)

				return resp.Result(), nil
			})

			tr := newRateLimitTransport(next, slog.Default(), 0.10)
			tr.sleep = sleeper.sleep
			resetAt := time.Now().Add(tt.resetIn)
			primeRateLimitState(t, tr, tt.remaining, 5000, resetAt)

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.github.example/repos/o/r", http.NoBody)
			if err != nil {
				t.Fatalf("creating request: %v", err)
			}

			resp, rtErr := tr.RoundTrip(req)
			if resp != nil {
				resp.Body.Close()
			}

			if len(sleeper.delays) != 0 {
				t.Errorf("sleeps = %v, want none on the pre-emptive path", sleeper.delays)
			}

			if !tt.wantThrottled {
				if rtErr != nil {
					t.Fatalf("RoundTrip() error = %v, want nil", rtErr)
				}

				if nextCalls != 1 {
					t.Errorf("next.RoundTrip calls = %d, want 1", nextCalls)
				}

				return
			}

			var thr *ThrottledError
			if !errors.As(rtErr, &thr) {
				t.Fatalf("RoundTrip() error = %v, want *ThrottledError", rtErr)
			}

			if thr.Remaining != tt.remaining || thr.Limit != 5000 || !thr.ResetAt.Equal(resetAt) {
				t.Errorf("ThrottledError = %+v, want remaining=%d limit=5000 resetAt=%v", thr, tt.remaining, resetAt)
			}

			if nextCalls != 0 {
				t.Errorf("next.RoundTrip calls = %d, want 0 (throttled request never sent)", nextCalls)
			}
		})
	}
}

func TestRateLimitTransport_RetryDelayAboveCap_FailsFast(t *testing.T) {
	t.Parallel()

	sleeper := &recordingSleeper{}
	nextCalls := 0
	next := roundTripFunc(func(*http.Request) (*http.Response, error) {
		nextCalls++

		resp := httptest.NewRecorder()
		resp.Header().Set("Retry-After", "3600")
		withRateLimitHeaders(resp, 0, 5000, time.Now().Add(time.Hour))
		resp.WriteHeader(http.StatusForbidden)

		return resp.Result(), nil
	})

	tr := newRateLimitTransport(next, slog.Default(), 0.10)
	tr.sleep = sleeper.sleep

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.github.example/repos/o/r", http.NoBody)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}

	resp, rtErr := tr.RoundTrip(req)
	if resp != nil {
		resp.Body.Close()
	}

	if rtErr == nil {
		t.Fatal("RoundTrip() = nil error, want fail-fast error for above-cap retry delay")
	}

	if !strings.Contains(rtErr.Error(), "sleep cap") {
		t.Errorf("RoundTrip() error = %q, want it to name the sleep cap", rtErr)
	}

	if nextCalls != 1 {
		t.Errorf("next.RoundTrip calls = %d, want 1 (initial request only, no retry)", nextCalls)
	}

	if len(sleeper.delays) != 0 {
		t.Errorf("sleeps = %v, want none on the fail-fast path", sleeper.delays)
	}
}

// TestRateLimitTransport_FindingITimeline_CapPreventsDoubleClaim
// replays the INV-0012 finding I timeline against virtual time: a
// worker claims a job (in-flight lease), the transport hits an
// exhausted budget, and the reaper hands the job to a second worker
// iff the transport blocks longer than JOB_ACK_TIMEOUT. Post
// DESIGN-0021 Phase 3 the pre-emptive path cannot block at all — it
// returns a ThrottledError instantly — and the reactive 403 retry
// path is bounded by maxRateLimitSleep, which the static guard below
// pins under the lease. Neutralizing either protection must fail
// this test.
func TestRateLimitTransport_FindingITimeline_CapPreventsDoubleClaim(t *testing.T) {
	t.Parallel()

	// Mirrors the JOB_ACK_TIMEOUT default in internal/config.
	const jobAckTimeout = 5 * time.Minute

	if maxRateLimitSleep >= jobAckTimeout {
		t.Fatalf("maxRateLimitSleep = %v, must stay below JOB_ACK_TIMEOUT %v", maxRateLimitSleep, jobAckTimeout)
	}

	sleeper := &recordingSleeper{}
	nextCalls := 0
	next := roundTripFunc(func(*http.Request) (*http.Response, error) {
		nextCalls++

		resp := httptest.NewRecorder()
		withRateLimitHeaders(resp, 0, 5000, time.Now().Add(50*time.Minute))
		resp.WriteHeader(http.StatusForbidden)

		return resp.Result(), nil
	})

	tr := newRateLimitTransport(next, slog.Default(), 0.10)
	tr.sleep = sleeper.sleep
	primeRateLimitState(t, tr, 0, 5000, time.Now().Add(50*time.Minute))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.github.example/repos/o/r", http.NoBody)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}

	resp, rtErr := tr.RoundTrip(req)
	if resp != nil {
		resp.Body.Close()
	}

	if rtErr == nil {
		t.Fatal("RoundTrip() = nil error, want deferral error under exhausted budget")
	}

	var thr *ThrottledError
	if !errors.As(rtErr, &thr) {
		t.Errorf("RoundTrip() error = %v, want *ThrottledError so the worker can defer the job", rtErr)
	}

	blocked := sleeper.virtualBlocked()
	if claimedTwice := blocked > jobAckTimeout; claimedTwice {
		t.Errorf(
			"handler blocked %v inside the transport, outliving the %v lease — the reaper would requeue and a second worker would claim the job (finding I)",
			blocked,
			jobAckTimeout,
		)
	}

	if nextCalls != 0 {
		t.Errorf("next.RoundTrip calls = %d, want 0 — no calls should be issued against an exhausted budget", nextCalls)
	}
}
