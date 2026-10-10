# Temporal for repo-guardian v2

Reference configuration for the Temporal cluster repo-guardian v2 runs
on (DESIGN-0026 § Temporal deployment). It layers values onto the
upstream [`temporalio/helm-charts`](https://github.com/temporalio/helm-charts)
chart plus a few supporting manifests. It is **not** part of the
repo-guardian chart: Temporal is shared infrastructure with its own
lifecycle.

| Pin | Version |
| --- | --- |
| `temporal` chart | `1.7.0` (`TEMPORAL_CHART_VERSION` in the Makefile) |
| Temporal server / admin-tools | `1.32.0` (floor: **1.31**) |

## Files

| File | Kind | Purpose |
| --- | --- | --- |
| `values-base.yaml` | values | pinned server version, frontend + internode mTLS, `matching.enableFairness`, resources |
| `persistence-cnpg.yaml` | manifest | CNPG `Cluster` for Temporal's stores, separate from repo-guardian's Postgres |
| `values-persistence-cnpg.yaml` | values | points the default store at that cluster |
| `visibility-postgres.yaml` | values | **baked (default):** SQL visibility in its own database on the same CNPG cluster |
| `visibility-opensearch-baked.yaml` | values | **baked:** in-cluster OpenSearch 2.x |
| `visibility-external.yaml` | values | **external:** Elasticsearch 7/8 or OpenSearch 2.x (Amazon: 2.19) |
| `namespace-job.yaml` | manifest | one-shot, idempotent `temporal operator namespace create repo-guardian --retention 7d` |
| `networkpolicy.yaml` | manifest | frontend reachable only from Temporal itself and labelled repo-guardian namespaces |

Pick exactly one `visibility-*.yaml`.

## Install

Prerequisites: the CloudNativePG operator, and three
`kubernetes.io/tls` Secrets in the `temporal` namespace issued from one
CA (for example by cert-manager), each with `ca.crt`:
`temporal-internode-tls` (SAN `temporal-internode`),
`temporal-frontend-tls` (SAN `temporal-frontend`) and
`temporal-admin-tls` (a client certificate for the namespace Job).
repo-guardian gets its own client certificate from the same CA.

```bash
kubectl create namespace temporal
kubectl -n temporal apply -f contrib/temporal/persistence-cnpg.yaml

helm upgrade --install temporal temporal \
  --repo https://go.temporal.io/helm-charts --version 1.7.0 \
  --namespace temporal \
  -f contrib/temporal/values-base.yaml \
  -f contrib/temporal/values-persistence-cnpg.yaml \
  -f contrib/temporal/visibility-postgres.yaml

kubectl -n temporal apply -f contrib/temporal/namespace-job.yaml
kubectl -n temporal apply -f contrib/temporal/networkpolicy.yaml
kubectl label namespace repo-guardian repo-guardian.io/temporal-client=true
```

### OpenSearch (baked)

Install OpenSearch 2.x before the Temporal chart, pinned — 3.x is not in
Temporal's documented support matrix until homelab testing verifies it
(DESIGN-0026 OQ9):

```bash
helm upgrade --install opensearch opensearch \
  --repo https://opensearch-project.github.io/helm-charts --version 2.36.0 \
  --namespace temporal
```

Chart `2.36.0` ships OpenSearch `2.19.4`.

Then create `temporal-opensearch` (key `password`) and
`temporal-opensearch-ca` (key `ca.crt`) and use
`visibility-opensearch-baked.yaml`.

## Alternative: edge TLS and OIDC

The homelab dev cluster runs a different client-auth shape, and the
repo-guardian chart supports it (`temporal.auth.oidc.*`). TLS ends at a gRPC
ingress (for example `temporal-grpc.example.com:443`), and every call
carries a bearer token that Temporal's JWT authorizer checks. There are
no frontend, internode or admin certificates. Layer it on with
`server.config.authorization` in place of the `tls` block in
`values-base.yaml`:

```yaml
server:
  config:
    authorization:
      jwtKeyProvider:
        keySourceURIs:
          - https://<idp>/realms/<realm>/protocol/openid-connect/certs
        refreshInterval: 5m
      audience: temporal
      permissionsClaimName: permissions
      authorizer: default
      claimMapper: default
```

- **The token.** The client-credentials client repo-guardian uses (the
  chart's `temporal.auth.oidc.clientId`, its secret mounted as a file) must
  issue tokens with audience `temporal`, carrying a `permissions` claim
  that the default claim mapper reads as `<namespace>:<role>`, for
  example `repo-guardian:writer`.
- **Internal traffic.** Keep the chart's internal-frontend enabled.
  Temporal's own services and admin-tools reach the server through it
  without a token.
- **KEDA.** Under OIDC the chart refuses `keda.trigger: temporal`
  (INV-0020), so autoscaling uses the default `prometheus` trigger.
- **Reachability.** The authorizer is now the boundary, so the ingress
  may sit outside the cluster. `networkpolicy.yaml` still applies to
  in-cluster callers.

This is the interim shape. The target is DESIGN-0028: mTLS from an
OpenBao-issued client CA *and* a JWT on every call.

## Checking a change

```bash
make lint-temporal-contrib
```

renders the chart against every visibility mode (it fails if a mode
stops rendering, loses a server service, or drops the fairness flag)
and parses the three manifests.

## Why these choices

- **Separate Postgres.** Temporal's persistence traffic and upgrades
  never contend with repo-guardian's findings store.
- **Fairness on.** `InstallationWorkflow` sets a fairness key per
  installation so one large org cannot starve the rest (DESIGN-0026 OQ4).
- **mTLS plus a NetworkPolicy.** Open-source Temporal has no
  per-namespace authorization by default, so reachability is the
  boundary (OQ11).
- **7-day retention.** Enough to debug a check; Postgres holds the
  durable record.
