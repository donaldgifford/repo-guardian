# Security

This document describes the security mechanisms implemented in repo-guardian
to protect the webhook endpoint and ensure only legitimate GitHub traffic is
processed.

## Defense-in-Depth

repo-guardian uses two independent layers of protection on the webhook endpoint.
Both layers must pass before any payload processing occurs:

```
Incoming request
      |
      v
[IP Allowlist Middleware]  -- 403 if source IP not in GitHub ranges
      |
      v
[HMAC Signature Validation]  -- 401 if signature invalid
      |
      v
[Event Processing]
```

## Layer 1: GitHub Webhook IP Allowlist

An HTTP middleware rejects requests from IP addresses outside GitHub's published
webhook CIDR ranges. This drops non-GitHub traffic before it reaches application
code, reducing attack surface.

### How It Works

1. On startup, the middleware fetches GitHub's webhook IP ranges from the
   [`/meta` API](https://api.github.com/meta) (`hooks` field).
2. The CIDR ranges are parsed and cached in memory.
3. A background goroutine refreshes the ranges every 24 hours.
4. Each incoming webhook request's source IP is checked against the cached
   ranges. Requests from IPs outside the ranges receive a `403 Forbidden`.

### Fail-Closed by Default

If the initial `/meta` fetch fails (e.g., network issue at startup), the
middleware **rejects all traffic** until a successful refresh occurs. This
prevents an attacker from bypassing the allowlist by disrupting the fetch.

This behavior is configurable:

| Environment Variable | Default | Description |
|---|---|---|
| `WEBHOOK_IP_ALLOWLIST` | `true` | Enable the IP allowlist middleware |
| `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN` | `false` | Allow all traffic when ranges are unavailable |
| `TRUST_PROXY_HEADERS` | `false` | Read client IP from `X-Forwarded-For` header |

### Proxy Deployments

When deployed behind a reverse proxy (e.g., Tailscale Funnel, ALB), the
middleware reads the client IP from the `X-Forwarded-For` header (leftmost
entry) when `TRUST_PROXY_HEADERS=true`. When `false`, it uses `RemoteAddr`
directly.

Only enable `TRUST_PROXY_HEADERS` when the proxy is trusted. An untrusted
client can spoof `X-Forwarded-For` to bypass the allowlist if this is enabled
without a trusted proxy in front.

### Observability

Rejected requests are tracked via the `repo_guardian_webhook_rejected_total`
Prometheus counter with a `reason` label:

- `ip_not_allowed` -- source IP is not in GitHub's CIDR ranges
- `allowlist_unavailable` -- ranges have not been loaded (fail-closed)

## Layer 2: HMAC Signature Validation

Every webhook request from GitHub includes an `X-Hub-Signature-256` header
containing an HMAC-SHA256 signature of the request body, computed using the
shared webhook secret.

### How It Works

1. When the GitHub App is configured, a webhook secret is set. Both GitHub and
   repo-guardian share this secret.
2. GitHub computes `HMAC-SHA256(webhook_secret, request_body)` and sends the
   result in the `X-Hub-Signature-256` header as `sha256=<hex_digest>`.
3. repo-guardian recomputes the HMAC using the same secret and the received
   request body.
4. If the signatures do not match, the request is rejected with
   `401 Unauthorized`.

This ensures:

- **Authenticity** -- only GitHub (which knows the secret) can produce a valid
  signature.
- **Integrity** -- any modification to the payload in transit invalidates the
  signature.

The validation is performed by the
[`go-github`](https://github.com/google/go-github) library's
`github.ValidatePayload()` function, which handles constant-time comparison
to prevent timing attacks.

### Secret Management

The webhook secret is provided via the `GITHUB_WEBHOOK_SECRET` environment
variable. In Kubernetes, this is stored in a Secret resource and injected
into the pod.

## Additional Security Measures

### Minimal Container Image

The production Docker image uses Google's `distroless` base image, which
contains no shell, package manager, or unnecessary binaries. This minimizes
the attack surface if the container is compromised.

### Read-Only Filesystem

The deployment is configured with `readOnlyRootFilesystem: true` and runs as
a non-root user (`runAsNonRoot: true`), following the principle of least
privilege.

### GitHub App Private Key

The GitHub App private key (used to authenticate as the App and generate
installation tokens) is mounted as a read-only file from a Kubernetes Secret,
or injected via environment variable. The key is never logged or exposed in
API responses.

### Rate Limiting

repo-guardian monitors the GitHub API rate limit and implements pre-emptive
throttling when the remaining budget drops below a configurable threshold
(default: 10%). This prevents the app from exhausting its rate limit and
ensures graceful degradation.

## Reporting Vulnerabilities

If you discover a security vulnerability, please report it by opening a
private issue or contacting the maintainer directly. Do not open a public
issue for security vulnerabilities.
