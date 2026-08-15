# Security

This document describes the security model for repo-guardian's webhook
endpoint: what the application enforces, and what belongs to the
operator's edge layer.

## Trust Model

The application-layer security boundary is **HMAC signature
validation**. Every delivery must carry a valid
`X-Hub-Signature-256` computed with the shared webhook secret;
anything else is rejected with `401 Unauthorized` before any payload
processing.

**Source-IP enforcement is operator-owned.** The app ships no
ingress and reads no client-IP headers. Where a source-IP layer is
wanted, it lives at the operator's edge — an ALB security group
referencing the GitHub prefix list, a Cloudflare WAF rule, an ngrok
`restrict-ips` traffic policy — enforced at the true source address,
fail-closed, before traffic reaches the pod. The options and recipes
are in [docs/operations/ingress.md](docs/operations/ingress.md).

```
Incoming request
      |
      v
[Operator's edge layer]  -- source-IP enforcement, where configured
      |                     (SG / WAF / traffic policy — outside the app)
      v
[HMAC Signature Validation]  -- 401 if signature invalid
      |
      v
[Event Processing]
```

### History: the removed in-app IP allowlist

Through appVersion 1.13.0 the binary carried an IP-allowlist
middleware (a `403` layer in front of HMAC) plus a baked Tailscale
sidecar in the chart. Both were removed in IMPL-0024 after
[INV-0016](docs/investigation/0016-retire-the-baked-tailscale-sidecar-for-operator-managed-ingress.md)
found the middleware was **spoofable behind every documented proxy
topology**: with `TRUST_PROXY_HEADERS=true` it trusted the leftmost
`X-Forwarded-For` entry, which the client controls (proxies append
the true source to the *end*), and the sidecar deployment forced it
fail-open anyway. A source-IP check that runs behind a proxy on a
client-controlled header is not a second layer — it is a false sense
of one. Do not reintroduce it as a "cheap second layer"; put the IP
layer at the edge, where the true source address is available and
the check can be fail-closed.

## HMAC Signature Validation

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

### Observability

Rejected deliveries are tracked via the
`repo_guardian_webhook_rejected_total{reason="signature"}` Prometheus
counter. A burst means a wrong or rotated webhook secret — not an
unwanted-source problem; that signal lives at the operator's edge
layer (each option in
[docs/operations/ingress.md](docs/operations/ingress.md) names where
its client-IP evidence lives).

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

### Scope-Constrained Rule Evaluation

In multi-installation deployments where a single repo-guardian instance is
authorized against multiple GitHub organizations, the optional top-level
`scope { orgs = [...] }` HCL block engages strict mode where every rule
must declare its own `scope { }` sub-block.

This is not a substitute for GitHub's permission model -- the App's
installations still control which repos the service can see -- but it
narrows the rule blast radius: a misconfigured rule cannot accidentally
fire against an org the operator did not list. Strict-mode validation
runs at config load time, so missing or empty scope blocks fail loudly
rather than silently applying everywhere.

Out-of-scope evaluations are observable via the
`repo_guardian_out_of_scope_total{level, org}` counter, providing a
canary for misconfigured policies. See
[`docs/ADDING_RULES.md`](docs/ADDING_RULES.md#multi-org-configuration)
for the full strict-mode error taxonomy.

## Reporting Vulnerabilities

If you discover a security vulnerability, please report it by opening a
private issue or contacting the maintainer directly. Do not open a public
issue for security vulnerabilities.
