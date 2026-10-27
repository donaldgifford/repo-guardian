# Running repo-guardian on AWS

How to run repo-guardian v2 on EKS with the AWS-managed pieces around
it: RDS or Aurora for Postgres, Secrets Manager and the External
Secrets Operator (ESO) for credentials, and an AWS load balancer for
the webhook route. Nothing in the binary is AWS-aware. The work is in
provisioning the managed services and getting their credentials into
Kubernetes Secrets that the chart references.

Read [Turning on repo-guardian v2](v2-onboarding.md) first. This page
covers only what is different on AWS. [Scaling repo-guardian](scaling.md)
covers sizing.

If you provision with `libtftest-tf-modules/modules/rds/*`, the
variable-level companion to this page is
[External Postgres on RDS — Terraform variable reference](rds-terraform-variables.md):
settings for `instance` / `cluster` / `serverless`, each with and
without `proxy`.

> [!NOTE]
> Valkey (and ElastiCache with it) was removed in v2.
> There is no queue or scheduler backend: Temporal holds every workflow,
> timer and schedule.
> If you are coming from 1.x, delete the ElastiCache cluster once the
> cutover in [Moving to repo-guardian v2](v2-migration.md) is done.

## What runs where

| Piece | On AWS | Owned by |
| --- | --- | --- |
| `ingest`, `evaluator`, `remediator`, `api`, `ui` | EKS, one Deployment per role (`topology: split`) | this chart |
| Postgres (the controls database) | RDS for PostgreSQL or Aurora PostgreSQL, ideally behind RDS Proxy | you |
| Temporal | Temporal Cloud, or a self-hosted cluster (for example on EKS with its own RDS database) | you |
| Credentials | Secrets Manager, synced into Kubernetes Secrets by ESO | you |
| Webhook route | an ALB or NLB through the AWS Load Balancer Controller | you |
| Images and chart | GHCR (public), or the private ECR mirror | maintainers publish, you pull |

## Temporal

Temporal is required and is not part of this chart. repo-guardian is a
client of it: the workers dial the frontend, nothing more. Running
Temporal (its own persistence database, upgrades, retention) is the
operator's job, the same way the database is.

