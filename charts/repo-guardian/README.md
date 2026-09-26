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
  --version 2.0.0-rc.0 \
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
  --version 2.0.0-rc.0 \
  --namespace repo-guardian \
  --create-namespace \
  -f values.yaml
```

## Prerequisites

- Kubernetes 1.28+
- Helm 3.14+ or 4.x (OCI support)
- A Temporal cluster the pods can reach (`temporal.address`); see
  `contrib/temporal/` for a pinned install
- A registered [GitHub App](https://docs.github.com/en/apps/creating-github-apps) with:
  - **Permissions:** Contents (Read & Write), Pull Requests (Read & Write), Metadata (Read)
  - **Events:** `repository`, `installation_repositories`, `installation`, and `push` (needed for `watch = true` reconcilers)

## Quick Start

1. Create a values file with your GitHub App credentials:

```yaml
config:
  appId: "YOUR_APP_ID"
  dryRun: true  # Start in dry-run mode

temporal:
  address: temporal-frontend.temporal.svc.cluster.local:7233

policy:
  config: |
    guardian {}

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
  --version 2.0.0-rc.0 \
  --namespace repo-guardian \
  --create-namespace \
  -f values.yaml
```

3. Once verified, set `config.dryRun: false` to enable live mode.

## Choosing a deployment shape

Chart 2.0.0 runs repo-guardian as roles on Temporal (DESIGN-0026).
`topology` picks how the roles are packaged:

| Topology | Renders | Use when |
|----------|---------|----------|
| `split` (default) | `ingest` (webhooks, 2 replicas + PDB), `worker` (workflows and activities, optional KEDA), and with `api.enabled` an `api` Deployment (ClusterIP + PDB) | Production: each role scales and fails on its own, and each holds only its own credentials. |
| `all` | One Deployment running every role; the API listens on `api.port` | Homelab and small installs. |

Each role holds only what it needs: the GitHub App key goes to
`worker` and `all`, the webhook secret to `ingest` and `all`, the
Temporal mTLS Secret (`temporal.tls.existingSecret`) to every role
that dials Temporal, and `api` gets a read-only database login and
nothing else.

The store is still Postgres, in one of three modes:

| Mode | Values | Use when |
|------|--------|----------|
| **Baked** (default) | `store.postgres.mode=baked` | No external operator dependencies. A single-pod Postgres StatefulSet with a generated password; homelab and dev clusters. |
| **CNPG** | `store.postgres.mode=cnpg` | You run [CloudNativePG](https://cloudnative-pg.io/) and want it to own Postgres lifecycle. Renders a `Cluster` CR and an optional `Pooler`. |
| **External** | `store.postgres.mode=external` + `existingSecret` | Managed Postgres (RDS, Cloud SQL). The chart renders no database. |

### The API's read-only role

The split `api` role reads through `repoguardian_ro` (DESIGN-0027):

- **baked:** an init script creates it on new volumes, and a
  `post-install,post-upgrade` hook Job creates it on existing ones,
  resets its password and re-runs the grants. With
  `store.postgres.baked.existingSecret`, put its password under
  `existingSecretROKey` (default `RO_PASSWORD`).
- **CNPG:** a managed role in `pg_read_all_data`. With one instance
  the API connects through `-rw`, since `-ro` has no endpoints.
- **external:** create the role yourself and set
  `api.roDsn.existingSecret`; the render fails without it.

In `all` the API reads with `STORE_DSN`.

### The UI

`ui.enabled` renders the business UI (DESIGN-0027): a Deployment,
Service and PDB running `ghcr.io/donaldgifford/repo-guardian-ui` at
the chart's `appVersion`. It proxies `/api` to the `api` Service and
signs users in against `api.auth.issuer`.

- **Requires** `api.enabled` and `api.auth.enabled`; the render fails
  otherwise.
- **Secret** `ui.existingSecret` with `oidc-client-secret` and
  `session-keys` (comma-separated, each at least 32 bytes; the first
  seals new sessions, any opens them, so rotate by prepending).
- **Ingress.** `ui.ingress.enabled` renders the chart's only Ingress,
  sending the whole `ui.ingress.host` to the UI. It fails render unless
  `api.auth.enabled`. The webhook route stays yours
  (`docs/operations/ingress.md`).
- The UI holds no GitHub, Temporal or database credential.

### Migrations

`migrate.enabled` (default `true`) runs `repo-guardian migrate` as a
hook Job: `post-install` (so a baked Postgres exists to connect to) and
`pre-upgrade` (so the schema lands before new pods roll). On an adopted
v1 database it also runs the one-time backfill.

### Mode-scoped secret knobs

Each `existingSecret` value is read by exactly one mode:
`store.postgres.existingSecret` only in `external` mode and
`store.postgres.baked.existingSecret` only in `baked` mode. Setting one
under any other mode fails the render with an error naming the right
knob.

### Schema validation

`values.schema.json` rejects removed values by name, before any pod
starts. The render-time `validateRemovedValues` guard repeats the check
for installs that skip schema validation.

For sizing, see [docs/operations/scaling.md](../../docs/operations/scaling.md).
For Postgres schema operations, see
[docs/operations/migrations.md](../../docs/operations/migrations.md).

### Upgrade notes (chart 2.0.0) — Temporal runtime, breaking

- **Temporal replaces Valkey.** `queue.*`, `scheduler.*`,
  `staleSweep.*`, `posture.exportInterval` and
  `config.{workerCount,queueSize,scheduleInterval,maxJobAttempts}` are
  removed and fail the render. Use `worker.concurrency`,
  `checkInterval` and `migrate.freshness`. The baked Valkey StatefulSet
  is no longer rendered; delete its PVC once you have cut over.
- **`temporal.address` and a policy are required.** The worker will not
  start without `GUARDIAN_CONFIG`.
- **Roles.** The default `topology: split` replaces the single
  Deployment. The webhook Service keeps the release's full name, so
  ingress routes survive.
- **Pods roll on policy edits.** `checksum/policy` and
  `checksum/templates` annotations roll every role that reads the
  policy; the worker then spreads the re-check across
  `policyRolloutWindow`.

The full runbook is
[docs/operations/v2-migration.md](../../docs/operations/v2-migration.md).

### Upgrade notes (chart 1.0.0 / appVersion 1.14.0) — operator-owned ingress, breaking

IMPL-0024 (DESIGN-0023). The chart no longer ships any ingress
machinery — the baked Tailscale sidecar and the in-app webhook IP
allowlist are both gone. Source-IP enforcement belongs at the edge
layer you already operate (ALB security groups + the GitHub prefix
list, Tailscale ACLs, an ngrok traffic policy, a Cloudflare WAF
rule); HMAC signature validation is the app-layer defense.

- **Removed values:** the entire `tailscale.*` and
  `webhookIPAllowlist.*` blocks. A values file still setting either
  fails at render time with a migration message — delete the block
  and pick an ingress option from
  [docs/operations/ingress.md](../../docs/operations/ingress.md#migrating-from-the-baked-sidecar).
- **Removed env vars:** `WEBHOOK_IP_ALLOWLIST`,
  `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN`, `TRUST_PROXY_HEADERS`. The
  binary warns at startup if they are still set (and ignores them);
  the `guardian {}` HCL attributes of the same names now fail policy
  load under the strict decode.
- **Metric repointed:** `webhook_rejected_total` now counts HMAC
  signature rejections (`reason="signature"`) — its only previous
  producer was the deleted allowlist. Dashboards keep working; the
  panel now answers "wrong or rotated webhook secret", not
  "unexpected source IP" (that signal lives at your edge layer).
- **Why the allowlist had to go, not just the sidecar:** it trusted
  the leftmost `X-Forwarded-For` entry, which the client controls —
  behind every documented proxy topology it was spoofable
  (INV-0016). Removing it is a security improvement, not a loss.

### Upgrade notes (chart 1.0.0-rc.12 / appVersion 1.13.0) — compliance posture

IMPL-0023. Compliance is now **state** rather than a set of counters,
and the observability surface is generated from your policy rather
than hand-maintained.

- **New posture gauges.** `repos_actionable{rule_name,org}`,
  `repos_tracked{org}` and `repos_unmeasurable{org,reason}` are
  published by the elected leader from the `rule_state` table. They
  answer "how many repositories are failing this rule right now",
  which no counter ever could: a counter only grows, so a repository
  fixed yesterday still shows in its rate.
- **Query them with `max by (...)`, never `sum`.** A demoted replica
  keeps serving its last values until it restarts, so `sum`
  double-counts through every failover. A fleet total is
  `sum(max by (org) (...))` — in that order; reversed it silently
  reports the largest org. See
  [scaling.md](../../docs/operations/scaling.md#compliance-posture-impl-0023-phases-12).
- **New values:** `posture.exportInterval` (default `60s`) and
  `posture.snapshotInterval` (default `24h`). The first is how stale
  the gauges may be; the second is the cadence of compliance-history
  rows for `repo-guardian report`. Neither costs GitHub API budget.
- **New alert `RepoGuardianPostureExportStalled`.** The one failure
  the compliance gauges cannot report about themselves — a frozen
  exporter keeps serving last week's numbers with total confidence.
- **`RepoGuardianNoSchedulerLeader` was fixed, not changed.** It
  watched `scheduler_is_leader{name="sweep"}`, a schedule deleted in
  IMPL-0015. An `== 0` comparison against an empty vector is empty, so
  it could not fire at all. It now watches `name="stale-sweep"`.
  **Expect it to start firing if your scheduler genuinely has no
  leader** — that is the alert working for the first time.
- **`RepoGuardianRepoAccessDenied` and
  `RepoGuardianPropertySchemaMissing` gained a second disjunct.**
  `increase()` cannot see a counter's first-ever increment, so a fleet
  where one repository loses access would never have alerted. Same
  fix, same reason: an alert that cannot fire reads as a healthy
  fleet.
- **Four metrics removed.** The unlabelled `properties_checked_total`,
  `properties_set_total`, `properties_already_correct_total` and
  `properties_prs_created_total` are gone. The last one **folded into
  `prs_created_total{org}`**, so a `github-action`-mode deployment
  will see that counter step up after upgrading — reconciler PRs were
  never counted there before, and should have been. Migration table in
  [contrib/README.md](../../contrib/README.md).
- **Dashboards are generated.** `repo-guardian monitoring generate
  --config guardian.hcl` emits four dashboards and the alerts your
  policy actually engages, as plain files or as grafana-operator CRs.
  The 61-panel hand-maintained dashboard is deleted.

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

### Upgrade notes (chart 1.0.0-rc.6) — schema preflight alert

Non-breaking, chart-only addition (IMPL-0017 Phase 4). Pairs with the
`annotation_properties` custom-property feature; no action needed if you
don't use it.

- **New starter alert.** `RepoGuardianPropertySchemaMissing` fires when
  `repo_guardian_custom_property_missing_schema_total` (per `org`,
  `property`) is non-zero for 30+ minutes — a mapped
  `annotation_properties` target has no matching custom-property
  definition in the org's schema. Tune via
  `prometheusRule.alerts.PropertySchemaMissing.*`.
- **Loki matching contract documented.** The exact warn-log text and
  structured keys (`org`, `missing_properties`) an operator can build a
  LogQL rule from, without reading Go source, are in
  [docs/operations/scaling.md](../../docs/operations/scaling.md#custom-property-schema-preflight-impl-0017-phase-3).

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
  ghcr.io/donaldgifford/charts/repo-guardian:2.0.0-rc.0
```

