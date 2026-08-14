# Postgres schema operations

This is the operator-facing guide for managing the `repo_state` table
under `STORE_BACKEND=postgres`. The schema is small (one table, three
indexes) and migrations apply at binary startup; most operators won't
touch this page in normal operation.

## Migration runtime

`repo-guardian` calls `golang-migrate` against an embedded `migrations/`
directory at startup. Migration is idempotent: `migrate.Up()` returns
`ErrNoChange` when the schema is already current.

Failures abort startup before the HTTP servers come up, so a botched
schema never serves a single webhook. The most common failure mode is
DSN misconfiguration (network, auth, missing database) — the binary
prints the underlying pgx error before exiting.

## Schema overview

```sql
CREATE TABLE repo_state (
    installation_id   BIGINT  NOT NULL,
    owner             TEXT    NOT NULL,
    repo              TEXT    NOT NULL,
    last_checked_at   TIMESTAMP WITH TIME ZONE,
    last_check_status TEXT    NOT NULL DEFAULT 'pending',
    last_error        TEXT,
    policy_version    TEXT    NOT NULL DEFAULT '',
    active            BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (installation_id, owner, repo)
);

CREATE INDEX idx_repo_state_freshness
    ON repo_state(last_checked_at NULLS FIRST);

CREATE INDEX idx_repo_state_policy_version
    ON repo_state(policy_version);

CREATE INDEX idx_repo_state_active_freshness
    ON repo_state(last_checked_at NULLS FIRST)
    WHERE active;
```

The indexes drive the stale-sweep query in
`internal/store/postgres/postgres.go.StaleRepos`:

```sql
SELECT ...
FROM repo_state
WHERE active
  AND (last_checked_at IS NULL
       OR last_checked_at < $1
       OR policy_version <> $2)
ORDER BY last_checked_at NULLS FIRST
LIMIT $3
```

Both `last_checked_at` and `policy_version` are indexed so the OR-of-three
filter doesn't degrade to a sequential scan even at 100k+ rows. The
partial index carries the `WHERE active` predicate, so parked rows are
absent from it entirely rather than scanned and discarded — the sweep
does not get slower as an org accumulates archived repositories.

Note the parentheses. `AND` binds tighter than `OR`, so writing
`... OR policy_version <> $2 AND active` would gate only the last arm and
a parked repository would return to the sweep the moment the policy hash
changed. `TestPostgresStore_DeactivateExcludesFromSweep` covers exactly
this.

### `active` — the parking column (INV-0015)

`active` is `true` for every row unless the worker parked it. Parking
means "stop handing this repository to the sweep," and there are three
reasons, all carried on `repo_guardian_repos_parked_total{reason}`:

| `reason` | Trigger | Normal? |
|---|---|---|
| `access_denied` | Check failed with 403/404 — the App was uninstalled from that repository, lost a permission, or the repository was deleted | A trickle is expected; a step change is not |
| `archived` | `skip_archived` is on and the repository is archived | Yes |
| `fork` | `skip_forks` is on and the repository is a fork | Yes |

Before this column each of the three burned budget indefinitely, in two
different ways. An unreadable repository failed every attempt, exhausted
`MAX_JOB_ATTEMPTS`, and was handed straight back by the next stale sweep
— the full attempt budget every cycle, forever, with failures
indistinguishable from a transient 500. An archived or forked repository
was cheaper but just as permanent: enqueue, installation client,
`GetRepository`, skip, write back success, and round again every
freshness cycle for as long as the repository exists.

Parked rows are **kept, never deleted**, so the history stays queryable:

```sql
SELECT installation_id, owner, repo, last_check_status, last_error
FROM repo_state
WHERE NOT active
ORDER BY owner, repo;
```

`last_check_status` distinguishes the two kinds: access-denied parks
record `'error'` with the underlying API failure in `last_error`, while
archived and fork parks record `'skipped'`, because nothing went wrong.
Both put something in `last_error` — there is no separate reason column,
and the reason is what makes the query above answer *why* a row is
parked, so a skip stores its bare reason (`archived`, `fork`) there.

