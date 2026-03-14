# repo-guardian

GitHub App that automates repository onboarding and compliance

## Installation

```bash
helm repo add repo-guardian https://donaldgifford.github.io/repo-guardian
helm repo update
helm install repo-guardian repo-guardian/repo-guardian \
  --namespace platform-tools \
  --create-namespace \
  -f values-prod.yaml
```

### From OCI Registry

```bash
helm install repo-guardian \
  oci://YOUR_REGISTRY/helm-charts/repo-guardian \
  --version 0.1.0 \
  --namespace platform-tools \
  --create-namespace \
  -f values-prod.yaml
```

## Prerequisites

- Kubernetes 1.28+
- Helm 3.14+
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
helm install repo-guardian repo-guardian/repo-guardian \
  --namespace platform-tools \
  --create-namespace \
  -f values.yaml
```

3. Once verified, set `config.dryRun: false` to enable live mode.

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
| templates | object | `{"codeowners":"","dependabot":"","renovate":""}` | Template overrides for CODEOWNERS, dependabot, renovate |
| templates.codeowners | string | `""` | Custom CODEOWNERS template (empty = use embedded default) |
| templates.dependabot | string | `""` | Custom dependabot template |
| templates.renovate | string | `""` | Custom renovate template |
| tolerations | list | `[]` | Tolerations |
| webhookIPAllowlist | object | `{"enabled":true,"failOpen":false,"trustProxyHeaders":false}` | Webhook IP allowlist configuration |
| webhookIPAllowlist.enabled | bool | `true` | Enable GitHub IP allowlist middleware |
| webhookIPAllowlist.failOpen | bool | `false` | Allow requests when allowlist unavailable |
| webhookIPAllowlist.trustProxyHeaders | bool | `false` | Trust X-Forwarded-For proxy headers |

