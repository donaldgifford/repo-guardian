package github

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// maxRateLimitSleep caps how long the transport may block a caller
// waiting out a rate limit, on both the pre-emptive throttle and the
// 403 retry path. It MUST stay below the queue's JOB_ACK_TIMEOUT
// (default 5m): a transport sleep that outlives the in-flight lease
// makes the reaper hand the same job to another worker, amplifying
// load against an already-exhausted budget (INV-0012 finding I).
// When the computed delay exceeds the cap the transport fails fast
// instead of sleeping at all — the queue's nack/reaper cycle is the
// retry mechanism at that timescale.
//
// DESIGN-0021 Phase 0 scaffolding: Phase 3 replaces the pre-emptive
// sleep with a ThrottledError return and removes this cap.
const maxRateLimitSleep = 60 * time.Second

// ThrottledError signals that the transport pre-emptively refused to
// send a request because the remaining rate-limit budget is at or
// below the throttle threshold. It is a deferral signal, not a
// failure: the request was never sent, and the work should be retried
// once GitHub's quota window resets at ResetAt. The worker translates
// it into a queue.RetryAfterError so the job parks in the delayed set
// instead of blocking a leased worker slot (DESIGN-0021 Phase 3).
type ThrottledError struct {
	ResetAt   time.Time
	Remaining int
	Limit     int
}

// Error names the reset time so a bare log line is actionable without
// unwrapping.
func (e *ThrottledError) Error() string {
	return fmt.Sprintf("github rate limit throttled: %d/%d remaining, resets at %s",
		e.Remaining, e.Limit, e.ResetAt.UTC().Format(time.RFC3339))
}

// rateLimitTransport is an http.RoundTripper that handles GitHub API rate
// limits transparently. It wraps another transport and provides:
//   - Pre-emptive throttling when remaining budget is below a threshold
//   - Automatic retry on primary rate limits (403 + X-RateLimit-Remaining: 0)
//   - Automatic retry on secondary rate limits (403 + Retry-After header)
type rateLimitTransport struct {
	next      http.RoundTripper
	logger    *slog.Logger
	threshold float64 // Fraction of limit at which to start throttling (e.g., 0.10).

	// sleep is swapped out by tests to observe requested delays
	// without waiting them out.
	sleep func(ctx context.Context, d time.Duration) error

	mu        sync.Mutex
	remaining int
	limit     int
	resetAt   time.Time
}

// newRateLimitTransport wraps the given transport with rate limit handling.
func newRateLimitTransport(next http.RoundTripper, logger *slog.Logger, threshold float64) *rateLimitTransport {
	return &rateLimitTransport{
		next:      next,
		logger:    logger,
		threshold: threshold,
		sleep:     sleepWithContext,
	}
}

// RoundTrip executes an HTTP request with rate limit awareness.
func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.waitIfNeeded(req.Context()); err != nil {
		return nil, err
	}

	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	t.updateFromResponse(resp)

	if !t.isRateLimited(resp) {
		return resp, nil
	}

	// Close the first response body before retrying. Drain errors are
	// not actionable here — we're about to retry the request.
	_ = resp.Body.Close()

	// Rate limited — compute delay and retry once.
	delay := t.rateLimitDelay(resp)
	reason := t.rateLimitReason(resp)

	if delay > maxRateLimitSleep {
		t.logger.Warn("rate limit retry delay exceeds sleep cap; failing fast for queue retry",
			"reason", reason,
			"delay", delay,
			"cap", maxRateLimitSleep,
			"status", resp.StatusCode,
		)

		return nil, fmt.Errorf(
			"github rate limited (%s): retry delay %s exceeds sleep cap %s; failing fast so the queue can retry",
			reason, delay.Round(time.Second), maxRateLimitSleep)
	}

	t.logger.Warn("github api rate limited, waiting to retry",
		"reason", reason,
		"delay", delay,
		"status", resp.StatusCode,
	)

	metrics.GitHubRateLimitWaitsTotal.WithLabelValues(reason).Inc()
	metrics.GitHubRateLimitWaitSeconds.Observe(delay.Seconds())

	if err := t.sleep(req.Context(), delay); err != nil {
		return nil, err
	}

	// Replay the request body for the retry.
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}

		req.Body = body
	}

	retryResp, retryErr := t.next.RoundTrip(req)
	if retryErr != nil {
		return nil, retryErr
	}

	t.updateFromResponse(retryResp)

	return retryResp, nil
}

