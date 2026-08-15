# repo-guardian

A GitHub App (Go) that automates repository onboarding and compliance across a GitHub organization. It detects missing configuration files (CODEOWNERS, Dependabot, Renovate) and creates pull requests with sensible defaults.

## How It Works

repo-guardian monitors your GitHub organization for new repositories and periodically reconciles existing ones. When it finds a repo missing required configuration files, it creates a single PR adding all missing files at once.

**Trigger sources:**

- **Webhooks** -- new repo created, repos added to installation, new installation (each seeds persistent per-repo state and enqueues an immediate check)
- **Push events** -- pushes to the default branch that touch watched files (`watch = true` reconcilers) trigger a re-check
- **Stale sweep** -- a leader-elected scheduler periodically re-enqueues repos whose last check is older than `RECONCILE_FRESHNESS` or whose stored policy version differs from the running one (no full-fleet enumeration per tick)
- **Discovery** -- a periodic enumerator catches repos whose webhooks were missed and seeds them into the state store

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

- Go 1.26+ (managed via [mise](https://mise.jdx.dev/))
- Postgres + Valkey backing services (the Helm chart bakes both by default; `make dev-services` brings them up locally)
- A registered [GitHub App](https://docs.github.com/en/apps/creating-github-apps) with:
  - **Permissions:** Contents (Read & Write), Pull Requests (Read & Write), Metadata (Read)
  - **Events:** `repository`, `installation_repositories`, `installation`, and `push` (needed for `watch = true` reconcilers)
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
| `SCHEDULE_INTERVAL` | No | `168h` | Stale-sweep tick cadence (Go duration). Each tick re-enqueues repos stale per `RECONCILE_FRESHNESS`. |
| `SKIP_FORKS` | No | `true` | Skip forked repositories |
| `SKIP_ARCHIVED` | No | `true` | Skip archived repositories |
| `DRY_RUN` | No | `false` | Log actions without creating PRs |
| `LOG_LEVEL` | No | `info` | Log verbosity: debug, info, warn, error |
| `RATE_LIMIT_THRESHOLD` | No | `0.10` | Fraction of rate limit budget that triggers pre-emptive throttling |
| `CUSTOM_PROPERTIES_MODE` | No | `""` | Custom properties mode: `""` (disabled), `github-action`, or `api` |
| `GUARDIAN_CONFIG` | No | `""` | Path to HCL policy config file or directory |
| `STORE_BACKEND` | No | `postgres` | Persistent state store backend. Only `postgres` is supported since IMPL-0016 (chart 1.0). |
| `STORE_DSN` | Yes (when backend=postgres) | -- | Postgres connection string (e.g., `postgres://user:pass@host:5432/db?sslmode=disable`) |
| `QUEUE_BACKEND` | No | `valkey` | Work queue backend. Only `valkey` is supported. |
| `QUEUE_VALKEY_DSN` | Yes (when backend=valkey) | -- | Valkey/Redis connection string (e.g., `redis://host:6379/0`) |
| `SCHEDULER_BACKEND` | No | `valkey` | Sweep scheduler backend. Only `valkey` is supported; shares the queue's Valkey instance. |
| `RECONCILE_FRESHNESS` | No | `24h` | StaleSweeper freshness window. Repos whose `last_checked_at` is older than this (or whose `policy_version` differs) are eligible for re-enqueue on the next sweep tick. |
| `STALE_SWEEP_BATCH_SIZE` | No | `200` | Maximum repos the StaleSweeper enqueues per tick. Bound the per-tick API + queue pressure on large fleets. |
| `RATE_LIMIT_RESERVE` | No | `0.1` | Sweeper-side reserve gate. The StaleSweeper refuses to enqueue when an installation's remaining GitHub rate-limit budget falls below `limit × RATE_LIMIT_RESERVE`. Distinct from the client-side `RATE_LIMIT_THRESHOLD` throttle. |
| `DISCOVERY_ENABLED` | No | `true` | Toggle the periodic `Discoverer` (IMPL-0015 Phase 1). When `false` the binary still discovers via webhooks; only the periodic enumeration path is disabled. |
| `DISCOVERY_INTERVAL` | No | `1h` | Cadence between `Discoverer.Discover` invocations. Lower values burn more API budget on `list_installations` + `list_installation_repos`; higher values delay discovery of repos the webhook path missed. |
| `DISCOVERY_RESERVE_FRACTION` | No | `0.20` | `BudgetTracker` reserve floor (fraction of the rate-limit window held in reserve). Must be in `[0, 1]`. |
| `DISCOVERY_ESTIMATED_COST_PER_REPO` | No | `10` | Operator estimate of the rate-limit cost of a single reconcile. Drives the `BudgetTracker`'s spendable-enqueue accounting. Must be > 0. |

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

`make run-local` brings up local Postgres + Valkey via
`docker-compose.dev.yaml`, then starts the binary against them. The
in-memory backends were removed in IMPL-0016 (chart 1.0.0); Postgres
and Valkey are now required at startup.

```bash
make build
# Bring up Postgres + Valkey, then start the binary (env vars wired
# automatically; override via the shell if you need a non-default DSN):
make run-local

# Stop the backing services when done:
make dev-stop
```

If you only want the backing services without starting the binary
(e.g., for `go test ./...`), run `make dev-services` directly.

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

The Dockerfile uses a multi-stage build: `golang:1.26` builder + `distroless/static` runtime. The final image is ~20MB and runs as a non-root user. There is no Dockerfile `HEALTHCHECK` (the distroless base has no shell to run one); Kubernetes probes `/healthz` and `/readyz` via the chart instead.

## Kubernetes Deployment

### Helm Chart (Recommended)

repo-guardian ships with a Helm chart at `charts/repo-guardian/`. See the [chart README](charts/repo-guardian/README.md) for the full values reference.

**Quick install** (chart published to GHCR, signed with cosign + SLSA):

```bash
helm install repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 1.0.0-rc.2 \
  --namespace repo-guardian \
  --create-namespace \
  -f values.yaml
```

The default install brings up baked Postgres + Valkey StatefulSets alongside the Deployment — no external infrastructure needed. See [Choosing a deployment shape](charts/repo-guardian/README.md#choosing-a-deployment-shape) for CNPG-managed and external (RDS / ElastiCache) shapes.

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

### Health Checks

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness probe -- always returns 200 |
| `GET /readyz` | Readiness probe -- returns 200 when the work queue is accepting jobs, 503 otherwise |

### Exposing Webhooks

The Service exposes port 80 (mapped to container port 8080). Ingress is operator-owned — pick an option (ALB via Gateway API, Tailscale operator Funnel, Cloudflare Tunnel, ngrok) from the matrix in [docs/operations/ingress.md](docs/operations/ingress.md) and route external webhook traffic to `POST /webhooks/github`. Configure your GitHub App's webhook URL to point to this endpoint. Source-IP enforcement, where wanted, lives at that edge layer; the app validates every delivery's HMAC signature (see [SECURITY.md](SECURITY.md)).

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
| `repo_guardian_webhook_rejected_total` | Counter | `reason` | Webhooks rejected by signature validation (`reason="signature"`) |
| `repo_guardian_errors_total` | Counter | `operation`, `org` | Errors by operation |
| `repo_guardian_github_rate_remaining` | Gauge | -- | GitHub API rate limit remaining |
| `repo_guardian_github_rate_limit_waits_total` | Counter | `reason` | Rate limit waits by reason |
| `repo_guardian_github_rate_limit_wait_seconds` | Histogram | -- | Duration of rate limit waits |
| `repo_guardian_queue_depth` | Gauge | `queue` | Current queue depth (jobs waiting). |
| `repo_guardian_queue_enqueued_total` / `_claimed_total` / `_acked_total` / `_reaped_total` | Counter | `queue` | Queue lifecycle counters (IMPL-0011). |
| `repo_guardian_scheduler_is_leader` | Gauge | `name` | 1 on the leader pod, 0 elsewhere. |
| `repo_guardian_store_query_seconds` | Histogram | `op`, `outcome` | Persistent store query latency. |
| `repo_guardian_rate_limit_remaining` | Gauge | `installation_id` | Per-installation GitHub rate-limit budget observed at sweep time. |
| `repo_guardian_rate_limit_reserve_blocked_total` | Counter | `installation_id` | Enqueues blocked by the `RATE_LIMIT_RESERVE` gate. |
| `repo_guardian_open_prs_by_rule` | Gauge | `org`, `rule`, `age_bucket` | Open repo-guardian PRs joined against actionable rules (IMPL-0013 P1). |
| `repo_guardian_pr_open_with_empty_actionable_total` / `pr_orphan_left_total` / `prs_closed_total` | Counter | `org`, ... | PR convergence + drift counters (IMPL-0013). |
| `repo_guardian_store_writeback_total` | Counter | `installation_id`, `outcome` | Worker persisted job outcome to the state store (IMPL-0015 Phase 0). Rises 1:1 with `repos_checked_total` modulo errors. |
| `repo_guardian_store_writeback_duration_seconds` | Histogram | -- | Latency of `UpdateRepoState` from the worker pool. Target p99 < 50ms. |
| `repo_guardian_repo_discovered_total` | Counter | `installation_id` | New repos surfaced by the `Discoverer` (IMPL-0015 Phase 1). |
| `repo_guardian_discovery_duration_seconds` | Histogram | -- | Wall-clock per `Discoverer.Discover` invocation. |
| `repo_guardian_discovery_api_calls_total` | Counter | `installation_id`, `endpoint` | GitHub API calls the Discoverer made. |
| `repo_guardian_api_budget_remaining` / `_spendable` / `_reserve_fraction` / `_utilisation` | Gauge | `installation_id` | `BudgetTracker` rate-limit observability (IMPL-0015 Phase 1). |
| `repo_guardian_api_budget_refresh_total` | Counter | `installation_id`, `outcome` | `BudgetTracker` refresh attempts. |
| `repo_guardian_enqueue_gated_by_budget_total` | Counter | `installation_id` | Enqueues blocked by the `BudgetTracker` reserve gate (StaleSweeper + Discoverer share this counter). |

See [`docs/operations/scaling.md`](docs/operations/scaling.md) for the full metric catalogue with operator playbooks and [`contrib/README.md`](contrib/README.md) for example PromQL queries, a Grafana dashboard, alerting rules, and migration recipes for the `Counter` -> `CounterVec` promotion of `prs_created_total` and `prs_updated_total`.

### Rate Limiting

repo-guardian includes a built-in rate limit transport that:

- Tracks GitHub API rate limit headers on every response
- Pre-emptively throttles requests when the remaining budget drops below the configured threshold (default 10%)
- Automatically retries once on primary rate limits (403 + `X-RateLimit-Remaining: 0`)
- Automatically retries once on secondary rate limits (403 + `Retry-After` header)

## Security

The app-layer security boundary on the webhook endpoint is **HMAC signature validation** -- every delivery's `X-Hub-Signature-256` header is verified against the shared webhook secret, ensuring authenticity and integrity. Source-IP enforcement is operator-owned and lives at your edge layer (ALB security group, Cloudflare WAF, ngrok traffic policy -- see [docs/operations/ingress.md](docs/operations/ingress.md)).

See [SECURITY.md](SECURITY.md) for the full trust model, including why the former in-app IP allowlist was removed (INV-0016: it was spoofable behind every documented proxy).

## Architecture

```
cmd/repo-guardian/main.go  -> entrypoint (dual HTTP servers, graceful shutdown)
internal/
  catalog/    -> Backstage catalog-info.yaml parser
  config/     -> configuration (12-factor env vars, validated at startup)
  policy/     -> HCL parser, validation, scope and ignore matchers, YAML path evaluator, content assertions
  github/     -> GitHub API client (go-github v68, ghinstallation v2, rate limit transport)
  checker/    -> check-and-PR engine + setting/branch-protection rules + scope evaluation gates + StaleSweeper
  reconciler/ -> pluggable post-check reconcilers (custom_properties, label_sync, branch_protection, workflow_sync)
  rules/      -> TemplateStore (embedded fallback templates)
  webhook/    -> HTTP handler for GitHub webhook events (HMAC-validated) + push event handler + discovery write-back via Store.UpsertIfMissing
  scheduler/  -> Scheduler interface + valkey/ (SETNX leader-elected sweep cadence) + Discoverer (periodic enumeration safety net for missed webhooks)
  store/      -> per-repo state interface + postgres/ (pgx/v5 + pgxpool, embedded migrations); UpsertIfMissing for atomic discovery, UpdateRepoState for worker write-back
  queue/      -> work-queue interface + valkey/ (LIST + ZSET + reaper goroutine)
  worker/     -> in-process worker pool consuming queue.Queue.Subscribe; persists job outcome to the state store on every processed job
  budget/     -> per-installation rate-limit cache shared by StaleSweeper + Discoverer (BudgetTracker, IMPL-0015 Phase 1)
  metrics/    -> Prometheus metric definitions (most counters labeled with org)
```

**Core flow:** GitHub webhook (or periodic Discoverer for missed deliveries) seeds `repo_state` via `Store.UpsertIfMissing`. The leader-elected StaleSweeper queries Postgres for rows older than `RECONCILE_FRESHNESS` (or whose `policy_version` differs) and enqueues them to the Valkey work queue. Worker pool consumes the queue, runs the checker engine + reconcilers against the GitHub API, then writes the outcome back to Postgres so a multi-replica sweep converges without duplicate work. Both schedulers gate enqueue on a shared `BudgetTracker` so a single installation can't burn the hourly rate-limit window.

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
