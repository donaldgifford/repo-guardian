# repo-guardian

A GitHub App (Go) that automates repository onboarding and compliance across a GitHub organization. It detects missing configuration files (CODEOWNERS, Dependabot, Renovate) and creates pull requests with sensible defaults.

## How It Works

repo-guardian monitors your GitHub organization for new repositories and periodically reconciles all existing ones. When it finds a repo missing required configuration files, it creates a single PR adding all missing files at once.

**Trigger sources:**
- **Webhooks** -- new repo created, repos added to installation, new installation
- **Scheduler** -- weekly reconciliation of all repos (configurable interval)

**Built-in rules:**
- **CODEOWNERS** -- adds `.github/CODEOWNERS` with a placeholder team
- **Dependabot** -- adds `.github/dependabot.yml` for GitHub Actions updates
- **Renovate** -- adds `renovate.json` (disabled by default)

Each rule checks multiple file paths (e.g., CODEOWNERS can live at root, `.github/`, or `docs/`), and skips repos that already have the file or an open PR addressing it.

## Prerequisites

- Go 1.25+ (managed via [mise](https://mise.jdx.dev/))
- A registered [GitHub App](https://docs.github.com/en/apps/creating-github-apps) with:
  - **Permissions:** Contents (Read & Write), Pull Requests (Read & Write), Metadata (Read)
  - **Events:** `repository`, `installation_repositories`, `installation`
  - A generated private key (PEM file)
  - A webhook secret

## Configuration

All configuration is via environment variables (12-factor):

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GITHUB_APP_ID` | Yes | -- | GitHub App numeric ID |
| `GITHUB_PRIVATE_KEY_PATH` | Yes* | -- | Path to the App's PEM private key file |
| `GITHUB_PRIVATE_KEY` | Yes* | -- | Raw PEM private key content (mutually exclusive with path) |
| `GITHUB_WEBHOOK_SECRET` | Yes | -- | HMAC secret for webhook payload validation |
| `LISTEN_ADDR` | No | `:8080` | Webhook server listen address |
| `METRICS_ADDR` | No | `:9090` | Prometheus metrics server listen address |
| `WORKER_COUNT` | No | `5` | Number of concurrent repo check workers |
| `QUEUE_SIZE` | No | `1000` | Work queue buffer size |
| `TEMPLATE_DIR` | No | `/etc/repo-guardian/templates` | Directory for template overrides |
| `SCHEDULE_INTERVAL` | No | `168h` | Reconciliation interval (Go duration) |
| `SKIP_FORKS` | No | `true` | Skip forked repositories |
| `SKIP_ARCHIVED` | No | `true` | Skip archived repositories |
| `DRY_RUN` | No | `false` | Log actions without creating PRs |
| `LOG_LEVEL` | No | `info` | Log verbosity: debug, info, warn, error |
| `RATE_LIMIT_THRESHOLD` | No | `0.10` | Fraction of rate limit budget that triggers pre-emptive throttling |
| `CUSTOM_PROPERTIES_MODE` | No | `""` | Custom properties mode: `""` (disabled), `github-action`, or `api` |
| `WEBHOOK_IP_ALLOWLIST` | No | `true` | Enable GitHub webhook IP allowlist middleware |
| `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN` | No | `false` | Allow requests when IP ranges are unavailable |
| `TRUST_PROXY_HEADERS` | No | `false` | Read client IP from `X-Forwarded-For` header |

*One of `GITHUB_PRIVATE_KEY_PATH` or `GITHUB_PRIVATE_KEY` is required (mutually exclusive).

Boolean values accept Go's `strconv.ParseBool` formats: `1`, `t`, `TRUE`, `true`, `0`, `f`, `FALSE`, `false`. Invalid values (e.g., `yes`, `no`) will cause a startup error.

## Quick Start (Local Development)

1. **Install tools:**
   ```bash
   mise install
   ```

2. **Copy and fill in environment config:**
   ```bash
   cp .env.example .env
   # Edit .env with your GitHub App credentials
   ```

3. **Run with Docker Compose (dry-run mode):**
   ```bash
   make compose-up
   ```

4. **Run with ngrok tunnel** (for receiving live webhooks):
   ```bash
   make compose-up-tunnel
   ```
   This starts an ngrok tunnel that forwards public webhook traffic to your local instance. Set the ngrok URL as your GitHub App's webhook URL.

5. **View logs:**
   ```bash
   make compose-logs
   ```

6. **Stop:**
   ```bash
   make compose-down
   ```

### Running Without Docker

```bash
make build
# Export required env vars, then:
make run-local
```

## Build & Development

```bash
make build            # Build binary to build/bin/repo-guardian
make test             # Run tests with race detector
make test-coverage    # Run tests with coverage report
make lint             # Run golangci-lint
make lint-fix         # Run golangci-lint with auto-fix
make fmt              # Format code (gofmt, goimports, gofumpt, golines, gci)
make check            # Quick pre-commit check (lint + test)
make ci               # Full CI pipeline (lint + test + build)
```

Run a single test:
```bash
go test -v -race -run TestName ./internal/package/...
```

## Docker

```bash
make docker-build              # Build local dev image (single-arch)
make docker-build-multiarch    # Validate multi-arch build
make docker-push               # Build and push multi-arch image
```

The Dockerfile uses a multi-stage build: `golang:1.25` builder + `distroless/static` runtime. The final image is ~20MB and runs as a non-root user.

## Kubernetes Deployment

repo-guardian ships with [Kustomize](https://kustomize.io/) manifests for deployment to Kubernetes (designed for EKS).

### Directory Structure

```
deploy/
  base/                        # Shared base manifests
    deployment.yaml            # Pod spec, probes, volume mounts
    service.yaml               # ClusterIP: 80->8080 (http), 9090->9090 (metrics)
    configmap.yaml             # Default file templates (CODEOWNERS, Dependabot, Renovate)
    serviceaccount.yaml        # ServiceAccount for the pod
    secret.yaml                # GitHub App credentials template
    kustomization.yaml         # Base kustomization (namespace: platform-tools)
  overlays/
    dev/                       # Dev overlay: DRY_RUN=true, LOG_LEVEL=debug, 24h schedule
    prod/                      # Prod overlay: DRY_RUN=false, LOG_LEVEL=info, 168h schedule
    tailscale/                 # Tailscale Funnel sidecar for webhook delivery
```

### Prerequisites

Before deploying, create a Kubernetes Secret with the GitHub App credentials:

```bash
kubectl -n platform-tools create secret generic repo-guardian-github \
  --from-literal=app-id=YOUR_APP_ID \
  --from-literal=webhook-secret=YOUR_WEBHOOK_SECRET \
  --from-file=private-key=path/to/private-key.pem
```

### Deploy

```bash
# Dev (dry-run mode, debug logging, 24h reconciliation)
kubectl apply -k deploy/overlays/dev/

# Prod (live mode, info logging, weekly reconciliation)
kubectl apply -k deploy/overlays/prod/
```

### Health Checks

The deployment configures Kubernetes probes against:

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness probe -- always returns 200 |
| `GET /readyz` | Readiness probe -- returns 200 when the work queue is accepting jobs, 503 otherwise |

### Customizing Templates

The default file templates are stored in the `repo-guardian-templates` ConfigMap. To override them, edit `deploy/base/configmap.yaml` or provide a custom ConfigMap in your overlay. Templates use `.tmpl` extension and are mounted at `/etc/repo-guardian/templates`.

### Tailscale Funnel (Dev/Test)

The `tailscale` overlay adds a Tailscale Funnel sidecar for exposing the webhook endpoint via a stable `*.ts.net` URL with automatic TLS. This is useful for dev/test environments where you need GitHub to deliver webhooks without a public load balancer.

```bash
# Create the Tailscale auth key secret first
kubectl -n platform-tools create secret generic tailscale-auth \
  --from-literal=authkey='tskey-auth-XXXXX'

# Deploy with Tailscale sidecar
kubectl apply -k deploy/overlays/tailscale/
```

The sidecar uses userspace networking (no `CAP_NET_ADMIN` required) and proxies Funnel HTTPS (port 443) to the app on `127.0.0.1:8080`. Set `TRUST_PROXY_HEADERS=true` when using Funnel, as the sidecar forwards client IPs via `X-Forwarded-For`.

For production, use a proper Ingress or LoadBalancer instead of Funnel.

### Exposing Webhooks

The Service exposes port 80 (mapped to container port 8080). You'll need an Ingress or LoadBalancer to route external webhook traffic to `POST /webhooks/github`. Configure your GitHub App's webhook URL to point to this endpoint.

## Observability

### Prometheus Metrics

Available at `METRICS_ADDR` (default `:9090/metrics`):

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `repo_guardian_repos_checked_total` | Counter | `trigger` | Repos checked (webhook/scheduler) |
| `repo_guardian_prs_created_total` | Counter | -- | PRs created |
| `repo_guardian_prs_updated_total` | Counter | -- | PRs updated |
| `repo_guardian_files_missing_total` | Counter | `rule_name` | Missing files detected |
| `repo_guardian_check_duration_seconds` | Histogram | -- | Check duration per repo |
| `repo_guardian_webhook_received_total` | Counter | `event_type` | Webhooks received |
| `repo_guardian_webhook_rejected_total` | Counter | `reason` | Webhooks rejected by IP allowlist |
| `repo_guardian_errors_total` | Counter | `operation` | Errors by operation |
| `repo_guardian_github_rate_remaining` | Gauge | -- | GitHub API rate limit remaining |
| `repo_guardian_github_rate_limit_waits_total` | Counter | `reason` | Rate limit waits by reason |
| `repo_guardian_github_rate_limit_wait_seconds` | Histogram | -- | Duration of rate limit waits |
| `repo_guardian_properties_checked_total` | Counter | -- | Repos where custom properties were evaluated |
| `repo_guardian_properties_prs_created_total` | Counter | -- | PRs created for custom properties |
| `repo_guardian_properties_set_total` | Counter | -- | Properties set via API |
| `repo_guardian_properties_already_correct_total` | Counter | -- | Properties already matching |

### Rate Limiting

repo-guardian includes a built-in rate limit transport that:
- Tracks GitHub API rate limit headers on every response
- Pre-emptively throttles requests when the remaining budget drops below the configured threshold (default 10%)
- Automatically retries once on primary rate limits (403 + `X-RateLimit-Remaining: 0`)
- Automatically retries once on secondary rate limits (403 + `Retry-After` header)

## Security

repo-guardian uses two layers of defense on the webhook endpoint:

1. **IP Allowlist Middleware** -- rejects requests from IPs outside GitHub's published webhook CIDR ranges (fetched from the `/meta` API). Fail-closed by default.
2. **HMAC Signature Validation** -- verifies the `X-Hub-Signature-256` header using a shared webhook secret. Ensures authenticity and integrity.

See [SECURITY.md](SECURITY.md) for full details.

## Architecture

```
cmd/repo-guardian/main.go  -> entrypoint (dual HTTP servers, graceful shutdown)
internal/
  catalog/    -> Backstage catalog-info.yaml parser
  config/     -> configuration (12-factor env vars, validated at startup)
  github/     -> GitHub API client (go-github v68, ghinstallation v2, rate limit transport)
  checker/    -> check-and-PR engine + buffered work queue + custom properties checker
  rules/      -> FileRule registry + TemplateStore (embedded fallback templates)
  webhook/    -> HTTP handler for GitHub webhook events (HMAC-validated) + IP allowlist middleware
  scheduler/  -> in-process ticker for periodic reconciliation
  metrics/    -> Prometheus metric definitions
```

**Core flow:** GitHub webhook OR weekly scheduler -> work queue (buffered channel) -> checker engine -> GitHub API (create PRs for missing files).

## Documentation

Structured documentation is managed with [docz](https://github.com/donaldgifford/docz):

| Type | Directory | Description |
|------|-----------|-------------|
| RFC | [`docs/rfc/`](docs/rfc/) | High-level proposals |
| Design | [`docs/design/`](docs/design/) | Technical design documents |
| Implementation | [`docs/impl/`](docs/impl/) | Phased implementation plans |
| Investigation | [`docs/investigation/`](docs/investigation/) | Research and spike findings |
| ADR | [`docs/adr/`](docs/adr/) | Architecture decision records |

## License

See [LICENSE](LICENSE) for details.
