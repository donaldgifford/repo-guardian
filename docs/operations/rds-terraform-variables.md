# External Postgres on RDS — Terraform variable reference

Concrete variable settings for running repo-guardian against
`libtftest-tf-modules/modules/rds/*`, for each of the six shapes:
`instance` / `cluster` / `serverless`, each with or without `proxy`.

[Running repo-guardian on AWS](aws.md) covers the *choice* between those
shapes and the surrounding wiring (External Secrets Operator, chart
values, ElastiCache). This doc is the variable-level companion: what to
set, what the module defaults get wrong for this specific consumer, and
what breaks if you leave it alone.

<!--toc:start-->
- [What repo-guardian requires of any Postgres](#what-repo-guardian-requires-of-any-postgres)
- [Shape matrix](#shape-matrix)
- [Variables common to all three DB modules](#variables-common-to-all-three-db-modules)
  - [Parameter groups and engine version](#parameter-groups-and-engine-version)
- [`modules/rds/instance`](#modulesrdsinstance)
- [`modules/rds/cluster`](#modulesrdscluster)
- [`modules/rds/serverless`](#modulesrdsserverless)
- [`modules/rds/proxy`](#modulesrdsproxy)
- [Building the DSN](#building-the-dsn)
- [Chart values](#chart-values)
- [Verification checklist](#verification-checklist)
- [Known limitations](#known-limitations)
<!--toc:end-->

## What repo-guardian requires of any Postgres

Five requirements drive every setting below. They come from the code,
not from preference.

1. **A database must already exist.** Every pod calls
   `pgstore.Migrate(cfg.StoreDSN)` at startup
   (`cmd/repo-guardian/main.go:391`) and fails fast if it can't apply
   `0001_init.up.sql`. The module's `database_name` defaults to `null`,
   which creates **no** database — so this must be set explicitly or the
   binary crash-loops on first boot.
2. **DDL privileges on that database.** Migrations run in-process, in
   every replica, on every start. There is no separate migration Job and
   no separate migration DSN.
3. **A static username/password DSN.** `postgres.New` does
   `pgxpool.ParseConfig(dsn)` and nothing else — no `BeforeConnect` hook,
   no credential refresh. This is what makes IAM auth unusable (below).
4. **Writer access.** `StaleRepos` is read-heavy but everything shares
   one DSN slot; reader endpoints have no wiring today
   (DESIGN-0016 is Draft).
5. **Plain Postgres, default parameters.** The schema is two indexes and
   a table with no version-specific syntax, so any supported engine
   version works — but run the **same major the chart runs** (18; both
   `store.postgres.baked.image: postgres:18.4` and
   `store.postgres.cnpg.imageName: ...postgresql:18.4`) so dev/test on
   the chart-managed shapes exercises the major prod runs on. Neither
   chart mode sets a single custom Postgres parameter, so RDS
   default-valued parameter groups are correct — see
   [Parameter groups](#parameter-groups-and-engine-version).

## Shape matrix

| Shape | Module(s) | Endpoint the DSN points at | Use when |
|---|---|---|---|
| Instance, no proxy | `instance` | `endpoint` | Simplest. Dev, or single-replica prod. |
| Instance + proxy | `instance` + `proxy` | `proxy_endpoint` | **Recommended default.** Survives failover, absorbs replica restarts. |
| Cluster, no proxy | `cluster` | `cluster_endpoint` | HA without the proxy hop. |
| Cluster + proxy | `cluster` + `proxy` | `proxy_endpoint` | HA + connection stability. Best availability. |
| Serverless, no proxy | `serverless` | `cluster_endpoint` | Not recommended — scaling events cause connection storms. |
| Serverless + proxy | `serverless` + `proxy` | `proxy_endpoint` | **Recommended for spiky/idle workloads.** Proxy is close to mandatory here. |

repo-guardian's load shape is long idle stretches punctuated by sweep
bursts, which suits Serverless v2 — but only with the proxy in front.

## Variables common to all three DB modules

### Composition plumbing (required, no defaults)

Identical across `instance`, `cluster`, and `serverless`. These locate
the VPC remote state; Terragrunt injects them in production.

| Variable | Notes |
|---|---|
| `region` | Deployment region. |
| `remote_state_bucket` | Bucket holding the VPC stack state. |
| `remote_state_bucket_region` | Region *of the bucket* — differs from `region` in multi-region setups. |
| `vpc_name` | Composed into the state key. |
| `account_name` | Prefix of the account-scoped state key. |
| `account_id` | 12-digit ID owning the bucket. |
| `deploy_role_name` | Role assumed for the cross-account state read. |
| `identifier_prefix` | Must match `^[a-z][a-z0-9-]{0,61}[a-z0-9]$`. |
| `engine` | Validated per module — see each section. |

### Must-set for repo-guardian (module defaults are wrong for us)

| Variable | Default | Set to | Why |
|---|---|---|---|
| `database_name` | `null` | `"repoguardian"` | **The one that bites.** Default creates no database; migrations fail on first boot. |
| `allowed_consumer_sg_ids` | `[]` | EKS node SG, **or** the proxy SG | Default is reachable from nowhere. With a proxy this is the *proxy's* SG, not the nodes'. |
| `master_username` | `"admin"` | `"admin"` or `"repoguardian"` | Cosmetic, but it goes in the DSN — pick before first apply, changing it later forces replacement. |

### Recommended

| Variable | Default | Recommendation |
|---|---|---|
| `engine_version` | `null` (module resolves major 18) | **Pin `"18"`** — the major the chart runs (`postgres:18.4` baked, CNPG `18.4`). Pinning matters even though the module's default is currently also 18: `default_major_map` is Renovate-bumped, so an unpinned prod DB changes major whenever the module updates. See [Parameter groups and engine version](#parameter-groups-and-engine-version). |
| `manage_master_user_password` | `true` | Keep `true` — but read [Known limitations](#known-limitations) on rotation, which is not free for this consumer. |
| `backup_retention_period` | `7` | `7` dev / `30` prod. |
| `deletion_protection` | `true` | Keep `true`. |
| `publicly_accessible` | `false` | Keep `false`. |
| `performance_insights_enabled` | `false` | `true` in prod — the cheapest way to see whether the sweep is DB-bound. |
| `skip_final_snapshot` | `false` | Keep `false`; supply `final_snapshot_identifier` at destroy time. |
| `iam_database_authentication_enabled` | `false` | **Keep `false`** — unusable, see limitations. |

### Parameter groups and engine version

The modules create parameter groups **unconditionally** — you never
create or attach one yourself, and there is nothing to configure inside
them today:

- `instance` creates one `aws_db_parameter_group`; `cluster` and
  `serverless` create the Aurora pair (`aws_rds_cluster_parameter_group`
  + `aws_db_parameter_group` for the instances).
- All are **deliberately empty** in module v1 ("No custom `parameter`
  blocks — per-parameter tuning is a later additive change"). They exist
  as rename-safe attachment points (`create_before_destroy`) so future
  tuning never forces instance replacement.
- The family auto-resolves from `engine` + `engine_version` via a static
  map: `postgres16/17/18` on `instance`, `aurora-postgresql14`–`18` on
  the Aurora shapes. `parameter_family` is an override for the rare case
  the map lags a new major — with `engine_version = "18"` it resolves to
  `postgres18` / `aurora-postgresql18` and you should leave it null.

**repo-guardian needs nothing custom in them.** This is verifiable on
both sides: the chart's baked mode runs stock `postgres:18.4` with no
`args` and no config file, the CNPG `Cluster` CR sets no
`postgresql.parameters` block, and the store itself needs no
non-default server behaviour (one table, two indexes, parameterized
queries via pgx, and session advisory locks for migrations — all
default-on). So the empty default-valued groups the module creates are
not a gap to fill; they are the same "stock Postgres" contract the
chart-managed shapes already run.

Three parameter-adjacent facts still worth knowing:

- **`rds.force_ssl = 1` is the default on RDS for PostgreSQL 15+** —
  plaintext connections are rejected server-side. This is why
  `sslmode=require` in the DSN is mandatory on the `instance` shape, not
  just recommended. Aurora PostgreSQL does *not* force TLS by default;
  there the proxy's `require_tls = true` is what enforces it.
- **`max_connections` is a family default derived from instance
  memory** (`{DBInstanceClassMemory/9531392}` for Postgres — ≈112 on a
  `db.t4g.micro`). It bounds the [connection math](#chart-values);
  size `replicas × maxConns` against it, don't raise it in a parameter
  group.
- **If tuning is ever genuinely needed** (e.g.
  `log_min_duration_statement` while chasing a slow sweep), the module
  cannot express it today — no variable accepts parameter blocks. That's
  a module feature request, not a values tweak; and if a parameter turns
  out to matter for repo-guardian itself, mirror it into the chart's
  baked/CNPG shapes so all four deployment shapes keep running the same
  effective config.

## `modules/rds/instance`

Non-Aurora, single instance.

### Required beyond the common set

| Variable | Constraint | Suggested |
|---|---|---|
| `engine` | `postgres` or `mysql` | `"postgres"` |
| `instance_class` | — | `db.t4g.micro` dev / `db.t4g.medium` prod |
| `allocated_storage` | `>= 20` | `20` — the schema is one small table; storage is not the constraint |

### Instance-only optionals

| Variable | Default | Notes |
|---|---|---|
| `multi_az` | `false` | `true` for prod HA. The only HA lever this module has. |
| `max_allocated_storage` | `null` | Set above `allocated_storage` to enable autoscaling. Rarely needed here. |
| `storage_type` | `gp3` | Leave. |
| `iops` / `storage_throughput` | `null` | Leave. `iops` becomes required if you switch to `io2`. |
| `ca_cert_identifier` | `null` | Set only if pinning a specific RDS CA. |

```hcl
module "repo_guardian_db" {
  source = ".../modules/rds/instance"

  region                     = "us-east-1"
  remote_state_bucket        = var.remote_state_bucket
  remote_state_bucket_region = "us-east-1"
  vpc_name                   = "platform"
  account_name               = var.account_name
  account_id                 = var.account_id
  deploy_role_name           = var.deploy_role_name

  identifier_prefix = "repo-guardian"
  engine            = "postgres"
  engine_version    = "18" # match the chart's baked/CNPG major (postgres:18.4)
  instance_class    = "db.t4g.medium"
  allocated_storage = 20
  # parameter_family omitted — auto-resolves to "postgres18"; the module
  # creates the (empty, default-valued) parameter group itself.

  database_name           = "repoguardian"
  master_username         = "repoguardian"
  allowed_consumer_sg_ids = [module.eks.node_security_group_id]  # or [module.proxy.proxy_security_group_id]

  multi_az                     = true
  backup_retention_period      = 30
  performance_insights_enabled = true

  tags = local.tags
}
```

## `modules/rds/cluster`

Aurora provisioned. Same surface as `instance` minus the storage knobs,
plus cluster concerns.

### Required beyond the common set

| Variable | Constraint | Suggested |
|---|---|---|
| `engine` | `aurora-postgresql` or `aurora-mysql` | `"aurora-postgresql"` |
| `engine_version` | optional but pin it | `"18"` — chart-major parity, resolves family `aurora-postgresql18` |
| `instance_class` | — | `db.t4g.medium` / `db.r6g.large` |

Note there is **no `allocated_storage`** — Aurora storage is elastic.

### Cluster-only optionals

| Variable | Default | Notes |
|---|---|---|
| `backtrack_window` | — | Aurora-MySQL only in practice; leave unset for Postgres. |
| `enabled_cloudwatch_logs_exports` | — | `["postgresql"]` to ship logs. |
| `promotion_tier` | — | Failover priority. Single-instance clusters can ignore it. |

**Endpoint choice matters here.** The module emits both
`cluster_endpoint` (writer) and `reader_endpoint`. Use
**`cluster_endpoint`** — repo-guardian writes on every reconcile, and
pointing it at the reader produces read-only transaction errors on the
first write-back.

## `modules/rds/serverless`

Aurora Serverless v2.

### Required beyond the common set

| Variable | Constraint | Suggested |
|---|---|---|
| `engine` | `aurora-postgresql` or `aurora-mysql` | `"aurora-postgresql"` |
| `engine_version` | optional but pin it | `"18"` — chart-major parity, resolves family `aurora-postgresql18` |
| `min_acu` | `0.5` – `256` | `0.5` |
| `max_acu` | `0.5` – `256`, `>= min_acu` | `4` dev / `16` prod |

`min_acu = 0.5` still bills continuously (~$0.12/ACU-hour for Postgres
in us-east-1 at time of writing), so the floor is a cost decision, not a
performance one. repo-guardian idles between sweeps, which is precisely
the shape that makes a low floor worth it.

Same writer-endpoint rule as `cluster`: use `cluster_endpoint`.

**Pair this with the proxy.** Serverless v2 scaling events drop
connections; without a proxy, pgxpool sees them as connection errors
mid-sweep.

## `modules/rds/proxy`

The proxy module reads the target's remote state rather than taking its
attributes as inputs, so its required surface is just pointers.

### Required

| Variable | Notes |
|---|---|
| `region`, `remote_state_bucket`, `remote_state_bucket_region`, `account_name`, `account_id`, `deploy_role_name` | Same plumbing as the DB modules. |
| `name` | `^[a-zA-Z][a-zA-Z0-9-]{0,58}[a-zA-Z0-9]$`. AWS additionally rejects `--`. |
| `target_type` | **`rds-instance`** \| **`aurora-cluster`** \| **`serverless`** — must match the module you deployed. Selects both the remote-state key shape and whether the target is keyed by instance or cluster identifier. |
| `target_identifier` | The DB's `identifier_prefix`. |

### Optionals that matter for repo-guardian

| Variable | Default | Set to | Why |
|---|---|---|---|
| `allowed_consumer_sg_ids` | `[]` | EKS node SG | Default is reachable from nowhere. |
| `require_tls` | `true` | `true` | Keep. Your DSN then needs `sslmode=require` or stricter. |
| `require_iam_auth` | `false` | **`false`** | `true` makes repo-guardian unable to connect at all. |
| `max_connections_percent` | `100` | `50`–`75` | Leaves headroom for admin sessions. |
| `max_idle_connections_percent` | `50` | `<= max_connections_percent` | Cross-bound is enforced by a precondition. |
| `connection_borrow_timeout` | `120` | `120` | Fine as-is at this QPS. |
| `idle_client_timeout` | `1800` | `1800` | Fine. Sweeps are far shorter than 30 min. |
| `session_pinning_filters` | `[]` | `["EXCLUDE_VARIABLE_SETS"]` | Reduces pinning. See the pinning note below. |
| `create_read_only_endpoint` | `false` | `false` | Aurora-only, and unusable until DESIGN-0016 lands. |
| `debug_logging` | `false` | `false` | Logs SQL to CloudWatch. |

### The two-apply security-group dance

The proxy sits between the app and the DB, so the SG wiring is *not*
symmetric and can't be done in one pass:

1. Apply the **DB** module with `allowed_consumer_sg_ids = []` (or just
   the nodes, temporarily).
2. Apply the **proxy** module with
   `allowed_consumer_sg_ids = [<EKS node SG>]`.
3. Re-apply the **DB** module with
   `allowed_consumer_sg_ids = [module.proxy.proxy_security_group_id]`.

The module documents this: `proxy_security_group_id` is emitted as an
output specifically "so it can be added to the DB module's
`allowed_consumer_sg_ids` on a subsequent apply." Once the proxy is in
front, the nodes should generally **not** retain direct DB ingress —
that path bypasses the pool.

### Pinning

RDS Proxy falls back to a dedicated (pinned) connection when a session
carries state it can't safely multiplex. Two things repo-guardian does
are relevant:

- **Migrations take a session-level advisory lock.** golang-migrate's
  pgx/v5 driver issues `SELECT pg_advisory_lock($1)`
  (`database/pgx/v5/pgx.go:229`). That's session state by definition, so
  every pod's startup migration will pin its connection for the duration.
  It's brief and correct — but it's why you may see pinning spikes on
  rollouts specifically.
- **pgx uses prepared statements by default.** `QueryExecModeCacheStatement`
  is the default exec mode (`pgx@v5.9.2/conn.go:190`).

Neither is a reason to avoid the proxy. But if you see
`DatabaseConnectionsCurrentlySessionPinned` sitting high in CloudWatch,
the lever is `default_query_exec_mode=simple_protocol` in the DSN —
pgx parses that key directly out of the connection string, so it needs
no code change. Trade-off: simple protocol disables the statement cache,
which costs a round trip per query. At repo-guardian's QPS that is
irrelevant; measure before reaching for it.

## Building the DSN

```text
postgres://<master_username>:<password>@<endpoint>:5432/<database_name>?sslmode=require
```

| Shape | `<endpoint>` from |
|---|---|
| instance, no proxy | `module.db.endpoint` (or `address` for host-only) |
| instance + proxy | `module.proxy.proxy_endpoint` |
| cluster / serverless, no proxy | `module.db.cluster_endpoint` ← writer, **not** `reader_endpoint` |
| cluster / serverless + proxy | `module.proxy.proxy_endpoint` |

`endpoint` on the instance module includes the port; `address` is
host-only. Check which you're interpolating so you don't end up with
`host:5432:5432`.

**Percent-encode the password.** RDS-managed master passwords exclude
`/`, `@`, `"`, and space — but *not* `%`, `+`, `:`, `?` — so encoding
is still required. There is no way to validate or transform the DSN at
the `secretKeyRef` (env injection is a byte copy), so correctness lives
at the producer: the [ESO example in aws.md](aws.md#credentials-via-external-secrets-operator)
assembles the DSN with `urlquery | replace "+" "%20"`. The replace is
load-bearing: pgx treats `+` in the password position as a **literal
plus**, so an encoder that maps space → `+` mints a wrong password.

How an unencoded password actually fails (verified against pgx v5.9.2,
the parser the binary uses):

- space, `/`, or a malformed `%`-escape → hard parse error; the pod
  crash-loops at startup before doing any work (password redacted in
  the error).
- raw `@`, `+`, `:` → tolerated; pgx recovers the correct password.
- `%` followed by two hex digits → **silently decodes to a different
  password** and presents as `password authentication failed` at
  startup. This is the diagnosis trap: with Secrets Manager rotation
  in the picture it looks exactly like a rotation problem. If auth
  fails right after a password change and the new password contains
  `%`, check encoding before chasing rotation.

Eyeballing the Secret is not a decisive check for the last case — the
Secret holds the *encoded* form and pgx uses the *decoded* form. To see
exactly the password the binary will present to Postgres:

```bash
kubectl get secret <name> -n <ns> -o jsonpath='{.data.STORE_DSN}' | base64 -d |
  python3 -c 'import sys
from urllib.parse import urlsplit, unquote
print(repr(unquote(urlsplit(sys.stdin.read().strip()).password)))'
```

(`unquote`, not `unquote_plus` — pgx treats `+` in the password as a
literal plus, and so does `unquote`.)

If that output doesn't match the real password, the DSN in the Secret
is mis-encoded, regardless of how correct it looks raw.

**`sslmode` is not optional in practice.** pgx defaults to `prefer`,
which silently accepts an unencrypted connection. RDS accepts TLS
everywhere and the proxy *requires* it when `require_tls = true`, so set
at least `sslmode=require`. For `verify-full` you must also mount the
RDS CA bundle and add `sslrootcert=`; `require` encrypts without
verifying the server certificate.

## Chart values

Identical for all six shapes — only the DSN inside the Secret changes:

```yaml
store:
  backend: postgres
  postgres:
    mode: external
    existingSecret: repo-guardian-store-dsn
    existingSecretKey: STORE_DSN
    maxConns: 10
```

Guards worth knowing: setting `store.postgres.existingSecret` while
`mode != external` fails the render deliberately
(`repo-guardian.validateBackendSecrets`), as does setting
`store.postgres.baked.existingSecret` outside baked mode. That's
IMPL-0018's fix for silently-ignored config.

**Connection math:** `maxConns` is per replica, so the ceiling is
`replicas × maxConns`. Three replicas at the chart default of 16 is 48
client connections — against a `db.t4g.micro`
(`max_connections` ≈ 112) that's already 43% of the instance before
anything else connects. With a proxy, size
`max_connections_percent` so `replicas × maxConns` fits inside the
proxy's share.

## Verification checklist

```bash
# 1. The database exists (the #1 first-boot failure)
psql "$STORE_DSN" -c '\l' | grep repoguardian

# 2. TLS is actually on
psql "$STORE_DSN" -c 'SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid();'

# 3. Migrations applied
psql "$STORE_DSN" -c '\dt' | grep -E 'repo_state|schema_migrations'

# 4. From inside the cluster, not your laptop — SG rules differ
kubectl run pg-probe --rm -it --restart=Never -n repo-guardian \
  --image=postgres:18-alpine -- psql "$STORE_DSN" -c 'SELECT 1;'
```

Then confirm the pod got past migrations:

```bash
kubectl logs -n repo-guardian deploy/repo-guardian | grep -i migrat
```

## Known limitations

- **IAM database authentication is unusable.** Not "discouraged" —
  unusable. `postgres.New` builds the pool from a static DSN with no
  `BeforeConnect` hook, and IAM tokens expire every 15 minutes, so the
  pool would authenticate once and then fail every subsequent
  connection. Leave `iam_database_authentication_enabled = false` on the
  DB and `require_iam_auth = false` on the proxy. Supporting it means a
  code change (a `BeforeConnect` that mints a token per connection).

- **Password rotation is not transparent, even with a proxy.** With
  `manage_master_user_password = true`, AWS rotates the secret. Clients
  authenticate to RDS Proxy with the *same* credentials the proxy holds
  in Secrets Manager — the proxy does not issue separate static
  credentials — so a rotation invalidates the password the pods are
  using. External Secrets Operator re-syncs the k8s Secret, but existing
  pgxpool connections keep the old password until they're recycled, and
  the Deployment needs a restart (or a `reloader`-style annotation) to
  pick up the new env value at all. Options: accept a brief blip and
  automate the restart, or set `manage_master_user_password = false` and
  own rotation on your own schedule.

- **Migrations run in every replica, through whatever the DSN points
  at.** There is no separate migration DSN, so with a proxy the
  advisory-lock handshake goes through the proxy too. It is safe —
  golang-migrate's lock serialises concurrent pods — but you cannot
  route migrations to the direct endpoint while runtime traffic uses the
  proxy without a code or chart change. `postgres.Migrate` is exported
  separately with a one-shot Job in mind; the chart doesn't ship one yet.

- **No reader/writer split.** Aurora reader endpoints and the proxy's
  `create_read_only_endpoint` have nowhere to plug in. Tracked in
  [DESIGN-0016](../design/0016-separate-postgres-read-and-write-endpoints.md)
  (Draft).

- **`master_username` is baked into the DSN.** Changing it after first
  apply forces a replacement. Decide before the first apply.
