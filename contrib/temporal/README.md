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
| `values-base.yaml` | values | pinned server version, frontend + internode mTLS with a 1m certificate refresh, JWT authorization on the external frontend, the internal frontend, `matching.enableFairness`, resources |
| `persistence-cnpg.yaml` | manifest | CNPG `Cluster` for Temporal's stores, separate from repo-guardian's Postgres |
| `values-persistence-cnpg.yaml` | values | points the default store at that cluster |
| `visibility-postgres.yaml` | values | **baked (default):** SQL visibility in its own database on the same CNPG cluster |
| `visibility-opensearch-baked.yaml` | values | **baked:** in-cluster OpenSearch 2.x |
| `visibility-external.yaml` | values | **external:** Elasticsearch 7/8 or OpenSearch 2.x (Amazon: 2.19) |
| `namespace-job.yaml` | manifest | one-shot, idempotent `temporal operator namespace create repo-guardian --retention 7d`, through the internal frontend |
| `networkpolicy.yaml` | manifest | external frontend reachable only from Temporal itself and labelled repo-guardian namespaces; internal frontend from the `temporal` namespace only |

Pick exactly one `visibility-*.yaml`.

## Install

Prerequisites:

- The CloudNativePG operator.
- Two `kubernetes.io/tls` Secrets in the `temporal` namespace, each
  with `ca.crt` (for example from cert-manager):
  `temporal-internode-tls` (SAN `temporal-internode`, server and client
  auth: the namespace Job presents it too) and `temporal-frontend-tls`
  (SAN `temporal-frontend`).
- The CA that issues repo-guardian's client certificates (DESIGN-0028:
  OpenBao's PKI engine behind a cert-manager Vault issuer) in the
  frontend's `clientCaFiles`. If it is not the CA in
  `temporal-frontend-tls`'s `ca.crt`, mount it and list it under
  `server.config.tls.frontend.server.clientCaFiles`.
- An IdP for the JWT authorizer: Keycloak or Okta. Set
  `server.config.authorization.jwtKeyProvider.keySourceURIs` to its
  JWKS URL, and `audience` to this environment's own value
  (`temporal-dev`, `temporal-prod`, ...), so a token minted for one
  environment is refused by another even when they trust the same IdP.
  repo-guardian's client needs a `permissions` claim of
  `repo-guardian:write`.

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

## Checking a change

```bash
make lint-temporal-contrib
```

renders the chart against every visibility mode (it fails if a mode
stops rendering, loses a server service, drops the fairness flag, the
JWT authorizer, the TLS refresh or the internal frontend) and parses
the three manifests.

## Upgrading an mTLS-only install

Before DESIGN-0028 the external frontend authorized every client with a
trusted certificate. With `authorization` on, a client without a valid
token is refused, so:

1. Create the IdP client for repo-guardian (`permissions` claim
   `repo-guardian:write`, this environment's audience) and set
   `temporal.auth.oidc.*` in the repo-guardian chart **before** the
   Temporal upgrade; the token is ignored until the authorizer is on.
2. If KEDA scales the worker, switch to `worker.keda.trigger:
   prometheus`: KEDA's temporal trigger cannot present a token.
3. Upgrade Temporal with this `values-base.yaml`, re-apply
   `namespace-job.yaml` and `networkpolicy.yaml`, and delete the
   unused `temporal-admin-tls` Secret.

See [docs/operations/temporal-client-auth.md](../../docs/operations/temporal-client-auth.md).

## Why these choices

- **Separate Postgres.** Temporal's persistence traffic and upgrades
  never contend with repo-guardian's findings store.
- **Fairness on.** `InstallationWorkflow` sets a fairness key per
  installation so one large org cannot starve the rest (DESIGN-0026 OQ4).
- **mTLS and a NetworkPolicy bound reachability; the JWT authorizer
  bounds rights.** Open-source Temporal's default claim mapper ignores
  the certificate subject, so a certificate alone grants no
  per-namespace rights: it only lets a client connect. The token's
  `permissions` claim decides what it may do in which namespace
  (DESIGN-0026 OQ11, DESIGN-0028).
- **An internal frontend for admin traffic.** It skips the authorizer,
  so the server's own services and the namespace Job need no token,
  and the NetworkPolicy keeps it inside the `temporal` namespace.
- **7-day retention.** Enough to debug a check; Postgres holds the
  durable record.
