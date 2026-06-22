# Running repo-guardian on AWS

The chart defaults install Postgres via CloudNativePG (CNPG) and Valkey
as a baked StatefulSet. On AWS those primitives have native managed
equivalents — RDS / Aurora Postgres and ElastiCache for Valkey. This
guide covers wiring the chart at those services, plus the
"one-noisy-org" workload-isolation question that comes up at
enterprise scale.

The chart already supports external Postgres and external Valkey via
`store.postgres.mode=external` and `queue.valkey.mode=external`. Both
modes read connection strings from operator-supplied k8s Secrets;
nothing in the binary is AWS-aware. The work is in provisioning the
managed services and plumbing their credentials into k8s.

For prior reading: `docs/operations/scaling.md` covers the sizing
math for multi-org GHEC fleets — this doc covers how to land that
sizing on AWS infrastructure specifically.

## Postgres on AWS

### Option matrix

| Service | Best for | Notes |
|---|---|---|
| **RDS Postgres** (provisioned) | Predictable steady-state workloads | Cheapest per-hour for known load. Manual instance-class sizing. Multi-AZ for HA. |
| **RDS Postgres Serverless v2** | Spiky / unpredictable workloads | ACU-based autoscaling. Scale-to-zero possible (0.5 ACU floor without scale-to-zero enabled). Sweet spot for repo-guardian if you want auto-pause during the long quiet windows between weekly sweeps. |
| **Aurora Postgres** | HA + read scaling | Storage-replicated cluster with separate reader/writer endpoints. Best fault tolerance. Reader endpoint can offload `StaleRepos` queries — but the chart doesn't surface separate reader/writer DSNs today (see Gaps below). |

All three speak vanilla Postgres protocol, so the binary's
`internal/store/postgres/` works unchanged. The decision is
operational, not architectural.

### Connection pooling: use RDS Proxy

For any of the three Postgres variants, put **RDS Proxy** in front
of it. RDS Proxy is AWS's managed PgBouncer-equivalent:

- Transaction-mode pooling reduces backend connection count
- Holds DB credentials internally and handles rotation transparently
- Survives instance failovers without dropping client connections
- ~5ms RTT added — irrelevant at repo-guardian's QPS
- Required practice for Serverless v2 (scaling events cause
  connection storms otherwise)

The binary connects to the Proxy endpoint, not the DB instance
endpoint. The Proxy maintains the actual pool of backend
connections. This replaces the CNPG `pooler` block from the
homelab pattern.

### Credentials via External Secrets Operator

The recommended flow:

```
AWS Secrets Manager (DB credentials)
   ↓ ESO ExternalSecret
k8s Secret (`repo-guardian-store-dsn`, key `STORE_DSN`)
   ↓ chart references via existingSecret
repo-guardian Pod
```

Example `ExternalSecret`:

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: repo-guardian-store-dsn
  namespace: repo-guardian
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: ClusterSecretStore
  target:
    name: repo-guardian-store-dsn
    template:
      data:
        STORE_DSN: "postgres://{{ .username }}:{{ .password }}@{{ .endpoint }}:5432/{{ .dbname }}?sslmode=require"
  data:
    - secretKey: username
      remoteRef:
        key: rds/repo-guardian/master
        property: username
    - secretKey: password
      remoteRef:
        key: rds/repo-guardian/master
        property: password
    - secretKey: endpoint
      remoteRef:
        key: rds/repo-guardian/master
        property: endpoint
    - secretKey: dbname
      remoteRef:
        key: rds/repo-guardian/master
        property: dbname
```

The endpoint in the DSN should be the **RDS Proxy endpoint**, not
the DB instance/cluster endpoint.

### Chart values for external Postgres

```yaml
store:
  backend: postgres
  postgres:
    mode: external
    existingSecret: repo-guardian-store-dsn   # k8s Secret name
    existingSecretKey: STORE_DSN              # key inside the Secret
    maxConns: 10                              # per-replica pool cap
