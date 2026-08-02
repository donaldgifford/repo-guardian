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
// waiting out a reactive 403 retry. It MUST stay below the queue's
// JOB_ACK_TIMEOUT (default 5m): a transport sleep that outlives the
// in-flight lease makes the reaper hand the same job to another
// worker, amplifying load against an already-exhausted budget
// (INV-0012 finding I). When the computed retry delay exceeds the cap
// the transport fails fast instead of sleeping at all — the queue's
// retry cycle is the mechanism at that timescale.
//
// The pre-emptive path never sleeps: it returns a ThrottledError so
// the job defers to the delayed set (DESIGN-0021 Phase 3).
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
	if thr := t.shouldThrottle(); thr != nil {
		t.logger.Warn("pre-emptive rate limit throttle; deferring for queue retry",
			"remaining", thr.Remaining,
			"limit", thr.Limit,
			"reset_at", thr.ResetAt,
		)

		return nil, thr
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

// shouldThrottle returns a ThrottledError when the remaining budget
// is at or below the configured threshold and the reset is still
// ahead, nil otherwise. It never sleeps — deferring the work until
// the reset is the queue's job, not the transport's (DESIGN-0021
// Phase 3, INV-0012 finding I).
func (t *rateLimitTransport) shouldThrottle() *ThrottledError {
	t.mu.Lock()
	limit := t.limit
	remaining := t.remaining
	resetAt := t.resetAt
	t.mu.Unlock()

	// Skip on first request (no rate limit data yet).
	if limit == 0 {
		return nil
	}

	if remaining > int(float64(limit)*t.threshold) {
		return nil
	}

	// Reset already elapsed — the next response repopulates the
	// snapshot with the fresh window.
	if time.Until(resetAt) <= 0 {
		return nil
	}

	return &ThrottledError{ResetAt: resetAt, Remaining: remaining, Limit: limit}
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
