---
id: DESIGN-0004
title: "GitHub Webhook IP Allowlist Middleware"
status: Superseded
author: Donald Gifford
created: 2026-03-14
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0004: GitHub Webhook IP Allowlist Middleware

> **Superseded by [DESIGN-0023](0023-operator-owned-ingress-remove-the-tailscale-sidecar-and-ip.md)**
> (IMPL-0024, appVersion `1.14.0`): the middleware this designed was
> removed — [INV-0016](../investigation/0016-retire-the-baked-tailscale-sidecar-for-operator-managed-ingress.md)
> Observation 5 found it spoofable behind every documented proxy
> (leftmost `X-Forwarded-For` is client-controlled). HMAC is the
> app-layer boundary; source-IP enforcement lives at the operator's
> edge per [docs/operations/ingress.md](../operations/ingress.md).
> Kept as a historical record.

**Status:** Superseded
**Author:** Donald Gifford
**Date:** 2026-03-14

## Overview

Add an HTTP middleware to the webhook handler that rejects requests from IP
addresses outside GitHub's published webhook IP ranges. This provides
defense-in-depth on top of the existing HMAC signature validation --
non-GitHub traffic is rejected before any payload processing occurs.

## Goals and Non-Goals

### Goals

- Reject webhook requests from non-GitHub source IPs with a 403 response.
- Fetch GitHub's webhook IP ranges from the `/meta` API endpoint.
- Cache the IP ranges and refresh periodically (they change infrequently).
- Handle `X-Forwarded-For` / proxy headers correctly for deployments behind
  a load balancer or Tailscale Funnel.
- Make the middleware optional via configuration so it can be disabled in
  environments where IP filtering is not needed or handled externally.

### Non-Goals

- Replace HMAC signature validation -- both layers work together.
- Block non-webhook routes (health checks, metrics should remain open).
- Support non-GitHub webhook sources.

## Background

[INV-0001: Tailscale Funnel for Webhook Testing](../investigation/0001-tailscale-funnel-for-webhook-testing.md)
identified that Tailscale Funnel exposes the webhook endpoint to the public
internet with no IP-level filtering. While HMAC validation rejects
unauthorized payloads, adding IP allowlisting reduces the attack surface by
dropping non-GitHub traffic before it reaches application code.

GitHub publishes its webhook source IP ranges at `https://api.github.com/meta`
in the `hooks` field. These are CIDR blocks that can be used for allowlisting.

## Detailed Design

### Middleware Architecture

```
Incoming request
      |
      v
[IP Allowlist Middleware]  -- 403 if source IP not in GitHub ranges
      |
      v
[Webhook Handler]          -- 401 if HMAC signature invalid
      |
      v
[Event Processing]
```

The middleware wraps only the webhook route (`/webhooks/github`). Health and
metrics endpoints are not affected.

### GitHub Meta API

```
GET https://api.github.com/meta
```

Response (relevant fields):

```json
{
  "hooks": [
    "192.30.252.0/22",
    "185.199.108.0/22",
    "140.82.112.0/20",
    "143.55.64.0/20",
    ...
  ]
}
```

### IP Range Cache

```go
type GitHubIPAllowlist struct {
    mu       sync.RWMutex
    networks []*net.IPNet
    logger   *slog.Logger
}
```

- **Initialization**: Fetch from `/meta` on startup. If the fetch fails, log
  a warning and allow all traffic (fail-open) until the first successful
  refresh.
- **Refresh**: Background goroutine refreshes every 24 hours. GitHub's IP
  ranges change infrequently.
- **Thread safety**: `sync.RWMutex` protects the network list. Reads are
  concurrent; writes happen only during refresh.

### Source IP Extraction

The middleware must handle multiple deployment scenarios:

1. **Direct connection** (e.g., Tailscale Funnel): Use `r.RemoteAddr`.
2. **Behind a load balancer** (e.g., ALB in EKS): Use `X-Forwarded-For`
   header, taking the last trusted entry.

Configuration option: `TRUST_PROXY_HEADERS` (default: `false`). When `true`,
the middleware reads `X-Forwarded-For`. When `false`, it uses `r.RemoteAddr`
directly.

For Tailscale Funnel specifically, the `RemoteAddr` will be the Funnel proxy
IP (localhost/pod IP), not GitHub's IP. The actual client IP may be in
`X-Forwarded-For` or `Tailscale-Forwarded-For`. This needs to be verified
during implementation.

### Middleware Function

```go
func (a *GitHubIPAllowlist) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ip := a.extractIP(r)
        if !a.IsAllowed(ip) {
            a.logger.Warn("rejected request from non-GitHub IP",
                "remote_ip", ip,
                "path", r.URL.Path,
            )
            http.Error(w, "forbidden", http.StatusForbidden)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

### Configuration

| Variable | Default | Description |
|---|---|---|
| `WEBHOOK_IP_ALLOWLIST` | `true` | Enable GitHub IP allowlist middleware |
| `TRUST_PROXY_HEADERS` | `false` | Read client IP from `X-Forwarded-For` |

## API / Interface Changes

No changes to the `Client` interface or public API. The middleware is wired
in `main.go` when setting up the webhook route.

## Files to Change

| Action | File | Description |
|--------|------|-------------|
| Create | `internal/webhook/allowlist.go` | `GitHubIPAllowlist` struct, meta fetch, middleware |
| Create | `internal/webhook/allowlist_test.go` | Unit tests |
| Modify | `internal/config/config.go` | Add `WebhookIPAllowlist` and `TrustProxyHeaders` fields |
| Modify | `cmd/repo-guardian/main.go` | Wire middleware on webhook route |
| Modify | `internal/metrics/metrics.go` | Add `webhook_rejected_total` counter (label: reason) |

## Testing Strategy

### Unit tests (`allowlist_test.go`)

- **Allowed IP**: Request from a GitHub CIDR range -- passes through.
- **Blocked IP**: Request from a non-GitHub IP -- returns 403.
- **IPv6 support**: GitHub CIDRs include IPv6 ranges.
- **X-Forwarded-For**: With `TrustProxyHeaders=true`, reads from header.
- **Cache refresh**: Mock `/meta` endpoint, verify refresh updates ranges.
- **Fetch failure**: `/meta` unreachable -- fails open, logs warning.
- **Disabled**: `WebhookIPAllowlist=false` -- all requests pass through.

### Integration test

- Deploy to Talos cluster with middleware enabled.
- Verify GitHub webhooks still pass through.
- `curl` the webhook endpoint directly -- expect 403.

## Open Questions

*All resolved.*

1. ~~**Tailscale Funnel and source IP**~~: **Resolved.** Funnel sets
   `RemoteAddr` to `127.0.0.1` (sidecar localhost proxy) and forwards the
   original client IP in the `X-Forwarded-For` header. It also sets
   `Tailscale-Funnel-Request: ?1` to indicate traffic arrived via Funnel.
   The Tailscale overlay requires `TRUST_PROXY_HEADERS=true`.

## Decisions

1. **Fail-closed by default**: If the `/meta` fetch fails on startup, the
   middleware rejects all webhook traffic until a successful refresh. This is
   the more secure default. Configurable via `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN`
   (default: `false`) for environments where availability is preferred over
   strict security.

## References

- [INV-0001: Tailscale Funnel for Webhook Testing](../investigation/0001-tailscale-funnel-for-webhook-testing.md)
- [GitHub Meta API](https://api.github.com/meta)
- [GitHub Webhook IP Ranges](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/about-githubs-ip-addresses)
- [RFC-0001: Repo Compliance App](../rfc/0001-repo-compliance-app-repo-guardian.md)
