package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	gh "github.com/google/go-github/v68/github"
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

// AsThrottled reports whether err carries a rate-limit deferral
// signal, normalising the three shapes it can take:
//
//   - this package's *ThrottledError — the transport's pre-emptive
//     reserve (remaining at or below the threshold);
//   - go-github's *github.RateLimitError — its client-side pre-check,
//     which short-circuits ABOVE our transport whenever a prior
//     response showed remaining=0 (the bypass context key is
//     unexported in go-github v68, so this path is unavoidable and
//     our transport never sees the request);
//   - go-github's *github.AbuseRateLimitError — a secondary rate limit,
//     also a 403, which must never be read as access denial.
//
// The transport's above-cap secondary-limit error wraps a
// *ThrottledError, so it is the first shape.
//
// Workers call this instead of errors.As directly so exactly one
// deferral signal crosses the client boundary and go-github stays
// encapsulated in this package (DESIGN-0021 Phase 3; the second
// shape was discovered by the IMPL-0022 task 3.4 chain test).
func AsThrottled(err error) (*ThrottledError, bool) {
	var thr *ThrottledError
	if errors.As(err, &thr) {
		return thr, true
	}

	var rle *gh.RateLimitError
	if errors.As(err, &rle) {
		return &ThrottledError{
			ResetAt:   rle.Rate.Reset.Time,
			Remaining: rle.Rate.Remaining,
			Limit:     rle.Rate.Limit,
		}, true
	}

	// A secondary rate limit is a 403 too. It carries Retry-After rather
	// than rate headers; without one the reset is zero and callers back
	// off.
	var abuse *gh.AbuseRateLimitError
	if errors.As(err, &abuse) {
		thr := &ThrottledError{}
		if d := abuse.GetRetryAfter(); d > 0 {
			thr.ResetAt = time.Now().Add(d)
		}

		return thr, true
	}

	return nil, false
}

// rateLimitTransport is an http.RoundTripper that handles GitHub API rate
// limits transparently. It wraps another transport and provides:
//   - Pre-emptive throttling when a bucket's remaining budget is below a threshold
//   - Automatic retry on primary rate limits (403 + X-RateLimit-Remaining: 0, or 429)
//   - Automatic retry on secondary rate limits (403 or 429 + Retry-After header)
//   - GraphQL rate limits classified from the body into a *ThrottledError
type rateLimitTransport struct {
	next      http.RoundTripper
	logger    *slog.Logger
	threshold float64 // Fraction of limit at which to start throttling (e.g., 0.10).

	// sleep is swapped out by tests to observe requested delays
	// without waiting them out.
	sleep func(ctx context.Context, d time.Duration) error

	// snapshots holds one budget view per x-ratelimit-resource: GitHub
	// meters core, graphql and search separately, so a GraphQL response
	// must not move the core view, nor core's exhaustion refuse a
	// GraphQL call (IMPL-0028 task 2.5).
	mu        sync.Mutex
	snapshots map[string]rateSnapshot
}

// rateSnapshot is the last rate headers seen for one bucket.
type rateSnapshot struct {
	remaining int
	limit     int
	resetAt   time.Time
}

// The rate-limit buckets the transport tracks by name. Any other
// x-ratelimit-resource value is tracked under its own name too.
const (
	bucketCore    = "core"
	bucketGraphQL = "graphql"
	bucketSearch  = "search"
)

// bucketFor names the bucket req will spend.
func bucketFor(req *http.Request) string {
	switch path := req.URL.Path; {
	case strings.HasSuffix(path, graphQLPath):
		return bucketGraphQL
	case strings.Contains(path, "/search/"):
		return bucketSearch
	default:
		return bucketCore
	}
}

// responseBucket names the bucket resp was metered against: GitHub's
// x-ratelimit-resource header, else the request's bucket.
func responseBucket(req *http.Request, resp *http.Response) string {
	if r := resp.Header.Get("X-RateLimit-Resource"); r != "" {
		return r
	}

	return bucketFor(req)
}

// newRateLimitTransport wraps the given transport with rate limit handling.
//
// The counting transport goes directly beneath it, so every request
// actually sent — the rate-limit retry included — is counted, and a
// request the reserve refuses is not.
func newRateLimitTransport(next http.RoundTripper, logger *slog.Logger, threshold float64) *rateLimitTransport {
	return &rateLimitTransport{
		next:      &countingTransport{next: next},
		logger:    logger,
		threshold: threshold,
		sleep:     sleepWithContext,
		snapshots: make(map[string]rateSnapshot),
	}
}

// RoundTrip executes an HTTP request with rate limit awareness.
func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	bucket := bucketFor(req)

	if thr := t.shouldThrottle(bucket); thr != nil {
		t.logger.Warn("pre-emptive rate limit throttle; deferring for queue retry",
			"bucket", bucket,
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

	t.updateFromResponse(req, resp)

	if bucket == bucketGraphQL {
		if thr := t.graphQLThrottle(resp); thr != nil {
			_ = resp.Body.Close()

			t.logger.Warn("github graphql rate limited; deferring for queue retry", "reset_at", thr.ResetAt)

			return nil, thr
		}
	}

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

		// Wrapping a ThrottledError makes AsThrottled defer the work until
		// the server's retry time instead of retrying it as a failure.
		return nil, fmt.Errorf(
			"github rate limited (%s): retry delay %s exceeds sleep cap %s; failing fast so the queue can retry: %w",
			reason, delay.Round(time.Second), maxRateLimitSleep, &ThrottledError{ResetAt: time.Now().Add(delay)})
	}

	t.logger.Warn("github api rate limited, waiting to retry",
		"reason", reason,
		"delay", delay,
		"status", resp.StatusCode,
	)

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

	t.updateFromResponse(req, retryResp)

	return retryResp, nil
}

