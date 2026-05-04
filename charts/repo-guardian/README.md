# repo-guardian

GitHub App that automates repository onboarding and compliance

## Installation

The chart is published as an OCI artifact at
`oci://ghcr.io/donaldgifford/charts/repo-guardian` (public, signed
with cosign keyless, SLSA Level 3 provenance).

```bash
helm install repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 0.4.0 \
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
  --version 0.4.0 \
  --namespace repo-guardian \
  --create-namespace \
  -f values.yaml
```

3. Once verified, set `config.dryRun: false` to enable live mode.

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
  ghcr.io/donaldgifford/charts/repo-guardian:0.4.0
```

### SLSA provenance

```bash
cosign verify-attestation --type slsaprovenance \
  --certificate-identity-regexp \
    '^https://github.com/slsa-framework/slsa-github-generator/.+' \
  --certificate-oidc-issuer \
    'https://token.actions.githubusercontent.com' \
  ghcr.io/donaldgifford/charts/repo-guardian:0.4.0
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
| config | object | `{"appId":"","dryRun":false,"logLevel":"info","metricsPort":9090,"org":"","port":8080,"queueSize":100,"scheduleInterval":"168h","skipArchived":true,"skipForks":true,"workerCount":5}` | repo-guardian application configuration (env vars) |
| config.appId | string | `""` | GitHub App ID |
| config.dryRun | bool | `false` | Dry run mode |
| config.logLevel | string | `"info"` | Log level (debug, info, warn, error) |
| config.metricsPort | int | `9090` | Metrics listen port |
| config.org | string | `""` | GitHub organization name |
| config.port | int | `8080` | Webhook listen port |
| config.queueSize | int | `100` | Queue size for check queue |
| config.scheduleInterval | string | `"168h"` | Reconciliation schedule interval (Go duration) |
| config.skipArchived | bool | `true` | Skip archived repositories |
| config.skipForks | bool | `true` | Skip forked repositories |
| config.workerCount | int | `5` | Worker count for check queue |
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
| policy | object | `{"config":"","existingConfigMap":""}` | HCL policy configuration |
| policy.config | string | `""` | Inline HCL policy config (creates a ConfigMap) |
| policy.existingConfigMap | string | `""` | Use an existing ConfigMap for policy config |
| readinessProbe.httpGet.path | string | `"/readyz"` |  |
| readinessProbe.httpGet.port | string | `"http"` |  |
| readinessProbe.initialDelaySeconds | int | `5` |  |
| readinessProbe.periodSeconds | int | `10` |  |
| replicaCount | int | `1` | Number of replicas |
| resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | Container resource requests and limits |
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

