# GitHub API Rate Limit Handling

## Context

The app currently logs rate limit info from every GitHub API response (`logRateLimit` in `client.go`) but takes no action. Workers process jobs with zero delay between API calls. On startup, the scheduler enqueues all repos and workers hammer the API — burning ~350 calls/minute during initial reconciliation. There is no backoff, no pre-emptive throttling, and no retry on rate limit errors.

## Approach

Add an `http.RoundTripper` middleware that wraps the `ghinstallation` transport. This is transparent to all 11 client methods and requires no changes to the `Client` interface. Each installation gets its own transport instance (correct — GitHub rate limits are per-installation).

**Transport chain:** `go-github` -> `rateLimitTransport` (NEW) -> `ghinstallation` -> `http.DefaultTransport`

The transport does three things:
1. **Pre-emptive throttle**: When `remaining < 10%` of limit, spread remaining budget evenly until reset
2. **Primary rate limit retry**: On 403 with `X-RateLimit-Remaining: 0`, wait until reset + retry once
3. **Secondary rate limit retry**: On 403 with `Retry-After` header, wait that duration + retry once

## Files to Change

| Action | File |
|--------|------|
| Create | `internal/github/ratelimit.go` |
| Create | `internal/github/ratelimit_test.go` |
| Modify | `internal/github/client.go` |
| Modify | `internal/config/config.go` |
| Modify | `internal/metrics/metrics.go` |
| Modify | `cmd/repo-guardian/main.go` |

## Implementation Steps

### Step 1: Add metrics (`internal/metrics/metrics.go`)

Add two new metrics:
- `repo_guardian_github_rate_limit_waits_total` (counter, label: `reason` — `preemptive`, `primary`, `secondary`)
- `repo_guardian_github_rate_limit_wait_seconds` (histogram)

### Step 2: Add config (`internal/config/config.go`)

Add `RateLimitThreshold float64` field (default `0.10`). Add `envOrDefaultFloat` helper following the existing `envOrDefaultInt` pattern. Env var: `RATE_LIMIT_THRESHOLD`.

### Step 3: Create transport (`internal/github/ratelimit.go`)

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

`RoundTrip` flow:
1. `waitIfNeeded(ctx)` — if `remaining < limit * threshold`, sleep to spread budget until reset. If `remaining == 0`, sleep until reset.
2. `next.RoundTrip(req)` — make the actual request.
3. `updateFromResponse(resp)` — parse `X-RateLimit-*` headers, update state + Prometheus gauge.
4. If rate limited (403): compute delay from `X-RateLimit-Reset` or `Retry-After`, log at WARN, sleep (respecting ctx), retry once.

All sleeps use `select` with `ctx.Done()` for cancellation. Max 1 retry. Request body replayed via `req.GetBody()` on retry.

Edge cases:
- First request (`limit == 0`): skip pre-emptive check
- Negative wait (clock skew): floor at 1s
- Concurrent access: mutex protects state, released before sleeping

### Step 4: Wire into client (`internal/github/client.go`)

- In `NewClient`: wrap `ghinstallation.AppsTransport` with `rateLimitTransport`, pass threshold
- In `getInstallClient`: wrap installation transport with its own `rateLimitTransport`
- Add `rateLimitThreshold float64` parameter to `NewClient`
- Remove `logRateLimit` method and all 11 call sites (transport now handles this)

### Step 5: Update main (`cmd/repo-guardian/main.go`)

Pass `cfg.RateLimitThreshold` to `ghclient.NewClient`.

### Step 6: Tests (`internal/github/ratelimit_test.go`)

Using `httptest.Server` (same pattern as existing `client_test.go`):

1. **Normal request** — plenty of budget, no delay
2. **Pre-emptive throttle** — low remaining, verify delay between requests
3. **Primary rate limit** — 403 on first call, 200 on retry
4. **Secondary rate limit** — 403 with `Retry-After`, 200 on retry
5. **Context cancellation** — cancel during wait, verify immediate return
6. **Retry exhausted** — 403 on both calls, verify error returned

## Implementation Phases

### Phase 1: Infrastructure (Metrics + Config)

**Steps 1–2** from above: add the two new Prometheus metrics and the `RateLimitThreshold` config field.

**What changes:**
- `internal/metrics/metrics.go` — new counter and histogram
- `internal/config/config.go` — new `RateLimitThreshold` field, `envOrDefaultFloat` helper, env var `RATE_LIMIT_THRESHOLD`

**Success criteria:**
- `make test` passes — existing tests still green, config tests cover the new field (default value, env override, invalid value)
- `make lint` passes with no new violations
- App starts and `/metrics` endpoint includes the two new metrics (both at zero)

---

### Phase 2: Core Transport

**Step 3**: create `rateLimitTransport` implementing `http.RoundTripper` with pre-emptive throttling, primary rate limit retry, and secondary rate limit retry.

**What changes:**
- `internal/github/ratelimit.go` — new file with the transport struct and `RoundTrip` method

**Success criteria:**
- All 6 test cases in `internal/github/ratelimit_test.go` pass:
  1. Normal request — no delay, response returned as-is
  2. Pre-emptive throttle — measurable delay when remaining is below threshold
  3. Primary rate limit (403 + `X-RateLimit-Remaining: 0`) — waits, retries once, returns 200
  4. Secondary rate limit (403 + `Retry-After`) — waits specified duration, retries once, returns 200
  5. Context cancellation — returns `context.Canceled` immediately, does not block
  6. Retry exhausted (403 on both attempts) — returns the 403 response, no infinite loop
- `make lint` passes — no linter violations on the new file
- Transport is self-contained: no dependency on `client.go` internals

---

### Phase 3: Integration (Wire + Cleanup)

**Steps 4–5**: wrap `ghinstallation` transports with `rateLimitTransport` in the client constructor, pass config from `main.go`, and remove the old `logRateLimit` method and its 11 call sites.

**What changes:**
- `internal/github/client.go` — `NewClient` and `getInstallClient` wrap transports; `logRateLimit` removed; `rateLimitThreshold` parameter added
- `cmd/repo-guardian/main.go` — passes `cfg.RateLimitThreshold` to `NewClient`

**Success criteria:**
- `make test` passes — all existing client tests still green (they use mock clients that don't go through the transport)
- `make lint` passes
- Zero references to `logRateLimit` remain in the codebase
- `NewClient` signature includes the threshold parameter and `main.go` supplies it

---

### Phase 4: End-to-End Validation

Manual verification against the live GitHub API using the local Docker Compose setup.

**What to do:**
1. `make compose-up` with `DRY_RUN=true`
2. Let the scheduler reconcile all repos (initial burst)
3. Tail logs for WARN-level rate limit messages
4. Curl `/metrics` and confirm `rate_limit_waits_total` and `rate_limit_wait_seconds` have non-zero values if throttling occurred
5. Confirm the app completes reconciliation without 403 errors

**Success criteria:**
- App does not return any unhandled 403 rate limit errors during reconciliation
- If rate limit budget is low, WARN logs show pre-emptive throttling kicking in with wait durations
- Prometheus metrics reflect actual wait counts and durations
- No regressions — PRs are still created correctly in dry-run mode

## Verification

1. `make test` — all existing + new tests pass
2. `make lint` — no linter violations
3. `make compose-up` — start app, observe WARN logs when throttling kicks in instead of burning through API calls
4. Check `/metrics` endpoint for new `rate_limit_waits_total` and `rate_limit_wait_seconds` metrics