// shouldThrottle returns a ThrottledError when bucket's remaining
// budget is at or below the configured threshold and its reset is
// still ahead, nil otherwise. It never sleeps — deferring the work
// until the reset is the queue's job, not the transport's (DESIGN-0021
// Phase 3, INV-0012 finding I).
func (t *rateLimitTransport) shouldThrottle(bucket string) *ThrottledError {
	t.mu.Lock()
	snap := t.snapshots[bucket]
	t.mu.Unlock()

	// Skip on first request (no rate limit data yet).
	if snap.limit == 0 {
		return nil
	}

	if snap.remaining > int(float64(snap.limit)*t.threshold) {
		return nil
	}

	// Reset already elapsed — the next response repopulates the
	// snapshot with the fresh window.
	if time.Until(snap.resetAt) <= 0 {
		return nil
	}

	return &ThrottledError{ResetAt: snap.resetAt, Remaining: snap.remaining, Limit: snap.limit}
}

// updateFromResponse records resp's rate headers under the bucket it
// was metered against.
func (t *rateLimitTransport) updateFromResponse(req *http.Request, resp *http.Response) {
	if resp == nil {
		return
	}

	obs, ok := parseRateHeaders(resp)
	if !ok {
		return
	}

	bucket := responseBucket(req, resp)

	t.mu.Lock()
	t.snapshots[bucket] = rateSnapshot{remaining: obs.Remaining, limit: obs.Limit, resetAt: obs.ResetAt}
	t.mu.Unlock()

	t.logger.Debug("github api rate limit",
		"bucket", bucket,
		"remaining", obs.Remaining,
		"limit", obs.Limit,
		"reset", obs.ResetAt,
	)
}

// isRateLimited returns true if the response indicates a rate limit
// error: any 429, or a 403 carrying exhausted rate headers or
// Retry-After.
func (*rateLimitTransport) isRateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}

	return resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "")
}

// maxGraphQLPeek bounds how much of a GraphQL body the transport reads
// to classify it. Rate-limit errors are short; a larger body is data.
const maxGraphQLPeek = 64 << 10

// graphQLError is one entry of a GraphQL response's errors array.
type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// decodeGraphQLErrors returns body's errors array, or nil when body is
// not a GraphQL JSON response.
func decodeGraphQLErrors(body []byte) []graphQLError {
	var env struct {
		Errors []graphQLError `json:"errors"`
	}

	if err := json.Unmarshal(body, &env); err != nil {
		return nil
	}

	return env.Errors
}

// graphQLThrottle classifies a GraphQL response from its body. GitHub
// reports a primary GraphQL limit as a 200 with errors[].type
// RATE_LIMITED and a secondary limit as a 200 or 403 whose message
// names it; both become a *ThrottledError so AsThrottled stays the only
// detector. The body is restored for the caller whatever the outcome.
func (t *rateLimitTransport) graphQLThrottle(resp *http.Response) *ThrottledError {
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusForbidden {
		return nil
	}

	peek, err := io.ReadAll(io.LimitReader(resp.Body, maxGraphQLPeek))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(peek), resp.Body), resp.Body}

	// An unreadable or non-JSON body is not a rate-limit response; the
	// caller meets the same read error, or the data, itself.
	var errs []graphQLError
	if err == nil {
		errs = decodeGraphQLErrors(peek)
	}

	recordGraphQLErrors(resp.Request, errs)

	for _, e := range errs {
		if e.Type == "RATE_LIMITED" || strings.Contains(strings.ToLower(e.Message), "secondary rate limit") {
			obs, _ := parseRateHeaders(resp)

			return &ThrottledError{
				ResetAt:   time.Now().Add(t.rateLimitDelay(resp)),
				Remaining: obs.Remaining,
				Limit:     obs.Limit,
			}
		}
	}

	return nil
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

// GraphQLErrorTypes collects the errors[].type values of the GraphQL
// responses sent under one context. The GraphQL client keeps only an
// error's message, but the writer must tell STALE_DATA from any other
// failure, so the transport, which already reads the body, records
// the types here. Safe for concurrent use.
type GraphQLErrorTypes struct {
	mu    sync.Mutex
	types []string
}

type graphQLErrorTypesKey struct{}

// WithGraphQLErrorTypes returns a context under which the transport
// records every GraphQL error type into the returned collector.
func WithGraphQLErrorTypes(ctx context.Context) (context.Context, *GraphQLErrorTypes) {
	g := &GraphQLErrorTypes{}

	return context.WithValue(ctx, graphQLErrorTypesKey{}, g), g
}

// Has reports whether any recorded GraphQL error carried typ.
func (g *GraphQLErrorTypes) Has(typ string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	return slices.Contains(g.types, typ)
}

// recordGraphQLErrors adds errs' types to req's collector, if any.
func recordGraphQLErrors(req *http.Request, errs []graphQLError) {
	if req == nil || len(errs) == 0 {
		return
	}

	g, ok := req.Context().Value(graphQLErrorTypesKey{}).(*GraphQLErrorTypes)
	if !ok {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, e := range errs {
		if e.Type != "" {
			g.types = append(g.types, e.Type)
		}
	}
}