### SLSA provenance

```bash
cosign verify-attestation --type slsaprovenance \
  --certificate-identity-regexp \
    '^https://github.com/slsa-framework/slsa-github-generator/.+' \
  --certificate-oidc-issuer \
    'https://token.actions.githubusercontent.com' \
  ghcr.io/donaldgifford/charts/repo-guardian:2.0.0-rc.0
```

The provenance attestation records the build workflow path, source
commit SHA, and builder image — useful for downstream policy
enforcement.

## Releasing

### Per-registry publish workflows

`release.yml` orchestrates publishing on every push to `main`: after
the semver bump + goreleaser jobs it always calls `ghcr.yml` (and
`ecr.yml`, when the `ECR_PUBLISH_ENABLED` repository variable is
`true`) as reusable workflows. Each registry workflow has the same
shape:

- an **image** job gated on a non-empty release tag (so container
  images only publish on actual binary releases), and
- a **chart** job that runs on every push and is idempotent via a
  `helm pull` precheck — it skips when the `Chart.yaml` version is
  already in the registry.

The chart job regenerates `CHANGELOG.md` via git-cliff filtered to
`charts/**` (so the published `.tgz` ships with a current changelog),
packages, pushes, signs with cosign keyless, and attaches a SLSA
provenance attestation.

