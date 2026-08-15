# `deploy/` has moved to the Helm chart

The Kustomize base + overlays that used to live at `deploy/base/` and
`deploy/overlays/{dev,prod,tailscale}` were removed in **IMPL-0014
Phase 2** (2026-05). The Helm chart at `charts/repo-guardian/` has been
the supported deployment surface since IMPL-0004 (chart 0.1.0) and is
the only path that tracks the current binary.

This file is a six-month tombstone. **It will be deleted on or after
2026-11-30.** If you land here after that date, see the chart README
directly: [`charts/repo-guardian/README.md`](../charts/repo-guardian/README.md).

## Install

```bash
helm install repo-guardian oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 0.6.0 \
  --namespace repo-guardian \
  --create-namespace \
  --values your-values.yaml
```

Pin a specific version with `--version X.Y.Z`. The chart is OCI-only as
of IMPL-0010; there is no `helm repo add` step.

## Old overlay → chart values map

The deleted overlays each baked a set of opinionated tweaks on top of
`deploy/base/`. Their equivalents in `values.yaml` are:

### `dev`

The `dev` overlay set debug logging, single-replica, dry-run mode, and
exposed the webhook on a NodePort. Equivalent chart values:

```yaml
replicaCount: 1
env:
  LOG_LEVEL: debug
  DRY_RUN: "true"
service:
  type: NodePort
```

### `prod`

The `prod` overlay scaled to two replicas, set resource requests/limits,
and turned dry-run off. Equivalent chart values:

```yaml
replicaCount: 2
env:
  LOG_LEVEL: info
  DRY_RUN: "false"
resources:
  requests:
    cpu: 100m
    memory: 256Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

For multi-replica deployments, also configure the durable backends
documented under `docs/operations/scaling.md` — `store.postgres.mode`,
`queue.valkey.mode`, and `scheduler.backend=valkey`.

### `tailscale`

The `tailscale` overlay published the webhook via Tailscale Funnel and
forwarded client IPs through `X-Forwarded-For`. There is no values
equivalent anymore: the in-app IP allowlist and its
`TRUST_PROXY_HEADERS` / `WEBHOOK_IP_ALLOWLIST` env vars were removed
in IMPL-0024 (chart `1.0.0`), and ingress is operator-owned. Set up
the Funnel `Ingress` (or another edge) per
[`docs/operations/ingress.md`](../docs/operations/ingress.md) and
point it at the chart's `Service`.

## See also

- [`charts/repo-guardian/README.md`](../charts/repo-guardian/README.md) — full chart values reference
- [`docs/operations/scaling.md`](../docs/operations/scaling.md) — multi-replica recipe
- [DESIGN-0011](../docs/design/0011-publish-helm-chart-via-oci-registry.md) — OCI distribution rationale
- [DESIGN-0014](../docs/design/0014-remove-legacy-engine-path-and-deprecated-overlays.md) — removal rationale