// waitIfNeeded applies pre-emptive throttling when the remaining budget
// is below the configured threshold.
func (t *rateLimitTransport) waitIfNeeded(ctx context.Context) error {
	t.mu.Lock()
	limit := t.limit
	remaining := t.remaining
	resetAt := t.resetAt
	t.mu.Unlock()

	// Skip on first request (no rate limit data yet).
	if limit == 0 {
		return nil
	}

	thresholdCount := int(float64(limit) * t.threshold)
	if remaining > thresholdCount {
		return nil
	}

	untilReset := time.Until(resetAt)

	if untilReset <= 0 {
		return nil
	}

	var delay time.Duration

	if remaining == 0 {
		// Fully exhausted — wait until reset.
		delay = untilReset
	} else {
		// Spread remaining budget evenly until reset.
		delay = untilReset / time.Duration(remaining)
	}

	// Floor at 1 second to handle clock skew.
	if delay < time.Second {
		delay = time.Second
	}

	if delay > maxRateLimitSleep {
		t.logger.Warn("pre-emptive rate limit delay exceeds sleep cap; failing fast for queue retry",
			"remaining", remaining,
			"limit", limit,
			"delay", delay,
			"cap", maxRateLimitSleep,
			"reset_at", resetAt,
		)

		return fmt.Errorf(
			"github rate limit: pre-emptive delay %s exceeds sleep cap %s; failing fast so the queue can retry",
			delay.Round(time.Second), maxRateLimitSleep)
	}

	t.logger.Warn("pre-emptive rate limit throttle",
		"remaining", remaining,
		"limit", limit,
		"delay", delay,
		"reset_at", resetAt,
	)

	metrics.GitHubRateLimitWaitsTotal.WithLabelValues("preemptive").Inc()
	metrics.GitHubRateLimitWaitSeconds.Observe(delay.Seconds())

	return t.sleep(ctx, delay)
}

// updateFromResponse parses rate limit headers and updates internal state.
func (t *rateLimitTransport) updateFromResponse(resp *http.Response) {
	if resp == nil {
		return
	}

	remaining := resp.Header.Get("X-RateLimit-Remaining")
	limit := resp.Header.Get("X-RateLimit-Limit")
	reset := resp.Header.Get("X-RateLimit-Reset")

	if remaining == "" || limit == "" || reset == "" {
		return
	}

	r, err := strconv.Atoi(remaining)
	if err != nil {
		return
	}

	l, err := strconv.Atoi(limit)
	if err != nil {
		return
	}

	resetUnix, err := strconv.ParseInt(reset, 10, 64)
	if err != nil {
		return
	}

	t.mu.Lock()
	t.remaining = r
	t.limit = l
	t.resetAt = time.Unix(resetUnix, 0)
	t.mu.Unlock()

	metrics.GitHubRateRemaining.Set(float64(r))

	t.logger.Debug("github api rate limit",
		"remaining", r,
		"limit", l,
		"reset", time.Unix(resetUnix, 0),
	)
}

// isRateLimited returns true if the response indicates a rate limit error.
func (*rateLimitTransport) isRateLimited(resp *http.Response) bool {
	return resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "")
}

// rateLimitDelay computes how long to wait before retrying.
func (*rateLimitTransport) rateLimitDelay(resp *http.Response) time.Duration {
	// Secondary rate limit — Retry-After header (seconds).
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		seconds, err := strconv.Atoi(retryAfter)
		if err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}

	// Primary rate limit — wait until X-RateLimit-Reset.
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		resetUnix, err := strconv.ParseInt(reset, 10, 64)
		if err == nil {
			delay := time.Until(time.Unix(resetUnix, 0))
			if delay > 0 {
				return delay
			}
		}
	}

	// Fallback: 1 second floor.
	return time.Second
}

// rateLimitReason returns a label for the type of rate limit encountered.
func (*rateLimitTransport) rateLimitReason(resp *http.Response) string {
	if resp.Header.Get("Retry-After") != "" {
		return "secondary"
	}

	return "primary"
}

// sleepWithContext sleeps for the given duration, returning early if the
// context is canceled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