To cut a chart release: bump `version` (and optionally `appVersion`)
in `charts/repo-guardian/Chart.yaml`, merge to `main`, and the chart
job handles the rest. Chart-only changes (PR labeled `dont-release`)
still publish the chart because the chart job does not gate on the
binary release.

Each registry workflow also exposes `workflow_dispatch` with `tag` +
`dry_run` inputs for ad-hoc republishes. For a manual smoke test on a
feature branch, dispatch with `dry_run: true` — the job packages and
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

### Publishing to ECR

ECR publishing IS wired into this repo's CI via `ecr.yml`, but gated
off by default: set the `ECR_PUBLISH_ENABLED` repository variable to
`true` once the AWS-side prep (OIDC role, repositories, permissions)
in [docs/operations/ecr-publish-setup.md](../../docs/operations/ecr-publish-setup.md)
is complete. `ecr.yml` also remains directly invokable via
`workflow_dispatch` for standalone testing regardless of the gate.
For a manual / third-party-CI recipe, see
[`docs/publishing-to-ecr.md`](docs/publishing-to-ecr.md).

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

### Repository discovery and scheduling

Each repository has its own `RepoWorkflow` in Temporal: it checks the
repository, schedules its next check `checkInterval` later (jittered),
and waits on a durable timer. Webhooks signal it to re-check at once.
A per-installation `InstallationWorkflow` hands out the GitHub rate
budget; a check that finds the budget exhausted waits on a timer until
the reset rather than holding a worker. Discovery
(`discovery.interval`) enumerates installations and starts workflows
for repositories the webhooks missed.

