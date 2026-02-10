package github

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// rateLimitTransport is an http.RoundTripper that handles GitHub API rate
// limits transparently. It wraps another transport and provides:
//   - Pre-emptive throttling when remaining budget is below a threshold
//   - Automatic retry on primary rate limits (403 + X-RateLimit-Remaining: 0)
//   - Automatic retry on secondary rate limits (403 + Retry-After header)
type rateLimitTransport struct {
	next      http.RoundTripper
	logger    *slog.Logger
	threshold float64 // Fraction of limit at which to start throttling (e.g., 0.10).

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

	// Rate limited — compute delay and retry once.
	delay := t.rateLimitDelay(resp)
	reason := t.rateLimitReason(resp)

	t.logger.Warn("github api rate limited, waiting to retry",
		"reason", reason,
		"delay", delay,
		"status", resp.StatusCode,
	)

	metrics.GitHubRateLimitWaitsTotal.WithLabelValues(reason).Inc()
	metrics.GitHubRateLimitWaitSeconds.Observe(delay.Seconds())

	if err := sleepWithContext(req.Context(), delay); err != nil {
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

	t.logger.Warn("pre-emptive rate limit throttle",
		"remaining", remaining,
		"limit", limit,
		"delay", delay,
		"reset_at", resetAt,
	)

	metrics.GitHubRateLimitWaitsTotal.WithLabelValues("preemptive").Inc()
	metrics.GitHubRateLimitWaitSeconds.Observe(delay.Seconds())

	return sleepWithContext(ctx, delay)
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