**Only discovery can un-park a row.** Discovery is the one component that
observes the installation's real repository set, so a repository whose
access is restored rejoins the sweep on the next discovery pass with no
operator action. Reactivation deliberately does not touch
`last_checked_at` or `policy_version`; overwriting those would reset the
freshness gate and re-check the whole fleet on every discovery pass.

There is intentionally no way to reactivate from the check path, and none
from SQL that we document — if you set `active = true` by hand on a
repository the App still cannot read, it will simply be parked again on
its next check.

Un-archiving a repository, or the App regaining access to one, therefore
needs no operator action at all: discovery stops filtering it, upserts
it, and the row goes live again.

#### Why only these three reasons park

Parking is stable only when the check path's skip conditions are a
**subset** of discovery's filters. Discovery filters archived and fork
repositories, so a repository parked for either reason is not offered
back — it settles.

An *empty* repository (no default branch yet) is skipped by the engine
too, but discovery does **not** filter it. Parking it would have
discovery re-upsert the row on its very next pass, flipping `active`
back to `true`, and the two would churn against each other at the
discovery interval forever. So empty repositories are skipped the old
way, with an ordinary success write-back, and freshness governs the next
check. `TestPool_EmptyRepo_IsSkippedButNotParked` pins this boundary; the
invariant is restated on `checker.skipReason`, which is where a fourth
skip reason would be added.

Watch `repo_guardian_repos_parked_total`. Only
`reason="access_denied"` is alerted (`RepoGuardianRepoAccessDenied`) —
archived and fork parks are routine bookkeeping. A steady access-denied
trickle is normal in a large org (repositories get deleted); a step
change means the App lost access to something it used to have.

## Out-of-band migration runs

Multi-replica deployments may want to apply migrations from a one-shot
Job rather than letting every pod race at startup. The `Migrate`
function is exported separately from `New`:

```go
import "github.com/donaldgifford/repo-guardian/internal/store/postgres"

if err := postgres.Migrate(dsn); err != nil {
    log.Fatal(err)
}
```

Wrap that in a `kubectl create job` and gate the runtime Deployment on
its completion. `helm upgrade` order is: Job (migration) → Deployment
(runtime) → ... If you do this, set `STORE_BACKEND=postgres` on the
Deployment env but disable the startup migrate inside the binary by
exposing a future `STORE_SKIP_MIGRATE=true` knob (not yet implemented;
file an issue if you need it).

For the homelab / single-replica path, the default startup-migrate
behaviour is fine.

## Backups

CNPG-managed Postgres exposes a `backup` field on the `Cluster` CR;
see the upstream operator docs for the supported configurations
(volume snapshots, S3 backups, scheduled WAL archiving).

The baked single-pod StatefulSet has no built-in backup. Treat it as
ephemeral state — losing it means the next reconcile cycle re-checks
every repo from scratch (slow but harmless). Production deployments
should either move to `mode=cnpg` or `mode=external` with a managed
Postgres provider.

## Migration 0003 — posture state (IMPL-0023)

`0003_rule_state.up.sql` is additive only, so applying it is safe on a
live fleet and needs no downtime:

- `rule_state` — one row per `(installation_id, owner, repo,
  rule_name)` holding `actionable`, `actionable_since`, `rule_kind`,
  and `policy_version`. Written by the worker's best-effort write-back
  after every check.
- `idx_rule_state_actionable` — partial index on `(owner, rule_name)
  WHERE actionable`, supporting the posture query.
- `compliance_snapshot` — daily point-in-time counts, independent of
  Prometheus retention.
- `repo_state.catalog_parse_ok` — nullable, so "no catalog rule was
  evaluated" stays distinct from "evaluated and malformed".

Rows appear as repos are checked, not at migration time. A freshly
migrated fleet reports zero tracked rules until the first sweep
completes; that is expected, not a failure.

**Sizing.** At 5k repos × ~6 rules, `rule_state` holds ~30k rows and is
rewritten one repo at a time. `compliance_snapshot` grows by
orgs × rules rows per day (~120/day at that scale), so no retention
machinery ships initially — revisit if you pass a few million rows.

**Dead installations.** Same recipe as `repo_state`: one more
`DELETE ... WHERE installation_id = <id>` at cutover.

## Rolling back the schema