### Editing a policy or template

The policy version hashes the HCL policy and the loaded template
bodies. Editing `policy.config` or `templates.files` changes the pod
checksums, so the roles roll; the new worker then signals every
repository to re-check at a random point inside
`policyRolloutWindow` (default `24h`) rather than all at once. A
ConfigMap you manage yourself (`policy.existingConfigMap`,
`templates.existingConfigMap`) is not hashed: roll the pods after
editing it.

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
| api | object | `{"auth":{"audience":"repo-guardian-api","enabled":true,"groupsClaim":"groups","issuer":"","nameClaim":"preferred_username"},"authz":{"clients":{},"defaultOrgs":[],"groups":{}},"enabled":false,"pdb":{"enabled":true,"minAvailable":1},"port":8081,"prStaleAfter":"720h","replicas":2,"resources":{},"roDsn":{"existingSecret":"","existingSecretKey":"STORE_RO_DSN"},"status":{"public":true}}` | The api role: the read-only JSON API (DESIGN-0027). It holds only a read-only database credential: no App key, no webhook secret. |
| api.auth.audience | string | `"repo-guardian-api"` | Expected token audience. |
| api.auth.enabled | bool | `true` | Require an OIDC bearer token on every data endpoint. |
| api.auth.groupsClaim | string | `"groups"` | Claim listing the caller's groups. |
| api.auth.issuer | string | `""` | OIDC issuer URL. Required when auth is enabled. |
| api.auth.nameClaim | string | `"preferred_username"` | Claim naming the caller in logs. |
| api.authz.clients | object | `{}` | Machine clients (by `azp`) → groups above, for IdPs that put no groups on client-credentials tokens. |
| api.authz.defaultOrgs | list | `[]` | Orgs granted to every authenticated caller. |
| api.authz.groups | object | `{}` | Group → org grants, rendered into the authz ConfigMap. Keys are group names; values list orgs (`["*"]` for every org). |
| api.enabled | bool | `false` | Render the api role (split) and its Service. In `all` the API always runs on `api.port`; this gates its Service and auth. |
| api.pdb.enabled | bool | `true` | Render a PodDisruptionBudget. |
| api.pdb.minAvailable | int | `1` | Pods kept through voluntary disruptions. |
| api.port | int | `8081` | API port beside other roles (`all`). The api role alone serves on `config.port`. |
| api.prStaleAfter | string | `"720h"` | Open repo-guardian PRs older than this count as stale. |
| api.replicas | int | `2` | Replica count (split only). |
| api.resources | object | `{}` | Resources; empty falls back to `resources`. |
| api.roDsn.existingSecret | string | `""` | Secret holding STORE_RO_DSN. Required for `store.postgres.mode: external`; baked and cnpg derive it from the chart-managed `repoguardian_ro` role when empty. |
| api.roDsn.existingSecretKey | string | `"STORE_RO_DSN"` | Key inside existingSecret. |
| api.status.public | bool | `true` | Serve /status without a token. |
| checkInterval | string | `"24h"` | Each repository's check cadence (Go duration). |
| checksRetention | string | `"2160h"` | How long check history is kept before SnapshotWorkflow prunes it. |
| config | object | `{"appId":"","dryRun":false,"logLevel":"info","metricsPort":9090,"port":8080,"skipArchived":true,"skipForks":true}` | repo-guardian application configuration (env vars) |
| config.appId | string | `""` | GitHub App ID |
| config.dryRun | bool | `false` | Dry run mode |
| config.logLevel | string | `"info"` | Log level (debug, info, warn, error) |
| config.metricsPort | int | `9090` | Metrics listen port |
| config.port | int | `8080` | Webhook listen port |
| config.skipArchived | bool | `true` | Skip archived repositories |
| config.skipForks | bool | `true` | Skip forked repositories |
| discovery | object | `{"enabled":true,"interval":"1h"}` | Repository discovery. The discovery schedule enumerates every installation's repositories and starts a RepoWorkflow for each new one. Webhooks (`installation_repositories.added`, `repository.created`) are the primary on-ramp; this is the safety net for missed deliveries. |
| discovery.enabled | bool | `true` | Toggle the discovery schedule. When false, webhooks still discover repositories; only the periodic enumeration stops. |
| discovery.interval | string | `"1h"` | Cadence between discovery runs. Lower values spend more API budget on list_installation_repos; higher values delay discovery of repositories the webhook path missed. |
| extraEnv | list | `[]` | Additional environment variables |
| extraVolumeMounts | list | `[]` | Additional volume mounts |
| extraVolumes | list | `[]` | Additional volumes |
| fullnameOverride | string | `""` | Override the full release name |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/donaldgifford/repo-guardian"` | Container image repository |
| image.tag | string | `""` | Overrides the image tag (default: appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets |
| ingest | object | `{"pdb":{"enabled":true,"minAvailable":1},"replicas":2,"resources":{}}` | The ingest role: receives webhooks and signals workflows. Stateless. |
| ingest.pdb.enabled | bool | `true` | Render a PodDisruptionBudget. |
| ingest.pdb.minAvailable | int | `1` | Pods kept through voluntary disruptions. |
| ingest.replicas | int | `2` | Replica count (split only). |
| ingest.resources | object | `{}` | Resources; empty falls back to `resources`. |
| livenessProbe.httpGet.path | string | `"/healthz"` |  |
| livenessProbe.httpGet.port | string | `"http"` |  |
| livenessProbe.initialDelaySeconds | int | `5` |  |
| livenessProbe.periodSeconds | int | `15` |  |
| migrate.activeDeadlineSeconds | int | `600` | Upper bound on the Job's run time, in seconds. The v1 backfill is one transaction; raise this for very large fleets. |
| migrate.backoffLimit | int | `6` | Job retries before the release fails. On a first install the Job may start before Postgres accepts connections; retries cover it. |
| migrate.enabled | bool | `true` | Run `repo-guardian migrate` as a hook Job (v2 schema migrations and the one-time v1 backfill): post-install, so a baked Postgres exists to connect to, and pre-upgrade, so the schema lands before new pods roll. |
| migrate.freshness | string | `"24h"` | The v1 backfill's freshness: a v1 repo checked within this window keeps its due-time; older ones are due immediately after cutover. |
| migrate.resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | Resources for the migrate container. |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` | Node selector |
| podAnnotations | object | `{}` | Pod annotations |
| podLabels | object | `{}` | Pod labels |
| podSecurityContext | object | `{}` | Pod security context |
| policy | object | `{"autoClosePR":true,"config":"","existingConfigMap":"","orphanCleanup":true}` | HCL policy configuration |
| policy.autoClosePR | bool | `true` | Auto-close repo-guardian PRs when every file rule is satisfied on the default branch (IMPL-0013 Phase 3). When `true` (default), the PR is closed with a sticky comment and the reconcile branch is deleted. When `false`, the PR stays open until a human closes it. |
| policy.config | string | `""` | Inline HCL policy config (creates a ConfigMap) |
| policy.existingConfigMap | string | `""` | Use an existing ConfigMap for policy config |
| policy.orphanCleanup | bool | `true` | Remove files from repo-guardian's own reconcile branch once the rule that added them is satisfied on the default branch (IMPL-0013 Phase 3). When `true` (default), a PR stops proposing files that are no longer needed. When `false`, no file is ever deleted from the reconcile branch and PR bodies may list rules already satisfied on the default branch.  This is a kill switch, not a tuning knob: orphan cleanup is the only path that deletes files, and INV-0014 is a fixed defect in it that produced PRs proposing to remove files repositories legitimately owned. Leave it enabled unless you have reason not to. |
| policyRolloutWindow | string | `"24h"` | Span a policy change is spread across: every repository is re-checked at a random point inside it, not all at once. |
| posture | object | `{"snapshotInterval":"24h"}` | Compliance history (IMPL-0023 / DESIGN-0022). SnapshotWorkflow writes one compliance_history row per org on this cadence. |
| posture.snapshotInterval | string | `"24h"` | Cadence between compliance-history snapshots, the rows `repo-guardian report --since` reads. This is not the posture gauges: Prometheus already keeps those for its retention window, typically weeks, and this table exists for "how compliant were we last quarter", which no operational metrics store can answer. Daily is already finer than any question the report asks, and the rows are permanent — there is no retention machinery — so shortening it accrues storage forever for resolution nobody requested. |
| prometheusRule | object | `{"alerts":{},"enabled":false,"labels":{}}` | Prometheus PrometheusRule with starter alerts (IMPL-0011 P6). |
| prometheusRule.alerts | object | `{}` | Per-alert overrides: each key under `alerts.<name>` accepts `for`, `severity`, `threshold`, and `enabled`. See the rendered PrometheusRule template for the canonical alert names. |
| prometheusRule.enabled | bool | `false` | Create PrometheusRule with starter alerts. |
| prometheusRule.labels | object | `{}` | Additional labels (e.g., to match Prometheus operator `ruleSelector`). |
| readinessProbe.httpGet.path | string | `"/readyz"` |  |
| readinessProbe.httpGet.port | string | `"http"` |  |
| readinessProbe.initialDelaySeconds | int | `5` |  |
| readinessProbe.periodSeconds | int | `10` |  |
| replicaCount | int | `1` | Number of replicas |
| resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | Container resource requests and limits |
| revisionHistoryLimit | int | `3` | Number of old ReplicaSets retained for rollback. Defaults to 3 to keep the kubectl `get rs` view tidy; bump if you need more rollback headroom. Kubernetes default is 10. |
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
| store | object | `{"backend":"postgres","postgres":{"baked":{"existingSecret":"","existingSecretKey":"POSTGRES_PASSWORD","existingSecretROKey":"RO_PASSWORD","image":"postgres:18.4","podSecurityContext":{"fsGroup":999,"fsGroupChangePolicy":"OnRootMismatch","runAsGroup":999,"runAsNonRoot":true,"runAsUser":999},"resources":{"limits":{"cpu":"1000m","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}},"storageClassName":"","storageSize":"10Gi"},"cnpg":{"imageName":"ghcr.io/cloudnative-pg/postgresql:18.4","instances":1,"pooler":{"enabled":false,"instances":1,"monitoring":{"enablePodMonitor":false},"pgbouncer":{"defaultPoolSize":25,"maxClientConnections":100,"parameters":{},"poolMode":"transaction"},"service":{"annotations":{},"enabled":false,"labels":{"bgp.cilium.io/advertise-service":"default","bgp.cilium.io/ip-pool":"default"},"type":"LoadBalancer"},"type":"rw"},"storage":{"size":"10Gi","storageClass":""}},"existingSecret":"","existingSecretKey":"STORE_DSN","maxConns":16,"mode":"baked"}}` | Persistent state store (per-repo reconcile state). See DESIGN-0012 §Backend modes. The in-memory backend was removed in IMPL-0016 (chart 1.0); postgres is the only supported value. |
| store.backend | string | `"postgres"` | Backend implementation. Only "postgres" is supported. |
| store.postgres | object | `{"baked":{"existingSecret":"","existingSecretKey":"POSTGRES_PASSWORD","existingSecretROKey":"RO_PASSWORD","image":"postgres:18.4","podSecurityContext":{"fsGroup":999,"fsGroupChangePolicy":"OnRootMismatch","runAsGroup":999,"runAsNonRoot":true,"runAsUser":999},"resources":{"limits":{"cpu":"1000m","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}},"storageClassName":"","storageSize":"10Gi"},"cnpg":{"imageName":"ghcr.io/cloudnative-pg/postgresql:18.4","instances":1,"pooler":{"enabled":false,"instances":1,"monitoring":{"enablePodMonitor":false},"pgbouncer":{"defaultPoolSize":25,"maxClientConnections":100,"parameters":{},"poolMode":"transaction"},"service":{"annotations":{},"enabled":false,"labels":{"bgp.cilium.io/advertise-service":"default","bgp.cilium.io/ip-pool":"default"},"type":"LoadBalancer"},"type":"rw"},"storage":{"size":"10Gi","storageClass":""}},"existingSecret":"","existingSecretKey":"STORE_DSN","maxConns":16,"mode":"baked"}` | Postgres-specific configuration. Ignored when backend != postgres. |
| store.postgres.baked | object | `{"existingSecret":"","existingSecretKey":"POSTGRES_PASSWORD","existingSecretROKey":"RO_PASSWORD","image":"postgres:18.4","podSecurityContext":{"fsGroup":999,"fsGroupChangePolicy":"OnRootMismatch","runAsGroup":999,"runAsNonRoot":true,"runAsUser":999},"resources":{"limits":{"cpu":"1000m","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}},"storageClassName":"","storageSize":"10Gi"}` | Baked Postgres-only configuration. |
| store.postgres.baked.existingSecret | string | `""` | Operator-supplied Secret holding the Postgres password. When set, the chart does NOT generate its own password Secret; the baked StatefulSet reads the password from this Secret and the app assembles STORE_DSN at runtime via $(POSTGRES_PASSWORD). Use this for GitOps/ArgoCD: the chart's default `lookup`-based password preservation returns nothing under `helm template`, so the generated password rotates on every sync and drifts from the already-initialised data directory (auth failures). Password must be URL-safe (alphanumeric) — it is interpolated into the DSN URL. |
| store.postgres.baked.existingSecretKey | string | `"POSTGRES_PASSWORD"` | Key inside existingSecret holding the password. |
| store.postgres.baked.existingSecretROKey | string | `"RO_PASSWORD"` | Key inside existingSecret holding the api role's read-only password (`repoguardian_ro`). Read only for a split API with no `api.roDsn.existingSecret`. URL-safe, like the admin password. |
| store.postgres.baked.image | string | `"postgres:18.4"` | Pinned image. Bump intentionally. |
| store.postgres.baked.podSecurityContext | object | `{"fsGroup":999,"fsGroupChangePolicy":"OnRootMismatch","runAsGroup":999,"runAsNonRoot":true,"runAsUser":999}` | Pod securityContext. Runs Postgres as the image's built-in non-root user (uid/gid 999) so the entrypoint skips its root-only chown of PGDATA (which fails on clusters/storage that forbid chown, e.g. restrictive admission policies or NFS root_squash); fsGroup makes kubelet set volume group ownership instead. Set to `{}` to restore the image default (root entrypoint + chown). |
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
| templates | object | `{"existingConfigMap":"","files":{}}` | File-template overrides for the binary's TemplateStore.  Breaking change in chart 0.4.0: the legacy `templates.codeowners`, `templates.dependabot`, and `templates.renovate` slots have been removed. Move existing values into `templates.files` keyed by filename (with `.tmpl` suffix), e.g.:    templates:     files:       codeowners.tmpl: |         * @platform-team       dependabot.tmpl: |         version: 2         updates: ...  When `templates.existingConfigMap` is non-empty the chart skips rendering its own ConfigMap and the Deployment mounts the named ConfigMap at TEMPLATE_DIR. This is the escape hatch for operators who manage templates out-of-band (GitOps, Argo ApplicationSet, etc). |
| templates.existingConfigMap | string | `""` | Name of an existing ConfigMap to mount at TEMPLATE_DIR instead of rendering one from `templates.files`. |
| templates.files | object | `{}` | Map of filename to template content. Each entry becomes a key in the rendered ConfigMap and is loaded by the binary at startup. Filenames must end in `.tmpl`. |
| templating | object | `{"strict":false,"vars":{}}` | Templating configuration: env-var injection and strict-mode validation.  `templating.vars` exposes arbitrary environment variables to the binary's `env "VAR"` template helper. Values flow through to the Deployment's container env list; they are NOT secrets — use `secrets.*` or `extraEnv` (with valueFrom: secretKeyRef) for secret material. The chart rejects keys that collide with chart-managed env vars (GITHUB_APP_ID, WEBHOOK_SECRET, etc).  `templating.strict` toggles `STRICT_TEMPLATES=true` on the Deployment. When enabled the binary validates every compiled PR template against a zero-value PRVars context at startup and fails fast on missing-field references. |
| templating.strict | bool | `false` | Enable startup-time strict validation of compiled PR templates (sets STRICT_TEMPLATES=true on the Deployment). |
| templating.vars | object | `{}` | Map of env-var key to value. Keys must not collide with chart-managed env vars; the chart fails template rendering on collisions. |
| temporal | object | `{"address":"","namespace":"repo-guardian","taskQueue":"repo-guardian","tls":{"existingSecret":"","serverName":""}}` | Temporal connection. Every role that dials Temporal (ingest, worker, all) gets these; api never does. |
| temporal.address | string | `""` | Frontend `host:port`. Required. |
| temporal.namespace | string | `"repo-guardian"` | Temporal namespace. |
| temporal.taskQueue | string | `"repo-guardian"` | Task queue the worker polls and ingest signals through. |
| temporal.tls.existingSecret | string | `""` | Secret with `tls.crt`, `tls.key` and (optionally) `ca.crt` for mTLS. Mounted only into roles that dial Temporal. |
| temporal.tls.serverName | string | `""` | Server name to verify the frontend certificate against. |
| tolerations | list | `[]` | Tolerations |
| topology | string | `"split"` | Deployment shape (DESIGN-0026 § Chart 2.0.0). `split` renders one Deployment per role: `ingest` (webhooks), `worker` (Temporal worker) and, when `api.enabled`, `api`. `all` renders a single Deployment running every role, for small installs and the homelab. |
| ui | object | `{"enabled":false,"existingSecret":"","githubHost":"github.com","image":{"repository":"ghcr.io/donaldgifford/repo-guardian-ui","tag":""},"ingress":{"annotations":{},"className":"","enabled":false,"host":"","tls":[]},"oidc":{"clientId":"repo-guardian-ui","scopes":["openid","profile","groups","offline_access"]},"pdb":{"enabled":true,"minAvailable":1},"port":3000,"publicUrl":"","replicas":2,"resources":{"limits":{"memory":"256Mi"},"requests":{"cpu":"50m","memory":"96Mi"}},"sessionTTL":"8h"}` | The business UI (DESIGN-0027): a Bun BFF serving the SPA, proxying /api to the api Service and holding the OIDC session. Requires api.enabled and api.auth.enabled. |
| ui.enabled | bool | `false` | Render the ui Deployment, Service and PDB. |
| ui.existingSecret | string | `""` | Secret with keys `oidc-client-secret` and `session-keys` (comma-separated, at least 32 bytes each; the first seals). Required. |
| ui.githubHost | string | `"github.com"` | GitHub host PR and repository links point at. |
| ui.image.repository | string | `"ghcr.io/donaldgifford/repo-guardian-ui"` | UI image repository. |
| ui.image.tag | string | `""` | UI image tag; defaults to the chart's appVersion, because both images are released together from one tag. |
| ui.ingress.annotations | object | `{}` | Annotations for the Ingress. |
| ui.ingress.className | string | `""` | IngressClass name. |
| ui.ingress.enabled | bool | `false` | Render an Ingress sending the whole host to the ui Service. The only Ingress the chart renders; fails render unless api.auth.enabled. |
| ui.ingress.host | string | `""` | The UI's host name. |
| ui.ingress.tls | list | `[]` | TLS blocks, as in networking.k8s.io/v1 IngressTLS. |
| ui.oidc.clientId | string | `"repo-guardian-ui"` | OIDC client ID (a confidential client). The issuer is api.auth.issuer: the UI and the API trust the same IdP. |
| ui.oidc.scopes | list | `["openid","profile","groups","offline_access"]` | Scopes requested at login. |
| ui.pdb.enabled | bool | `true` | Render a PodDisruptionBudget. |
| ui.pdb.minAvailable | int | `1` | Pods kept through voluntary disruptions. |
| ui.port | int | `3000` | Container port the BFF listens on. |
| ui.publicUrl | string | `""` | Browser-facing origin (https://host). Defaults to https://<ui.ingress.host>; set it when the ingress is yours. |
| ui.replicas | int | `2` | Replica count. |
| ui.resources | object | `{"limits":{"memory":"256Mi"},"requests":{"cpu":"50m","memory":"96Mi"}}` | Resources for the ui container. |
| ui.sessionTTL | string | `"8h"` | Absolute session length; token refresh never extends it. |
| worker | object | `{"concurrency":10,"keda":{"enabled":false,"maxReplicas":4,"minReplicas":1,"targetQueueSize":"50"},"replicas":1,"resources":{}}` | The worker role: runs workflows and activities (split only). |
| worker.concurrency | int | `10` | Concurrent activities per pod (WORKER_ACTIVITY_CONCURRENCY). |
| worker.keda | object | `{"enabled":false,"maxReplicas":4,"minReplicas":1,"targetQueueSize":"50"}` | KEDA autoscaling on Temporal backlog. Requires the KEDA CRDs. |
| worker.keda.enabled | bool | `false` | Render a ScaledObject with a `temporal` trigger. |
| worker.keda.maxReplicas | int | `4` | Ceiling. Size it to the GitHub budget, not the backlog: checks are rate-limit bound, so pods past the budget only park more timers (INV-0018 Obs 7). |
| worker.keda.minReplicas | int | `1` | Floor. |
| worker.keda.targetQueueSize | string | `"50"` | Backlog per replica before KEDA scales out. |
| worker.replicas | int | `1` | Replica count. Ignored when `worker.keda.enabled`. |
| worker.resources | object | `{}` | Resources; empty falls back to `resources`. |

