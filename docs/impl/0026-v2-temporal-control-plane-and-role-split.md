---
id: IMPL-0026
title: "v2 Temporal control plane and role split"
status: Draft
author: Donald Gifford
created: 2026-09-25
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0026: v2 Temporal control plane and role split

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-09-25

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Execution order](#execution-order)
- [Pre-implementation audit (2026-09-25)](#pre-implementation-audit-2026-09-25)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: The v2 branch, CI and release plumbing](#phase-1-the-v2-branch-ci-and-release-plumbing)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Temporal foundations](#phase-2-temporal-foundations)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: RepoWorkflow and the check activities](#phase-3-repoworkflow-and-the-check-activities)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Rate budget — InstallationWorkflow, fairness, priority](#phase-4-rate-budget--installationworkflow-fairness-priority)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Roles and ingest](#phase-5-roles-and-ingest)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Discovery, snapshots, policy rollout, bootstrap](#phase-6-discovery-snapshots-policy-rollout-bootstrap)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Delete the v1 runtime](#phase-7-delete-the-v1-runtime)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: Observability and chart 2.0.0](#phase-8-observability-and-chart-200)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
  - [Phase 9: Cutover — runbook, shadow run, homelab swap, rc.1](#phase-9-cutover--runbook-shadow-run-homelab-swap-rc1)
    - [Tasks](#tasks-8)
    - [Success Criteria](#success-criteria-8)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Build DESIGN-0026 on the `v2` branch. Replace v1's Valkey queue,
leader-elected scheduler, reaper, stale sweep and posture exporter with
Temporal:

- one `RepoWorkflow` per repository;
- one `InstallationWorkflow` per installation for the rate budget;
- Schedules for discovery and snapshots;
- rollout and bootstrap workflows.

Split the binary into `ingest`, `worker`, `api`, `migrate` and `all`
roles, ship chart 2.0.0, and cut over the homelab from a live v1 to
`v2.0.0-rc.1`. The homelab Temporal cluster is the test bed
throughout; there is no separate spike.

**Implements:** DESIGN-0026 (from INV-0018, INV-0019)

## Scope

### In Scope

- `v2` branch CI and release changes (DESIGN-0026 § The `v2` branch).
- `contrib/temporal/` reference configuration. The homelab cluster is
  brought in line with it.
- `internal/temporal` (client, mTLS, version floor, SDK metrics),
  `internal/workflows`, `internal/activities`.
- Every workflow in DESIGN-0026's table, with its activities, tests and
  replay histories.
- Roles and CLI dispatch, and readiness checks.
- The ingest HTTP path and the new webhook event table.
- Deletion of the queue, scheduler, reaper, sweep, posture exporter,
  worker package, Valkey wiring, the removed metrics, and the
  removed env and HCL knobs with migration errors.
- The v1 store implementation and `rule_state` write-back, deleted
  together with their callers (IMPL-0025 OQ1).
- The monitoring generator (E1/E2 deleted, E3 rebuilt, E4 trimmed) and
  the v2 alert catalogue.
- Chart 2.0.0: topology, roles, KEDA, secret scoping, removed-value
  guards, checksum annotations, migrate hook wiring.
- `docs/operations/v2-migration.md`, `migrate verify-shadow`, and the
  homelab cutover.

### Out of Scope

- The findings schema, store and backfill (IMPL-0025).
- The `api` role's endpoints and the UI (IMPL-0027). This IMPL only
  reserves the role name in dispatch and the chart slots.
- Notifications on transitions (a later design; the `Notify` activity
  hook is not built).
- Upgrade-on-Continue-as-New, which waits for GA (DESIGN-0026 OQ7).
- GA release `v2.0.0` and the fast-forward of `main`, which follow rc
  soak.

## Execution order

See IMPL-0025 § Execution order for the cross-IMPL graph. In short:

1. Phase 1 here goes first.
2. Phase 2 runs in parallel with IMPL-0025 Phases 1–5.
3. Phases 3–6 need IMPL-0025 Phases 2–4.
4. Phase 7 needs IMPL-0025 Phase 7.
5. Phase 9 needs IMPL-0025 Phase 8 and IMPL-0027's chart phase if the
   UI ships in rc.1 (OQ10).

## Pre-implementation audit (2026-09-25)

Code at `main` @ `5d9c440`. Deltas and facts DESIGN-0026 did not name:

- **Blast radius of the deletion** (Phase 7):
  - `internal/queue` (valkey.go 602, reaper.go 229 LOC; the
    integration test alone is 807);
  - `internal/scheduler` (valkey 206; discoverer.go 240, reused as
    activity logic);
  - `internal/worker` (worker.go 579 plus 1,469 LOC of tests);
  - `internal/checker/{sweep,posture}.go` and their tests;
  - `internal/checker/multireplica_integration_test.go`;
  - `internal/observability/valkey.go`.

  The redis imports sit in 5 non-test and 5 test files. go.mod drops
  `redis/go-redis/v9` and `redisotel`.
- **The LogQL contract test locks lines that Phase 7 deletes.**
  `TestLogLines_AreStillEmittedByTheBinary`
  (`monitoring/dashboard/logline_test.go:29`) locks, from `e4.go:17-33`:
  - `worker.go`: "parking repository until discovery sees it again",
    "job exceeded attempt cap", "store write-back failed", "rule-state
    write-back failed", "rate limit throttled; deferring job";
  - `sweep.go`: "stale-sweep complete";
  - `handler.go`: "failed to enqueue job".

  The E4 constants must be deleted or re-pointed **in the same commit**
  as the code (the IMPL-0024 1.2 lesson). The parking line is re-emitted
  by the `Park` activity, so it survives with an unchanged string.
- **The alert catalogue** (`monitoring/alert/alert.go`) defines 24
  alerts. After Phase 7/8:
  - **Deleted** (metrics gone): PostureExportStalled, StaleOpenPRs,
    PRDrift, SettingRemediationChurn, BranchProtectionChurn,
    RuleNeverApplies, PropertySchemaMissing, CatalogParseFailures,
    PRBurst, QueueDepthHigh, ReaperRequeues, QueueBackpressure,
    JobsExhausted, NoSchedulerLeader, RateLimitThrottling,
    RepoAccessDenied, NoRepoChecks.
  - **Kept or reworked**: HighErrorRate and AllChecksFailing →
    CheckErrorRate on `checks_total`; SlowChecks; NoWebhooks;
    WebhookRejectionsHigh; StoreQueryErrors → StoreErrors;
    RateLimitNearExhaustion.
  - **New**: ScheduleToStartLatencyHigh, NoWorkerPollers,
    WorkflowTaskFailures, TemporalUnreachable.

  The chart's `prometheusrule.yaml` hand-mirrors 12 of them and must
  move in step.
- **E1 and E2 read only business metrics**, so both are deleted. E3
  reads 14 `repo_guardian_*` series; 9 of them are queue, scheduler,
  posture or writeback series that disappear.
- **Config** (`config.go`) reads 13 env vars that v2 removes (lines
  234–349), plus `validateBackend` for queue and scheduler (402, 410).
  The HCL attributes are parsed at `types.go:76-78`,
  `loader.go:371-373, 409-414` and `validate.go:36,40`. The examples
  that set them are `examples/values-multi-org.yaml:23-25`,
  `values-with-policy.yaml:25-27`, `guardian-enterprise.hcl:38-40`,
  `guardian-minimal.hcl:17-19` and `guardian-full.hcl:19-21`, all
  exercised by `examples/examples_test.go`. They are fixed in the same
  commit as the rejection.
- **Chart:**
  - 14 env vars in `deployment.yaml` go away (83–174);
  - `values.yaml` blocks `queue:` 300, `scheduler:` 358,
    `staleSweep:` 363 and `posture:` 391;
  - schema entries at `values.schema.json:10, 37-66, 80-93`;
  - `reservedEnvVars` (`_helpers.tpl:84-85`) lists 9 of the removed
    vars and lacks `MAX_JOB_ATTEMPTS` and `POSTURE_EXPORT_INTERVAL`;
  - `validateRemovedValues` (`_helpers.tpl:218`) is the pattern to
    extend.

  Test suites with removed-key hits:

  | Suite | Hits |
  | --- | --- |
  | `backend_shapes_test.yaml` | 44 |
  | `deployment_env_test.yaml` | 12 |
  | `prometheusrule_test.yaml` | 7 |
  | `values_guard_test.yaml` | 15 |

- **`handleReadyz` checks nothing** (`main.go:706`, 503 only after
  shutdown starts). v2 readiness adds Temporal connectivity and the
  schema version (IMPL-0025 `RequireSchema`).
- **`gracefulShutdown`** (`main.go:572`) takes a queue, a redis client
  and a worker pool. It is rewritten per role.
- **Release plumbing:**
  - `docker-bake.hcl:34`, `ghcr.yml:80-83` and `ecr.yml:110-113` all
    emit `latest` unconditionally;
  - `release.yml` triggers on push to `main` only, so `v2` pushes never
    run the semver bump (good) and never publish (so rc publishing is
    a `workflow_dispatch` of `ghcr.yml`/`ecr.yml` with a tag);
  - `ct.yaml:5` is `target-branch: main`;
  - `changelog-update.yml:29` checks out `ref: main`.
- **go.mod:** no `go.temporal.io/*`. go 1.26.6.
- **`make run-local`** hard-codes Valkey DSNs, and
  `docker-compose.dev.yaml` runs `valkey/valkey:9.1-alpine`. Both
  change in Phase 2 (OQ3).
- **CLAUDE.md** has 14 paragraphs describing v1 queue, scheduler and
  posture contracts (Release section, :166–:243). They are rewritten in
  Phase 7, not deleted piecemeal.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Every task
leaves `make lint && make test` green on `v2`. The v1 runtime keeps
building and running until Phase 7 removes it in one step (IMPL-0025
OQ1).

---

### Phase 1: The v2 branch, CI and release plumbing

#### Tasks

- [ ] 1.1 Create `v2` from `main` and push it. Protect it on GitHub
  (PR required, the same checks as `main`).
- [ ] 1.2 On `v2`, edit `ci.yml:7,9`, `license-check.yml:7,9` and
  `security.yml:5-7` so `branches: [main, v2]`. `release.yml` and
  `gh-pages.yml` stay `main`-only.
- [ ] 1.3 On `v2`, set `ct.yaml` `target-branch: v2`.
- [ ] 1.4 Image tags. In `docker-bake.hcl` `tags()` (`:28-36`), emit
  `latest` only when `VERSION` has no pre-release suffix. In
  `ghcr.yml:80-83` and `ecr.yml:110-113`, give
  `type=raw,value=latest` the input
  `enable=${{ !contains(inputs.tag, '-') }}`. Apply this change on
  `main` as well (a small PR), so a manual rc dispatch from either
  branch can never move `latest`.
- [ ] 1.5 `changelog-update.yml`: add a `ref` input (default `main`)
  so the changelog can be regenerated from `v2`.
- [ ] 1.6 Chart `version: 2.0.0-rc.0` on `v2`. The first real publish
  is rc.1 in Phase 9. `appVersion` tracks the rc tag.
- [ ] 1.7 Document the branch workflow in `CONTRIBUTING.md` (or
  CLAUDE.md on `v2`):
  - engine fixes land on `main` first and merge forward;
  - rc tags are cut by hand from `v2` with `dont-release` PRs
    (CLAUDE.md rule 6);
  - image and chart publishing uses `workflow_dispatch` with the rc
    tag.
- [ ] 1.8 First merge-forward drill: merge `main` into `v2` and
  confirm CI runs on the merge PR.

#### Success Criteria

- A PR into `v2` runs `ci`, `license-check` and `security`, and a
  push to `v2` does not run `release.yml`.
- A dry-run dispatch of `ghcr.yml` with tag `v2.0.0-rc.0` produces a
  metadata tag list that does not contain `latest` (inspect the
  metadata step output).

---

### Phase 2: Temporal foundations

#### Tasks

- [ ] 2.1 Homelab audit, recorded in this doc under a "Homelab Temporal
  baseline" subsection:
  - the server version (must be ≥ 1.31, or upgrade first);
  - persistence and visibility store;
  - `matching.enableFairness` in dynamic config;
  - frontend TLS mode;
  - whether a `repo-guardian` namespace exists.
- [ ] 2.2 `contrib/temporal/`, with the files from DESIGN-0026's table:
  - `values-base.yaml`, `persistence-cnpg.yaml`,
    `visibility-postgres.yaml`, `visibility-opensearch-baked.yaml`,
    `visibility-external.yaml`, `namespace-job.yaml`,
    `networkpolicy.yaml`;
  - a README with the install order.

  Pin the upstream `temporalio/helm-charts` version. Validate with
  `helm template` in a new `make lint-temporal-contrib` target that
  renders each combination.
- [ ] 2.3 Align the homelab cluster with `contrib/temporal/` (base,
  CNPG, Postgres visibility, mTLS, NetworkPolicy, namespace Job).
  Record any deviation.
- [ ] 2.4 Add `go.temporal.io/sdk` (latest at the time of this phase,
  pinned) and `go.temporal.io/sdk/contrib/opentelemetry`.
- [ ] 2.5 `internal/temporal`:
  - `Config` from env (`TEMPORAL_ADDRESS`, `_NAMESPACE`, `_TASK_QUEUE`,
    `_TLS_CERT_PATH`, `_TLS_KEY_PATH`, `_TLS_CA_PATH`,
    `_TLS_SERVER_NAME`);
  - `Dial(ctx, cfg, logger) (client.Client, error)` with mTLS, an
    `slog` adapter and the OTel metrics handler on the existing meter
    provider (IMPL-0023 bridge, so SDK series land on `/metrics`);
  - `CheckServerVersion(ctx, c, "1.31.0")` via `GetSystemInfo` /
    `ServerVersion`; below the floor it returns an error naming the
    floor;
  - `Ping` for readiness.
- [ ] 2.6 Local dev loop (OQ3): replace the `valkey` service in
  `docker-compose.dev.yaml` with `temporal` (the `temporalio/temporal`
  CLI image running `server start-dev`, UI on 8233, namespace
  `repo-guardian` pre-created). Update `make run-local` and
  `dev-services`. The v1 runtime still needs Valkey until Phase 7, so
  keep `valkey` behind a compose profile `v1` until then.
- [ ] 2.7 Integration harness `internal/temporal/temporaltest`:
  `Start(t) client.Client` using the SDK's `testsuite.StartDevServer`
  with a pinned CLI version (OQ2), behind the `integration` build tag.
- [ ] 2.8 A throwaway smoke workflow in a test only: dial the homelab
  frontend with mTLS from a dev machine, run it, and delete it. This
  proves the network path, certificates and namespace before real
  workflows exist.

#### Success Criteria

- The homelab runs Temporal ≥ 1.31 with fairness enabled, matching
  `contrib/temporal/`, and the baseline is recorded.
- `internal/temporal` connects to both the dev server and the homelab,
  and refuses a server below the floor (tested against a stub
  `GetSystemInfo`).
- SDK metrics such as `temporal_request` and
  `temporal_workflow_task_schedule_to_start_latency` appear on the
  process's `/metrics`.

---

### Phase 3: RepoWorkflow and the check activities

#### Tasks

- [ ] 3.1 `internal/activities`:
  - `Activities{engine, store (IMPL-0025 Writer/Reader), ghFactory,
    policyVersion, logger}`, registered by struct;
  - activity names are constants in `internal/workflows/names.go` so
    workflows never import activity code.
- [ ] 3.2 `CheckRepo(ctx, CheckInput{RepoID, CheckKey, Trigger,
  Priority}) (CheckResult, error)`:
  - load the repository row, create the installation client, then
    `engine.CheckRepo`;
  - classify in v1's order (`worker.processJob` 173-245): `AsThrottled`
    → `Deferred{Until: reset+jitter}` returned as a value, then
    `IsAccessDenied` → `Parked{access_denied}`, then `AsSkipped` →
    `Parked{archived|fork}`, else error;
  - on success, `StageCheck` with the outcome set (DESIGN-0026 OQ17),
    returning `Checked{CheckKey, Calls, Rate}`;
  - the call count and rate come from a per-activity counting wrapper
    on the transport (task 4.4 consumes them).
- [ ] 3.3 `RecordCheck`, `RecordCheckError` and `Park` activities as
  thin wrappers over the IMPL-0025 store. `Park` re-emits the log line
  "parking repository until discovery sees it again" with v1's keys
  (the E4 contract).
- [ ] 3.4 `internal/workflows/repo.go`, `RepoWorkflow(ctx, RepoState)`:
  - the loop from DESIGN-0026 § RepoWorkflow: selector over the timer
    and the `recheck`, `policy_changed` and `park` signal channels;
  - coalescing with a pending flag and max priority;
  - `next_due` = finished + `CHECK_INTERVAL` ± 10% jitter, using
    `workflow.SideEffect` for randomness;
  - on `Deferred`, a durable timer until `Until`, with no retry
    consumed;
  - on `Parked`, `Park` then return;
  - on a `CheckRepo` failure after retries, `RecordCheckError` and
    continue (OQ18 (a));
  - ContinueAsNew on `GetContinueAsNewSuggested()` or 100 iterations,
    carrying `RepoState{RepoID, NextDue, PendingTrigger,
    PendingPriority, LastPolicyVersion}`.

  The budget acquire is stubbed, always granted, until Phase 4, behind
  a `workflow.GetVersion` point named `budget-v1` so Phase 4 lands as a
  patch rather than an incompatible change.
- [ ] 3.5 `check_key` = `<workflowID>/<runID>/<iteration>`, built in
  the workflow. That is deterministic, and a retry of `RecordCheck`
  reuses the same key.
- [ ] 3.6 Activity options per DESIGN-0026's table:
  - `CheckRepo`: StartToClose 15m, retry exponential 30s→30m, 10
    attempts, non-retryable error types for config errors;
  - store activities: StartToClose 30s, unlimited retries 1s→1m.
- [ ] 3.7 Worker bootstrap, `internal/temporal/worker.go`:
  - `NewWorker(c, cfg, acts)` with `MaxConcurrentActivityExecutionSize
    = WORKER_ACTIVITY_CONCURRENCY` (default 10);
  - deployment versioning `UseVersioning: true`, build ID = binary
    version, default behaviour `AutoUpgrade` (DESIGN-0026 OQ7).
- [ ] 3.8 Workflow tests (`testsuite.WorkflowTestSuite`,
  time-skipping):
  - timer loop over three iterations;
  - 20 signals during a check → at most two checks;
  - `policy_changed` pulls `next_due` in, but never later than the
    current value;
  - `Deferred` → timer → retry with no attempt consumed;
  - `Parked` → workflow completes;
  - failure after retries → `RecordCheckError` and continue;
  - ContinueAsNew state carry-over.
- [ ] 3.9 Activity tests using the existing hand-written
  `github.Client` fakes (checker package pattern, embedding
  `mocks.MockClient`) and the IMPL-0025 store mocks:
  - the classification table, including a secondary-rate-limit 403
    that must classify as throttled, not access-denied;
  - the `StageCheck` → `RecordCheck` handoff;
  - the E4 parking log line.
- [ ] 3.10 Integration test (dev server + `pgtest` Postgres + an
  `httptest` GitHub):
  - start `repo/<id>` for a seeded repository, let one check run, and
    assert findings rows and one `checks` row;
  - kill the worker mid-`CheckRepo` and restart it, then assert exactly
    one final `checks` row for the key.
- [ ] 3.11 Replay-test harness, `internal/workflows/replay_test.go`:
  replays every file under `testdata/histories/*.json` with
  `worker.NewWorkflowReplayer`. Capture the first histories from the
  homelab after deploying Phase 3 there (OQ5). Add a CI job gated on
  `internal/workflows/**`.

#### Success Criteria

- On the homelab, a Phase 3 build running `worker` (with the v1 runtime
  still deployed separately for comparison) checks a handful of
  repositories through `RepoWorkflow`. Findings match v1's `rule_state`
  for the same repositories, in `Actionable()` parity.
- The workflow and activity tests above pass.
- The replay job is green on committed homelab histories, and fails
  when a deliberate non-deterministic edit is made (for example,
  reordering the selector). Prove it once.

---

### Phase 4: Rate budget — InstallationWorkflow, fairness, priority

#### Tasks

- [ ] 4.1 `internal/workflows/installation.go`, `InstallationWorkflow`:
  - state `{Limit, Remaining, Reset, Reserved, Estimate (EWMA, seed
    20), Leases map[id]{Amount, Expires}}`;
  - an Update handler `acquire(estimate, priority)` returning
    `Granted{LeaseID} | Wait{Until}`, with a validator rejecting
    non-positive estimates;
  - a Signal handler `report(LeaseID, Calls, Remaining, Reset)`;
  - a lease-expiry timer sweep;
  - reserve = `Limit × RATE_LIMIT_THRESHOLD`;
  - optimistic grants when the budget is unknown or the reset has
    passed;
  - ContinueAsNew per OQ6.
- [ ] 4.2 In `RepoWorkflow`, behind the `budget-v1` version point,
  `UpdateWithStartWorkflow` on `installation/<id>` before `CheckRepo`
  (Update-with-Start, so the first repository of an installation
  creates it). On `Wait`, a durable timer, then acquire again. After
  `CheckRepo`, `SignalExternalWorkflow` `report`. A `Deferred` result
  reports `remaining=0, reset`.
- [ ] 4.3 Fairness and priority:
  - every activity and child start sets `Priority{PriorityKey: p,
    FairnessKey: "<installation_id>"}`;
  - `PriorityKey` is 2 for webhook/push, 3 for schedule, and 4 for
    rollout/bootstrap;
  - the workflow start options for `repo/<id>` carry the same values.
- [ ] 4.4 A counting transport wrapper in `internal/github`: a
  per-context call counter plus the last `X-RateLimit-*` observed. It
  is installed **inside** otelhttp and the rate-limit transport, so the
  IMPL-0022/0023 transport-ordering contract and
  `TestTransportOrder_ThrottledRequestIsStillMeasured` still hold. It
  feeds `Checked{Calls, Rate}` and `RecordCheck`'s installation rate
  snapshot. `github.Client` gains no methods.
- [ ] 4.5 Metrics: `budget_acquire_total{result}` and
  `rate_limit_remaining{installation_id}`, set from `report` in the
  activity layer, not from workflow code.
- [ ] 4.6 Workflow tests:
  - grant within budget;
  - deny → wait → grant after reset;
  - lease expiry releases the reservation;
  - a `Deferred` report closes the gate for all waiters;
  - EWMA convergence;
  - ContinueAsNew preserves leases.
- [ ] 4.7 Homelab burst test: a test-only driver
  (`cmd/rg-burst`, build-tagged `burst`) issues 20,000 acquire/report
  pairs against one `installation/<synthetic>` and records p50/p99
  Update latency, history size at ContinueAsNew, and frontend CPU.
  Record the numbers here and set the ContinueAsNew threshold from
  them.
- [ ] 4.8 A patched `RepoWorkflow` deploy on the homelab. Deploy the
  Phase 4 build over running Phase 3 executions, confirm they pick up
  the `budget-v1` branch at the next iteration with zero
  `WorkflowTaskFailed`, then capture new histories into
  `testdata/histories/`.

#### Success Criteria

- The burst test completes with p99 acquire latency recorded, and the
  chosen ContinueAsNew threshold is justified in this doc.
- Upgrading a live homelab fleet from the Phase 3 build to the Phase 4
  build causes no workflow task failures.
- The transport-ordering test is still green.

---

### Phase 5: Roles and ingest

#### Tasks

- [ ] 5.1 Dispatch (`main.go:78-97`): `ingest | worker | api | all |
  migrate | report | monitoring | help`. No arguments or a leading `-`
  means `all`. Split `run()` into `runIngest`, `runWorker`, `runAll`,
  and a `runAPI` stub that IMPL-0027 fills.
- [ ] 5.2 Per-role config validation in `config.Load`:
  - `ingest` needs the webhook secret and Temporal, and **refuses** to
    start if the App key or `STORE_DSN` is set, so a misconfigured
    chart cannot mount them;
  - `worker` needs the App key, Temporal, `STORE_DSN` and the policy;
  - `all` needs the union.
- [ ] 5.3 Readiness: `/readyz` checks `temporal.Ping`, plus
  `RequireSchema` for `worker` (and `api`). `/healthz` is unchanged.
  Readiness is re-evaluated every 10s in the background, and the
  handler reads the cached state.
- [ ] 5.4 `internal/ingest`:
  - HMAC validation (the v1 handler's code, 401 unchanged, with
    `webhook_rejected_total{reason="signature"}`);
  - parse, then stateless filter: tag pushes, non-default branches,
    pushes touching no watched path (`policy.ExtractWatchedPaths`,
    including `commit.Removed`), and unhandled types (204);
  - otherwise `ExecuteWorkflow(WebhookWorkflow,
    ID="webhook/<X-GitHub-Delivery>",
    WorkflowIDReusePolicy=RejectDuplicate)` with a minimal input;
  - 202, or 202 for an already-started duplicate;
  - 503 when Temporal is unreachable, counted as
    `webhook_temporal_errors_total`.

  Keep the v1 log line "invalid webhook payload" (E4). The v1 line
  "failed to enqueue job" becomes "failed to start webhook workflow",
  and the E4 constant is updated in the same commit.
- [ ] 5.5 `WebhookWorkflow` and the `RouteWebhook` activity, following
  DESIGN-0026's event table:
  - upsert installation/repository;
  - `SignalWithStartWorkflow` on `repo/<id>` with `recheck{trigger,
    priority=2}`;
  - single-installation `DiscoveryWorkflow` for the created, added,
    unarchived and unsuspend cases;
  - renamed/transferred by `provider_repo_id` (IMPL-0025 Phase 4);
  - deleted and removed → `Park(removed)` plus a `park` signal;
  - `installation.deleted` → `MarkInstallationRemoved` plus park all
    with `installation_removed`;
  - suspend → mark suspended (the workflow's acquire returns `Wait`
    until unsuspended).
- [ ] 5.6 Tests:
  - ingest: table-driven over every event type and filter branch,
    duplicate delivery → one workflow, and 503 on an unreachable
    client;
  - `RouteWebhook`: per event, with store and fake-client assertions;
  - an integration test of the webhook POST → findings row via the dev
    server.

#### Success Criteria

- `repo-guardian ingest` runs without App-key or database env vars,
  and refuses to start with them.
- On the homelab, a push to a watched path on a test repository
  produces a check within one minute.
- A redelivered webhook from GitHub's UI does not start a second
  workflow.
- A rename webhook updates the row in place, and the workflow ID is
  unchanged.

---

### Phase 6: Discovery, snapshots, policy rollout, bootstrap

#### Tasks

- [ ] 6.1 `DiscoveryWorkflow` plus the `ListInstallations`,
  `ListInstallationRepos`, `UpsertDiscovered` (batched 100) and
  `SignalRepos` activities:
  - reuse `scheduler/discoverer.go`'s filter logic (skip
    archived/forks from the same `SkipArchived/SkipForks` fields,
    which keeps the INV-0015 subset invariant exact), moved into
    `internal/activities/discovery.go`;
  - park `removed` only after a complete listing;
  - `SignalWithStart` for created and reactivated repositories with
    `next_due` jittered across one `CHECK_INTERVAL`;
  - a `service_runs` row per run.
- [ ] 6.2 Schedules. The worker ensures them idempotently at startup:
  - `discovery` every `DISCOVERY_INTERVAL` and `snapshot` every
    `COMPLIANCE_SNAPSHOT_INTERVAL`, with overlap policy skip;
  - it creates them if missing and updates the spec if the interval
    changed;
  - it deletes `discovery` if `DISCOVERY_ENABLED=false`.
- [ ] 6.3 `SnapshotWorkflow`: `InsertComplianceSnapshot` then
  `PruneChecks(now − CHECKS_RETENTION)`, with a `service_runs` row.
- [ ] 6.4 `PolicyRolloutWorkflow`. At worker startup:
  `RecordPolicyVersion(VersionV2, Summarize(cfg))`, and on first seen,
  start `policy-rollout/<v>` with `RejectDuplicate`. The workflow:
  - pages `ListActiveRepositories` (500 per page);
  - sends `SignalRepos` `policy_changed{v, by=now+POLICY_ROLLOUT_WINDOW}`
    in batches;
  - sleeps until the window ends, then pages
    `repositories WHERE policy_version <> v`;
  - re-signals the stragglers once, then calls `CompletePolicyRollout`.
- [ ] 6.5 `BootstrapWorkflow(bootstrap/v1)`:
  - started by `repo-guardian migrate` after a backfill that adopted
    v1 (IMPL-0025 `v2_meta.adopted_v1_at`), with the Temporal client
    optional in `migrate`; see OQ7;
  - pages active repositories and runs `SignalWithStart` on
    `repo/<id>` with `next_due = repositories.next_due_at`, priority 4;
  - skips parked rows;
  - is idempotent through the workflow ID.
- [ ] 6.6 Tests:
  - discovery: complete vs failed listing (parks nothing on error);
    subset invariant (an archived repository is never un-parked by
    discovery when `skip_archived` is set);
  - rollout: every workflow signalled; straggler pass;
  - bootstrap: one workflow per active row, none for parked rows, and
    a re-run is a no-op;
  - schedule reconciliation: interval change updates the spec.

#### Success Criteria

- On the homelab (v2 against a scratch database), a full discovery
  creates one `RepoWorkflow` per active repository. Checks then spread
  across the rollout window, and every repository's `policy_version`
  equals the v2 version by the window's end.
- Snapshot rows appear daily and old `checks` rows are pruned.

---

### Phase 7: Delete the v1 runtime

One PR, so the branch is never half-migrated. Order within the PR
follows the LogQL contract test.

#### Tasks

- [ ] 7.1 Delete:
  - `internal/queue/**` and `internal/scheduler/**` (after 6.1 has
    moved the discoverer logic);
  - `internal/worker/**`;
  - `internal/checker/{sweep,posture}*.go` and
    `multireplica_integration_test.go`;
  - `internal/observability/valkey.go` and its test;
  - the v1 store implementation (`postgres.go`, `report.go`,
    `compliance.go`, `migrate.go`, the v1 `Store` interface and its
    mock), keeping `migrations/` as the IMPL-0025 test fixture and
    `pgtest/v1sql`.
- [ ] 7.2 `main.go`: remove `bringUp`, `newQueue`, `newScheduler`,
  `scheduleHandlers`, `podID`, `newStore`, and the v1
  `gracefulShutdown` signature. Per-role shutdown stops the Temporal
  worker (`worker.Stop`, which drains in-flight activities within
  `shutdownTimeout`), closes the client and pool, and shuts down the
  HTTP servers.
- [ ] 7.3 Metrics: delete the series that DESIGN-0026 § Observability
  removes from `metrics.go`: the 25 business and 13 queue/scheduler
  series, plus `github_rate_remaining` and `installation_info`. First
  reconcile the exact name list against the 52 registrations and
  record it here, because the design's "38" and those two named extras
  may overlap. Add
  `checks_total{outcome}`, `budget_acquire_total{result}`,
  `webhook_temporal_errors_total` and `discovery_duration_seconds`
  (kept). `metrics_test.go` asserts the exact registered name set.
- [ ] 7.4 E4 constants (`dashboard/e4.go`): drop "job exceeded attempt
  cap", "store write-back failed", "rule-state write-back failed",
  "rate limit throttled; deferring job" and "stale-sweep complete".
  Add "check deferred until budget reset" (emitted by `RepoWorkflow`'s
  activity layer on `Deferred`) and "check failed after retries". Do
  this in the same commit as 7.1.
- [ ] 7.5 Config removal:
  - drop the 13 env vars from `config.go` and add them to
    `removedEnvVars` (`main.go:740`, warn-and-ignore, with a link to
    `docs/operations/v2-migration.md`);
  - HCL `worker_count`, `queue_size` and `schedule_interval` are
    removed from `guardianBodySchema`, `setGuardianAttr` and
    `mergeGuardianConfig` (the INV-0010 lockstep in reverse), so they
    fail load with "Unsupported argument" plus a migration hint. The
    hint is added by a pre-check that recognizes the three names;
  - update the five `examples/` files and `examples_test.go` in the
    same commit;
  - a regression test proven non-vacuous.
- [ ] 7.6 go.mod: drop `redis/go-redis/v9`, `redisotel` and
  `rediscmd`; `go mod tidy`. Remove the compose `v1` profile, which
  deletes the Valkey service.
- [ ] 7.7 `.mockery.yaml`: remove the `queue`, `scheduler` and v1
  `Store` entries; `make mocks`.
- [ ] 7.8 CLAUDE.md on `v2`:
  - rewrite the Architecture package list and the Core flow;
  - replace the IMPL-0011/0015/0016/0022/0023 runtime-contract
    paragraphs with the v2 contracts: payloads carry IDs only; nothing
    blocks in an activity (Deferred is a result); `repo/<id>` is the
    lock; discovery is the only un-parker; replay tests gate workflow
    edits; logic in activities.
  - The transport-ordering contract stays.

#### Success Criteria

- `grep -rn 'go-redis\|internal/queue\|internal/scheduler\|internal/worker' --include='*.go' .`
  returns nothing, and `go mod why github.com/redis/go-redis/v9`
  reports the module is not needed.
- `make ci`, `make test-integration`, `make lint-monitoring` and
  `TestLogLines_AreStillEmittedByTheBinary` are green.
- The binary starts in `all` against the dev compose with no Valkey
  present.

---

### Phase 8: Observability and chart 2.0.0

#### Tasks

- [ ] 8.1 Monitoring generator:
  - delete `dashboard/e1.go` and `e2.go` and their emit wiring;
  - rebuild E3 around the otelhttp and otelpgx series, the Temporal SDK
    series (schedule-to-start, task latency and failures, pollers,
    sticky cache), `checks_total`, `budget_acquire_total`,
    `rate_limit_remaining` and `discovery_duration_seconds`;
  - trim E4 to the surviving log panels;
  - DESIGN-0024's multi-instance flags apply unchanged if it has
    landed.
  - Then `make monitoring-generate`, and commit `contrib/generated/`.
- [ ] 8.2 Alert catalogue: apply the audit's delete/rework/new lists.
  Mechanism gating is unchanged. `TestCatalogue_RareEventAlertsCatchTheFirstIncrement`
  is updated for the new set. Confirm `NoWorkerPollers` and
  `ScheduleToStartLatencyHigh` metric names against the homelab's
  actual `/metrics` output before committing, not from docs.
- [ ] 8.3 Chart templates for `topology: split | all` (default
  `split`):
  - `deployment-ingest.yaml` and `service-ingest.yaml`;
  - `deployment-worker.yaml`, plus `scaledobject-worker.yaml` (KEDA,
    off by default);
  - `deployment-all.yaml`;
  - PDBs for ingest;
  - reserved slots for `api` and `ui` that render nothing until
    IMPL-0027.

  Every template stamps `namespace: {{ .Release.Namespace }}`. The
  `/metrics` port and ServiceMonitor apply per role.
- [ ] 8.4 Secret scoping:
  - the App key is mounted only in worker/all;
  - the webhook secret only in ingest/all;
  - Temporal mTLS from `temporal.tls.existingSecret` in every role that
    dials Temporal.

  helm-unittest asserts the absence of each secret in the other roles.
- [ ] 8.5 Values:
  - new: `temporal.*`, `checkInterval`, `policyRolloutWindow`,
    `worker.{replicas, concurrency, keda.*}`, `ingest.replicas`,
    `migrate.{enabled: true, freshness}`, `checksRetention`;
  - wire IMPL-0025's `migrate-job.yaml`;
  - remove `queue.*`, `scheduler.*`, `staleSweep.*`,
    `posture.exportInterval`, `config.{workerCount, queueSize,
    scheduleInterval, maxJobAttempts}`, and the queue Valkey templates
    and helpers (`valkeyFullname`, `queueSecretName`,
    `queueSecretKey`);
  - `validateBackendSecrets` loses its queue arms.
- [ ] 8.6 Removed-value guards:
  - extend `validateRemovedValues` and add schema `const` entries for
    every removed key, each naming `docs/operations/v2-migration.md`;
  - extend `reservedEnvVars` with the v2 names and remove the deleted
    ones;
  - negative tests in `values_guard_test.yaml`.
- [ ] 8.7 Add `checksum/policy` and `checksum/templates` pod annotations
  on worker, ingest and all. A test asserts that the annotation changes
  when `policy.config` changes.
- [ ] 8.8 `prometheusrule.yaml`: mirror the new catalogue. Keep
  `make lint-alerts-chart` green, with its non-empty guard.
- [ ] 8.9 Rewrite the chart test suites:
  - `backend_shapes_test.yaml` (queue shapes out; store shapes stay);
  - `deployment_env_test.yaml` (per-role env);
  - `prometheusrule_test.yaml`;
  - `values_guard_test.yaml`;
  - a new `topology_test.yaml` covering split/all, KEDA on/off and the
    migrate hook.

  `ci/ci-values.yaml` needs `temporal.address`.
- [ ] 8.10 Regenerate NOTES.txt and the README
  (`README.md.gotmpl`, then `make helm-docs`).

#### Success Criteria

- `make helm-test`, `make lint-alerts`, `make lint-monitoring` and
  `ct lint` against `v2` are green.
- The chart renders in both topologies with every removed key
  rejected by name.
- `helm template | grep -E '^kind:|^  namespace:'` pairs every kind
  with the release namespace.

---

### Phase 9: Cutover — runbook, shadow run, homelab swap, rc.1

#### Tasks

- [ ] 9.1 `docs/operations/v2-migration.md`, with:
  - prerequisites: Temporal from `contrib/temporal/`, namespace, and
    `guardian.hcl` edited to remove the three attributes;
  - the removed env, values and HCL tables with replacements;
  - IMPL-0025's data-migration section and reporting-divergence table;
  - the step list from DESIGN-0026 § Cutover;
  - redelivering outage-window webhooks;
  - rollback;
  - "what resumes and why".
- [ ] 9.2 `repo-guardian migrate verify-shadow --v1-dsn --v2-dsn`
  (DESIGN-0026 OQ15): diff v2 findings against v1 `rule_state` by
  `(org, repo, rule)`, checking `Actionable()` parity; report
  mismatches grouped by reason, then exit non-zero on any unexplained
  mismatch. The documented divergences (foreign PR, branch missing)
  are classified, not failed.
- [ ] 9.3 A PR-identity lock test: the branch name
  `repo-guardian/add-missing-files`, the title const, and the markers
  `<!-- repo-guardian:reconcile-log:v1 -->` and hash tag format are
  asserted equal to literal strings, with a comment saying they must
  not change on `v2`.
- [ ] 9.4 Cut `v2.0.0-rc.1`:
  - a `dont-release` PR bumps the chart to `2.0.0-rc.1` and sets
    `appVersion`;
  - push the tag by hand;
  - `workflow_dispatch` `ghcr.yml` with the tag;
  - verify no `latest` moved, cosign signatures and SLSA provenance
    are present, and the chart is published to OCI.
- [ ] 9.5 Homelab shadow run: restore the live v1 database to scratch
  and deploy rc.1 with `DRY_RUN=true`, its own Temporal namespace and
  `all` topology. Let one rollout window pass, run `verify-shadow`,
  and record the results here.
- [ ] 9.6 Homelab cutover from the live v1, following the runbook
  step by step (OQ8):
  - `pg_dump`, `migrate --dry-run`, scale v1 to 0, chart upgrade to
    rc.1 (the migrate hook runs, then bootstrap), point ingress at
    ingest, redeliver webhooks;
  - watch one rollout window;
  - record timings, API budget use, and any runbook edits.
- [ ] 9.7 Rollback drill on the homelab: scale v2 to 0 and redeploy
  the last v1 chart against the same database. Confirm v1 starts, its
  stale sweep runs, and it adopts an open PR that v2 updated. Then roll
  forward again.
- [ ] 9.8 Update the docz status of DESIGN-0026 and this IMPL. Update
  CLAUDE.md on `v2` with the cutover facts learned.

#### Success Criteria

- The homelab runs rc.1 in production mode after the cutover. Within
  one `POLICY_ROLLOUT_WINDOW`:
  - every active repository has a v2 check;
  - no `migrated_from_v1` reasons remain;
  - no duplicate repo-guardian PRs exist;
  - v1's open PRs were updated in place.
- `verify-shadow` reported only documented divergences.
- The rollback drill succeeded.
- `latest` still points at the last v1 release.

---

## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `.github/workflows/{ci,license-check,security,ghcr,ecr,changelog-update}.yml`, `ct.yaml`, `docker-bake.hcl` | Modify | v2 branch, no `latest` on pre-releases |
| `contrib/temporal/**` | Create | Temporal reference values, namespace Job, NetworkPolicy |
| `internal/temporal/**` | Create | client, version floor, worker bootstrap, test harness |
| `internal/workflows/**` | Create | Repo, Installation, Discovery, Webhook, PolicyRollout, Bootstrap, Snapshot; replay tests and histories |
| `internal/activities/**` | Create | CheckRepo, Record*, Park, discovery, routing, rollout, snapshot |
| `internal/ingest/**` | Create | HMAC, filter, start WebhookWorkflow |
| `internal/github/counting.go` | Create | per-check call counter and rate observation |
| `cmd/repo-guardian/main.go` (+ `roles.go`, `migrate.go`) | Modify | role dispatch, readiness, shutdown, `verify-shadow` |
| `internal/config/config.go`, `internal/policy/{types,loader,validate}.go`, `examples/**` | Modify | removed knobs, new env |
| `internal/metrics/metrics.go` | Modify | business and queue/scheduler series removed, 4 added |
| `internal/monitoring/**`, `contrib/generated/**` | Modify | E1/E2 deleted, E3 rebuilt, E4 trimmed, alert catalogue |
| `internal/{queue,scheduler,worker}/**`, `internal/checker/{sweep,posture}*`, `internal/observability/valkey*`, v1 store | Delete | v1 runtime |
| `charts/repo-guardian/**` | Modify / Create | chart 2.0.0 |
| `docker-compose.dev.yaml`, `Makefile`, `mise.toml`, `.mockery.yaml`, `go.mod` | Modify | dev loop, targets, deps |
| `docs/operations/v2-migration.md`, `CLAUDE.md` | Create / Modify | runbook, contracts |

## Testing Plan

- [ ] Workflow unit tests (time-skipping) for every workflow (3.8, 4.6,
  6.6).
- [ ] Replay tests over committed homelab histories, as a CI gate
  (3.11), proven to fail on a non-deterministic edit.
- [ ] Activity classification tests, including throttle-before-403
  (3.9).
- [ ] Integration: dev server, Postgres and httptest GitHub, covering
  check, crash-restart idempotency, and webhook → findings (3.10,
  5.6).
- [ ] Ingest table tests, duplicate delivery, and 503 (5.6).
- [ ] Removed-knob regressions for env, HCL and chart values (7.5,
  8.6).
- [ ] Chart: topology, secret scoping, checksum, KEDA, migrate hook,
  alert rules (8.9).
- [ ] Homelab: burst test (4.7), patched deploy (4.8), shadow run
  (9.5), cutover (9.6), rollback drill (9.7).

## Dependencies

- IMPL-0025 Phases 2–4 (store, enrichment, identity) before Phase 3;
  IMPL-0025 Phase 7 before Phase 7; IMPL-0025 Phase 8 before Phase 9.
- The homelab Temporal cluster at ≥ 1.31 with fairness enabled, mTLS
  certificates, and a CNPG cluster.
- New modules: `go.temporal.io/sdk` and
  `go.temporal.io/sdk/contrib/opentelemetry`.
- KEDA in the homelab for the optional scaler test (not required for
  rc.1).
- IMPL-0027's chart phase, if the UI ships in rc.1 (OQ10).

## Open Questions

1. **How rc images get published from `v2`.** `release.yml` runs only
   on `main`, and the registry workflows are `workflow_dispatch`-able.
   - (a) A manual tag push, then `workflow_dispatch` of `ghcr.yml`
     (and `ecr.yml` when enabled) with the tag. There is no new
     workflow, and publishing stays a deliberate act.
   - (b) Add a `push: tags: ['v2.*-rc.*']` trigger that calls the
     registry workflows automatically.
   - (c) Publish per-commit `v2-<sha>` images from `v2` CI for homelab
     testing, and rc tags manually.
   - other:

2. **Temporal server for integration tests.**
   - (a) The Go SDK's `testsuite.StartDevServer` with a pinned CLI
     version (it downloads the CLI once and caches it on the runner).
     No Docker is needed for Temporal.
   - (b) A testcontainers `temporalio/temporal` container running
     `server start-dev`, which matches how Postgres tests run.
   - (c) Integration tests against the homelab cluster only.
   - other:

3. **Local dev loop.**
   - (a) A `temporal` service in `docker-compose.dev.yaml` (the CLI
     image running `start-dev`, UI on 8233), with Valkey behind a `v1`
     profile until Phase 7.
   - (b) Install the Temporal CLI via mise and have `make dev-services`
     start it as a background process.
   - (c) Point local runs at the homelab cluster with a dev namespace.
   - other:

4. **Package layout.**
   - (a) `internal/workflows` (deterministic code only, with activity
     names as constants), `internal/activities`, `internal/temporal`
     (client/worker plumbing) and `internal/ingest`. A depguard rule
     forbids `internal/workflows` from importing engine, store or
     GitHub packages.
   - (b) One `internal/controlplane` package holding workflows and
     activities together.
   - other:

5. **Source of replay histories.**
   - (a) Captured from the homelab with `temporal workflow show
     --output json` after each workflow-changing deploy, and committed
     under `testdata/histories/`. Bootstrap with one history from the
     integration suite so the CI gate is non-empty from Phase 3.
   - (b) Generated by the integration suite only (less realistic: no
     real signals or ContinueAsNew).
   - other:

6. **`InstallationWorkflow` ContinueAsNew trigger before the burst
   test sizes it.**
   - (a) `GetContinueAsNewSuggested()` or 2,000 handled
     Updates/Signals, whichever is first. Revisit with the 4.7 numbers.
   - (b) A fixed 500 events.
   - (c) Suggested only.
   - other:

7. **Who starts `BootstrapWorkflow`.** DESIGN-0026 says `migrate`
   starts it, but the migrate hook Job otherwise needs only DDL rights.
   - (a) `migrate` writes a `v2_meta.bootstrap_pending` flag, and the
     worker starts `bootstrap/v1` at startup when the flag is set
     (clearing it when the workflow completes). The migrate Job needs
     no Temporal credentials.
   - (b) `migrate` dials Temporal and starts it directly, which gives
     the hook Job the mTLS secret.
   - other:

8. **Homelab cutover target.**
   - (a) Rehearse on a restored copy first (9.5 shadow run), then cut
     over the live homelab v1 instance.
   - (b) Cut over the live instance directly; the shadow run is
     optional.
   - other:

9. **Removed env vars in v2.0.**
   - (a) Warn-and-ignore with a migration link (the IMPL-0024 shape),
     while the HCL attributes and chart values fail hard.
   - (b) Fail startup on any removed env var.
   - other:

10. **Is the UI part of rc.1?**
    - (a) No. rc.1 ships ingest, worker and migrate, plus the `api`
      role if IMPL-0027 Phases 1–4 are done. The UI lands in a later
      rc once the BFF image exists, so the cutover is not blocked on a
      new repository.
    - (b) Yes. rc.1 waits for IMPL-0027 in full.
    - other:

## References

- DESIGN-0026 — Temporal control plane and role split
- DESIGN-0025 / IMPL-0025 — findings model, store, backfill
- DESIGN-0027 / IMPL-0027 — api and UI roles
- INV-0018, INV-0019 — v2 re-topology, Temporal
- IMPL-0022 — delayed requeue (the classification being ported)
- IMPL-0023 — OTel bridge, monitoring generator, LogQL contract
- IMPL-0024 — removed-value guard pattern
- INV-0015 — parking and the subset invariant
- [Temporal Go SDK](https://github.com/temporalio/sdk-go) ·
  [Priority and Fairness](https://docs.temporal.io/develop/task-queue-priority-fairness) ·
  [Worker Versioning](https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning) ·
  [temporalio/helm-charts](https://github.com/temporalio/helm-charts) ·
  [KEDA Temporal scaler](https://keda.sh/docs/latest/scalers/temporal/)
