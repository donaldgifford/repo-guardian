# Migrating from v1 to v2

v2 replaces the Valkey work queue, the leader-elected sweeps and the
posture gauges with Temporal workflows. It also replaces v1's
`repo_state`/`rule_state` rows with findings. The cutover reuses the
same Postgres database, the same webhook URL and secret, and the same
PR identity, so v2 picks up where v1 stopped. Designs:
[DESIGN-0025](../design/0025-v2-findings-model-and-v1-data-migration.md)
(data), [DESIGN-0026](../design/0026-v2-temporal-control-plane-and-role-split.md)
(runtime and cutover) and
[DESIGN-0027](../design/0027-v2-read-only-api-business-ui-and-status-page.md) (API).

<!--toc:start-->
- [Prerequisites](#prerequisites)
- [Removed configuration](#removed-configuration)
  - [Environment variables](#environment-variables)
  - [Removed chart values](#removed-chart-values)
  - [HCL](#hcl)
- [The data migration](#the-data-migration)
- [Reporting divergences](#reporting-divergences)
- [Cutover steps](#cutover-steps)
- [Redelivering webhooks](#redelivering-webhooks)
- [What resumes, and why](#what-resumes-and-why)
- [Rollback](#rollback)
<!--toc:end-->

## Prerequisites

- **v1 on the last 1.x release.** The migration adopts v1's schema only
  at golang-migrate version 3 with `dirty = false`. Anything else fails
  with "upgrade to the last v1.x first".
- **A Temporal cluster** the pods can reach, with a namespace created
  for repo-guardian (default `repo-guardian`). `contrib/temporal/` pins
  a tested install. Set `temporal.address`. For mTLS, also set
  `temporal.tls.existingSecret` (`tls.crt`, `tls.key`, optionally
  `ca.crt`).
- **`guardian.hcl` edited.** Remove `worker_count`, `queue_size` and
  `schedule_interval` from `guardian {}`. v2 fails to load a policy that
  still sets them.
- **Values edited.** Every key in [Removed chart
  values](#removed-chart-values) is gone, and a policy is required
  (`policy.config` or `policy.existingConfigMap`).
- **For the API** (optional, `api.enabled`): an OIDC issuer
  (`api.auth.issuer`), and on external Postgres a `repoguardian_ro`
  login you create, passed in `api.roDsn.existingSecret`.
- **Room for the re-check.** Every repository is re-checked across
  `policyRolloutWindow` (default `24h`) after the swap. Budget for one
  full-fleet pass of GitHub API calls in that window.

## Removed configuration

### Environment variables

These are warned about and ignored at startup:

| Removed | Replacement |
| --- | --- |
| `QUEUE_BACKEND`, `SCHEDULER_BACKEND`, `QUEUE_VALKEY_DSN` | `TEMPORAL_ADDRESS`, `TEMPORAL_NAMESPACE`, `TEMPORAL_TASK_QUEUE` |
| `JOB_ACK_TIMEOUT`, `REAPER_INTERVAL`, `MAX_JOB_ATTEMPTS` | Temporal activity timeouts and retry policies |
| `POD_NAME` (lock identity) | none; Temporal Schedules need no leader |
| `STALE_SWEEP_BATCH_SIZE` | none; each repository schedules its own next check |
| `POSTURE_EXPORT_INTERVAL` | none; the API reads compliance from Postgres |
| `WORKER_COUNT`, `QUEUE_SIZE` | `WORKER_ACTIVITY_CONCURRENCY` |
| `SCHEDULE_INTERVAL` | `CHECK_INTERVAL` |

`RECONCILE_FRESHNESS` is read once, by `migrate`, to seed each
repository's first due time. Otherwise it is ignored.

### Removed chart values

Chart 2.0.0 fails the render on each of these, naming the key:

| Removed | Replacement |
| --- | --- |
| `queue.*` | `temporal.*` |
| `scheduler.*` | none (Temporal Schedules) |
| `staleSweep.freshness` | `checkInterval` for the cadence; `migrate.freshness` for the backfill |
| `staleSweep.batchSize` | none |
| `posture.exportInterval` | none |
| `config.workerCount`, `config.queueSize` | `worker.concurrency` |
| `config.scheduleInterval` | `checkInterval` |
| `config.maxJobAttempts` | none |

New values: `topology` (`split` or `all`), `temporal.*`,
`checkInterval`, `policyRolloutWindow`, `checksRetention`, `ingest.*`,
`worker.*` (including optional KEDA), `api.*` and `migrate.freshness`.
`migrate.enabled` now defaults to `true`. See the chart README.

### HCL

`guardian { worker_count, queue_size, schedule_interval }` fail the
policy load. Delete them: concurrency is `WORKER_ACTIVITY_CONCURRENCY`
and cadence is `CHECK_INTERVAL`.

## The data migration

`repo-guardian migrate` runs as the chart's hook Job. On a v1 database
it:

1. records that the v1 schema is present (`00001_adopt_v1`);
2. creates the v2 tables (`00002_v2_schema`);
3. backfills them from v1, in one transaction (`00003_backfill_v1`).

v1's tables are left untouched. That is what makes rollback work.

The migration refuses to run while `repo_state.last_checked_at` was
written in the last 60 seconds, which catches a v1 replica that is
still running. `--force-running` overrides it. Only use it on a restored
copy.

**Adoption rules** (DESIGN-0025 § v1 → v2 migration):

- **Installations** come from `repo_state`'s distinct
  `(installation_id, owner)` pairs. An id with several owners keeps the
  most recently checked one and logs the rest.
- **Repositories** keep `active`, `last_checked_at`, the last outcome
  and the error.
  - Parked rows get a park reason. `error` becomes `access_denied`;
    `skipped` with `archived` or `fork` keeps that reason; anything else
    is `unknown`.
  - Two rows that differ only in case keep the most recently checked
    one and log the rest.
- **Next due time** is `last_checked_at + freshness`. Rows never checked
  are spread across one freshness window.
- **Findings.** `actionable = false` becomes `compliant`.
  `actionable = true` becomes `non_compliant` with reason
  `migrated_from_v1`, keeping v1's `actionable_since`. Each repository's
  first v2 check replaces `migrated_from_v1` with a real reason.
- **History.** Compliance snapshots carry over. Every finding's history
  starts with one `created` event at the migration.
- **Policy version.** It is stored as `v1:<hash>`, which never equals a
  v2 version, so every repository counts as drifted and is re-checked.

**Rehearse first.** Restore a copy of production into a scratch
database and preview the counts and collisions without writing:

```bash
repo-guardian migrate --dsn "$SCRATCH_DSN" --dry-run --force-running
```

Take a dump before the real run. The migration only adds tables, but a
dump is the only way back from a bad backfill:

```bash
pg_dump --format=custom --file=repo-guardian-v1.dump "$STORE_DSN"
```

## Reporting divergences

GitHub actions are unchanged, but some records change. Each of these
moves a compliance number on purpose:

| Case | v1 records | v2 records | Effect on compliance % |
| --- | --- | --- | --- |
| Foreign PR open for the rule | `actionable=false` (compliant) | `non_compliant` + `foreign_pr` | falls |
| Branch-protection target branch missing | `actionable=false` (compliant) | `not_applicable` / `branch_missing` | denominator shrinks |
| Rule out of scope, ignored, gate closed | no row | `not_applicable` row | none (excluded) |
| Global ignore, policy out of scope, empty repo | rows cleared | `not_applicable` rows | none (excluded) |
| Gate referee errored | no row | `unknown` / `gate_error` | none (excluded, shown) |
| File and setting rule share a name | one row, last kind wins | two findings | both counted |

Compliance for a rule is `compliant / (compliant + non_compliant)` over
active repositories. `not_applicable` and `unknown` are excluded from
both terms and reported separately. An empty denominator is
*unmeasured*, never 100%. `repo-guardian migrate verify-shadow`
classifies these cases when it compares v1 and v2.

## Cutover steps

1. **Install Temporal** and create the namespace.
2. **Dry-run the migration** against a restored copy (above) and review
   the counts and collisions.
3. **Dump the database:** `pg_dump`, as above.
4. **Scale v1 to zero.** From here until v2's ingest is ready, GitHub
   records webhook deliveries as failed.

   ```bash
   kubectl -n repo-guardian scale deploy/repo-guardian --replicas=0
   ```

5. **Upgrade to chart 2.x** with the edited values and policy:

   ```bash
   helm upgrade repo-guardian oci://ghcr.io/donaldgifford/charts/repo-guardian \
     --version 2.0.0 -n repo-guardian -f values.yaml
   ```

   The `pre-upgrade` hook runs `migrate`. The worker then starts
   `BootstrapWorkflow`, which starts one `RepoWorkflow` per active
   repository and rolls the policy out across `policyRolloutWindow`.
6. **Point ingress at the webhook Service.** It keeps the release's full
   name, so an existing route usually needs no change.
7. **Verify:**
   - `kubectl get pods`: ingest and worker are ready, and the migrate Job
     succeeded.
   - Temporal UI: `BootstrapWorkflow` completed, and there is one
     `repo/<id>` workflow per active repository.
   - `repo-guardian report`, or the API's `/api/v1/summary`, shows the
     fleet. Expect `migrated_from_v1` reasons to drain across the
     rollout window.
8. **Redeliver** the outage window's failed webhooks (below). This is
   optional.

## Redelivering webhooks

Deliveries that failed while v1 was down are listed under the GitHub
App's **Advanced → Recent Deliveries**. Redeliver any that matter. v2
accepts them on the same URL and secret. The redelivery is optional,
because the rollout re-checks every repository anyway. It just brings
push-triggered re-checks forward.

## What resumes, and why

- **Webhooks.** `POST /webhooks/github` on the same Service, with the
  same secret.
- **Pull requests.** The branch `repo-guardian/add-missing-files`, the
  PR title, the `<!-- repo-guardian:reconcile-log:v1 -->` marker and its
  hash tag are unchanged, and a test locks them. v2's first check finds
  v1's open PR and updates it.
- **Parked repositories stay parked.** Bootstrap starts workflows only
  for active rows, and discovery re-applies v1's un-park rule.
- **Nothing in Valkey is needed.** Postgres was always the source of
  truth. In-flight and delayed jobs are dropped, and the rollout
  re-checks every repository they referenced.
- **The re-check is paced.** `policyRolloutWindow` spreads it out, and
  each installation's rate budget bounds it further.

## Rollback

Rollback is supported until a later v2 release drops the v1 tables.

1. Scale v2 to zero (`ingest`, `worker`, and `api` if enabled).
2. Deploy the last v1 chart against the same database. v1 finds its
   schema at version 3 and its tables as they were at the swap. Its
   stale sweep re-checks everything older than freshness, and it adopts
   any PR v2 opened, because the PR identity is the same.
3. Abandon the Temporal namespace, or delete it.

Rollback loses everything v2 learned since the swap. If the backfill
itself was bad, restore the `pg_dump` instead.