What repo-guardian needs from it is listed in
[2. A Temporal cluster](v2-onboarding.md#2-a-temporal-cluster): server
1.31 or newer, a namespace, reachability on port 7233 from the
repo-guardian namespace, and mTLS or OIDC credentials. On AWS that
usually means one more security-group rule (EKS nodes to the Temporal
frontend) and one more Secret.

KEDA's default trigger reads the Temporal **server's**
`approximate_backlog_count` from Prometheus. If your Prometheus does
not scrape the Temporal server (a managed Temporal, for example), use
`keda.trigger: temporal` with mTLS, or set `evaluator.keda.query` /
`remediator.keda.query` to a query your metrics can answer. See
[Autoscaling with KEDA](v2-onboarding.md#autoscaling-with-keda). For
Amazon Managed Service for Prometheus, point
`keda.prometheus.serverAddress` at its query endpoint and name your
SigV4 TriggerAuthentication in `keda.prometheus.authenticationRef`.

## Postgres on AWS

### Option matrix

| Service | Best for | Notes |
|---|---|---|
| **RDS for PostgreSQL** (provisioned) | Predictable steady-state load | Cheapest per hour for a known load. Manual instance-class sizing. Multi-AZ for HA. |
| **Aurora Serverless v2** | Spiky or idle load | ACU-based scaling. repo-guardian is mostly idle between evaluations, which suits it, but only with RDS Proxy in front. |
| **Aurora PostgreSQL** (provisioned) | HA and read scaling | Separate writer and reader endpoints. The api role's read-only DSN can use the reader endpoint (see [Reader endpoint for the API](#reader-endpoint-for-the-api)). |

All three speak plain Postgres, so `internal/store/postgres` works
unchanged. The decision is operational, not architectural.

**Version and parameter parity with the chart.** The chart's two
managed shapes both pin Postgres 18 (`store.postgres.baked.image:
postgres:18.4`, `store.postgres.cnpg.imageName: ...postgresql:18.4`).
Pin the RDS `engine_version` to the same major so dev and test on the
chart-managed shapes run the major production runs. Neither chart mode
sets custom Postgres parameters, so default parameter groups are
correct. Details:
[Parameter groups and engine version](rds-terraform-variables.md#parameter-groups-and-engine-version).

The database must be **empty** before the first migration: the controls
schema chain refuses a database that already holds an older schema.

### Database users

v2 does not connect as one user. Column-level grants are the boundary
between the evaluator and the remediator, so each role gets its own
login. Migrations grant to these users and never create them, so on RDS
you create them yourself, before the first `helm install`.

| User | Used by | Secret (chart value) | Env var in the pod |
| --- | --- | --- | --- |
| `rg_owner` | the migrate Job only: owns the schema, runs `repo-guardian migrate` | `store.postgres.existingSecret` | `STORE_DSN` |
| `rg_evaluator` | evaluator pods | `store.controls.evaluator.existingSecret` | `STORE_DSN_EVALUATOR` |
| `rg_remediator` | remediator pods | `store.controls.remediator.existingSecret` | `STORE_DSN_REMEDIATOR` |
| `rg_all` | `topology: all` pods only (a member of both roles above) | `store.controls.all.existingSecret` | both of the above |
| a read-only login, here `rg_api_ro` | api pods | `api.roDsn.existingSecret` | `STORE_RO_DSN` |

The chart-managed shapes (`baked`, `cnpg`) create the same users for
you, and call the read-only one `repoguardian_ro`. On `external` the
names are yours; only the grants matter.

As the RDS master user, before the first install:

```sql
-- The owner. Not a superuser, so the grants the migrations write bind.
CREATE ROLE rg_owner LOGIN PASSWORD '...' NOSUPERUSER NOCREATEROLE;
GRANT ALL ON SCHEMA public TO rg_owner;

-- The application users.
CREATE ROLE rg_evaluator  LOGIN PASSWORD '...' NOSUPERUSER;
CREATE ROLE rg_remediator LOGIN PASSWORD '...' NOSUPERUSER;
GRANT USAGE ON SCHEMA public TO rg_evaluator, rg_remediator;
-- Only for topology: all.
-- CREATE ROLE rg_all LOGIN PASSWORD '...' NOSUPERUSER IN ROLE rg_evaluator, rg_remediator;

-- The API's read-only login.
CREATE ROLE rg_api_ro LOGIN PASSWORD '...' NOSUPERUSER;
GRANT CONNECT ON DATABASE repoguardian TO rg_api_ro;
GRANT USAGE ON SCHEMA public TO rg_api_ro;
```

Then, connected **as `rg_owner`**, give the read-only login every table
the migrations create now and later:

```sql
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO rg_api_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO rg_api_ro;
```

The row-level-security read policies on the controls tables are
`PUBLIC`, so plain `SELECT` is enough for the API to see every row.
Change `repoguardian` to your `database_name`.

### Chart values for external Postgres

```yaml
store:
  postgres:
    mode: external
    existingSecret: repo-guardian-db-owner          # rg_owner; the migrate Job
    existingSecretKey: STORE_DSN
  controls:
    enabled: true                                   # per-role users; off by default
    evaluator:
      existingSecret: repo-guardian-db-evaluator
      existingSecretKey: STORE_DSN_EVALUATOR
    remediator:
      existingSecret: repo-guardian-db-remediator
      existingSecretKey: STORE_DSN_REMEDIATOR

api:
  enabled: true
  roDsn:
    existingSecret: repo-guardian-db-ro             # required on external with split + api
    existingSecretKey: STORE_RO_DSN
```

With `store.controls.enabled: true`, a split evaluator or remediator
pod gets only its own DSN and no `STORE_DSN`; only the migrate Job keeps
the owner's credentials. With it off, every controls pod falls back to
`STORE_DSN`, which on RDS means the workers run as the owner. Turn it on.

The chart refuses to render when a needed Secret is missing:
`store.controls.evaluator.existingSecret` while `github.eval.appId` is
set, `store.controls.remediator.existingSecret` while
`github.remediate.appId` is set (`store.controls.all.existingSecret`
under `topology: all`), and any `store.controls.*.existingSecret` set
outside `mode: external`.

Each controls worker logs which DSN it used at startup (`dsn_env` and
`db_user` on the "controls store" line). Check it after the first
deploy.

### Credentials via External Secrets Operator

Keep one Secrets Manager secret per database user, and one ESO
`ExternalSecret` per Kubernetes Secret. RDS Proxy needs a Secrets
Manager secret per user anyway, and a pod then can only ever be handed
its own credentials.

```text
Secrets Manager: rds/repo-guardian/evaluator
   ↓ ESO ExternalSecret
k8s Secret repo-guardian-db-evaluator (key STORE_DSN_EVALUATOR)
   ↓ store.controls.evaluator.existingSecret
evaluator pods
```

Example for the evaluator; repeat it for the owner (`STORE_DSN`), the
remediator (`STORE_DSN_REMEDIATOR`) and the API (`STORE_RO_DSN`):

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: repo-guardian-db-evaluator
  namespace: repo-guardian
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: ClusterSecretStore
  target:
    name: repo-guardian-db-evaluator
    template:
      data:
        # urlquery percent-encodes reserved characters; the replace
        # re-maps space from "+" (query-string form) to %20 (userinfo
        # form). Without this, a password containing @ : / % or space
        # produces a DSN that fails to parse or authenticates with the
        # wrong password. ESO templates are Go text/template + Sprig,
        # so both functions are available.
        STORE_DSN_EVALUATOR: "postgres://{{ .username }}:{{ .password | urlquery | replace \"+\" \"%20\" }}@{{ .endpoint }}:5432/{{ .dbname }}?sslmode=require"
  data:
    - secretKey: username
      remoteRef:
        key: rds/repo-guardian/evaluator
        property: username
    - secretKey: password
      remoteRef:
        key: rds/repo-guardian/evaluator
        property: password
    - secretKey: endpoint
      remoteRef:
        key: rds/repo-guardian/evaluator
        property: endpoint
    - secretKey: dbname
      remoteRef:
        key: rds/repo-guardian/evaluator
        property: dbname
```

Point the worker DSNs at the **RDS Proxy endpoint**. The owner and API
DSNs can point elsewhere (below). Why the encoding matters, and how a
mis-encoded password fails:
[Building the DSN](rds-terraform-variables.md#building-the-dsn).

### Connection pooling: RDS Proxy

Put RDS Proxy in front of any of the three Postgres variants for the
evaluator and remediator:

- It survives instance failovers without dropping client connections.
- It absorbs the reconnect burst when a KEDA scale-out or a rollout
  starts several pods at once.
- It is close to required with Serverless v2, where scaling events
  otherwise drop connections.
- It adds a few milliseconds per round trip, which does not matter at
  repo-guardian's query rate.

Register a Secrets Manager secret for each user that connects through
the proxy (`rg_evaluator`, `rg_remediator`). Size the proxy's
`max_connections_percent` to the sum of the worker pools; the math is
in [Connection math](scaling.md#connection-math).

Two DSNs do not need the proxy:

- **The owner (`STORE_DSN`)** is used only by the migrate Job, a few
  times per release. Goose holds a session-level advisory lock while it
  migrates, which pins a proxy connection for the duration. That is
  harmless, but with `store.controls.enabled` the owner DSN is its own
  Secret, so you can point it at the instance or cluster endpoint
  directly and skip the proxy.
- **The API (`STORE_RO_DSN`)** sets `default_transaction_read_only` and
  a 5s `statement_timeout` on every new session. Those `SET`s pin each
  connection to the proxy (at most 8 per api replica). Point it at the
  database or a reader endpoint directly instead.

### Reader endpoint for the API

The api role opens its own pool (8 connections per replica) on
`STORE_RO_DSN`, runs every read in a read-only transaction, and writes
nothing. On Aurora you can point `api.roDsn` at the **reader endpoint**
to keep dashboard and UI traffic off the writer. The workers must stay
on the writer: they write on every evaluation and remediation.

### Gaps and workarounds

- **No IAM database authentication.** The pools are built from static
  DSNs with no per-connection token hook, and IAM tokens expire after
  15 minutes. Use password auth through Secrets Manager. RDS Proxy can
  still use IAM between the proxy and the database.
- **Password rotation is not transparent.** A pod reads its DSN from
  the environment at start. When Secrets Manager rotates a password and
  ESO re-syncs the Kubernetes Secret, open connections keep working
  until they are recycled, but new ones fail until the pod restarts.
  RDS Proxy does not remove this: clients authenticate to the proxy with
  the same credentials it holds. Automate a rollout on Secret change
  (a reloader-style annotation), or rotate on your own schedule. Each
  user rotates separately, so only that role's pods restart.

## Webhook route

The chart ships no webhook Ingress. On EKS, the AWS Load Balancer
Controller with the Gateway API is the documented option:
[AWS LBC + Gateway API (EKS)](ingress.md#aws-lbc-gateway-api-eks). Route
both App paths, `/webhooks/github/eval` and `/webhooks/github/remediate`,
to the ingest Service. If you want to restrict source IPs to GitHub's
hook ranges, do it on the load balancer: the app checks only the HMAC
signature.

## Images, ECR and IAM

The public images and chart are on GHCR. Maintainers also publish to a
private ECR mirror (`repo-guardian` for the image, `repo-guardian-chart`
for the chart); pulling from it needs ECR read on the node role or the
pull identity. See
[Consumer-side install](ecr-publish-setup.md#consumer-side-install).

The repo-guardian pods need **no AWS permissions**: they speak Postgres,
Temporal and the GitHub API. The IAM role (IRSA or EKS Pod Identity)
belongs to ESO's ServiceAccount, with `secretsmanager:GetSecretValue`
on the repo-guardian secrets listed below. Leave
`serviceAccount.annotations` empty unless something else in your
cluster needs it.

## One busy organisation

At enterprise scale the question is what happens when one large
organisation has far more work than the rest. In v2 it is answered by
the rate budget, not by the infrastructure:

- Each App's installation has its own budget workflow,
  `installation/eval/<installation id>` and
  `installation/remediate/<installation id>`, tracking that
  installation's GitHub rate limit. A busy organisation that exhausts
  its budget makes its own evaluations wait for its own reset. It
  cannot spend another organisation's budget.
- A waiting evaluation is a durable Temporal timer, not a held worker
  slot, so other installations' work keeps flowing through the same
  pods.
- Every activity and workflow start carries the installation id as its
  Temporal fairness key.

There is no queue to partition and nothing to shard. The levers that
remain are the ones in
[The GitHub rate budget](scaling.md#the-github-rate-budget).

## End-to-end example

Values for a fleet of a few thousand repositories across tens of GitHub
Enterprise Cloud organisations, with remediation on:

```yaml
topology: split

temporal:
  address: temporal-frontend.temporal.svc:7233
  namespace: repo-guardian
  tls:
    existingSecret: repo-guardian-temporal-mtls

github:
  eval:
    appId: "123456"
    existingSecret: rg-eval-app            # private-key, webhook-secret
  remediate:
    appId: "234567"
    existingSecret: rg-remediate-app

ingest:
  replicas: 2

evaluator:
  concurrency: 10
  dbPoolSize: 12
  keda:
    enabled: true
    minReplicas: 1
    maxReplicas: 4

remediator:
  concurrency: 10
  dbPoolSize: 0                            # concurrency + 4 = 14
  keda:
    enabled: true
    minReplicas: 0
    maxReplicas: 2

keda:
  trigger: prometheus
  prometheus:
    serverAddress: http://prometheus.monitoring:9090

store:
  postgres:
    mode: external
    existingSecret: repo-guardian-db-owner
  controls:
    enabled: true
    evaluator:
      existingSecret: repo-guardian-db-evaluator
    remediator:
      existingSecret: repo-guardian-db-remediator

api:
  enabled: true
  replicas: 2
  roDsn:
    existingSecret: repo-guardian-db-ro
  auth:
    issuer: https://idp.example.com/realms/platform

# plus policy.files (or policy.existingConfigMap): see v2-onboarding.md
```

Companion AWS resources:

| Resource | Purpose | Sizing |
|---|---|---|
| Aurora PostgreSQL cluster (or RDS for PostgreSQL, Multi-AZ) | The controls database | `db.r6g.large` writer + 1 reader; storage auto-scaling |
| RDS Proxy | Pool for the evaluator and remediator | Fronts the writer; one auth secret per worker user; `max_connections_percent` 50–75 |
| Secrets Manager: `rds/repo-guardian/{owner,evaluator,remediator,api-ro}` | One secret per database user | Rotation on your schedule, see above |
| Secrets Manager: the two App keys and webhook secrets | `rg-eval-app`, `rg-remediate-app` | Keys rotate by adding a second key on the App registration |
| ESO ClusterSecretStore `aws-secrets-manager` | Secrets Manager to Kubernetes | IRSA or Pod Identity on ESO's ServiceAccount |
| Temporal | Workflows, timers, schedules | Yours: Temporal Cloud, or self-hosted with its own database |
| EKS namespace `repo-guardian` | Pod placement | Private subnets; egress to `api.github.com` and the Temporal frontend |
| ALB or NLB via the AWS Load Balancer Controller | Webhook route to ingest | Optionally restricted to GitHub's hook ranges |
| VPC endpoints | Secrets Manager, STS, ECR | Optional; cut NAT cost at scale |

Security-group rules:

- EKS nodes → RDS Proxy on 5432 (evaluator, remediator)
- EKS nodes → the database writer on 5432 (migrate Job, if it skips the proxy)
- EKS nodes → the reader endpoint on 5432 (api, if it uses the reader)
- RDS Proxy → the database on 5432
- EKS nodes → the Temporal frontend on 7233
