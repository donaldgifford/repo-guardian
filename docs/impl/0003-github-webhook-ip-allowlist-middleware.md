---
id: IMPL-0003
title: "GitHub Webhook IP Allowlist Middleware"
status: Draft
author: Donald Gifford
created: 2026-03-14
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0003: GitHub Webhook IP Allowlist Middleware

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-14

## Objective

Implement an HTTP middleware that rejects webhook requests from IP addresses
outside GitHub's published webhook CIDR ranges. Provides defense-in-depth
on top of existing HMAC signature validation.

**Implements:** [DESIGN-0004: GitHub Webhook IP Allowlist Middleware](../design/0004-github-webhook-ip-allowlist-middleware.md)

## Scope

### In Scope

- Fetch and cache GitHub webhook IP ranges from `/meta` API
- HTTP middleware that checks source IP against cached ranges
- Fail-closed by default, configurable fail-open
- `X-Forwarded-For` support for proxy deployments
- Configuration via environment variables
- Prometheus metric for rejected requests
- Determine how Tailscale Funnel forwards client IPs

### Out of Scope

- Blocking non-webhook routes (health, metrics stay open)
- Replacing HMAC validation (both layers coexist)
- Supporting non-GitHub webhook sources

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met.

---

### Phase 1: Configuration

Add three new environment variables to the config package.

#### Tasks

- [x] **`internal/config/config.go`**:
  - [x] Add `WebhookIPAllowlist bool` field
    - Env var: `WEBHOOK_IP_ALLOWLIST`, default: `true`
  - [x] Add `WebhookIPAllowlistFailOpen bool` field
    - Env var: `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN`, default: `false`
  - [x] Add `TrustProxyHeaders bool` field
    - Env var: `TRUST_PROXY_HEADERS`, default: `false`
  - [x] Load all three in `Load()` using existing `envOrDefaultBool` helper
- [x] **`internal/config/config_test.go`**:
  - [x] Test default values: allowlist=true, fail-open=false, trust-proxy=false
  - [x] Test env var overrides for each field
  - [x] Test invalid boolean values return parse errors

#### Success Criteria

- `go test -v -race ./internal/config/...` passes with new test cases
- All three fields default correctly when env vars are unset
- `make check` passes

---

### Phase 2: Metrics

Add a Prometheus counter for rejected webhook requests.

#### Tasks

- [x] **`internal/metrics/metrics.go`**:
  - [x] Add `WebhookRejectedTotal` counter vec with `reason` label
    - Name: `repo_guardian_webhook_rejected_total`
    - Help: `"Webhook requests rejected by IP allowlist."`
    - Label values: `"ip_not_allowed"`, `"allowlist_unavailable"`

#### Success Criteria

- `make build` compiles without errors
- `make lint` passes
- Metric follows `repo_guardian_` naming convention

---

### Phase 3: Core Allowlist

Implement the `GitHubIPAllowlist` struct with IP range fetching, caching, and
IP matching. This phase does not include the HTTP middleware.

#### Tasks

- [x] **`internal/webhook/allowlist.go`** (new file):
  - [x] Define `GitHubIPAllowlist` struct with fields:
    - `mu sync.RWMutex` for thread-safe access
    - `networks []*net.IPNet` for parsed CIDR ranges
    - `loaded bool` to track whether initial fetch succeeded
    - `failOpen bool` for fail-open/fail-closed behavior
    - `trustProxy bool` for `X-Forwarded-For` support
    - `logger *slog.Logger`
    - `metaURL string` (defaults to `https://api.github.com/meta`, overridable
      for tests)
  - [x] Define `metaResponse` struct for JSON unmarshaling:
    - `Hooks []string \`json:"hooks"\``
  - [x] Constructor: `NewGitHubIPAllowlist(failOpen, trustProxy bool,
    logger *slog.Logger) *GitHubIPAllowlist`
  - [x] `fetchRanges(ctx context.Context) ([]*net.IPNet, error)`:
    - HTTP GET to `metaURL` with 10s timeout
    - Parse JSON, extract `hooks` field
    - Parse each CIDR via `net.ParseCIDR`, skip invalid with warning log
    - Return parsed networks
  - [x] `Refresh(ctx context.Context) error`:
    - Call `fetchRanges`
    - On success: write-lock, update `networks`, set `loaded = true`
    - On failure: log error, keep previous ranges intact
  - [x] `StartRefresh(ctx context.Context)`:
    - Call `Refresh` once synchronously (startup fetch)
    - Launch background goroutine with 24h ticker for periodic refresh
    - Respect `ctx.Done()` for shutdown
  - [x] `IsAllowed(ip net.IP) bool`:
    - Read-lock
    - If not loaded: return `failOpen` value
    - Iterate networks, return `true` on match
    - Return `false` if no match
  - [x] `extractIP(r *http.Request) net.IP`:
    - If `trustProxy` and `X-Forwarded-For` present: parse leftmost IP
    - Otherwise: parse from `r.RemoteAddr` via `net.SplitHostPort`
    - Return `nil` on parse failure

