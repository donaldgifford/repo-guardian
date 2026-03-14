---
id: DESIGN-0002
title: "GitHub API Rate Limit Handling"
status: Approved
author: Donald Gifford
created: 2026-03-01
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0002: GitHub API Rate Limit Handling

**Status:** Approved
**Author:** Donald Gifford
**Date:** 2026-03-01

## Summary

Add transparent rate limit handling to the GitHub API client via an
`http.RoundTripper` middleware that wraps the `ghinstallation` transport.
Provides pre-emptive throttling, primary rate limit retry, and secondary rate
limit retry without changes to the `Client` interface.

## Problem Statement

The app currently logs rate limit info from every GitHub API response
(`logRateLimit` in `client.go`) but takes no action. Workers process jobs with
zero delay between API calls. On startup, the scheduler enqueues all repos and
workers hammer the API -- burning ~350 calls/minute during initial
reconciliation. There is no backoff, no pre-emptive throttling, and no retry on
rate limit errors.

## Design

### Transport Chain

```
go-github -> rateLimitTransport (NEW) -> ghinstallation -> http.DefaultTransport
```

Each installation gets its own transport instance (correct -- GitHub rate limits
are per-installation).

### rateLimitTransport

```go
type rateLimitTransport struct {
    next      http.RoundTripper
    logger    *slog.Logger
    threshold float64  // e.g. 0.10 = start throttling at 10% remaining

    mu        sync.Mutex
    remaining int
    limit     int
    resetAt   time.Time
}
```

The transport does three things:

1. **Pre-emptive throttle**: When `remaining < 10%` of limit, spread remaining
   budget evenly until reset.
2. **Primary rate limit retry**: On 403 with `X-RateLimit-Remaining: 0`, wait
   until reset + retry once.
3. **Secondary rate limit retry**: On 403 with `Retry-After` header, wait that
   duration + retry once.

### RoundTrip Flow

1. `waitIfNeeded(ctx)` -- if `remaining < limit * threshold`, sleep to spread
   budget until reset. If `remaining == 0`, sleep until reset.
2. `next.RoundTrip(req)` -- make the actual request.
3. `updateFromResponse(resp)` -- parse `X-RateLimit-*` headers, update state +
   Prometheus gauge.
4. If rate limited (403): compute delay from `X-RateLimit-Reset` or
   `Retry-After`, log at WARN, sleep (respecting ctx), retry once.

### Edge Cases

- First request (`limit == 0`): skip pre-emptive check.
- Negative wait (clock skew): floor at 1s.
- Concurrent access: mutex protects state, released before sleeping.
- All sleeps use `select` with `ctx.Done()` for cancellation.
- Max 1 retry. Request body replayed via `req.GetBody()` on retry.

### Configuration

| Variable | Default | Description |
|---|---|---|
| `RATE_LIMIT_THRESHOLD` | `0.10` | Start throttling at this % remaining |

### Metrics

| Metric | Type | Description |
|---|---|---|
| `repo_guardian_github_rate_limit_waits_total` | Counter | Rate limit waits (label: `reason`) |
| `repo_guardian_github_rate_limit_wait_seconds` | Histogram | Duration of rate limit waits |

### Files Changed

| Action | File |
|--------|------|
| Create | `internal/github/ratelimit.go` |
| Create | `internal/github/ratelimit_test.go` |
| Modify | `internal/github/client.go` |
| Modify | `internal/config/config.go` |
| Modify | `internal/metrics/metrics.go` |
| Modify | `cmd/repo-guardian/main.go` |

## Implementation Phases

### Phase 1: Infrastructure (Metrics + Config)

Add new Prometheus metrics and `RateLimitThreshold` config field.

### Phase 2: Core Transport

Create `rateLimitTransport` implementing `http.RoundTripper` with pre-emptive
throttling and retry logic.

Test cases:
1. Normal request -- no delay
2. Pre-emptive throttle -- low remaining, verify delay
3. Primary rate limit -- 403, wait, retry, 200
4. Secondary rate limit -- 403 with Retry-After, wait, retry, 200
5. Context cancellation -- immediate return
6. Retry exhausted -- 403 on both calls, error returned

### Phase 3: Integration (Wire + Cleanup)

Wrap `ghinstallation` transports with `rateLimitTransport`. Remove old
`logRateLimit` method and its 11 call sites.

### Phase 4: End-to-End Validation

Manual verification with `DRY_RUN=true` against live GitHub API.

## References

- [RFC-0001: Repo Compliance App](../rfc/0001-repo-compliance-app-repo-guardian.md)
