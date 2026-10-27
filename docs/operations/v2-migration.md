# Moving to repo-guardian v2

v2.0.0 is a **fresh install**. A v1 deployment, or a v2 release
candidate from before the controls line, is stopped and replaced; it is
never upgraded in place. The new release starts on an empty database
and a new Temporal namespace, and the only thing it does to the old
deployment's work is close its open pull requests once the new ones
replace them. Rollback is the previous chart pointed back at the
previous database, which the cutover never reads or modifies.

The design is [DESIGN-0032](../design/0032-evaluation-and-remediation-workflows-change-driven-remediation.md)
(§ Migration / Rollout Plan, D26–D28); the pull-request tool is
[rgctl](https://github.com/donaldgifford/repo-guardian/blob/main/docs/operations/rgctl.md).

<!--toc:start-->
- [Before you start](#before-you-start)
- [Cutover](#cutover)
  - [1. Stop every old writer](#1-stop-every-old-writer)
  - [2. Stop the old Temporal work](#2-stop-the-old-temporal-work)
  - [3. Record the old pull requests](#3-record-the-old-pull-requests)
  - [4. Install against a fresh database](#4-install-against-a-fresh-database)
  - [5. Start the roles and verify evaluation](#5-start-the-roles-and-verify-evaluation)
  - [6. Close the old pull requests](#6-close-the-old-pull-requests)
- [Rollback](#rollback)
- [What is not carried over](#what-is-not-carried-over)
- [Removed configuration](#removed-configuration)
  - [Environment variables](#environment-variables)
  - [Removed chart values](#removed-chart-values)
  - [HCL](#hcl)
  - [Controls environment variables](#controls-environment-variables)
  - [Controls chart values](#controls-chart-values)
<!--toc:end-->

## Before you start

- **A new database.** An empty Postgres database (or a new CNPG
  cluster, or the chart's baked one on a new volume) with the owner role
  and the two application roles, `rg_evaluator` and `rg_remediator`.
  The chart provisions the roles in `baked` and `cnpg` modes; for an
  external database create them yourself
  ([Controls database roles](v2-onboarding.md#controls-database-roles)).
  Do not point the release at the old database: the controls schema is
  its own migration chain for an empty database and refuses one that
  holds the old tables.
- **A new Temporal namespace** (or the same cluster with a namespace the
  old deployment never used). The controls roles run their own worker
  deployments, `repo-guardian-eval` and `repo-guardian-remediate`, so
  nothing has to drain, but a separate namespace keeps the old
  executions out of the way of the new ones.
- **The two GitHub Apps.** The Evaluation App (read-only) and the
  Remediation App (writes), each with its own webhook URL:
  `/webhooks/github/eval` and `/webhooks/github/remediate`. The old v1
  App can be reduced to the Evaluation App's permissions, or replaced
  ([The two GitHub Apps](v2-onboarding.md#4-the-two-github-apps)). Keys
  are files mounted from Secrets, never environment values.
- **A controls policy directory** for `policy.files` or
  `policy.existingConfigMap`, with every assignment in
  `mode = "evaluate"` for the first install.
- **rgctl**, and the private key (or an operator token) of **every old
  App** that opened pull requests: a v1 deployment's App and, if you
  ran one, a release candidate's App.
- **Values edited** for the controls chart: every key under [Removed
  configuration](#removed-configuration) is gone, and the render fails
  naming any still set.

## Cutover

Follow the steps in order. The order is the safety: no old writer runs
while the old pull requests are recorded, and nothing is closed until
the new install has proved it can see the fleet.

### 1. Stop every old writer

Scale the release candidate's `worker` role (and `all`, if you ran that
topology) to zero, and scale any v1 Deployment to zero. Confirm no old
pod runs:

```bash
kubectl -n <namespace> scale deploy/<release>-repo-guardian-worker --replicas=0
kubectl -n <namespace> get pods -l app.kubernetes.io/name=repo-guardian
```

From here until the new ingest is ready, GitHub records the old App's
webhook deliveries as failed. Nothing needs redelivering: the new
install evaluates every repository on discovery.

### 2. Stop the old Temporal work

Only a release candidate has Temporal state; skip this for v1. On the
old namespace, delete the `discovery` and `snapshot` schedules, then
terminate every running execution on the old task queue, not only the
named prefixes (`repo/*`, `installation/*`, `policy-rollout/*`,
`bootstrap/v1`, `discovery/installation/*`, in-flight `webhook/*` and
any run a schedule started):

```bash
temporal schedule delete --schedule-id discovery
temporal schedule delete --schedule-id snapshot
temporal workflow terminate \
  --query 'TaskQueue="repo-guardian" AND ExecutionStatus="Running"' \
  --reason "controls cutover"
```

### 3. Record the old pull requests

Before anything closes them, list every open repo-guardian pull request
from GitHub and keep the record. Do this **once per old App**, with
that App's credentials, for every org it manages: rgctl matches a pull
request by its author, and each App is its own bot.

```bash
export RGCTL_APP_ID=<old app id> RGCTL_PRIVATE_KEY_FILE=~/.config/rgctl/<old app>.pem
rgctl prs list --org <org> --format json --out <app>-<org>.json
```

Keep the files: step 6 closes exactly what they list, and they are the
record of what the old deployment left open.

### 4. Install against a fresh database

Install the controls chart into the new database and namespace:

```bash
helm install repo-guardian oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version <2.x version> -n <namespace> -f values.yaml
```

The migrate hook Job applies the controls schema to the empty database
(one chain, `repo-guardian migrate --chain controls`) and activates the
policy version. On a database that already holds tables it fails on
its first `CREATE TABLE` rather than adopting them. Set each App's
webhook URL to its new route on your ingress
([A public route for webhooks](v2-onboarding.md#5-a-public-route-for-webhooks)).

### 5. Start the roles and verify evaluation

The `evaluator` and `remediator` roles (or `all`) start, and each
promotes its own build to the current version of its own worker
deployment at first start; there is nothing to drain. Verify:

- every pod is ready and the migrate Job succeeded;
- `temporal worker deployment describe --deployment-name repo-guardian-eval`
  (and `repo-guardian-remediate`) names the image's build as current;
- `ControlsDiscoveryWorkflow` completed and there is one
  `evaluation/<id>` workflow per managed repository;
- `repo-guardian report`, the API's `/api/v1/summary` or the UI shows
  posture per org, with no `unmeasurable` surprise.

A check that was in flight at step 1 is simply never resumed; the new
install evaluates that repository on its own schedule.

### 6. Close the old pull requests

Close an org's old pull requests **when that org turns on
`mode = "remediate"`**, not all at once: while the org is evaluate-only
the old pull requests keep proposing their fixes, and once per-control
pull requests replace them the old ones only duplicate them. For each
org, with the record files from step 3:

```bash
rgctl prs close --from <app>-<org>.json --yes
```

rgctl closes exactly the recorded pull requests on v1's three frozen
branches (`repo-guardian/add-missing-files`,
`repo-guardian/add-catalog-info`, `repo-guardian/set-custom-properties`)
and the release candidate's, with a comment pointing at the per-control
pull requests. It skips a pull request a human has edited and never
deletes its branch. `--delete-branch` is an opt-in; repo-guardian itself
never deletes a branch.

## Rollback

Rollback is the previous chart pointed back at the previous database:

1. Scale the new release to zero (or uninstall it).
2. Deploy the previous chart with its previous values. Its database was
   never read or modified by the cutover, so it starts where step 1
   stopped it.
3. For a release candidate, Temporal needs no step: its build is still
   the current version of its own `repo-guardian` worker deployment,
   because the controls builds were promoted on their own deployments.
   Its workers resume as soon as they start, and its bootstrap
   recreates the executions terminated at step 2.
4. Point each old App's webhook back at the old route.

There is no in-place upgrade path from v1 or from the release-candidate
line, and none back.

## What is not carried over

- **History.** Repository ids, rename and transfer events, findings,
  check logs and compliance snapshots restart on the new database. Keep
  the old database (and its values) until you no longer need its
  history, then archive it with `pg_dump` and drop it.
- **Executions terminated at step 2**, and any webhook delivered while
  the old deployment was down.
- **Old pull requests closed at step 6.** The new install opens one
  pull request per control on `repo-guardian/<control slug>` in place of
  each; a closed old one is never reopened.
- **The old App's identity on new pull requests.** New pull requests
  are authored by the Remediation App's bot.

## Removed configuration

### Environment variables

These were removed when v2 moved to Temporal, and the controls line
still ignores them. Every role logs one warning at startup naming each
one still set (`removed configuration env vars are set and ignored`,
with `migration` pointing here), so a stale Deployment patch shows up
in the logs. Remove them:

| Removed | Replacement |
| --- | --- |
| `QUEUE_BACKEND`, `SCHEDULER_BACKEND`, `QUEUE_VALKEY_DSN` | `TEMPORAL_ADDRESS`, `TEMPORAL_NAMESPACE`, `TEMPORAL_EVAL_TASK_QUEUE`, `TEMPORAL_REMEDIATE_TASK_QUEUE` |
| `JOB_ACK_TIMEOUT`, `REAPER_INTERVAL`, `MAX_JOB_ATTEMPTS` | Temporal activity timeouts and retry policies |
| `POD_NAME` (lock identity) | none; Temporal Schedules need no leader |
| `STALE_SWEEP_BATCH_SIZE` | none; each repository schedules its own next check |
| `POSTURE_EXPORT_INTERVAL` | none; the API reads compliance from Postgres |
| `WORKER_COUNT`, `QUEUE_SIZE` | `EVALUATOR_CONCURRENCY`, `REMEDIATOR_CONCURRENCY` |
| `SCHEDULE_INTERVAL` | `EVAL_INTERVAL` |
| `STORE_BACKEND` | none; Postgres is the only store |

`RECONCILE_FRESHNESS` has no replacement: nothing reads it. A fresh
install spreads first evaluations across `EVAL_INTERVAL` on its own.

### Removed chart values

Chart 2.x fails the render on each of these, naming the key:

| Removed | Replacement |
| --- | --- |
| `queue.*` | `temporal.*` |
| `scheduler.*` | none (Temporal Schedules) |
| `staleSweep.freshness` | `evalInterval` (`EVAL_INTERVAL`) |
| `staleSweep.batchSize` | none |
| `posture.exportInterval` | none |
| `config.workerCount`, `config.queueSize` | `evaluator.concurrency`, `remediator.concurrency` |
| `config.scheduleInterval` | `evalInterval` |
| `config.maxJobAttempts` | none |

The controls line renamed or removed more values since; see [Controls
chart values](#controls-chart-values). The chart README lists the
current set.

### HCL

`guardian.hcl` is not read at all on the controls line: the policy is
a directory (`enterprise.hcl`, `catalogue/`, `orgs/`, `templates/`).
Rewrite it as one; [v2 at a glance](v2-overview.md#what-changed-from-guardianhcl)
maps each v1 rule kind to its control type, and `rgctl config show`
prints what an old file declares.

### Controls environment variables

The switch to controls (IMPL-0029) removed the rule engine and the
single-App worker. The variables below are ignored, and the same
startup warning names each one still set, with `migration` pointing
here:

| Removed | Replacement |
| --- | --- |
| `GITHUB_APP_ID`, `GITHUB_PRIVATE_KEY_PATH`, `GITHUB_PRIVATE_KEY`, `GITHUB_WEBHOOK_SECRET` | `EVAL_*` and `REMEDIATE_*` (`_GITHUB_APP_ID`, `_GITHUB_PRIVATE_KEY_PATH`, `_WEBHOOK_SECRET`) |
| `CHECK_INTERVAL` | `EVAL_INTERVAL` |
| `WORKER_ACTIVITY_CONCURRENCY` | `EVALUATOR_CONCURRENCY`, `REMEDIATOR_CONCURRENCY` |
| `STORE_POSTGRES_MAX_CONNS` | `EVALUATOR_DB_POOL_SIZE`, `REMEDIATOR_DB_POOL_SIZE` |
| `TEMPLATE_DIR` | the policy root's `templates/` directory |
| `SKIP_FORKS`, `SKIP_ARCHIVED` | none: archived repositories and forks are always parked |
| `DRY_RUN`, `AUTO_CLOSE_PR`, `ORPHAN_CLEANUP`, `STRICT_TEMPLATES` | none: the evaluator never writes, and the rule engine they configured is gone |
| `RECONCILE_FRESHNESS` | none: the rc backfill that read it is gone |
| `PR_STALE_AFTER` | none: the findings endpoints that used it are gone |

`GUARDIAN_CONFIG` stays, but it now names the controls policy
directory (`enterprise.hcl`, `catalogue/`, `orgs/`, `templates/`), not
a `guardian.hcl` file. `RATE_LIMIT_THRESHOLD`, `DISCOVERY_*`,
`COMPLIANCE_SNAPSHOT_INTERVAL`, `POLICY_ROLLOUT_WINDOW` and
`CHECKS_RETENTION` keep their meaning.

### Controls chart values

The chart fails the render on each of these, naming the key and this
section (the schema first, the `validateRemovedValues` helper behind
it):

| Removed | Replacement |
| --- | --- |
| `policy.config` | `policy.files`: the policy directory as a nested map, a string per file and a map per directory (`enterprise.hcl`, `catalogue: {...}`, `orgs: {...}`, `templates: {...}`) |
| `templates.*` | the policy directory's `templates/`, in `policy.files` |
| `secrets.*`, `config.appId` | `github.eval.*` and `github.remediate.*` |
| `checkInterval` | `evalInterval` |
| `worker.replicas`, `worker.concurrency`, `worker.resources` | `evaluator.*` and `remediator.*` (`worker.buildId` stays) |
| `store.postgres.maxConns` | `evaluator.dbPoolSize`, `remediator.dbPoolSize` |
| `config.dryRun`, `config.skipForks`, `config.skipArchived`, `policy.autoClosePR`, `policy.orphanCleanup`, `templating.strict` | none |
| `migrate.freshness`, `api.prStaleAfter` | none |

`policy.existingConfigMap` stays: an operator ConfigMap is mounted
whole at `GUARDIAN_CONFIG`, so it holds flat keys unless you mount it
with your own `items` through `extraVolumes`. `templating.vars` stays
for the templates' `env` helper.