#### Success Criteria

- `make build` compiles without errors
- `make lint` passes
- `IsAllowed` correctly matches IPs against CIDR ranges
- `extractIP` handles both direct and proxied scenarios
- Thread-safe: read lock for checks, write lock for refresh

---

### Phase 4: HTTP Middleware

Add the `Middleware` method that wraps an `http.Handler`.

#### Tasks

- [x] **`internal/webhook/allowlist.go`** (add to existing file):
  - [x] `Middleware(next http.Handler) http.Handler`:
    - Extract IP via `extractIP(r)`
    - If IP is nil: log warning, increment
      `WebhookRejectedTotal("ip_not_allowed")`, return 403
    - If `!IsAllowed(ip)`:
      - Choose label: `"allowlist_unavailable"` if not loaded, else
        `"ip_not_allowed"`
      - Increment `WebhookRejectedTotal` with label
      - Log warning with `remote_ip` and `path`
      - Return 403 `"forbidden"`
    - Otherwise: call `next.ServeHTTP(w, r)`

#### Success Criteria

- Middleware returns 403 for non-GitHub IPs
- Middleware passes through for GitHub IPs
- Correct metric labels used for each rejection reason
- Health/metrics endpoints not affected (middleware only wraps webhook route)

---

### Phase 5: Wiring

Wire the middleware into `main.go` on the webhook route only.

#### Tasks

- [ ] **`cmd/repo-guardian/main.go`**:
  - [ ] If `cfg.WebhookIPAllowlist`:
    - Create `webhook.NewGitHubIPAllowlist(cfg.WebhookIPAllowlistFailOpen,
      cfg.TrustProxyHeaders, logger)`
    - Call `allowlist.StartRefresh(ctx)`
    - Wrap: `webhookHandler = allowlist.Middleware(webhookHandler)` before
      passing to `newMainServer`
    - Log: `"webhook IP allowlist enabled"` with `fail_open` and `trust_proxy`
  - [ ] If `!cfg.WebhookIPAllowlist`:
    - Log: `"webhook IP allowlist disabled"`
    - Pass unwrapped `webhookHandler` to `newMainServer`

#### Success Criteria

- `make build` compiles
- App starts with middleware enabled by default
- App starts without middleware when `WEBHOOK_IP_ALLOWLIST=false`
- Startup logs show allowlist configuration
- `make check` passes

---

### Phase 6: Tests

Comprehensive test coverage for the allowlist.

#### Tasks

- [ ] **`internal/webhook/allowlist_test.go`** (new file):
  - [ ] `TestIsAllowed_GitHubIP` -- IP within range returns true
  - [ ] `TestIsAllowed_NonGitHubIP` -- IP outside ranges returns false
  - [ ] `TestIsAllowed_IPv6` -- IPv6 CIDR matching works
  - [ ] `TestIsAllowed_NotLoaded_FailClosed` -- returns false when not loaded,
    failOpen=false
  - [ ] `TestIsAllowed_NotLoaded_FailOpen` -- returns true when not loaded,
    failOpen=true
  - [ ] `TestRefresh_Success` -- mock `/meta`, verify ranges loaded
  - [ ] `TestRefresh_Failure_KeepsPrevious` -- failed refresh keeps old ranges
  - [ ] `TestRefresh_InvalidCIDR_Skipped` -- invalid CIDRs logged and skipped
  - [ ] `TestExtractIP_RemoteAddr` -- extracts IP from RemoteAddr
  - [ ] `TestExtractIP_XForwardedFor` -- extracts leftmost IP when
    trustProxy=true
  - [ ] `TestExtractIP_XForwardedFor_Ignored` -- uses RemoteAddr when
    trustProxy=false
  - [ ] `TestMiddleware_AllowedIP` -- 200 response for GitHub IP
  - [ ] `TestMiddleware_BlockedIP` -- 403 response for non-GitHub IP
  - [ ] `TestMiddleware_NotLoaded_FailClosed` -- 403 when ranges not loaded