Every migration ships a matching `.down.sql`, and
`TestPostgresStore_MigrateUpDownUp` verifies the pair round-trips
against a real Postgres. To roll back manually:

```bash
# 0002 (posture state)
psql $STORE_DSN -c "DROP INDEX IF EXISTS idx_rule_state_actionable;"
psql $STORE_DSN -c "DROP TABLE IF EXISTS compliance_snapshot;"
psql $STORE_DSN -c "DROP TABLE IF EXISTS rule_state;"
psql $STORE_DSN -c "ALTER TABLE repo_state DROP COLUMN IF EXISTS catalog_parse_ok;"

# 0001 (repo state)
psql $STORE_DSN -c "DROP INDEX IF EXISTS idx_repo_state_policy_version;"
psql $STORE_DSN -c "DROP INDEX IF EXISTS idx_repo_state_freshness;"
psql $STORE_DSN -c "DROP TABLE IF EXISTS repo_state;"
```

Rolling back 0002 without downgrading the binary leaves a binary that
writes posture against tables that no longer exist. Those writes are
best-effort and will not fail jobs — they log at Warn and increment
`store_writeback_total{outcome="error"}` on every check, which is loud
but harmless. Downgrade the binary first if the rollback is meant to
last.

If you roll back manually with `psql`, also fix `schema_migrations` or
the next startup will believe the migration is still applied:

```bash
psql $STORE_DSN -c "UPDATE schema_migrations SET version = 1, dirty = false;"
```

The next binary startup re-applies the migrations and the tables come
back empty (no row recovery — the data is gone). Posture rebuilds
itself as repos are re-checked; `actionable_since` restarts from the
rebuild, so historical since-dates do not survive. Better to roll
forward (a new migration that mutates the schema) than down.

## Monitoring schema operations

`repo_guardian_store_query_seconds{operation="get_repo_state"|"update_repo_state"|"stale_repos",outcome="ok"|"error"}` exposes the per-query duration histogram. The starter PrometheusRule includes
a `RepoGuardianStoreQueryErrors` alert that fires when error rate
exceeds 10% over 10 minutes — usually a sign of pool exhaustion,
network flap, or migration drift.

## Removing memory backend

Chart `1.0.0-rc.1` / appVersion `1.9.0` (IMPL-0016) deletes the
in-memory store, queue, and ticker scheduler. The chart now ships
Postgres + Valkey as the only supported backing services.

If you're upgrading from chart `0.7.x` and were running with
`store.backend=memory` / `queue.backend=memory` /
`scheduler.backend=ticker`, the binary will refuse to start with:

```
STORE_BACKEND="memory" is no longer supported (memory backend
removed in IMPL-0016 (chart 1.0.0)). Migration runbook:
<this URL>
```

### Migration paths

1. **Out-of-the-box (baked Postgres + baked Valkey).** Simplest:
   the new defaults bring up StatefulSets for both services with
   chart-rendered Secrets. No values overrides needed. Suitable
   for homelab / single-cluster deployments.

   ```bash
   helm upgrade repo-guardian charts/repo-guardian \
     --reset-then-reuse-values
   ```

2. **CNPG-managed Postgres + baked Valkey.** Set
   `store.postgres.mode=cnpg` and the chart renders a
   CloudNativePG `Cluster` CR. Valkey continues to come up baked.

   ```yaml
   store:
     backend: postgres
     postgres:
       mode: cnpg
   ```

3. **Fully external infra.** Point both services at managed
   instances (RDS / ElastiCache for Valkey, etc.). The operator
   provides DSN secrets via `existingSecret`.

   ```yaml
   store:
     backend: postgres
     postgres:
       mode: external
       existingSecret: my-rds-secret
       existingSecretKey: STORE_DSN
   queue:
     backend: valkey
     valkey:
       mode: external
       existingSecret: my-valkey-secret
       existingSecretKey: QUEUE_VALKEY_DSN
   scheduler:
     backend: valkey
   ```

### Local development without memory backend

`make run-local` now depends on `make dev-services`, which brings
up `docker-compose.dev.yaml` (Postgres + Valkey containers at
`localhost:5432` / `localhost:6379` with default credentials). The
`make` target wires the DSN env vars automatically; nothing extra
in your shell.