```

With 3 replicas × `maxConns=10` = 30 client connections into RDS
Proxy. RDS Proxy's `MaxConnectionsPercent` defaults to 100% of the
DB's `max_connections`; set it to 50-75% to leave headroom for
direct admin connections.

### Gaps and workarounds

- **No IAM authentication.** The binary expects a static DSN; IAM
  tokens expire every 15 minutes and the binary has no refresh
  loop. Workaround: use password auth via Secrets Manager. RDS
  Proxy can use IAM internally between Proxy and DB even if clients
  use password auth, so you still get IAM at the DB level.
- **No reader/writer split.** `StaleRepos` is read-heavy and could
  go to an Aurora reader endpoint, but the chart has one DSN slot.
  Today everything goes through the writer (or Proxy fronting the
  writer). Bigger code change tracked separately — not blocking.
- **Secrets Manager rotation gap.** If AWS rotates the DB password
  via Secrets Manager → ESO re-syncs the k8s Secret → but the
  binary's open pgxpool connections still use the OLD password
  until they hit a transient failure and reconnect. RDS Proxy
  eliminates this because the Proxy holds the DB creds; clients
  talk to the Proxy with their own (static) creds.

## ElastiCache for Valkey

### Pick Valkey, not Redis

AWS announced ElastiCache for Valkey in late 2024. It is the
forward-compatible managed option:

- API-compatible with Redis 7.2; existing `go-redis` clients work
  unchanged
- Roughly 33% cheaper than ElastiCache for Redis at equivalent
  shape (per AWS's own pricing posts)
- Avoids the Redis BSL / SSPL relicensing situation that motivated
  the Linux Foundation Valkey fork

Both queue and scheduler backends use the same client connection
under the hood (`scheduler.backend=valkey` requires
`queue.backend=valkey`), so a single ElastiCache cluster serves
both.

### Cluster mode: pick **disabled**

ElastiCache for Valkey offers two cluster topologies:

| Mode | Topology | Use case |
|---|---|---|
| **Cluster mode disabled** | Single shard with primary + N replicas | **Recommended** for repo-guardian |
| **Cluster mode enabled** | N shards with M replicas each, key-space partitioned by hash slot | NOT useful for repo-guardian today (see "Sharding and noisy orgs" below) |

A single Valkey shard sustains 80-100k ops/sec. repo-guardian's
queue traffic at 3000 repos × 25 orgs is in the low hundreds of
ops/sec at peak burst, so one shard is comfortably oversized. Pick:

- 1 primary + 1 replica for HA
- `cache.t4g.small` or `cache.m7g.large` depending on memory budget
  (queue + ZSET + reaper lock fit in single-digit MB; the smallest
  node class is plenty)
- Enable in-transit encryption (TLS)
- Enable an AUTH token

### Chart values for external Valkey

```yaml
queue:
  backend: valkey
  valkey:
    mode: external
    existingSecret: repo-guardian-queue-dsn
    existingSecretKey: QUEUE_VALKEY_DSN
    jobAckTimeout: "5m"
    reaperInterval: "30s"

scheduler:
  backend: valkey                          # MANDATORY for multi-replica
```

The DSN format for ElastiCache:

```
rediss://default:<auth-token>@<primary-endpoint>:6379/0
```

`rediss://` (with the extra `s`) is the TLS variant — match it to
your in-transit-encryption setting.

The same `ExternalSecret` pattern from the Postgres section applies;
the only change is the AWS Secrets Manager key (typically
`elasticache/repo-guardian/auth`) and the template assembling the
`rediss://` URL.

## Sharding and noisy orgs

This is the question most operators bring up after running
repo-guardian against a real fleet: one large GitHub org has
hundreds of actionable repos every sweep, and its work backs up
the queue so the smaller orgs wait. The instinct is to reach for
Valkey cluster mode (sharding).

That instinct is wrong for repo-guardian as it ships today.

### Why Valkey sharding does NOT help

ElastiCache cluster mode partitions Valkey's key space by hash
slot, routing each key to one of N shards. The benefit is per-key
throughput parallelism — different keys land on different shards
and serve traffic in parallel.

repo-guardian's queue uses **a small number of single keys**:

- `repo-guardian:queue:jobs` — the work LIST
- `repo-guardian:queue:inflight` — the ZSET of dispatched jobs
- `repo-guardian:scheduler:leader` — the SETNX-held leader lock

Every queue operation touches the same key (`jobs`). Sharding sends
all queue traffic to one shard regardless of how many shards exist.
The other shards sit idle. You pay for them and gain nothing.

(Strictly: hash tags can force multiple keys onto the same shard
when multi-key Lua scripts need them together. We do use multi-key
operations — LIST + ZSET coordinated atomically. So even if we
sharded, we'd need the LIST and ZSET on the same shard, which
defeats the purpose.)