#### Success Criteria

- `go test -v -race ./internal/webhook/...` passes all test cases
- Coverage includes fail-open and fail-closed paths
- Coverage includes trustProxy true and false paths
- No flaky tests (httptest.Server mocks, no timing dependencies)
- `make check` passes

---

### Phase 7: Tailscale Funnel IP Investigation

Determine how Tailscale Funnel forwards the original client IP. Resolves
[DESIGN-0004 Open Question 1](../design/0004-github-webhook-ip-allowlist-middleware.md).

#### Tasks

- [ ] Deploy to Talos cluster with middleware enabled and
  `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN=true`
- [ ] Add temporary debug logging in middleware to dump `RemoteAddr` and all
  request headers
- [ ] Trigger a GitHub webhook and inspect the logs
- [ ] Determine correct `TRUST_PROXY_HEADERS` setting for Tailscale overlay
- [ ] Update Tailscale overlay `deployment-patch.yaml` with the finding
- [ ] Update DESIGN-0004 to close Open Question 1

#### Success Criteria

- We know how Funnel forwards client IPs
- Tailscale overlay is configured correctly
- DESIGN-0004 Open Question 1 is answered

---

### Phase 8: Documentation

Update documentation and observability assets.

#### Tasks

- [ ] **`CLAUDE.md`**: add `allowlist.go` to webhook package description,
  update metric count
- [ ] **Grafana dashboard** (`contrib/grafana/repo-guardian-dashboard.json`):
  add "Webhook Rejected" stat panel
- [ ] **DESIGN-0004**: update status from `Draft` to `Implemented`
- [ ] **This doc (IMPL-0003)**: update status to `Completed`

#### Success Criteria

- Documentation reflects the implemented feature
- `make check` passes

---

## File Changes

| File | Action | Phase | Description |
|------|--------|-------|-------------|
| `internal/config/config.go` | Modify | 1 | Add 3 new config fields |
| `internal/config/config_test.go` | Modify | 1 | Add config tests |
| `internal/metrics/metrics.go` | Modify | 2 | Add rejected counter |
| `internal/webhook/allowlist.go` | Create | 3-4 | Core allowlist + middleware |
| `cmd/repo-guardian/main.go` | Modify | 5 | Wire middleware |
| `internal/webhook/allowlist_test.go` | Create | 6 | Test coverage |

## Open Questions

1. **`X-Forwarded-For` parsing -- leftmost vs rightmost**: The leftmost IP
   in `X-Forwarded-For` is the original client but can be spoofed. Behind a
   single trusted proxy (Tailscale Funnel), leftmost is correct. For EKS/ALB
   with multiple proxy hops, the rightmost-before-ALB entry is more
   trustworthy. **Decision for now**: implement leftmost, document the
   assumption. Revisit for EKS production if needed.

2. **HTTP client for `/meta` fetch**: Use a plain `http.Client` with 10s
   timeout. The `/meta` endpoint is unauthenticated and rate-limited separately
   from the GitHub App API. No need to couple to the GitHub App auth flow.
   Fetches happen once every 24h.

3. **Refresh interval configurability**: Hardcode 24h. GitHub's IP ranges
   change very rarely. Can be made configurable later if needed.

## References

- [DESIGN-0004: GitHub Webhook IP Allowlist Middleware](../design/0004-github-webhook-ip-allowlist-middleware.md)
- [INV-0001: Tailscale Funnel for Webhook Testing](../investigation/0001-tailscale-funnel-for-webhook-testing.md)
- [GitHub Meta API](https://api.github.com/meta)