```bash
make dev-services   # bring up Postgres + Valkey
make run-local      # build + run against them
make dev-stop       # tear down when done
```

### Data loss

The in-memory store held only the post-IMPL-0011 sweep cadence
state (`last_checked_at` / `last_check_status` per repo). It was
non-durable by design — restarting a memory-backed binary already
discarded the state. Moving to Postgres means future restarts
*preserve* state; there's nothing to migrate forward, just a
fresh database to populate via normal reconcile activity.

## Removing the rate-limit reserve knobs (IMPL-0022)

The chart version shipping IMPL-0022 removes three published values.
**A values file that still sets any of them fails at render time** —
JSON Schema alone would have accepted the unknown keys and silently
ignored them, so the chart carries an explicit
`repo-guardian.validateRemovedValues` guard that names the removal and
points here. Delete the values:

| Removed value | Removed env var | What replaces it |
|---|---|---|
| `staleSweep.rateLimitReserve` | `RATE_LIMIT_RESERVE` | Nothing to set. Rate-limited work now defers itself with a due-time instead of being skipped at enqueue. |
| `discovery.reserveFraction` | `DISCOVERY_RESERVE_FRACTION` | Nothing to set. The BudgetTracker it configured is gone. |
| `discovery.estimatedCostPerRepo` | `DISCOVERY_ESTIMATED_COST_PER_REPO` | Nothing to set. Same. |

**There is no replacement knob, and that is the point.** The two gates
these values configured were two of three overlapping throttling
layers; IMPL-0022 collapses them into one delayed-requeue mechanism
that measures actual pressure rather than projecting it from operator
estimates. The BudgetTracker in particular never gated anything in
production — nothing outside tests populated its cache, so every
lookup fell open (INV-0012 finding A).

### What to check after upgrading

1. **Dashboards and alerts referencing the removed metrics.** These
   series stop being produced: `repo_guardian_api_budget_*` (five),
   `repo_guardian_enqueue_gated_by_budget_total`,
   `repo_guardian_rate_limit_reserve_blocked_total`,
   `repo_guardian_github_rate_limit_waits_total`,
   `repo_guardian_github_rate_limit_wait_seconds`. A panel or alert
   built on them will go silent, not error. The `contrib/` dashboard
   and alert pack are already updated; if you forked them, re-point at
   `queue_delayed_total` / `queue_delay_seconds` / `queue_delayed_depth`.
2. **The `RepoGuardianBudgetGated` alert is gone.** It could never fire
   anyway. Its replacements are `RepoGuardianQueueBackpressure` and
   the re-pointed `RepoGuardianRateLimitThrottling`.
3. **`repo_guardian_rate_limit_remaining` still works.** The sweep
   keeps sampling it once per installation per sweep; only the gating
   decision was removed. `RepoGuardianRateLimitNearExhaustion` keeps
   its feed.
4. **Set `config.maxJobAttempts` if the default of 10 does not suit
   you.** This is the one new knob — see the runbook below.

### New: `MAX_JOB_ATTEMPTS` and `REAPER_INTERVAL`'s second job

`config.maxJobAttempts` (default 10, env `MAX_JOB_ATTEMPTS`, schema
minimum 1) caps how many deliveries a job may accumulate — deferrals
and reaper requeues both count — before it is dropped with a terminal
error in `repo_state`. The next stale sweep re-enqueues the repo, so a
drop is a bounded retry, not an abandonment.

`REAPER_INTERVAL` (default 60s) now does double duty: it is both the
stuck-job reaping cadence *and* the delayed-job promotion cadence.
Delayed jobs are therefore delivered up to one interval after their
due-time. This is deliberate (DESIGN-0021 OQ3): one leader election and
one timer serve both jobs. If you tightened `REAPER_INTERVAL` for fast
worker-crash recovery you now also get tighter promotion latency; if
you stretched it to reduce Valkey load, deferred work waits longer.

Full operational detail — reading the new metrics, responding to the
two new alerts, forcing a deferral to verify the path — is in
[Delayed requeue — operator runbook](delayed-requeue-runbook.md).
