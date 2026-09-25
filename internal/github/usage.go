package github

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Usage records the GitHub requests one check sent and the last
// X-RateLimit-* headers it saw. countingTransport feeds it, so the
// numbers cost no extra API call. The v2 CheckRepo activity reports
// them to the installation's budget (DESIGN-0026 § Rate budget).
//
// Requests the transport refuses before sending are not counted; they
// never reached GitHub.
type Usage struct {
	mu    sync.Mutex
	calls int
	rate  *RateObservation
}

// RateObservation is one set of X-RateLimit-* headers.
type RateObservation struct {
	Limit      int
	Remaining  int
	ResetAt    time.Time
	ObservedAt time.Time
}

type usageKey struct{}

// WithUsage returns a context whose GitHub requests are recorded into
// the returned Usage.
func WithUsage(ctx context.Context) (context.Context, *Usage) {
	u := &Usage{}

	return context.WithValue(ctx, usageKey{}, u), u
}

func usageFrom(ctx context.Context) *Usage {
	u, _ := ctx.Value(usageKey{}).(*Usage) //nolint:errcheck // a missing recorder is the common case, not an error

	return u
}

// Calls returns the number of requests sent.
func (u *Usage) Calls() int {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.calls
}

// Rate returns the last rate-limit headers seen, or nil when no
// response carried them.
func (u *Usage) Rate() *RateObservation {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.rate == nil {
		return nil
	}

	r := *u.rate

	return &r
}

// record counts one sent request and keeps its rate headers. Nil-safe.
func (u *Usage) record(resp *http.Response) {
	if u == nil {
		return
	}

	obs, ok := parseRateHeaders(resp)

	u.mu.Lock()
	defer u.mu.Unlock()

	u.calls++

	if ok {
		obs.ObservedAt = time.Now()
		u.rate = &obs
	}
}

// parseRateHeaders reads the X-RateLimit-* headers. ok is false when any
// is missing or malformed.
func parseRateHeaders(resp *http.Response) (RateObservation, bool) {
	if resp == nil {
		return RateObservation{}, false
	}

	remaining, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
	if err != nil {
		return RateObservation{}, false
	}

	limit, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Limit"))
	if err != nil {
		return RateObservation{}, false
	}

	reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return RateObservation{}, false
	}

	return RateObservation{Limit: limit, Remaining: remaining, ResetAt: time.Unix(reset, 0)}, true
}

// countingTransport records each sent request into the request
// context's Usage. It sits inside otelhttp and inside the rate-limit
// transport (otelhttp → rate limit → counting → ghinstallation), so it
// sees exactly the requests that reach GitHub.
type countingTransport struct {
	next http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	usageFrom(req.Context()).record(resp)

	return resp, nil
}
