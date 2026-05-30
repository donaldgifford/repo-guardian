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
- **Renovate Config** -- adds `renovate.json` extending org preset (disabled by default)
- **Renovate Workflow** -- adds `.github/workflows/renovate.yml` with docker-based Renovate runner (disabled by default)

Each rule checks multiple file paths (e.g., CODEOWNERS can live at root, `.github/`, or `docs/`), and skips repos that already have the file or an open PR addressing it.

**Check modes** (via HCL policy config):

- **exists** -- file must be present (default, current behavior)
- **contains** -- file must exist and pass content assertions (regex patterns, YAML path checks)
- **exact** -- file must match the template exactly (YAML semantic comparison for `.yml`/`.yaml` files)

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
| `GITHUB_ORG` | No | `""` | GitHub org/user name for org-specific assertion patterns |
| `GUARDIAN_CONFIG` | No | `""` | Path to HCL policy config file or directory |

*One of `GITHUB_PRIVATE_KEY_PATH` or `GITHUB_PRIVATE_KEY` is required (mutually exclusive).

Boolean values accept Go's `strconv.ParseBool` formats: `1`, `t`, `TRUE`, `true`, `0`, `f`, `FALSE`, `false`. Invalid values (e.g., `yes`, `no`) will cause a startup error.

## Policy Configuration (HCL)

repo-guardian supports an optional HCL policy file for defining custom file rules with check modes, content assertions, and structured configuration. Set `GUARDIAN_CONFIG` to a `.hcl` file or a directory of `.hcl` files.

When no policy config is set, built-in defaults provide identical behavior to the environment variable configuration.

**Example `guardian.hcl`:**

```hcl
guardian {
  log_level  = "info"
  dry_run    = false
  skip_forks = true
}

rule "file" "codeowners" {
  check    = "exists"
  paths    = ["CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  pr {
    search_terms = ["codeowners", "CODEOWNERS"]
  }
}

rule "file" "catalog-info" {
  check    = "contains"
  paths    = ["catalog-info.yaml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"

  assertion {
    yaml_path = "spec.owner"
    contains  = "team"
    message   = "spec.owner must reference a team"
  }
}
```

**Enabling Renovate rules:**

```hcl
guardian {
  org = "myorg"  # or set GITHUB_ORG env var
}

rule "file" "renovate_workflow" {
  enabled  = true
  check    = "exact"
  paths    = [".github/workflows/renovate.yml"]
  target   = ".github/workflows/renovate.yml"
  template = "renovate-workflow"
  reconcile "workflow_sync" { watch = true }
}

rule "file" "renovate_config" {
  enabled  = true
  check    = "contains"
  paths    = ["renovate.json", "renovate.json5", ".renovaterc",
              ".renovaterc.json", ".github/renovate.json",
              ".github/renovate.json5"]
  target   = "renovate.json"
  template = "renovate"
  assertion {
    pattern = "github>myorg/renovate-config"
    message = "renovate.json must extend org preset"
  }
}
```

