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

### Pod Hardening

Every role and the migrate Job run non-root (uid 65534) with the
runtime's default seccomp profile, a read-only root filesystem, no
privilege escalation and every capability dropped, and mount no
service-account token: nothing in repo-guardian calls the Kubernetes
API. The UI pod is hardened the same way.

### GitHub App Private Keys

repo-guardian uses two GitHub Apps. The Evaluation App is read-only and
its key is mounted only into the evaluator; the Remediation App writes,
and its key is mounted only into the remediator. Keys are mounted as
read-only files from Kubernetes Secrets, never passed as values, and
never logged. Ingest holds neither key and no database credential; the
API holds read-only database access only. Inside the binary, the GitHub
writer package is unreachable from evaluator-side code (enforced by
depguard), and each role's database user can write only its own
columns.

### Rate Limiting

Each installation has its own rate budget per App, held by a Temporal
workflow: an evaluation acquires a lease before calling GitHub and is
deferred, never blocked, when the budget is below its reserve
(default 10%).

### Scoped Policy

The policy names the organisations it applies to (`enterprise.hcl`)
and adds per-org controls, exclusions and overrides in `orgs/*.hcl`;
nothing is written to a repository unless its assignment is in
`mode = "remediate"` and the Remediation App is installed there. This
does not replace GitHub's permission model: the Apps' installations
still decide which repositories repo-guardian can see. Adding a control
type is described in [docs/ADDING_RULES.md](docs/ADDING_RULES.md).

## Reporting Vulnerabilities

Report a vulnerability through GitHub's
[private vulnerability reporting](https://github.com/donaldgifford/repo-guardian/security/advisories/new).
Do not open a public issue, pull request or discussion for it.

- We acknowledge a report within **3 business days**.
- For a high or critical issue we send a fix or a mitigation plan within
  **30 days**, and publish a GitHub security advisory with the fixed
  version.

### Supported Versions

| Version | Supported |
| --- | --- |
| 2.x | Yes |
| 1.14.x | Security fixes for 90 days after 2.0.0 |
| older | No |

### Verifying What You Run

Every image, UI image and chart is signed with cosign (keyless) and
carries SLSA Level 3 provenance and an SPDX SBOM attestation; release
binaries ship SPDX SBOMs and a GPG-signed `checksums.txt`. The commands
are in [Verifying releases](docs/operations/verifying-releases.md).

### Supply-Chain Controls

The code is scanned on every pull request and weekly by govulncheck
(SARIF in code scanning) and CodeQL (Go, TypeScript and the workflows),
linted with golangci-lint's `gosec` and friends, and held to a
licence allowlist by go-licenses. Architectural least privilege is a
lint failure: depguard keeps the GitHub writer out of evaluator and API
code. The UI bans every HTML sink in ESLint and serves a strict CSP.
Dependabot raises security updates for Go modules, the UI and the
workflows; Renovate proposes every other update. Tools are pinned in
`mise.toml`, and the release branch requires CI to pass.

Images build from distroless bases with CGO off and `-trimpath`. The
publish workflows authenticate to registries with GitHub OIDC (no
static cloud keys), sign by digest, attest provenance with the SLSA
GitHub generator and SBOMs with cosign, and verify the attestation
before finishing. Pods run non-root, read-only, capability-free and
with no service-account token. Secret scanning with push protection is
on for the repository.
