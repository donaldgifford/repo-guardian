---
id: INV-0022
title: "Controls designs against the v2 code, GitHub and Temporal"
status: Concluded
author: Donald Gifford
created: 2026-10-06
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0022: Controls designs against the v2 code, GitHub and Temporal

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [F1 — One worker deployment cannot carry the cutover, the rollback or two roles](#f1--one-worker-deployment-cannot-carry-the-cutover-the-rollback-or-two-roles)
  - [F2 — The cutover terminate list is incomplete](#f2--the-cutover-terminate-list-is-incomplete)
  - [F3 — The replay suite keeps a fixture](#f3--the-replay-suite-keeps-a-fixture)
  - [F4 — Workflow-side signal-with-start does not exist; signals can be lost at completion](#f4--workflow-side-signal-with-start-does-not-exist-signals-can-be-lost-at-completion)
  - [F5 — Priority does not cross task queues](#f5--priority-does-not-cross-task-queues)
  - [F6 — The single-queue assumption runs through the code](#f6--the-single-queue-assumption-runs-through-the-code)
  - [F7 — The webhook secret header is undocumented](#f7--the-webhook-secret-header-is-undocumented)
  - [F8 — The Remediation App lacks Workflows; the Evaluation App is over-scoped](#f8--the-remediation-app-lacks-workflows-the-evaluation-app-is-over-scoped)
  - [F9 — The compare-and-swap commit needs GraphQL, which the throttle path cannot see](#f9--the-compare-and-swap-commit-needs-graphql-which-the-throttle-path-cannot-see)
  - [F10 — update-branch is asynchronous and its conflicts are 422s](#f10--update-branch-is-asynchronous-and-its-conflicts-are-422s)
  - [F11 — A human-closed PR leaves its branch behind](#f11--a-human-closed-pr-leaves-its-branch-behind)
  - [F12 — Ruleset reads miss inherited rulesets](#f12--ruleset-reads-miss-inherited-rulesets)
  - [F13 — Database roles cannot be created by migrations](#f13--database-roles-cannot-be-created-by-migrations)
  - [F14 — Policy rollout ignores a revert](#f14--policy-rollout-ignores-a-revert)
  - [F15 — Two park reasons have no result rule](#f15--two-park-reasons-have-no-result-rule)
  - [F16 — The API section understates what the UI loses](#f16--the-api-section-understates-what-the-ui-loses)
  - [F17 — The v1 runtime is still on the branch](#f17--the-v1-runtime-is-still-on-the-branch)
  - [F18 — Smaller corrections](#f18--smaller-corrections)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
  - [Decisions](#decisions)
  - [Phase-0 spikes](#phase-0-spikes)
- [References](#references)
<!--toc:end-->

## Question

Do DESIGN-0029 to DESIGN-0032 rest on true assumptions about three things they cannot change: the v2 code they replace or reuse, GitHub's API and webhook behaviour, and Temporal's worker versioning and workflow semantics? INV-0021 checked the twenty-three code assumptions DESIGN-0029 lists. This investigation is wider: it checks every concrete claim the four designs make in those three areas, before the implementation plans are written, so that a wrong claim changes a design rather than a half-built phase.

## Hypothesis

The designs hold in shape, because INV-0021 already verified the code they reuse. Problems are most likely where the controls model adds something the release candidate never had: a second GitHub App, a second task queue and worker role, a GraphQL write path, and a cutover onto a fresh database (DESIGN-0032 D27, added late on 2026-10-05).

## Context

The four designs were complete and every open question resolved except DESIGN-0034's deferred CODEOWNERS question. The next step was the implementation plans. A pre-implementation audit of cross-cutting designs is the project's practice, and an operations-docs review on the same day had already found one D27 contradiction in DESIGN-0032 (the `app_*` tables described as backfilled).

**Triggered by:** DESIGN-0029, DESIGN-0030, DESIGN-0031, DESIGN-0032

## Approach

Three read-only passes ran in parallel, one per area:

1. **Code reuse.** Extract every claim the designs make about existing packages, types, tests, workflows, tables, endpoints and chart values; check each against the code and its importers; look for code no design mentions that must change.
2. **GitHub.** Check every GitHub assumption against docs.github.com, the public GraphQL schema, go-github v68 in the module cache and `internal/github` / `internal/ingest`. Read-only `gh api` calls only; no writes.
3. **Temporal.** Check worker versioning, promotion, replay, signals, schedules and KEDA against the SDK source in the module cache and `internal/temporal`, `internal/workflows`, `internal/activities` and the chart.

Each claim got a verdict (confirmed, wrong, risky, incomplete, or needs a spike) with file or documentation evidence. Findings below are the ones that change a design or the implementation plan; confirmed claims are summarised in the conclusion.

## Environment

| Component | Version / Value |
| --------- | --------------- |
| Branch | `docs/controls-and-policies` on the `v2` line, commit `8deb08e` |
| Chart / appVersion | `2.0.0-rc.4` |
| go-github | `v68.0.0` (REST only; no GraphQL client in `go.mod`) |
| Temporal SDK / API | `go.temporal.io/sdk v1.49.0`, `go.temporal.io/api v1.63.5` |
| Temporal server (reference) | 1.31 minimum, 1.32 in `contrib/temporal` |

## Findings

### F1 — One worker deployment cannot carry the cutover, the rollback or two roles

**Verdict:** wrong (DESIGN-0032 Temporal mapping, Migration, D26/D27).

- Every worker joins one deployment, `DeploymentName = "repo-guardian"` (`internal/temporal/worker.go`), and promotes its own build at startup (`internal/temporal/deployment.go`).
- `SetCurrentVersion` is refused unless the new version polls every task queue the current version has polled, minus queues with an empty backlog and no recently added tasks (SDK `internal/worker_deployment_client.go:149-169`). The rc build is current on queue `repo-guardian`; the controls builds poll `repo-guardian-eval` and `repo-guardian-remediate`. Any task left on `repo-guardian` makes `PromoteBuild` retry, the `deployment` readiness check fails after two minutes, and nothing is dispatched. How long "recently added" lasts is undocumented.
- Rollback to "the previous chart against the previous database" does not roll Temporal back. `decide()` in `deployment.go` returns `superseded` when the current build has a higher semver, so rc workers never promote themselves over `2.0.0` and receive no tasks until someone runs `set-current-version` by hand.
- With both roles in one deployment, neither can be promoted until pods of the other role on the same build poll. A remediator at zero replicas (an evaluate-only install, KEDA minimum 0, or a scale-down) blocks every future promotion.

**Consequence:** one deployment per role under new names. See [Decisions](#decisions) 1.

### F2 — The cutover terminate list is incomplete

**Verdict:** incomplete (DESIGN-0032 Migration step 2).

Step 2 terminates `repo/*`, `installation/*` and `policy-rollout/*` and deletes the `discovery` and `snapshot` schedules. Queue `repo-guardian` also carries `bootstrap/v1`, `discovery/installation/<id>/<delivery>`, in-flight `webhook/*` executions and the runs the schedules started. Under F1 any one of them blocks promotion; with per-role deployments they are still old writers that must stop before the new database is used.

**Consequence:** terminate every running execution on task queue `repo-guardian`, not a list of id prefixes.

### F3 — The replay suite keeps a fixture

**Verdict:** wrong (DESIGN-0032 Testing, INV-0021 A16).

The replay suite does fail on an empty directory (`internal/workflows/replay_test.go:31`). But only `check_then_park.json` is a `RepoWorkflow` history; `installation_grant_report.json` is an `InstallationWorkflow` history, a type the design keeps under a new id, so removing `RepoWorkflow` never empties the directory. That fixture must be recaptured if `InstallationWorkflowInput` gains the App. The stated hazard, that an `AutoUpgrade` worker would receive tasks for a type it no longer registers, does not arise: the new queues never poll `repo-guardian`. The real hazard is F1. History capture is wired to `TestIntegration_OneCheck` (`integration_test.go:50,300`); `EvaluationWorkflow` and `RemediationWorkflow` need their own capture tests.

### F4 — Workflow-side signal-with-start does not exist; signals can be lost at completion

**Verdict:** risky wording (DESIGN-0032 sequence diagram, Workflow shape, D6).

- The Go SDK exposes only `SignalExternalWorkflow` and `ExecuteChildWorkflow` inside a workflow (`workflow/workflow.go`). Starting `remediation/<id>/<slug>` with a signal has to be an activity calling `client.SignalWithStartWorkflow`, as `startRepo` already does (`internal/activities/route.go`). The default id reuse policy lets a completed id start again.
- A signal received but never read from its channel is lost when the run completes; the SDK only warns through `GetUnhandledSignalNames`. The end of each run must drain the channel.
- Changed-path sets of up to 2048 commits travel in signals and in ContinueAsNew state, and an evaluation may start several remediation runs per iteration, so history growth needs measuring; one batched start activity per evaluation bounds it.
- The per-control id does serialize one run per (repository, control). Evaluation versus remediation, and controls within one repository, are not serialized by Temporal; the design relies on Postgres for that, which is consistent. `FOR UPDATE SKIP LOCKED` holds only while a transaction stays open across the GitHub-calling activity: one long-held connection per running remediation, which sizes the pool.

### F5 — Priority does not cross task queues

**Verdict:** wrong rationale (DESIGN-0032 Temporal mapping, A18).

Priority orders tasks within one queue. "Remediation runs at priority 3 and 4 so human (1) and push (2) evaluation go first" cannot hold across `-eval` and `-remediate`. There is also no priority 1 constant today (`internal/workflows/types.go` has 2, 3 and 4).

### F6 — The single-queue assumption runs through the code

**Verdict:** unplanned work.

- `startV2Worker` builds one worker; `all` needs two, one per queue, with separate registration sets, while the replay test registers the union. `workflows.Register` registers everything.
- Ingest's `RouteWebhook`, `NewRouter` and `NewServices` take one task queue.
- `serviceStarter.ensureSchedules` runs in every pod and must be partitioned per role.
- The api role already holds a Temporal client, for `DescribeTaskQueue` on one queue (`cmd/repo-guardian/api.go:46`, `internal/temporal/backlog.go`); the status backlog needs both. DESIGN-0032 calls it signal-only.
- The KEDA template renders one `ScaledObject` for `worker` with `.Values.temporal.taskQueue`, and DESIGN-0028's default query hard-codes `taskqueue="repo-guardian"`.
- `DefaultLeaseTTL` is the CheckRepo timeout plus 5 minutes and does not cover remediation activity timeouts; the budget `Holder` is the workflow id, so its format changes.

### F7 — The webhook secret header is undocumented

**Verdict:** risky (DESIGN-0032 Two GitHub Apps, A13).

`X-GitHub-Hook-Installation-Target-Type` and `-ID` are documented as headers, but their values are not. Real deliveries show `integration` and the App id (github/rest-api-description#7210); a pending docs change proposes documenting the type as `app`. The design's rationale for reading the header first, that `ValidatePayload` consumes the body, is also wrong: go-github has `ValidateSignature` and `ValidatePayloadFromBody` (`messages.go:194,275`). Selecting a secret from an unauthenticated header is not itself a hole, since a forger still needs a valid HMAC; the risk is trusting the header for attribution afterwards. A leaked Evaluation App secret could forge Evaluation App `installation` events, so the payload's `installation.app_id` should be checked against the App whose secret validated.

**Consequence:** one webhook URL per App. See [Decisions](#decisions) 2.

### F8 — The Remediation App lacks Workflows; the Evaluation App is over-scoped

**Verdict:** wrong (DESIGN-0032 permission tables, A23).

- `apply = "workflow"` writes `.github/workflows/repo-guardian-<slug>.yml`. GitHub gates files under `.github/workflows/` on the **Workflows** permission, which neither the design nor any doc mentions.
- Rulesets (`GET /repos/{o}/{r}/rulesets[/{id}]`) and custom property values (`GET .../properties/values`) need only **Metadata: read**. Administration: read is needed only for vulnerability alerts. Pull requests read covers labels, so Issues: read is redundant. The organisation property schema does need organisation Custom properties: read.
- Whether merge-policy settings fields are returned to a read-only App token is not stated in the docs.

### F9 — The compare-and-swap commit needs GraphQL, which the throttle path cannot see

**Verdict:** wrong, as an unstated dependency (DESIGN-0032 AR-0032-07, D21; DESIGN-0031 D8).

- `createCommitOnBranch` with `expectedHeadOid` is a real compare-and-swap (public schema: "expected at the head of the branch prior to the commit"). The branch must already exist, paths must be unique, there is no file-mode field (regular files only), and GitHub signs the commit "if supported". The REST alternatives give no such guarantee: `PATCH git/refs` takes only `sha` and `force`, and the Contents API is one commit per file.
- go-github is REST-only; `go.mod` has no GraphQL client.
- The throttle path cannot classify GraphQL limits. Primary-limit exhaustion returns HTTP 200 with an error body; secondary limits return 200 or 403. `isRateLimited` checks only 403 (`internal/github/ratelimit.go:271`), so REST 429 is missed as well, although the docs name both. `updateFromResponse` keeps one remaining/limit snapshot and ignores `x-ratelimit-resource`, so GraphQL traffic (its own `graphql` bucket, 5000 points per installation) would overwrite the core snapshot.
- The 100-file and 1 MiB limits DESIGN-0031 AR-0031-09 cites are in neither the schema nor the changelog.

### F10 — update-branch is asynchronous and its conflicts are 422s

**Verdict:** risky (DESIGN-0032 AR-0032-03, D17).

`PUT /pulls/{n}/update-branch` returns 202 and merges in the background. A conflict is a 422 ("merge conflict between base and head"); an `expected_head_sha` mismatch is also a 422 with a different message. The design never sets `expected_head_sha`, the existing call passes `nil` (`internal/github/client.go:981`) and treats every non-202 as an error, and the response for an already up-to-date branch is undocumented.

### F11 — A human-closed PR leaves its branch behind

**Verdict:** design gap (DESIGN-0032 D13, flowchart).

Closing a PR without merging does not delete its branch; GitHub's delete-on-merge setting fires only on merge. After the cooldown the ref create fails because `repo-guardian/<slug>` exists with no open PR, and the flowchart holds the control on `foreign_branch` permanently. The design's own update loop also deletes branches on terminal states (step 6), so the engine deletes branches today in design terms.

**Consequence:** repo-guardian never deletes branches. See [Decisions](#decisions) 4.

### F12 — Ruleset reads miss inherited rulesets

**Verdict:** risky in the code (DESIGN-0031 `branch_ruleset`).

The API offers `source_type` and `includes_parents` (default true). The client passes `includes_parents=false` (`internal/github/client.go:653`), so organisation and enterprise rulesets never appear, and go-github's `GetAllRulesets` does not paginate past its default page of 30. The list response also omits `rules`; each ruleset must be fetched by id.

### F13 — Database roles cannot be created by migrations

**Verdict:** incomplete (DESIGN-0032 D8).

`pgtest.AppRole` creates one fixed role, `rg_app`, which owns the schema and runs the migrations (`internal/store/postgres/pgtest.go:75-100`); migrations grant to `current_user`, and the read-only role is operator-provisioned. Creating `rg_evaluator` and `rg_remediator` from a migration needs CREATEROLE and per-role DSNs, and the chart has one `STORE_DSN`.

### F14 — Policy rollout ignores a revert

**Verdict:** wrong in effect (DESIGN-0030 D14).

"Newest recorded in `policy_versions`" means `ORDER BY first_seen_at`, and the insert is `ON CONFLICT DO NOTHING` (`queries/policy.sql`). Reverting the policy to an earlier version keeps that version's old `first_seen_at`, so the monotonic resolver would discard every resolution under it forever.

### F15 — Two park reasons have no result rule

**Verdict:** incomplete (DESIGN-0030 D6).

`park_reason` also has `installation_removed` and `unknown`, and the code writes the first (`internal/store/postgres/v2store_ops.go:237`). The design says what happens to results on `access_denied` (kept) and on `archived`, `fork` and `removed` (cleared), but not on these two.

### F16 — The API section understates what the UI loses

**Verdict:** wrong (DESIGN-0032 API).

- `/repositories` is not "as today, plus": the `Repository` schema requires `installation_id`, `last_check_outcome` and `policy_version` (`api/openapi.yaml:1139`), all dropped by `00001_core`.
- The UI uses `/policy`, `/installations` (its table is dropped), `/compliance/history` and `/rules/{kind}/{name}`, none of which the design gives a fate.
- `TestAPIQueries_AreScoped` scans only `api_*.sql`, and `/policy` today is an unscoped JSONB summary, so per-org filtering of `/policies` would be Go code the test cannot see.

### F17 — The v1 runtime is still on the branch

**Verdict:** unplanned blast radius.

IMPL-0025 Phase 16 (delete the v1 runtime) is open, with every task "deferred, human required", as are 17.1, 17.2, 17.5, 17.9 and 18.5 to 18.7. The `v1` subcommand still wires `internal/worker`, `scheduler`, `webhook`, `queue`, the checker's StaleSweeper, PostureExporter and SnapshotTaker, and the v1 Store. `internal/findings` is imported by `policy`, `api`, `report`, `store`, `store/postgres`, `activities`, `checker`, `worker` and `shadow`, and no design names it; `internal/report` hard-codes the findings remediation enums. `pgtest` seeds the v1 golang-migrate schema, and `adopt_v1.go`, `backfill_v1.go`, `dry_run.go` and `CheckV1Idle` are wired into `migrate`. D27 makes 18.3, 18.5 and 18.7 (shadow, backfill) obsolete.

### F18 — Smaller corrections

- **Compliance rounding** (DESIGN-0030): the percent is floored to one decimal (`compliance.sql:21`), not to an integer, and a Go copy, `store.CompliantPercent` (`api_views.go:22`), serves summed totals.
- **`service_runs.kind`** (DESIGN-0032 `00001_core`): the CHECK allows `discovery`, `snapshot`, `policy_rollout` and `bootstrap`; the remediator's `sweep`, `maintenance` and Remediation App discovery rows need it extended.
- **Foreign-PR matching** (DESIGN-0029 Non-goals) is listed as lifted from the checker, contradicting DESIGN-0032 D5, which drops foreign-PR detection.
- **E4 log lines** (DESIGN-0029 Non-goals): of nine matchers in `monitoring/dashboard/e4.go`, five are emitted only by `internal/worker`, one by `checker/sweep.go` and one by `reconciler/custom_properties.go`. `TestLogLines_AreStillEmittedByTheBinary` fails once those packages go; the matchers need re-cutting for v2 emitters.
- **`installation_info` topology label** (DESIGN-0029 D7): the gauge is per installation, needs an `app` label with two Apps, and IMPL-0025 16.3 deletes it.
- **Writer capability proof** (DESIGN-0032): depguard denies imports by file glob, and one package implementing Reader and Writer cannot be split by it; the Writer needs its own package.
- **Templates** (DESIGN-0030, DESIGN-0031 D11): templates come from `TEMPLATE_DIR` (a separate mount) plus the embedded `rules.TemplateStore`, while DESIGN-0030 requires every referenced file under the policy root, hashed in one pass.
- **Environment variables** (DESIGN-0032): the design does not say whether `EVALUATOR_CONCURRENCY` and the per-role pool sizes replace `WORKER_ACTIVITY_CONCURRENCY` and `STORE_POSTGRES_MAX_CONNS`.
- **PR listing and adoption**: the `head` filter is an exact ref, not a prefix, so listing is client-side over open PRs; the client does not capture `user` or `head.repo.id` (`client.go:160-168`), and adoption should match the bot's user id and type, not its login string.
- **Push events**: GitHub sends none when more than three tags or 5000 branches are pushed at once; the evaluation schedule covers it.

## Conclusion

**Answer:** The designs hold in shape but not in detail. Most code claims are confirmed (INV-0021's verdicts stand: the transport chain and throttle normalisation, the budget workflow's call sites, discovery's un-park rule, the API framework and scope test, the chart's secret scoping, the frozen v1 PR identity), as are the GitHub facts about push payloads, per-installation budgets, event permissions and the GraphQL compare-and-swap. Eighteen findings change a design or the plan. Four are structural: the single worker deployment (F1), the GraphQL dependency and the throttle path that cannot see it (F9), the two-App permission and webhook model (F7, F8), and database role provisioning (F13). The rest are corrections, gaps, and unplanned work that the implementation plan must sequence, chiefly the v1 runtime still on the branch (F17) and the single-queue assumptions in the code (F6).

## Recommendation

### Decisions

Resolved with the maintainer on 2026-10-06 and applied to DESIGN-0029 to DESIGN-0032 the same day.

1. **Worker deployments (F1, F2): (a).** One Temporal worker deployment per role, `repo-guardian-eval` and `repo-guardian-remediate`, with no version suffix: the deployment name is the stable identity and the build ID is the version. The new names are a one-time break; a future breaking cutover may add a generation (`repo-guardian-v3-eval`), never a release tag. The rc deployment is left untouched, so the new builds become current without draining `repo-guardian`, rollback needs no `set-current-version`, and each role promotes independently.
2. **Webhook routing (F7): (a).** One webhook URL per App. The path selects the secret and the App; nothing relies on the undocumented header values.
3. **Database roles (F13): (a).** The operator or the chart provisions `rg_evaluator` and `rg_remediator`, like today's read-only role: CNPG managed roles, an init script in baked mode, documented SQL for external Postgres, each with its own Secret and DSN. Migrations grant, never create.
4. **Branches (F11): repo-guardian never deletes branches.** Cleanup after merge is the organisation's GitHub setting, and repo-guardian does not compete with it. A branch left by a PR a human closed without merging holds the control with a reason that names the branch and says to delete it; a human or the organisation's tooling clears it. The update loop's delete-on-terminal step is removed. `rgctl prs close --delete-branch` is an operator's one-off migration step and is unaffected.
5. **Policy rollout (F14): (a).** An `activated_at` timestamp, updated whenever a version is deployed, orders the rollout.
6. **Park reasons (F15): (a).** `installation_removed` clears results (the repository has left the fleet); `unknown` keeps them (fail-safe).
7. **The v1 runtime (F17): (a).** The first phase of the controls implementation absorbs IMPL-0025 Phase 16; IMPL-0025 Phase 16 and tasks 18.3, 18.5 and 18.7 are superseded by D27.
8. **API (F16): (a).** DESIGN-0032 lists each existing endpoint and dropped field with its fate and the UI view it affects.

The corrections in F3 to F6, F8 to F10, F12 and F18 apply without a decision.

### Phase-0 spikes

1. **GraphQL commit through the transport chain:** the stale-head error's shape, the file-count and size limits, rate-limit headers, `AsThrottled` classification and budget accounting for GraphQL points (F9).
2. **update-branch:** an up-to-date branch, a conflict, a stale `expected_head_sha`, and the latency of the background merge (F10).
3. **Evaluation App minimal permissions:** register an App with the Metadata-based set and confirm settings fields, rulesets with `source_type` and `rules`, and property values are all readable (F8).
4. **`installation_repositories` for "all repositories" installations:** whether it fires on repository creation, or the Remediation App learns of a repository only from its own discovery.
5. **Temporal:** signal-with-start into a just-completing `RemediationWorkflow`, per-queue `approximate_backlog_count` labels for the KEDA query, and `EvaluationWorkflow` history growth under maximum changed-path signals (F4, F6).
6. **Database role provisioning** in baked, CNPG and external modes, and the extended `pgtest` harness (F13).
7. **Template sourcing** under the policy-root rule and its effect on the revision hash (F18).
8. **A policy revert** under the activation-ordered resolver (F14).
9. **Upgrade and rollback rehearsal** on the dev server with per-role deployments, which under decision 1 should only confirm.
10. **Label names:** case-insensitivity on create and update.

## Phase-0 results

Recorded by IMPL-0028 Phase 0 (OQ2: results live here, one subsection per spike). Spikes marked *pending* are maintainer-run; their tests are in the tree and write raw observations to `build/spike/<test>.json`.

### Dev Temporal baseline (IMPL-0028 0.1)

Read 2026-10-10 by the maintainer from the `temporal` namespace's ConfigMaps, the `repo-guardian-dev` Deployment env and the Temporal UI. Dev runs chart and image `2.0.0-rc.4` in Kubernetes namespace `repo-guardian-dev`, against Temporal namespace `repo-guardian-dev`: on dev the two names match by convention. A second install in Kubernetes namespace `repo-guardian` runs v1 and does not use Temporal.

| Item | Dev | `contrib/temporal/` |
| ---- | --- | ------------------- |
| Server | 1.32.0 (admin-tools 1.32.0, UI 2.54.1) | 1.32.0, floor 1.31 |
| Default store | `postgres12_pgx` | Postgres (CNPG) |
| Visibility store | `postgres12_pgx` (SQL visibility) | `visibility-postgres.yaml` |
| History shards | 512 | 512 |
| `matching.enableFairness` | **unset (off)**: no key in `temporal-dynamic-config` | `true` |
| Frontend transport | **TLS at the edge**: `temporal-grpc.fartlab.dev:443`, verified as that server name; no `tls:` block in `temporal-config` and no frontend/internode/admin TLS Secrets | in-cluster frontend and internode mTLS |
| Client identity | **OIDC bearer**: Keycloak client-credentials client `repo-guardian-temporal`, secret mounted as a file (`TEMPORAL_OIDC_*`); server `authorization` uses the `default` authorizer and claim mapper, audience `temporal`, claim `permissions`, JWKS from the realm's `certs` endpoint, refreshed every 5m | client certificate |
| Namespace | `repo-guardian-dev`: 7-day retention, not global, history and visibility archival disabled | `repo-guardian`, 7-day retention |

Worker deployment `repo-guardian`: current version `2.0.0-rc.4` (deployed 2026-09-27 22:50 EDT); `2.0.0-rc.3`, `rc.2` and `rc.1` drained; an early `dev` build inactive. Each release became current on its own start, with no manual `set-current-version`. Task queue `repo-guardian` has one poller (`repo-guardian-dev-all`, build `2.0.0-rc.4`, current) with workflow and activity handlers and no Nexus handler; it is the only queue that build polls.

The deviations (fairness off, edge TLS + OIDC in place of mTLS) are reconciled in 0.2. Under OIDC, KEDA's `temporal` trigger is refused (INV-0020), so dev autoscaling uses the default `prometheus` trigger.

### Bringing dev in line (IMPL-0028 0.2)

Decided 2026-10-10 by the maintainer:

- **Fairness: turn it on.** Add `matching.enableFairness: true` to dev's dynamic config, as `contrib/temporal/values-base.yaml` does. Until then the per-installation fairness keys are ignored on dev. Applied 2026-10-10: `temporal-dynamic-config` carries `matching.enableFairness: [{value: true}]`.
- **Edge TLS + OIDC: keep it for now, documented.** `contrib/temporal/README.md` § Alternative: edge TLS and OIDC now describes it as a supported shape. 0.3 runs first as an OIDC smoke over the chart's real path. Dev then moves to DESIGN-0028's target, mTLS from an OpenBao-issued client CA through a cert-manager Vault Issuer plus a JWT on every call (IMPL-0026), with the dev release on `temporal.tls.existingSecret`, and 0.3 is re-run as written.

### OIDC smoke (IMPL-0028 0.3)

Run 2026-10-10 by the maintainer from a workstation over dev's real client path: `temporal-grpc.fartlab.dev:443` with TLS, namespace `repo-guardian-dev`, and a bearer token minted by a client-credentials grant for `repo-guardian-temporal`, the same client and secret the chart mounts. `temporal workflow start` (type `rg-smoke`, queue `rg-smoke`, no worker) returned a run id, `describe` read `WORKFLOW_EXECUTION_STATUS_RUNNING`, and `terminate` succeeded. Transport and authorization work end to end for start, describe and terminate. The mTLS run as IMPL-0028 0.3 words it moves to the DESIGN-0028 cut-over (IMPL-0026), because dev has no client certificate until then.

### Budget burst (IMPL-0028 0.4)

Run 2026-10-10 by the maintainer: `cmd/rg-burst` from a workstation over dev's edge TLS + OIDC path, on its own unversioned worker (queue `rg-burst`, fresh installation id), concurrency 50. Frontend CPU from the Kubernetes pod dashboard (request 0.1 core, no limit).

| Run | Pairs | Bound | Failed | Rate | Acquire p50 / p99 / max | Last run's history | Frontend CPU |
| --- | ----- | ----- | ------ | ---- | ----------------------- | ------------------ | ------------ |
| 1 | 20,000 | 2,000 (default) | 5 (0.025%) | 61/s | 376 ms / 5.30 s / 25.8 s | 3,464 events, 671,178 B | peak about 0.6 core |
| 2 | 5,000 | 10,000 | 0 | 59/s | 375 ms / 4.64 s / 27.4 s | 2,095 events, 398,959 B | about 0.25 core |

- **About 1.7 events and 195 bytes per handled Update or Signal.** A run that ends at the 2,000-handled bound holds about 3,500 events and 670 KB, under a tenth of Temporal's 10,240-event / 10 MB warning limits.
- **The server's suggestion caps a run too.** Run 2 lifted the bound to 10,000 handled, but its last run held only 2,095 events. A single run would have needed about 17,000, so `GetContinueAsNewSuggested` ended runs at roughly the same size. Raising the bound buys nothing. Lowering it only adds handoffs, which are where the failures come from.
- **Failures and SDK warnings come from the handoff.** The 5 failed acquires were Updates caught in a ContinueAsNew handoff ("unexpected workflow task failure"). The warnings ("history contains events past expected last event ID", "premature end of stream") are stale workflow tasks the SDK drops and retries. In production `AcquireBudget` is an activity and retries; rg-burst calls without retries and counts them. 0.5 must tell these apart from nondeterminism when it counts `WorkflowTaskFailed`.
- **Latency.** p50 includes the workstation → edge → frontend round trip. The p99 and max tails line up with handoffs. One installation sustains about 60 grants a second against real demand of under one a second: a 5,000/h REST budget at about 12 calls per check is about 400 checks an hour.
- **Frontend CPU.** It peaked at about six times dev's 0.1-core request, with no throttling because there is no limit. `contrib/temporal/values-base.yaml` requests 250m.

**Decision: the `InstallationWorkflow` ContinueAsNew bound stays at `DefaultMaxHandled = 2000`.** It ends runs before the server suggestion, which keeps the cadence deterministic, and every run stays far below the history limits. The same bound applies to `installation/<app>/<id>` in Phase 4.

### Budget branch on the live build (IMPL-0028 0.5)

Read 2026-10-10 by the maintainer on dev (`2.0.0-rc.4`, current since 2026-09-27):

- **`budget-v1` is taken.** A running RepoWorkflow's only `Version` marker is change id `budget-v1` at version `1`.
- **Zero `WorkflowTaskFailed`.** Every running execution's current run was scanned (1 InstallationWorkflow, 8 RepoWorkflows, plus the two rg-burst runs from 0.4 before they were terminated), with no failed workflow task. The worker's SDK metrics were not read because the distroless image has no shell tools for an in-pod scrape. The history scan is the stronger check anyway.
- **Replay fixture.** `installation/160613249` (queue `repo-guardian`), its current run spanning 2026-10-08 14:05 to 2026-10-10 11:25 UTC: 3,520 events, 364 acquires, 365 reports, 48 lease-sweep timers, no failed tasks. Committed compact as `internal/workflows/testdata/histories/installation_dev_rc4.json` (1.7 MB). Payloads carry only repository and lease ids, counts, rate-limit values and timestamps. It replays clean on this branch's code, so the Phase 4 changes (App fields with `omitempty`) are replay-compatible with real rc.4 histories. A probe that added a timer at workflow start made the replay fail as nondeterministic, so the check is real.
- **Side effect of 0.4.** rg-burst left its InstallationWorkflows running on a queue with no worker. Both were terminated, and rg-burst now terminates its workflow on exit.

### Backlog metric on the homelab Prometheus (IMPL-0028 5.6)

Read 2026-10-10 by the maintainer from the homelab Prometheus, which scrapes the Temporal 1.32.0 matching service through a ServiceMonitor. This checks Spike 5(b)'s dev-server labels against the production reporter:

- **The Temporal namespace is `exported_namespace`, not `namespace`.** prometheus-operator stamps the target's Kubernetes namespace (`temporal`) into `namespace` and renames Temporal's own label to `exported_namespace="repo_guardian_dev"`. The default KEDA query selected `namespace=` and would have read nothing, so KEDA would never scale. Fixed: the query reads `keda.prometheus.namespaceLabel`, default `exported_namespace`, and setups that keep the original label set `namespace`. The generated Temporal alerts and the chart's PrometheusRule already avoided `namespace` for this reason.
- **Build labels match the spike.** Versioned series carry `worker_build_id` (`2_0_0_rc_4`), `worker_deployment_name` (`repo_guardian`) and `worker_version` (`repo_guardian_2_0_0_rc_4`). Each queue also has an `__unversioned__` series with no `worker_build_id`, which the existing `max by (..., worker_build_id)` keeps as its own group. Drained builds (`rc.1` to `rc.3`, `dev`) still report zero-valued series. Label values are sanitised (`-` and `.` become `_`), as the spike found.
- Series are split by `task_type` (`Workflow`, `Activity`) and `task_priority`, and carry `pod`/`instance`, which the inner `max` drops.

Still open in 5.6: comparing the query's value with `temporal task-queue describe` while each controls queue is non-empty. Those queues have pollers only once rc.5 runs on dev, and they have work only once IMPL-0029 gives the evaluator something to do.

### Spike 1: GraphQL commit (IMPL-0028 0.6)

Run 2026-10-09 by the maintainer against `repo-guardian/test` as a test Remediation App (Contents, Pull requests, Issues, Administration, Custom properties and Workflows write), through `getInstallClient`, so every call crossed otelhttp, the rate-limit transport and ghinstallation. Raw observations: `build/spike/TestSpike_GraphQLCommit.json` (not committed).

- **Correct `expectedHeadOid`:** HTTP 200, `data.createCommitOnBranch.commit.oid` set.
- **Stale `expectedHeadOid`: HTTP 200, not an HTTP error.** `data.createCommitOnBranch` is `null` and the body carries `errors: [{"type": "STALE_DATA", "path": ["createCommitOnBranch"], "message": "Expected branch to point to \"<oid>\" but it did not.  Pull and try again."}]`. `ErrExpectedHeadMismatch` is therefore `errors[].type == "STALE_DATA"` on a 200, which a status-code check never sees. The GraphQL error path is read before the throttle path's REST-shaped checks, and a `RATE_LIMITED` type is the throttle signal on the same 200 shape.
- **Limits: 101 files and a 1 MiB + 1 byte file both committed.** The 100-file and 1 MiB-per-file limits DESIGN-0031 AR-0031-09 cites are not the mutation's; the real ceiling was not reached (the 1 MiB + 1 request carried about 1.4 MB of base64). DESIGN-0031's change-set bounds stay as repo-guardian's own conservative limits, reworded so they no longer claim to be GitHub's.
- **Rate limit:** `x-ratelimit-resource: graphql`, limit 5000, and each mutation cost 1 point (`used` 1 to 6 over six mutations). GraphQL is its own bucket, separate from REST's `core`, confirming DESIGN-0032's per-resource budget snapshot.
- **Signature:** every commit `isValid: true`, `state: VALID`, `wasSignedByGitHub: true`, signer `web-flow`; REST `verification.verified: true, reason: valid`. The author is the App's bot (`<app-slug>[bot]`, its numeric noreply address) and the committer is `GitHub <noreply@github.com>`. Adoption by authenticated author (DESIGN-0032 AR-0032-02) keys on the author, never the committer, the same distinction rgctl's `web-flow` fix makes (IMPL-0027).

### Spike 2: update-branch (IMPL-0028 0.7)

Run 2026-10-09 by the maintainer; raw observations in `build/spike/TestSpike_UpdateBranch.json`.

| Case | Status | Body `message` |
| ---- | ------ | -------------- |
| PR already contains the base head | 422 | `There are no new commits on the base branch.` |
| `expected_head_sha` stale | 422 | `expected head sha didn’t match current head ref.` (a typographic apostrophe) |
| behind and clean | 202 | `Updating pull request branch.`; the head moved 1.7 s later |
| conflicting change on both sides | 422 | `merge conflict between base and head` |

Three different outcomes share one status, distinguished only by free-text messages, and the "already up to date" case is not in DESIGN-0032 D17 at all: treated as a conflict it would hold a healthy PR, treated as a mismatch it would defer forever. D17 is amended: after any 422 the run re-reads the PR (head SHA and the compare's `behind_by`) and classifies from state, so a head that moved defers, `behind_by == 0` is a no-op that proceeds to `Remediate`, and only a PR still behind at the expected head holds `conflict`. The message is logged, never matched. The 202 latency supports D17's "defer and re-pin on the next run".

### Spike 3: Evaluation App minimal permissions (IMPL-0028 0.8)

First run 2026-10-09 by the maintainer as a test Evaluation App holding Metadata, Contents and Pull requests read plus **repository** Custom properties read (the Organization permission the design names was not yet granted); raw observations in `build/spike/TestSpike_EvalAppPermissions.json`. For comparison the same reads were run as the test Remediation App (`build/spike/remediation-readback.json`).

| Read | Evaluation App (Metadata-based) | Remediation App (Administration write) |
| ---- | ------------------------------- | -------------------------------------- |
| `GET /repos`: `has_wiki`, `has_issues`, `has_projects`, `web_commit_signoff_required` | present | present |
| `GET /repos`: `allow_merge_commit`, `allow_squash_merge`, `allow_rebase_merge`, `allow_auto_merge`, `allow_update_branch`, `delete_branch_on_merge`, the four merge-commit title/message fields | **absent** | present |
| `GET /repos`: `security_and_analysis` | **absent** | present |
| rulesets `?includes_parents=true`, and by id with `conditions` and `rules` | 200, `source_type: Repository` | same |
| `GET /properties/values` | 200, `[]` | 200, `[]` |
| `GET /orgs/{org}/properties/schema` | 403 | 403 |
| `GET /vulnerability-alerts` | 403 | 204 |
| `GET /branches/main/protection` | 403 | 404 (no classic protection) |
| `GET /codeowners/errors` | 200, both lines reported | 200 |

- **Rulesets and CODEOWNERS errors hold** under the Metadata/Contents set, including `source_type`, `conditions` and `rules`.
- **The merge-policy settings and `security_and_analysis` do not.** DESIGN-0032's "repository settings are Metadata reads" is wrong for exactly the fields `repo_settings` manages: they are omitted, not nulled, so a reader that defaults a missing field would report every repository's merge policy as false. The design is amended to say so; which permission is the minimum (Administration read is the candidate, since the design already grants it for the vulnerability-alerts read) is the second run.
- **Not yet answered:** no org ruleset and no property value were visible to either App, so whether an inherited org ruleset and property values are Metadata reads is open; and the org schema 403 is expected until the App holds Organization Custom properties read.

**Second run (2026-10-09)**, after the org approved Administration read and Organization Custom properties read (repository Custom properties read was still granted), with an org property `Owner` set to `donald` on the repository:

- `security_and_analysis` is now returned, and `GET /vulnerability-alerts` is 204: **Administration read** is the permission for both, as the design already states for the vulnerability-alerts read.
- **The merge-policy fields are still omitted from REST `GET /repos` under Administration read.** They appear only for an App holding write permissions, which the Evaluation App must never hold.
- **GraphQL returns all of them under the same read-only set:** `repository { mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed autoMergeAllowed allowUpdateBranch deleteBranchOnMerge squashMergeCommitTitle squashMergeCommitMessage mergeCommitTitle mergeCommitMessage webCommitSignoffRequired hasWikiEnabled }` answered 200 with the real values (`deleteBranchOnMerge: true`, `allowUpdateBranch: false`, ...), costing one point of the `graphql` bucket, with `viewerCanAdminister: false`. The settings reader therefore reads repository settings through GraphQL, the same client `Writer.Commit` already adds.
- `GET /orgs/{org}/properties/schema` is 200 with Organization Custom properties read, returning `Owner` with `value_type`, `required`, `values_editable_by` and `require_explicit_values`.
- `GET /properties/values` returned `[{"property_name": "Owner", "value": "donald"}]`: the earlier `[]` meant no value was set. Whether values are readable with Metadata alone is still unproven, because repository Custom properties read was granted throughout.
- Still no org ruleset was visible.

**Third run (2026-10-09)**, with repository Custom properties read removed (installation: Metadata, Contents, Pull requests and Administration read): `GET /properties/values` still returned `[{"property_name": "Owner", "value": "donald"}]`. **Property values need no Custom properties permission**; they are readable with the Evaluation App's read set, as the design states. (The Organization permission granted for this run was "Custom organization roles", not "Custom properties", so the schema read was 403 again; run 2 already showed Organization Custom properties read returns it.)

**Spike 3's final Evaluation App set:** Metadata, Contents, Pull requests and Administration read, plus Organization Custom properties read; repository settings through GraphQL. DESIGN-0032's permission table matches it.

**Originally to confirm (third run):** remove repository Custom properties read from the test Evaluation App and re-run, to show property values are a Metadata read; and create an org ruleset targeting the repository, if the org's plan offers one, to show inherited rulesets list with `source_type: Organization`. **The test org's plan does not offer org rulesets** (maintainer, 2026-10-09), so the inherited-ruleset read is untested here and stays an assumption until an org with org rulesets is available. The reader already handles it the safe way: `Source` comes from `source_type`, and a ruleset it cannot attribute is never treated as a writable repository ruleset.

### Spike 4: `installation_repositories` on an all-repositories install (IMPL-0028 0.9)

Run 2026-10-10 by the maintainer in the `repo-guardian` test org, with both test Apps installed on **all repositories**. The Evaluation App subscribes to `push`, `pull_request` and `repository`; the Remediation App subscribes to nothing and has an active webhook pointing at a placeholder URL. Creating `repo-guardian/spike-allrepos-20261010` produced:

| App | Deliveries (UTC-4) |
| --- | ------------------ |
| Evaluation | `repository.created` 08:53:12, `push` 08:53:12 (the initial commit), `installation_repositories.added` 08:53:13 |
| Remediation | `installation_repositories.added` 08:53:13 |

The Remediation App's payload carried `repository_selection: "all"` and `repositories_added: [{id: 1413185081, full_name: "repo-guardian/spike-allrepos-20261010", private: true}]`. GitHub's documentation describes the event only for selected-repository changes, but **it fires on an all-repositories installation too**, about a second after `repository.created`, and GitHub sends it to Apps that subscribe to nothing.

- **Remediation App:** it learns of a new repository from its own lifecycle event, without waiting for its discovery. Discovery stays the backstop, because GitHub does not redeliver a failed delivery (each placeholder delivery here failed and was never retried). DESIGN-0032 is amended to say so.
- **Evaluation App: one new repository runs two discoveries.** `RouteWebhook` sends `repository.created` to `discover` (the one repository, then `recheck`). It sends `installation_repositories.added` to `discoverInstallation`, which starts a single-installation `DiscoveryWorkflow` keyed by delivery id. That workflow lists every repository in the installation and upserts them all. Both paths are idempotent, so the result is correct. The cost is a full installation listing (one call per 100 repositories, plus an upsert batch per 100) for every repository created in an all-repositories org, on top of the single-repository path that already handled it. The same happens on the rc today. Fixed in IMPL-0028 per Phase-0 OQ2.


### Spike 5: Temporal behaviours (IMPL-0028 0.10)

Run 2026-10-07 on the pinned dev server (CLI v1.9.1); tests in `internal/temporal/spike_integration_test.go`.

- **(a) Signal-with-start into a completing workflow.** 4 senders, 200 signal-with-starts against one id whose runs take one 30 ms activity and complete. With the end-of-run drain (`ReceiveAsync` until empty): 13 runs consumed **200 of 200**. Without it: 11 runs consumed 11, **189 lost**. F4 holds as written, and the drain DESIGN-0032 specifies was sufficient under this load: no signal was lost, including those that arrived while a run was completing.
- **(b) Backlog metric labels.** `approximate_backlog_count` is emitted per `namespace`, `taskqueue`, `partition` (4 by default), `task_priority`, `task_type`, `worker_build_id`, `worker_deployment_name` and `worker_version`, with `operation="TaskQueuePartitionManager"`. Three findings for the KEDA query:
  1. **Label values are sanitised: `-` becomes `_`.** The series read `namespace="repo_guardian"` and `taskqueue="repo_guardian_eval"`; the system namespace reads `temporal_system`. A query selecting `namespace="repo-guardian"` (DESIGN-0028, IMPL-0028 task 5.x as written) matches nothing. This is the dev server's Prometheus reporter; DESIGN-0028's homelab check confirms it against the production reporter before Phase 5.
  2. There is no aggregate series: the count is split by `task_priority`, and `worker_build_id` is a label. Both answer DESIGN-0028's two label questions with "it splits", so its default query now groups on both inside the `max` (`max by (partition, task_type, task_priority, worker_build_id)`) before summing; a `max` over priorities would keep only the largest and drop real backlog.
  3. It is approximate: five queued workflow tasks per queue read as 3 after 60 s. Fine as a scaling signal with an activation threshold of 1; not a count to alert on. The `physical_*`, `pri_physical_*` and `fair_physical_*` variants are per-physical-queue duplicates and must not be summed with it.
- **(c) Evaluation history growth.** One signal per iteration carrying N 60-byte changed paths, one batched start activity per iteration, measured with `DescribeWorkflowExecution`:

  | Paths per signal | Iterations | Events | History bytes | Bytes per iteration | Projected at 100 iterations |
  | ---------------- | ---------- | ------ | ------------- | ------------------- | --------------------------- |
  | 200 (filtered to the policy's paths) | 100 | 909 | 1.37 MB | 13.7 KB | 1.4 MB |
  | 2048 (one path per commit, full push) | 100 | 834 | 13.0 MB | 130 KB | 13 MB |
  | 20,000 (raw, many paths per commit) | 5 | 55 | 6.3 MB | 1.26 MB | 126 MB |

  Temporal warns at 10 MB of history and terminates at 50 MB, and a single payload is capped at 2 MB. An unfiltered push therefore breaks the 100-iteration bound DESIGN-0032 relies on: a 2048-path push stream crosses the warning, and a raw monorepo push crosses the hard limit by iteration 40. **The bound only holds if the signal carries paths already filtered to the policy's `Owns() ∪ Reads()`, with a cap above which the signal carries the unknown set instead.** DESIGN-0032's history-growth paragraph is amended accordingly (filter at the router, cap 256 paths).

### Spike 6: Database role provisioning (IMPL-0028 0.11)

Run 2026-10-07; `pgtest.ControlsRoles` (owner without `CREATEROLE`, `rg_evaluator`, `rg_remediator`, and `rg_all` created `IN ROLE` both) plus a cut-down controls schema with DESIGN-0032's matrix in `internal/store/postgres/roles_spike_integration_test.go`, every assertion run as the application role.

- **Column grants enforce record-is-narrow.** The remediator updating `remediated_generation, hold` succeeds; adding `status` to the same `UPDATE` fails with `42501 permission denied for table control_results`. The evaluator touching `hold` fails the same way. An evaluator `INSERT ... ON CONFLICT DO UPDATE` over its own columns succeeds.
- **`SELECT ... FOR UPDATE` needs only the one-column UPDATE grant**, as the matrix states (`repository_policy_state.last_remediation_at`).
- **The withdrawal cascade needs no DELETE on `rule_results`**: referential actions run with the owner's rights. The evaluator keeps DELETE there only for direct rule removal.
- **Identity columns need no sequence grant; `BIGSERIAL` does.** An INSERT into a `GENERATED ALWAYS AS IDENTITY` table succeeded with no sequence privilege; the same INSERT into a `BIGSERIAL` table failed with `permission denied for sequence`. The controls schema uses identity columns, and the matrix's "USAGE on the sequences" line is dropped.
- **The row-level security policies as written hide the other App's rows from reads.** A `CREATE POLICY ... TO rg_evaluator USING (app = 'eval')` policy is `FOR ALL`, so it filters SELECT too: the evaluator saw 1 of 2 `app_repository_access` rows. Resolution runs as the evaluator and needs the Remediation App's rows to compute `remediable`, so every `remediate` assignment would have resolved to `evaluate` with no error. Adding `CREATE POLICY ... FOR SELECT TO rg_evaluator, rg_remediator USING (true)` restores reads (2 of 2) while writes stay fenced: an evaluator `UPDATE` of a `remediate` row affects zero rows **without an error**, so the application must check affected-row counts where it relies on the fence. DESIGN-0032's matrix is amended.
- **`all` as a member of both roles** writes both Apps' rows (policies granted to a role apply to its members).
- **Modes.** External: the SQL `ControlsRoles` runs is the documented SQL. Baked: the rc's init-script plus hook pattern (`store-postgres-ro.yaml`) carries over; on the fresh install D27 requires, the init script always runs, and the hook remains for password rotation. One baked-mode gap: the image's `POSTGRES_USER` is a superuser, so "the migrating role holds no `CREATEROLE`" holds only if the init script also creates the owner role and the migrate Job connects as it. CNPG: managed roles carry login, password Secret, `inRoles` and the role flags, and no table privileges, which is consistent with migrations granting; `rg_all` is `inRoles: [rg_evaluator, rg_remediator]`. CNPG was not run here; Phase 6's chart tests cover rendering, and the homelab install covers behaviour.

### Spike 7: Template sourcing (IMPL-0028 0.12)

Run 2026-10-07: `internal/rules/policyroot_spike_test.go`, plus a throwaway local kind cluster (deleted) to confirm the projected layout.

- **ConfigMap keys cannot contain `/`.** `templates/codeowners.tmpl` is rejected (`[-._a-zA-Z0-9]+`). The chart encodes a nested path into the key (`templates__codeowners.tmpl`, rejecting file names that contain `__`) and maps it back with the volume's `items[].path`. This works: the pod saw `/policy/orgs/acme.hcl` and `/policy/templates/codeowners.tmpl`.
- **The projected layout defeats a naive walk.** Kubelet writes the data into `..<timestamp>/`, points `..data` at it, and makes each top-level path component a symlink through `..data` (`templates -> ..data/templates`). `filepath.WalkDir` on the mount does not follow those symlinks: it saw only the timestamped copies, whose directory name changes on every update. The reader must walk `<root>/..data/` when it exists, skip names starting with `..`, and key files by their path relative to it. The prototype does, reads all three files, and its hash is stable across reads.
- **Hash.** The prototype hashes root files and the embedded templates the root does not shadow, length-prefixed and sorted. A template edit and an embedded-default change each move the hash; a change to an embedded template the root shadows does not. DESIGN-0030 D20 says the embedded bytes are hashed; that is refined to the unshadowed ones, so a binary upgrade does not re-roll a fleet whose operator overrides that template.
- **Size.** A ConfigMap's data is capped at 1 MiB (`Too long: may not be more than 1048576 bytes`, confirmed). The policy root, HCL and templates together, must fit; `existingConfigMap` has the same cap. Helm also stores the release (values and rendered manifests, gzipped) in a Secret with the same cap, so the policy counts twice before compression.

### Spike 8: Policy revert (IMPL-0028 0.13)

Run 2026-10-07: `internal/store/postgres/policy_revert_spike_integration_test.go`.

- **The rc bug is confirmed.** Deploying A, then B, then A again: the third `RecordPolicyVersion` reports `firstSeen = false`, so no rollout starts, and the rc's `CurrentPolicyVersion` (ordered by `first_seen_at`) still returns B.
- **The D14 upsert fixes the ordering.** With `activated_at` and `ON CONFLICT (version) DO UPDATE SET activated_at = now() RETURNING (xmax = 0)`, the sequence A, B, C, A reports the right "newest by `activated_at`" at every step, `first_seen` is true only for C, and `first_seen_at` survives every reactivation.
- **Two problems the spike exposed beyond F14:**
  1. *Activation per pod is not activation per deploy.* D14 sets `activated_at` "on every deploy that loads that version", but the code that runs it is worker startup, once per pod. During a rollout from A to B, an old-version pod that restarts (eviction, OOM, node drain) re-activates A after B, and the monotonic resolver then discards every resolution under B until another B pod restarts. A pod cannot tell a revert from a straggler.
  2. *The rollout id rejects a revert's rollout.* Rollouts start as `policy-rollout/<version>` (`controls-rollout/<version>` in DESIGN-0032) with `REJECT_DUPLICATE`, so re-activating A cannot start A's rollout again while the earlier run is within namespace retention.

  See Phase-0 OQ1.

### Spike 9: Per-role deployment rehearsal (IMPL-0028 0.14)

Not run as a separate rehearsal (maintainer decision, 2026-10-10): no phase waited on it. The behaviour is covered by `TestControlsDeployments_DevServer` on the Temporal dev server: the eval deployment promotes at first start, the rc deployment keeps its build, and a remediate deployment with no pollers does not block the evaluator. Dev's history shows the same promotion for every rc build so far (rc.1 to rc.4, 0.1). Seeing it on the cluster is part of IMPL-0028 7.6, when rc.5 is deployed to dev.

### Spike 10: Label case (IMPL-0028 0.15)

Run 2026-10-09 by the maintainer; raw observations in `build/spike/TestSpike_LabelCase.json`.

- Creating `spike-case-N` when `Spike-Case-N` exists: **422** `Validation Failed`, `errors: [{"resource": "Label", "code": "already_exists", "field": "name"}]`.
- `GET /labels/spike-case-N` returns the label named `Spike-Case-N`: lookup is case-insensitive.
- `PATCH /labels/spike-case-N` with `new_name: SPIKE-CASE-N` succeeds and changes only the case; the old mixed-case path then also resolves to it, and the list holds one label.

Label names are unique case-insensitively, every endpoint addresses them case-insensitively, and a case change is an update. DESIGN-0031's `labels` control compares lower-cased names (as written) and remediates a case-only difference with `PATCH new_name`, never a create; a create returning `already_exists` means the read is stale and the run re-reads.

### Phase-0 open questions

#### OQ1: What activates a policy version?

**Resolved 2026-10-07: (a).** Split across the plans: IMPL-0028 Phase 6 builds the store method and the migrate Job's hook and policy mount; IMPL-0029 computes the controls version in the migrate Job and keys the rollout on the activation.

- (a) ✅ recommended: **the migrate Job.** It already runs once per `helm install`/`upgrade` (add `pre-rollback` to its hook list so a `helm rollback` also activates), loads the same binary and policy ConfigMap, computes the version and upserts `activated_at` as the owner. Workers only read the newest activation and start `controls-rollout/<version>/<activated_at unix seconds>` with `REJECT_DUPLICATE`, so each activation rolls out exactly once and a pod restart activates nothing.
- (b) Workers activate only when the version they load is not the current activation **and** their pod template hash is newer than the one recorded with it (a `deploy_generation` column fed from a chart-stamped env var). Keeps activation in the worker but adds a deploy-identity value to the chart and schema.
- (c) Keep per-pod activation and accept the straggler window: a restarting old pod discards resolutions until a new pod restarts. Simplest; wrong in exactly the failure it exists to prevent.
- other:

#### OQ2: Should `installation_repositories.added` on an all-repositories installation still run a full installation discovery?

**Resolved 2026-10-10: (a)**, with a bound. `WebhookInput` carries `RepositorySelection`. `RouteWebhook` discovers an all-repositories `added` from its payload when it names at most 100 repositories (one listing page). A larger payload, such as a selected → all switch, still goes to the batched, retried `DiscoveryWorkflow`, so one activity never upserts and signals thousands of repositories.

Spike 4 showed the event fires for every new repository on an all-repositories installation, alongside `repository.created`, so the Evaluation App lists its whole installation once per new repository.

- (a) recommended: **discover only the payload's `repositories_added` when `repository_selection` is `all`**, the same path as `repository.created`, and keep the full single-installation listing for `selected`, where the payload can name repositories the listing must confirm. The scheduled discovery stays the backstop for anything a payload misses.
- (b) Drop the `installation_repositories.added` route on the Evaluation App entirely when the installation is `all`, relying on `repository.created`. That saves the duplicate signal, but a repository created while the Evaluation App was suspended, then delivered on unsuspend, would wait for scheduled discovery.
- (c) Leave it: the listing is cheap (one call per 100 repositories) and idempotent.
- other:

## References

- DESIGN-0029, DESIGN-0030, DESIGN-0031, DESIGN-0032 (as amended 2026-10-06)
- INV-0021 (assumptions A1 to A23, the code half of this audit)
- IMPL-0025 Phases 16 to 18 (the v1 runtime and shadow/backfill tasks)
- DESIGN-0028 / IMPL-0026 (KEDA trigger and Temporal auth, not implemented)
- GitHub: webhook events and payloads; permissions required for GitHub Apps; REST rate limits; GraphQL rate limits; `createCommitOnBranch` in the public GraphQL schema; github/rest-api-description#7210
- Temporal Go SDK v1.49.0: `internal/worker_deployment_client.go`, `internal/internal_worker.go`, `workflow/workflow.go`