There's also a code-level gap: `cmd/repo-guardian/main.go` builds a
`redis.NewClient` (single-node) from the parsed DSN, not a
`redis.NewClusterClient`. Cluster mode would require the wiring to
detect cluster topology and pick the right client type. Not done
today.

### What actually helps with the noisy-org problem today

| Lever | Where | Effect |
|---|---|---|
| `staleSweep.rateLimitReserve` | values.yaml | The reserve gate skips repos when an installation drops below the reserve threshold. Already throttles the noisy org's sweep enqueues against the SAME installation's webhook-triggered work. |
| `staleSweep.batchSize` smaller | values.yaml | Spreads the noisy org's enqueues across more sweep ticks instead of dumping 500 jobs in one tick. The queue depth stays lower; smaller orgs aren't buried. |
| `config.workerCount` higher | values.yaml | Drains bursts faster. 30 workers × 3 replicas = 90 concurrent processors. Even a 500-job burst clears in <30 sec at 3 sec/repo. |
| Vertical-scale Valkey | ElastiCache instance class | A larger single shard sustains more ops/sec. Almost certainly not the bottleneck, but cheap to bump if it is. |

The combination above gives **eventual fairness** — every org's
work gets done within a sweep cycle, just not strict round-robin.
For most operators that's sufficient.

### What a real fix looks like (roadmap, not config)

Strict per-org fairness requires partitioning the queue by
installation/org so workers grab from multiple queues in
round-robin. That's a code change to `internal/queue/`:

- Multiple keys (`repo-guardian:queue:install-12345:jobs`,
  `:install-67890:jobs`, …)
- Worker pool with round-robin / work-stealing across keys
- Reaper updated to scan all in-flight ZSETs

This is INV/IMPL territory. If you hit the noisy-org problem hard
enough that the eventual-fairness mitigations aren't sufficient,
file an INV requesting per-org queue partitioning and we'll scope
it. Until then: tune the values, not the topology.

## End-to-end example

Full chart values for a 3000-repo × 25-org GHEC fleet on AWS:

```yaml
replicaCount: 3

config:
  workerCount: 15
  scheduleInterval: "12h"

staleSweep:
  freshness: "24h"
  batchSize: 500
  rateLimitReserve: 0.15

store:
  backend: postgres
  postgres:
    mode: external
    existingSecret: repo-guardian-store-dsn
    existingSecretKey: STORE_DSN
    maxConns: 10

queue:
  backend: valkey
  valkey:
    mode: external
    existingSecret: repo-guardian-queue-dsn
    existingSecretKey: QUEUE_VALKEY_DSN
    jobAckTimeout: "5m"
    reaperInterval: "30s"

scheduler:
  backend: valkey
```

Companion AWS resources (provisioned via Terraform, CDK, or
manually):

| Resource | Purpose | Sizing |
|---|---|---|
| Aurora Postgres cluster | Store backend | `db.r6g.large` writer + 1 reader; storage auto-scaling enabled |
| RDS Proxy | Connection pool | Fronts the Aurora writer endpoint; `MaxConnectionsPercent=50` |
| ElastiCache Valkey | Queue + scheduler | `cache.t4g.small` primary + 1 replica, cluster mode disabled, TLS + AUTH token enabled |
| Secrets Manager: `rds/repo-guardian/master` | DB credentials | Auto-rotation on a 30-day schedule |
| Secrets Manager: `elasticache/repo-guardian/auth` | Valkey AUTH token | Manual rotation (ElastiCache doesn't auto-rotate) |
| ESO ClusterSecretStore: `aws-secrets-manager` | Bridges Secrets Manager → k8s | IRSA-based access; one IAM role for the namespace |
| EKS namespace `repo-guardian` | Pod placement | Private subnets, NAT egress to github.com |
| VPC endpoints | Secrets Manager + STS + ECR | Optional but reduces NAT cost at scale |

Security-group rules to remember:

- EKS node SG → RDS Proxy SG on 5432
- EKS node SG → ElastiCache SG on 6379
- RDS Proxy SG → Aurora SG on 5432

The IRSA role for the repo-guardian ServiceAccount needs:
`secretsmanager:GetSecretValue` on the two secrets above. Nothing
else AWS-side — the binary speaks plain Postgres + plain
Valkey/Redis protocol once it has the DSNs.