Both Renovate rules are disabled by default. See [`docs/ADDING_RULES.md`](docs/ADDING_RULES.md#renovate-file-rules) for details on templates, check modes, and prerequisites.

**Multi-org configurations:** A top-level `scope { orgs = [...] }` block engages strict mode where every rule must declare its own `scope { }` sub-block. Use the literal `["*"]` to apply to every in-scope org, or a subset to target specific orgs. Single-org users do not need this — leaving `scope { }` out preserves the legacy "every rule applies to every repo" behavior. See [`examples/guardian-multi-org.hcl`](examples/guardian-multi-org.hcl) and the [Multi-org Configuration](docs/ADDING_RULES.md#multi-org-configuration) section of `ADDING_RULES.md`.

**Helm chart:** Use `policy.config` for inline HCL or `policy.existingConfigMap` to reference an external ConfigMap. See the [chart values](charts/repo-guardian/values.yaml).

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

### Helm Chart (Recommended)

repo-guardian ships with a Helm chart at `charts/repo-guardian/`. See the [chart README](charts/repo-guardian/README.md) for the full values reference.

**Quick install** (chart published to GHCR, signed with cosign + SLSA):

```bash
helm install repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 0.3.1 \
  --namespace repo-guardian \
  --create-namespace \
  -f values.yaml
```

See the [chart README](charts/repo-guardian/README.md) for cosign /
SLSA verification commands and the full values reference.

**Minimal values.yaml:**

```yaml
config:
  appId: "YOUR_APP_ID"
  dryRun: true

secrets:
  webhookSecret: "YOUR_WEBHOOK_SECRET"
  privateKey: |
    -----BEGIN RSA PRIVATE KEY-----
    YOUR_PRIVATE_KEY
    -----END RSA PRIVATE KEY-----
```

**Tailscale Funnel sidecar:**

```bash
helm install repo-guardian repo-guardian/repo-guardian \
  --set tailscale.enabled=true \
  -f values.yaml
```

When Tailscale is enabled, `TRUST_PROXY_HEADERS` and `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN` are automatically set to `true`.

### Health Checks

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness probe -- always returns 200 |
| `GET /readyz` | Readiness probe -- returns 200 when the work queue is accepting jobs, 503 otherwise |

### Exposing Webhooks

The Service exposes port 80 (mapped to container port 8080). You'll need an Ingress or LoadBalancer to route external webhook traffic to `POST /webhooks/github`. Configure your GitHub App's webhook URL to point to this endpoint.

## Observability

### Prometheus Metrics

Available at `METRICS_ADDR` (default `:9090/metrics`):

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `repo_guardian_repos_checked_total` | Counter | `trigger`, `org` | Repos checked (webhook/scheduler/push) |
| `repo_guardian_prs_created_total` | Counter | `org` | PRs created |
| `repo_guardian_prs_updated_total` | Counter | `org` | PRs updated |
| `repo_guardian_files_missing_total` | Counter | `rule_name`, `org` | Missing files detected |
| `repo_guardian_settings_checked_total` | Counter | `rule_name`, `org` | Setting rule evaluations |
| `repo_guardian_settings_mismatched_total` | Counter | `rule_name`, `org` | Setting mismatches detected |
| `repo_guardian_settings_remediated_total` | Counter | `rule_name`, `org` | Setting rules remediated |
| `repo_guardian_branch_protection_checked_total` | Counter | `rule_name`, `org` | Branch protection rule evaluations |
| `repo_guardian_branch_protection_remediated_total` | Counter | `rule_name`, `org` | Branch protection rules remediated |
| `repo_guardian_ignored_total` | Counter | `scope`, `org` | Repos/rules skipped by ignore lists |
| `repo_guardian_out_of_scope_total` | Counter | `level`, `org` | Rule evaluations skipped by strict-mode scope |
| `repo_guardian_check_duration_seconds` | Histogram | -- | Check duration per repo |
| `repo_guardian_webhook_received_total` | Counter | `event_type` | Webhooks received |
| `repo_guardian_webhook_rejected_total` | Counter | `reason` | Webhooks rejected by IP allowlist |
| `repo_guardian_errors_total` | Counter | `operation`, `org` | Errors by operation |
| `repo_guardian_github_rate_remaining` | Gauge | -- | GitHub API rate limit remaining |
| `repo_guardian_github_rate_limit_waits_total` | Counter | `reason` | Rate limit waits by reason |
| `repo_guardian_github_rate_limit_wait_seconds` | Histogram | -- | Duration of rate limit waits |
| `repo_guardian_properties_checked_total` | Counter | -- | Repos where custom properties were evaluated |
| `repo_guardian_properties_prs_created_total` | Counter | -- | PRs created for custom properties |
| `repo_guardian_properties_set_total` | Counter | -- | Properties set via API |
| `repo_guardian_properties_already_correct_total` | Counter | -- | Properties already matching |

See [`contrib/README.md`](contrib/README.md) for example PromQL queries, a Grafana dashboard, alerting rules, and migration recipes for the `Counter` -> `CounterVec` promotion of `prs_created_total` and `prs_updated_total`.

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
  policy/     -> HCL parser, validation, scope and ignore matchers, YAML path evaluator, content assertions
  github/     -> GitHub API client (go-github v68, ghinstallation v2, rate limit transport)
  checker/    -> check-and-PR engine + buffered work queue + setting/branch-protection rules + scope evaluation gates
  reconciler/ -> pluggable post-check reconcilers (custom_properties, label_sync, branch_protection, workflow_sync)
  rules/      -> FileRule registry + TemplateStore (embedded fallback templates)
  webhook/    -> HTTP handler for GitHub webhook events (HMAC-validated) + IP allowlist middleware + push event handler
  scheduler/  -> in-process ticker for periodic reconciliation
  metrics/    -> Prometheus metric definitions (most counters labeled with org)
```

**Core flow:** GitHub webhook OR weekly scheduler OR push event -> work queue (buffered channel) -> checker engine -> GitHub API (create PRs for missing files) -> reconcilers (post-check actions).

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
