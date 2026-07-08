# repo-guardian

GitHub App that automates repository onboarding and compliance

## Installation

The chart is published as an OCI artifact to two registries — pick
whichever you prefer. Both are signed with cosign keyless and ship
SLSA Level 3 provenance attestations.

**GHCR (public, anonymous pull):**

```bash
helm install repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 1.0.0-rc.2 \
  --namespace repo-guardian \
  --create-namespace \
  -f values.yaml
```

**ECR (private; requires AWS auth):**

```bash
aws ecr get-login-password --region <region> | \
  helm registry login <account>.dkr.ecr.<region>.amazonaws.com \
    --username AWS --password-stdin

helm install repo-guardian \
  oci://<account>.dkr.ecr.<region>.amazonaws.com/repo-guardian-chart \
  --version 1.0.0-rc.2 \
  --namespace repo-guardian \
  --create-namespace \
  -f values.yaml
```

## Prerequisites

- Kubernetes 1.28+
- Helm 3.14+ (OCI support)
- A registered [GitHub App](https://docs.github.com/en/apps/creating-github-apps) with:
  - **Permissions:** Contents (Read & Write), Pull Requests (Read & Write), Metadata (Read)
  - **Events:** `repository`, `installation_repositories`, `installation`

## Quick Start

1. Create a values file with your GitHub App credentials:

```yaml
config:
  appId: "YOUR_APP_ID"
  dryRun: true  # Start in dry-run mode

secrets:
  webhookSecret: "YOUR_WEBHOOK_SECRET"
  privateKey: |
    -----BEGIN RSA PRIVATE KEY-----
    YOUR_PRIVATE_KEY
    -----END RSA PRIVATE KEY-----
```

2. Install the chart:

```bash
helm install repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 1.0.0-rc.2 \
  --namespace repo-guardian \
  --create-namespace \
  -f values.yaml
```

3. Once verified, set `config.dryRun: false` to enable live mode.

## Choosing a deployment shape

Chart `1.0.0-rc.1`+ ships Postgres + Valkey as the only supported
backends (IMPL-0016 removed the in-memory store/queue and the
in-process ticker scheduler). Pick the row that matches your
operator scenario; the baked shape is the default and brings up
StatefulSets out-of-the-box.

| Shape | Values | Use when |
|-------|--------|----------|
| **Baked Postgres + Valkey** (default) | `store.postgres.mode=baked`, `queue.valkey.mode=baked` | You want multi-replica with no external operator dependencies. Chart renders single-pod Postgres + Valkey StatefulSets with auto-generated passwords. Suitable for homelab / small dev clusters. |
| **CNPG-managed Postgres + baked Valkey** | `store.postgres.mode=cnpg`, `queue.valkey.mode=baked` | You already run [CloudNativePG](https://cloudnative-pg.io/) in the cluster and want the operator to handle Postgres lifecycle (HA replicas, backups, monitoring). The chart renders a `Cluster` CR and an optional `Pooler` (PgBouncer) CR. An optional `LoadBalancer` Service in front of the pooler (Cilium BGP-annotated by default) exposes the database to clients outside the cluster — enable via `store.postgres.cnpg.pooler.service.enabled`. |
| **External Postgres + external Valkey** | `store.postgres.mode=external` + `existingSecret`; `queue.valkey.mode=external` + `existingSecret` | You're running a managed Postgres (RDS, Cloud SQL) and Redis-compatible service (ElastiCache for Valkey, etc.). Chart consumes the DSN from operator-supplied secrets and renders no backing resources. |

### Schema validation

`values.schema.json` (shipped with chart `1.0.0-rc.1`+) locks
`store.backend` to `"postgres"`, `queue.backend` to `"valkey"`, and
`scheduler.backend` to `"valkey"`. Operators upgrading from chart
`0.7.x` who carry forward `store.backend=memory` (or any other
removed value) will see a schema error at `helm install` /
`helm upgrade` time, *before* pod startup:

```
Error: values don't meet the specifications of the schema(s) in
the following chart(s):
repo-guardian:
- at '/store/backend': value must be 'postgres'
```

This is intentional: failing fast at chart-render time gives a
cleaner diagnostic than a `CrashLoopBackoff` from the binary's
startup validation (which also rejects the old values, with a
migration URL — see [migrations.md](../../docs/operations/migrations.md#removing-memory-backend)).

For sizing knobs (`replicaCount`, `workerCount`, `maxConns`,
`staleSweep.*`), see [docs/operations/scaling.md](../../docs/operations/scaling.md).
For Postgres schema operations, see
[docs/operations/migrations.md](../../docs/operations/migrations.md).

### Upgrade notes (chart 1.0.0-rc.1) — breaking

- **Memory backends removed.** `store.backend=memory`,
  `queue.backend=memory`, and `scheduler.backend=ticker` are
  rejected by both `values.schema.json` and the binary's startup
  validation. Postgres + Valkey are required. See
  [migrations.md](../../docs/operations/migrations.md#removing-memory-backend)
  for the operator migration recipes (baked / cnpg / external).
- **Default deployment shape changed.** A fresh `helm install`
  with no values overrides now brings up baked Postgres + baked
  Valkey StatefulSets, where chart `0.7.x` brought up only the
  Deployment. Set `store.postgres.mode=external` +
  `queue.valkey.mode=external` if you're pointing at managed
  infra.
- **`STORE_BACKEND` / `QUEUE_BACKEND` / `SCHEDULER_BACKEND` env
  vars are required.** Empty values fail validation; previous
  chart releases supplied defaults that mapped to memory/ticker.

### Upgrade notes (chart 0.5.0)

- **`terminationGracePeriodSeconds` raised to 60.** The Deployment
  now gives workers up to 60s to drain in-flight jobs on SIGTERM.
- **5 starter PrometheusRule alerts.** Set
  `prometheusRule.enabled=true` to render them; each is individually
  toggleable and threshold-tunable via
  `prometheusRule.alerts.<name>`.

### Upgrade notes (chart 0.6.0 / appVersion 1.7.0) — PR convergence

This release closes the INV-0005 drift gap. Existing repo-guardian
PRs that have every file rule satisfied on the default branch (e.g.
a maintainer hand-merged a CODEOWNERS file on a side branch) are
auto-closed on the next reconcile. The PR receives a final sticky
markdown-table comment summarising per-rule status, then closes,
then the reconcile branch is deleted.

- **Behaviour change opt-out.** Set `policy.autoClosePR: false` to
  preserve the legacy behaviour (PR stays open until a human closes
  it). Useful for compliance workflows that require manual
  PR-close attestation. The runtime override
  `AUTO_CLOSE_PR=false` (env on the Deployment) wins over the
  values file.
- **Sticky reconcile-log comment.** Every reconcile that touches an
  existing repo-guardian PR now posts (or edits) a sticky comment
  identified by the row-1 marker
  `<!-- repo-guardian:reconcile-log:v1 -->`. Operators can read
  the comment history to reconstruct what repo-guardian decided
  across the PR's lifetime.
- **Two new alerts.** `RepoGuardianStaleOpenPRs` fires when any
  PR has been open in the 30-day-plus bucket; `RepoGuardianPRDrift`
  fires when `pr_open_with_empty_actionable_total` rate is non-zero
  (indicates the convergence path failed). Both are starter alerts
  in the chart's PrometheusRule; tune via
  `prometheusRule.alerts.StaleOpenPRs.*` /
  `prometheusRule.alerts.PRDrift.*`.
- **Two new metrics.** `pr_orphan_left_total{org}` counts
  `Client.DeleteFile` failures during orphan cleanup;
  `prs_closed_total{org, reason="satisfied"}` counts auto-closures.
  PromQL recipes in
  [contrib/README.md](../../contrib/README.md).

For the operator-side upgrade runbook (smoke checks, opt-out,
rollback), see
[docs/operations/pr-convergence-migration.md](../../docs/operations/pr-convergence-migration.md).

## Verifying the chart

Every published version is signed with cosign (Sigstore keyless) via
the workflow's OIDC identity, plus a SLSA Level 3 provenance
attestation. Both can be verified offline against the public Sigstore
transparency log.

### Cosign signature

```bash
cosign verify \
  --certificate-identity-regexp \
    '^https://github.com/donaldgifford/repo-guardian/.+' \
  --certificate-oidc-issuer \
    'https://token.actions.githubusercontent.com' \
  ghcr.io/donaldgifford/charts/repo-guardian:1.0.0-rc.2
```

### SLSA provenance

```bash
cosign verify-attestation --type slsaprovenance \
  --certificate-identity-regexp \
    '^https://github.com/slsa-framework/slsa-github-generator/.+' \
  --certificate-oidc-issuer \
    'https://token.actions.githubusercontent.com' \
  ghcr.io/donaldgifford/charts/repo-guardian:1.0.0-rc.2
```

The provenance attestation records the build workflow path, source
commit SHA, and builder image — useful for downstream policy
enforcement.

## Releasing

### OCI publish to GHCR (default)

The chart publishes to `oci://ghcr.io/donaldgifford/charts/repo-guardian`
on every push to `main` that touches `charts/**`. The
`Chart Release (OCI)` workflow:

1. Reads `version` from `Chart.yaml`
2. Skips if that version already exists in the registry (idempotent)
3. Regenerates `CHANGELOG.md` via git-cliff filtered to
   `charts/**` so the published `.tgz` ships with a current changelog
4. Packages, pushes, signs with cosign keyless, and produces a SLSA
   provenance attestation

To cut a chart release: bump `version` (and optionally `appVersion`)
in `charts/repo-guardian/Chart.yaml`, merge to `main`, and the
workflow handles the rest.

For a manual smoke test on a feature branch, use
`workflow_dispatch` with `dry_run: true` — the job packages and
uploads the `.tgz` as a run artifact but skips the push and signing
steps.

### Yanking a chart version

**Don't delete-and-resurrect.** The publish workflow's idempotency
check is keyed on "tag exists in registry," not "tag has ever
existed," so manually deleting a version from GHCR will silently
re-publish on the next merge to `main`.

If a published version has a critical bug, **roll forward**: bump the
chart version (e.g., `0.3.0` → `0.3.1`) and republish a fixed chart.
Consumers who pinned to the broken version stay broken until they
upgrade — same model as the rest of the project (binary releases,
deployments).

### Publishing to ECR (alternative recipe)

The default workflow publishes only to GHCR. If you need ECR (or
another OCI registry) instead, see
[`docs/publishing-to-ecr.md`](docs/publishing-to-ecr.md) for a
manual / third-party-CI recipe. ECR fan-out is not wired into this
repo's CI.

## Security considerations

### `templating.vars` and the `env` helper

`templating.vars` (added in chart 0.4.0) injects arbitrary key/value
pairs as environment variables on the binary's container. Combined
with the `env "VAR"` template helper, this gives operators a clean
way to thread per-deployment context (Jira project, owning team,
etc.) into PR titles and bodies.

**The `env` helper is unrestricted by design.** Any env var
visible to the binary's process — including chart-managed values
like `GITHUB_APP_ID`, secret material like `GITHUB_PRIVATE_KEY` and
`WEBHOOK_SECRET`, and anything attached via `extraEnv` — is
readable from policy templates. The threat model assumes the
operator who writes the policy HCL is the same operator who
provisions runtime secrets, so reading those secrets back from
templates is not privilege escalation. **However**, rendered PR
text is visible to anyone with read access to the target
repository. Never reference secret env vars from PR templates:

```hcl
# DON'T:
defaults {
  pr {
    body = "deployed with key {{ env \"GITHUB_PRIVATE_KEY\" }}"
  }
}

# DO: keep secrets out of PR text entirely.
defaults {
  pr {
    body = "Project: {{ env \"JIRA_PROJECT\" }}"   # non-secret
  }
}
```

The chart's reserved-name list (`_helpers.tpl`) blocks
`templating.vars` from re-declaring chart-managed env keys
(`GITHUB_APP_ID`, `WEBHOOK_SECRET`, `STRICT_TEMPLATES`, etc.) at
helm-render time, but the `env` helper itself can read whatever
the OS gives it.

### Repository discovery and rate-limit budgeting (IMPL-0015)

`repo-guardian` 1.0.0-rc.2+ ships two cooperating schedulers on the
leader pod:

- **`Discoverer`** (`scheduler/discoverer.go`) enumerates installations
  + repos via the GitHub API at `DISCOVERY_INTERVAL` and persists
  discovery rows via `Store.UpsertIfMissing` — atomic
  `INSERT ... ON CONFLICT DO NOTHING` so concurrent webhooks +
  Discoverer never race. Newly-seeded rows enter `repo_state` with a
  jittered `LastCheckedAt` so a fleet onboarding doesn't cluster every
  repo's due-time at the same instant. Webhook-driven discovery
  (`installation_repositories.added`, `repository.created`,
  `installation.created`) is the primary on-ramp; the periodic
  Discoverer is the safety net for missed deliveries.
- **`StaleSweeper`** (`checker/sweep.go`) queries Postgres for rows
  whose `last_checked_at` is older than `RECONCILE_FRESHNESS` or whose
  `policy_version` differs from the current one, and enqueues each to
  the Valkey work queue. There is no full-fleet enumeration on every
  tick — the legacy `sweep` schedule was removed in 1.0.0-rc.2.

Both schedulers consult a shared **`BudgetTracker`** before each
enqueue. The tracker caches GitHub's rate-limit window per
installation and refuses to enqueue when `remaining < limit ×
reserveFraction`. When it blocks an enqueue,
`repo_guardian_enqueue_gated_by_budget_total{installation_id}` ticks
up; the starter alert `RepoGuardianBudgetGated` fires after 30
minutes of sustained gating so operators know the deployment is
rate-limit-bound. Tuning levers: `discovery.reserveFraction`
(default `0.20`), `discovery.estimatedCostPerRepo` (default `10`),
`staleSweep.freshness` (default `24h`). See
[`docs/operations/scaling.md`](../../docs/operations/scaling.md#discoverer--budgettracker-impl-0015-phase-1)
for the full metrics catalogue + alert tuning notes.

### Editing a template ConfigMap invalidates the policy version

`policy.Version` (the hash that gates stale-sweep re-enqueue) is
computed over BOTH the HCL policy AND the loaded template bodies.
Editing an entry in `templates.files` or in a ConfigMap referenced
via `templates.existingConfigMap` therefore changes the policy
hash on the next pod restart, which in turn marks every persisted
`repo_state` row as drifted (`policy_version <> current`) and
triggers the stale-sweeper to re-enqueue all repos on the next
sweep tick.

This is intentional — operators should observe
`repos_checked_total` climb across all in-scope repos within one
`SCHEDULE_INTERVAL` window after a template edit + redeploy. If
that does not happen, suspect either:

- The pod did not pick up the new ConfigMap content (Helm
  ConfigMap mounts are not auto-reloaded; the rollout must
  recreate the pod), or
- `STORE_BACKEND` is unset / not `postgres` (the hash is only
  consumed on the Postgres-backed sweep path).

### `STRICT_TEMPLATES` recommendation

Enable `templating.strict: true` (or set `STRICT_TEMPLATES=true`
on the Deployment env) in CI and production to validate compiled
PR templates against a zero-value `PRVars` context at startup.
This catches `.Catalog.X` references that work in the file-content
template path but fail in the PR-text path, surface as
location-prefixed errors at boot rather than at the first
incoming webhook.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules |
| config | object | `{"appId":"","dryRun":false,"logLevel":"info","metricsPort":9090,"port":8080,"queueSize":100,"scheduleInterval":"168h","skipArchived":true,"skipForks":true,"workerCount":5}` | repo-guardian application configuration (env vars) |
| config.appId | string | `""` | GitHub App ID |
| config.dryRun | bool | `false` | Dry run mode |
| config.logLevel | string | `"info"` | Log level (debug, info, warn, error) |
| config.metricsPort | int | `9090` | Metrics listen port |
| config.port | int | `8080` | Webhook listen port |
| config.queueSize | int | `100` | Queue size for check queue |
| config.scheduleInterval | string | `"168h"` | Reconciliation schedule interval (Go duration) |
| config.skipArchived | bool | `true` | Skip archived repositories |
| config.skipForks | bool | `true` | Skip forked repositories |
| config.workerCount | int | `5` | Worker count for check queue |
| discovery | object | `{"enabled":true,"estimatedCostPerRepo":10,"interval":"1h","reserveFraction":0.2}` | Repository discovery (IMPL-0015 Phase 1). The Discoverer runs on the leader pod, enumerates installations + repos via the GitHub API, and persists discovery rows via Store.UpsertIfMissing so the stale-sweeper picks them up on the next tick. Webhook-driven discovery (`installation_repositories.added` + `repository.created`) is the primary on-ramp; the periodic Discoverer is the safety net for missed deliveries. |
| discovery.enabled | bool | `true` | Toggle the Discoverer schedule. When false the binary still responds to discovery via webhooks; only the periodic enumeration path is disabled. |
| discovery.estimatedCostPerRepo | int | `10` | Operator estimate of the rate-limit cost of a single reconcile. Drives the BudgetTracker's spendable-enqueue accounting. Must be > 0. |
| discovery.interval | string | `"1h"` | Cadence between Discoverer.Discover invocations. Lower values burn more API budget on list_installations + list_installation_repos; higher values delay discovery of repos the webhook path missed. |
| discovery.reserveFraction | float | `0.2` | Fraction of the rate-limit budget the BudgetTracker holds in reserve when gating discovery enqueues. Must be in [0, 1]. |
| extraEnv | list | `[]` | Additional environment variables |
| extraVolumeMounts | list | `[]` | Additional volume mounts |
| extraVolumes | list | `[]` | Additional volumes |
| fullnameOverride | string | `""` | Override the full release name |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/donaldgifford/repo-guardian"` | Container image repository |
| image.tag | string | `""` | Overrides the image tag (default: appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets |
| livenessProbe.httpGet.path | string | `"/healthz"` |  |
| livenessProbe.httpGet.port | string | `"http"` |  |
| livenessProbe.initialDelaySeconds | int | `5` |  |
| livenessProbe.periodSeconds | int | `15` |  |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` | Node selector |
| podAnnotations | object | `{}` | Pod annotations |
| podLabels | object | `{}` | Pod labels |
| podSecurityContext | object | `{}` | Pod security context |
| policy | object | `{"autoClosePR":true,"config":"","existingConfigMap":""}` | HCL policy configuration |
| policy.autoClosePR | bool | `true` | Auto-close repo-guardian PRs when every file rule is satisfied on the default branch (IMPL-0013 Phase 3). When `true` (default), the PR is closed with a sticky comment and the reconcile branch is deleted. When `false`, the PR stays open until a human closes it. |
| policy.config | string | `""` | Inline HCL policy config (creates a ConfigMap) |
| policy.existingConfigMap | string | `""` | Use an existing ConfigMap for policy config |
| prometheusRule | object | `{"alerts":{},"enabled":false,"labels":{}}` | Prometheus PrometheusRule with starter alerts (IMPL-0011 P6). |
| prometheusRule.alerts | object | `{}` | Per-alert overrides: each key under `alerts.<name>` accepts `for`, `severity`, `threshold`, and `enabled`. See the rendered PrometheusRule template for the canonical alert names. |
| prometheusRule.enabled | bool | `false` | Create PrometheusRule with starter alerts. |
| prometheusRule.labels | object | `{}` | Additional labels (e.g., to match Prometheus operator `ruleSelector`). |
| queue | object | `{"backend":"valkey","size":1000,"valkey":{"baked":{"authPasswordLength":32,"image":"valkey/valkey:9.1","storageClassName":"","storageSize":"1Gi"},"existingSecret":"","existingSecretKey":"QUEUE_VALKEY_DSN","jobAckTimeout":"5m","mode":"baked","reaperInterval":"60s"}}` | Work queue for reconcile jobs. The in-memory backend was removed in IMPL-0016 (chart 1.0); valkey is the only supported value. |
| queue.backend | string | `"valkey"` | Backend implementation. Only "valkey" is supported. |
| queue.size | deprecated | `1000` | Buffered channel size; ignored since the in-memory backend was removed in IMPL-0016. Retained for values-schema backwards compatibility; will be deleted in a future chart-major release. |
| queue.valkey | object | `{"baked":{"authPasswordLength":32,"image":"valkey/valkey:9.1","storageClassName":"","storageSize":"1Gi"},"existingSecret":"","existingSecretKey":"QUEUE_VALKEY_DSN","jobAckTimeout":"5m","mode":"baked","reaperInterval":"60s"}` | Valkey-specific configuration. Ignored when backend != valkey. |
| queue.valkey.baked | object | `{"authPasswordLength":32,"image":"valkey/valkey:9.1","storageClassName":"","storageSize":"1Gi"}` | Baked Valkey-only configuration. |
| queue.valkey.baked.authPasswordLength | int | `32` | Generated AUTH password length (random alphanumeric). |
| queue.valkey.baked.image | string | `"valkey/valkey:9.1"` | Pinned image. Bump intentionally. |
| queue.valkey.baked.storageClassName | string | `""` | StorageClass name. Empty → cluster default. |
| queue.valkey.baked.storageSize | string | `"1Gi"` | Persistent volume size. |
| queue.valkey.existingSecret | string | `""` | Operator-supplied secret holding QUEUE_VALKEY_DSN. |
| queue.valkey.existingSecretKey | string | `"QUEUE_VALKEY_DSN"` | Key inside existingSecret holding the DSN. |
| queue.valkey.jobAckTimeout | string | `"5m"` | How long an in-flight job may sit before the reaper requeues it. |
| queue.valkey.mode | string | `"baked"` | Source of the Valkey deployment. One of: "baked"    — chart renders a single-pod Valkey Deployment; "external" — operator provides DSN via existingSecret. |
| queue.valkey.reaperInterval | string | `"60s"` | Cadence between reaper iterations. |
| readinessProbe.httpGet.path | string | `"/readyz"` |  |
| readinessProbe.httpGet.port | string | `"http"` |  |
| readinessProbe.initialDelaySeconds | int | `5` |  |
| readinessProbe.periodSeconds | int | `10` |  |
| replicaCount | int | `1` | Number of replicas |
| resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | Container resource requests and limits |
| revisionHistoryLimit | int | `3` | Number of old ReplicaSets retained for rollback. Defaults to 3 to keep the kubectl `get rs` view tidy; bump if you need more rollback headroom. Kubernetes default is 10. |
| scheduler | object | `{"backend":"valkey"}` | Scheduler (sweep cadence) backend. The single-replica ticker scheduler was removed in IMPL-0016 (chart 1.0); valkey is the only supported value. Scheduler shares the queue's Valkey instance. |
| scheduler.backend | string | `"valkey"` | Backend implementation. Only "valkey" is supported. |
| secrets | object | `{"create":true,"existingSecret":"","privateKey":"","privateKeyAsFile":true,"webhookSecret":""}` | GitHub App secrets |
| secrets.create | bool | `true` | Create secret resource (false = use existing secret) |
| secrets.existingSecret | string | `""` | Name of existing secret (when create=false) |
| secrets.privateKey | string | `""` | GitHub App private key (PEM format) |
| secrets.privateKeyAsFile | bool | `true` | Mount private key as file (true) or env var (false) |
| secrets.webhookSecret | string | `""` | GitHub webhook secret |
| securityContext | object | `{"readOnlyRootFilesystem":true,"runAsNonRoot":true,"runAsUser":65534}` | Container security context |
| service.httpPort | int | `80` | Webhook HTTP port |
| service.metricsPort | int | `9090` | Metrics port |
| service.type | string | `"ClusterIP"` | Service type |
| serviceAccount.annotations | object | `{}` | Annotations for the ServiceAccount |
| serviceAccount.create | bool | `true` | Create a ServiceAccount |
| serviceAccount.name | string | `""` | Override the ServiceAccount name |
| serviceMonitor.enabled | bool | `false` | Create Prometheus ServiceMonitor |
| serviceMonitor.interval | string | `"30s"` | Scrape interval |
| serviceMonitor.labels | object | `{}` | Additional labels for ServiceMonitor |
| staleSweep | object | `{"batchSize":200,"freshness":"24h","rateLimitReserve":0.1}` | Stale-sweep tuning (IMPL-0011 Phase 5d). |
| staleSweep.batchSize | int | `200` | Cap on rows returned per StaleRepos query. |
| staleSweep.freshness | string | `"24h"` | Maximum age of a stored last_checked_at before the sweep requeues. Default 24h. Effective only with store.backend=postgres. |
| staleSweep.rateLimitReserve | float | `0.1` | Fraction of an installation's GitHub rate-limit budget reserved against the stale-sweep enqueue path. |
| store | object | `{"backend":"postgres","postgres":{"baked":{"image":"postgres:18.4","resources":{"limits":{"cpu":"1000m","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}},"storageClassName":"","storageSize":"10Gi"},"cnpg":{"imageName":"ghcr.io/cloudnative-pg/postgresql:18.4","instances":1,"pooler":{"enabled":false,"instances":1,"monitoring":{"enablePodMonitor":false},"pgbouncer":{"defaultPoolSize":25,"maxClientConnections":100,"parameters":{},"poolMode":"transaction"},"service":{"annotations":{},"enabled":false,"labels":{"bgp.cilium.io/advertise-service":"default","bgp.cilium.io/ip-pool":"default"},"type":"LoadBalancer"},"type":"rw"},"storage":{"size":"10Gi","storageClass":""}},"existingSecret":"","existingSecretKey":"STORE_DSN","maxConns":16,"mode":"baked"}}` | Persistent state store (per-repo reconcile state). See DESIGN-0012 §Backend modes. The in-memory backend was removed in IMPL-0016 (chart 1.0); postgres is the only supported value. |
| store.backend | string | `"postgres"` | Backend implementation. Only "postgres" is supported. |
| store.postgres | object | `{"baked":{"image":"postgres:18.4","resources":{"limits":{"cpu":"1000m","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}},"storageClassName":"","storageSize":"10Gi"},"cnpg":{"imageName":"ghcr.io/cloudnative-pg/postgresql:18.4","instances":1,"pooler":{"enabled":false,"instances":1,"monitoring":{"enablePodMonitor":false},"pgbouncer":{"defaultPoolSize":25,"maxClientConnections":100,"parameters":{},"poolMode":"transaction"},"service":{"annotations":{},"enabled":false,"labels":{"bgp.cilium.io/advertise-service":"default","bgp.cilium.io/ip-pool":"default"},"type":"LoadBalancer"},"type":"rw"},"storage":{"size":"10Gi","storageClass":""}},"existingSecret":"","existingSecretKey":"STORE_DSN","maxConns":16,"mode":"baked"}` | Postgres-specific configuration. Ignored when backend != postgres. |
| store.postgres.baked | object | `{"image":"postgres:18.4","resources":{"limits":{"cpu":"1000m","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}},"storageClassName":"","storageSize":"10Gi"}` | Baked Postgres-only configuration. |
| store.postgres.baked.image | string | `"postgres:18.4"` | Pinned image. Bump intentionally. |
| store.postgres.baked.resources | object | `{"limits":{"cpu":"1000m","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}}` | Resource requests/limits for the Postgres container. |
| store.postgres.baked.storageClassName | string | `""` | StorageClass name. Empty → cluster default. |
| store.postgres.baked.storageSize | string | `"10Gi"` | Persistent volume size. |
| store.postgres.cnpg | object | `{"imageName":"ghcr.io/cloudnative-pg/postgresql:18.4","instances":1,"pooler":{"enabled":false,"instances":1,"monitoring":{"enablePodMonitor":false},"pgbouncer":{"defaultPoolSize":25,"maxClientConnections":100,"parameters":{},"poolMode":"transaction"},"service":{"annotations":{},"enabled":false,"labels":{"bgp.cilium.io/advertise-service":"default","bgp.cilium.io/ip-pool":"default"},"type":"LoadBalancer"},"type":"rw"},"storage":{"size":"10Gi","storageClass":""}}` | CloudNativePG-only configuration. |
| store.postgres.cnpg.imageName | string | `"ghcr.io/cloudnative-pg/postgresql:18.4"` | CNPG-managed Postgres image. |
| store.postgres.cnpg.instances | int | `1` | Number of CNPG instances. |
| store.postgres.cnpg.pooler | object | `{"enabled":false,"instances":1,"monitoring":{"enablePodMonitor":false},"pgbouncer":{"defaultPoolSize":25,"maxClientConnections":100,"parameters":{},"poolMode":"transaction"},"service":{"annotations":{},"enabled":false,"labels":{"bgp.cilium.io/advertise-service":"default","bgp.cilium.io/ip-pool":"default"},"type":"LoadBalancer"},"type":"rw"}` | Connection pooler (PgBouncer). Disabled by default. |
| store.postgres.cnpg.pooler.instances | int | `1` | Pooler replica count. |
| store.postgres.cnpg.pooler.monitoring.enablePodMonitor | bool | `false` | Render a `PodMonitor` for the pooler. Requires the Prometheus Operator CRD. |
| store.postgres.cnpg.pooler.pgbouncer.defaultPoolSize | int | `25` | PgBouncer `default_pool_size`. |
| store.postgres.cnpg.pooler.pgbouncer.maxClientConnections | int | `100` | PgBouncer `max_client_conn`. |
| store.postgres.cnpg.pooler.pgbouncer.parameters | object | `{}` | Extra `pgbouncer.ini` parameters (key-value pairs). |
| store.postgres.cnpg.pooler.pgbouncer.poolMode | string | `"transaction"` | PgBouncer pool mode: `session`, `transaction`, or `statement`. |
| store.postgres.cnpg.pooler.service.annotations | object | `{}` | Extra Service annotations. |
| store.postgres.cnpg.pooler.service.enabled | bool | `false` | Enable an external LoadBalancer Service in front of the pooler. |
| store.postgres.cnpg.pooler.service.labels | object | `{"bgp.cilium.io/advertise-service":"default","bgp.cilium.io/ip-pool":"default"}` | Service labels. Defaults wire Cilium BGP IP advertisement. |
| store.postgres.cnpg.pooler.service.type | string | `"LoadBalancer"` | Service `type` (usually `LoadBalancer`). |
| store.postgres.cnpg.pooler.type | string | `"rw"` | Pooler type: `rw` (primary) or `ro` (read-only replicas). |
| store.postgres.cnpg.storage | object | `{"size":"10Gi","storageClass":""}` | Storage block. |
| store.postgres.existingSecret | string | `""` | Operator-supplied secret holding STORE_DSN. Required when mode=external. |
| store.postgres.existingSecretKey | string | `"STORE_DSN"` | Key inside existingSecret holding the DSN. Default: STORE_DSN. |
| store.postgres.maxConns | int | `16` | Connection cap for the pgx pool. |
| store.postgres.mode | string | `"baked"` | Source of the Postgres deployment. One of: "baked"    — chart renders a single-pod Postgres Deployment; "cnpg"     — chart renders a CloudNativePG `Cluster` CR; "external" — operator provides DSN via existingSecret. |
| tailscale | object | `{"authKeySecret":"tailscale-auth","enabled":false,"hostname":"repo-guardian","image":"ghcr.io/tailscale/tailscale:latest","rbac":{"create":true},"userspace":true}` | Tailscale Funnel sidecar |
| tailscale.authKeySecret | string | `"tailscale-auth"` | Name of existing secret containing 'authkey' |
| tailscale.enabled | bool | `false` | Enable Tailscale sidecar container |
| tailscale.hostname | string | `"repo-guardian"` | Tailscale hostname (becomes <hostname>.<tailnet>.ts.net) |
| tailscale.image | string | `"ghcr.io/tailscale/tailscale:latest"` | Tailscale container image |
| tailscale.rbac | object | `{"create":true}` | Create RBAC for Tailscale state management |
| tailscale.userspace | bool | `true` | Use userspace networking (no CAP_NET_ADMIN needed) |
| templates | object | `{"existingConfigMap":"","files":{}}` | File-template overrides for the binary's TemplateStore.  Breaking change in chart 0.4.0: the legacy `templates.codeowners`, `templates.dependabot`, and `templates.renovate` slots have been removed. Move existing values into `templates.files` keyed by filename (with `.tmpl` suffix), e.g.:    templates:     files:       codeowners.tmpl: |         * @platform-team       dependabot.tmpl: |         version: 2         updates: ...  When `templates.existingConfigMap` is non-empty the chart skips rendering its own ConfigMap and the Deployment mounts the named ConfigMap at TEMPLATE_DIR. This is the escape hatch for operators who manage templates out-of-band (GitOps, Argo ApplicationSet, etc). |
| templates.existingConfigMap | string | `""` | Name of an existing ConfigMap to mount at TEMPLATE_DIR instead of rendering one from `templates.files`. |
| templates.files | object | `{}` | Map of filename to template content. Each entry becomes a key in the rendered ConfigMap and is loaded by the binary at startup. Filenames must end in `.tmpl`. |
| templating | object | `{"strict":false,"vars":{}}` | Templating configuration: env-var injection and strict-mode validation.  `templating.vars` exposes arbitrary environment variables to the binary's `env "VAR"` template helper. Values flow through to the Deployment's container env list; they are NOT secrets — use `secrets.*` or `extraEnv` (with valueFrom: secretKeyRef) for secret material. The chart rejects keys that collide with chart-managed env vars (GITHUB_APP_ID, WEBHOOK_SECRET, etc).  `templating.strict` toggles `STRICT_TEMPLATES=true` on the Deployment. When enabled the binary validates every compiled PR template against a zero-value PRVars context at startup and fails fast on missing-field references. |
| templating.strict | bool | `false` | Enable startup-time strict validation of compiled PR templates (sets STRICT_TEMPLATES=true on the Deployment). |
| templating.vars | object | `{}` | Map of env-var key to value. Keys must not collide with chart-managed env vars; the chart fails template rendering on collisions. |
| tolerations | list | `[]` | Tolerations |
| webhookIPAllowlist | object | `{"enabled":true,"failOpen":false,"trustProxyHeaders":false}` | Webhook IP allowlist configuration |
| webhookIPAllowlist.enabled | bool | `true` | Enable GitHub IP allowlist middleware |
| webhookIPAllowlist.failOpen | bool | `false` | Allow requests when allowlist unavailable |
| webhookIPAllowlist.trustProxyHeaders | bool | `false` | Trust X-Forwarded-For proxy headers |

