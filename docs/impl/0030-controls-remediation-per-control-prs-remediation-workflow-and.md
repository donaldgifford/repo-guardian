---
id: IMPL-0030
title: "Controls remediation: per-control PRs, remediation workflow and the v2.0.0 fresh install"
status: Draft
author: Donald Gifford
created: 2026-10-07
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0030: Controls remediation: per-control PRs, remediation workflow and the v2.0.0 fresh install

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [How the phases are ordered](#how-the-phases-are-ordered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Remediation schema, store and grants](#phase-1-remediation-schema-store-and-grants)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Evaluator-side hooks: PR observation and starting due remediations](#phase-2-evaluator-side-hooks-pr-observation-and-starting-due-remediations)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: RemediationWorkflow, activities and the GitHub fake](#phase-3-remediationworkflow-activities-and-the-github-fake)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: The pull-request path](#phase-4-the-pull-request-path)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: API remediations: recommend, direct and workflow](#phase-5-api-remediations-recommend-direct-and-workflow)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Sweep, lifecycle maintenance and Remediation App discovery](#phase-6-sweep-lifecycle-maintenance-and-remediation-app-discovery)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Metrics, alerts, API and UI](#phase-7-metrics-alerts-api-and-ui)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: Cutover code and documentation](#phase-8-cutover-code-and-documentation)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
  - [Phase 9: Remediation rc and the homelab fresh install](#phase-9-remediation-rc-and-the-homelab-fresh-install)
    - [Tasks](#tasks-8)
    - [Success Criteria](#success-criteria-8)
  - [Phase 10: v2.0.0](#phase-10-v200)
    - [Tasks](#tasks-9)
    - [Success Criteria](#success-criteria-9)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
  - [OQ1: Where do the remediation tables land in the goose chain?](#oq1-where-do-the-remediation-tables-land-in-the-goose-chain)
  - [OQ2: Who owns PR observation and the start-due-remediations activity?](#oq2-who-owns-pr-observation-and-the-start-due-remediations-activity)
  - [OQ3: Which control types remediate in v2.0.0?](#oq3-which-control-types-remediate-in-v200)
  - [OQ4: Does remediation ship as an rc before v2.0.0?](#oq4-does-remediation-ship-as-an-rc-before-v200)
  - [OQ5: Which Apps does the fresh install use?](#oq5-which-apps-does-the-fresh-install-use)
  - [OQ6: When are the old deployments' PRs closed?](#oq6-when-are-the-old-deployments-prs-closed)
  - [OQ7: What gates turning remediation on for the remaining orgs, and GA?](#oq7-what-gates-turning-remediation-on-for-the-remaining-orgs-and-ga)
  - [OQ8: How long are the old databases kept?](#oq8-how-long-are-the-old-databases-kept)
  - [OQ9: What does the sticky PR comment look like?](#oq9-what-does-the-sticky-pr-comment-look-like)
  - [OQ10: Is there a built-in default PR body?](#oq10-is-there-a-built-in-default-pr-body)
  - [OQ11: Which remediation holds alert?](#oq11-which-remediation-holds-alert)
  - [OQ12: Where is v2.0.0 tagged?](#oq12-where-is-v200-tagged)
  - [OQ13: How many PRs?](#oq13-how-many-prs)
- [References](#references)
<!--toc:end-->

## Objective

Build the remediation half of the controls line and ship it as v2.0.0:
per-control remediation PRs under a per-repository cap, run by a
`RemediationWorkflow` per repository and control on the remediator
role; API remediations under `apply = recommend | direct | workflow`;
the remediation sweep and lifecycle maintenance; the metrics, alerts,
API and UI that show it; and the homelab fresh install that replaces
the dev (v2 rc) and prod (v1) deployments with one v2 install.

**Implements:** DESIGN-0032 (Remediation, API remediations, the
remediation tables, Failure semantics, the remediation rows of the
Temporal mapping, Migration / Rollout Plan steps 2 to 4), the write
side of DESIGN-0031 (`Remediate`, `ChangeSet`, `Writer`,
`WorkflowApply`) and the remediation settings of DESIGN-0030
(`max_open_prs`, `reopen_after`, `effective_mode`).

## Scope

### In Scope

- The `remediations`, `remediation_steps` and `remediation_events`
  tables, the `remediation_due` and `remediation_maintenance` views,
  the remediator's column grants, and the store methods over them.
- PR observation by the evaluator (merged, closed by a user, observed
  head) and the one batched activity per evaluation that signal-with-starts
  the due remediations.
- `RemediationWorkflow` (`remediation/<repository id>/<control slug>`
  on `repo-guardian-remediate`): one run at a time, the re-check flag,
  the end-of-run re-read, epoch fencing before every write, durable
  operations with the step journal, `FOR UPDATE SKIP LOCKED` on
  overlapping attempts.
- The PR path: find, adopt or hold; slot reservation; descent check;
  update-branch; fresh evaluation at the pinned head; `closed_compliant`;
  `Remediate` with the self-check; one commit through
  `createCommitOnBranch`; journaled reverts of obsolete bot edits; PR
  create, adopt or update; narrow recording.
- API remediations: recommendations with proposals, journaled direct
  apply, and `workflow` apply for types that implement `WorkflowApply`.
- The `remediation-sweep` schedule (sweep plus maintenance) and the
  `controls-discovery-remediate` schedule.
- Remediation metrics, alerts, dashboard panels, the `/remediations`
  endpoint, the remediation object, and the UI Remediations view.
- The cutover code changes, the docs rewrite (`v2-migration.md` as the
  fresh-install runbook, the remediation parts of `v2-overview.md` and
  `v2-onboarding.md`, CLAUDE.md), the homelab fresh install, and the
  v2.0.0 release.

### Out of Scope

- Everything in IMPL-0028 (client split, `Writer` package, GraphQL
  `Commit`, throttle classification, two Apps, per-App webhooks, two
  queues and worker deployments, roles, KEDA, the goose skeleton,
  database role provisioning, and every phase-0 spike).
- Everything in IMPL-0029 (policy model, resolution, control types'
  evaluation, `control_results`, `EvaluationWorkflow`, the switch-over
  that deletes the old engine, the evaluate-only rc, posture).
- The evaluate-mode remediation preview (DESIGN-0032 D7, "a later
  phase").
- DESIGN-0034's CODEOWNERS `valid` rule; DESIGN-0034 stays Draft.
- Deleting branches. repo-guardian never deletes a branch (D30);
  `rgctl prs close --delete-branch` stays an operator's opt-in.
- Foreign-PR detection (D5).
- IMPL-0026 (Temporal credential reload, OpenBao certificates), which
  runs after IMPL-0028 on its own schedule.

## How the phases are ordered

Phases 1 to 7 are code, each ending with `make lint` and `make test`
green and a commit per numbered task. Phase 1 is storage, so every
later phase can run its tests as `rg_remediator`. Phase 2 lands the two
evaluator-side pieces remediation depends on but that have no consumer
before it. Phase 3 is the workflow shell and the GitHub fake the
behaviour tests need. Phases 4 and 5 are the two write paths. Phase 6
is the scheduled half, which needs both paths to exist. Phase 7 makes
it visible. Phase 8 is the cutover code and the docs, written against
the built behaviour. Phase 9 is human-run: the remediation rc and the
homelab fresh install. Phase 10 is GA.

The plan lands as one PR into `v2` with the `dont-release` label (OQ13).
No assignment is in `remediate` mode until Phase 9, so merging Phases 1
to 8 changes nothing a running deployment does. Phases 9 and 10 (the
homelab fresh install and the tags) are human-run and marked `deferred -
human required`.

## Implementation Phases

### Phase 1: Remediation schema, store and grants

#### Tasks

- [ ] 1.1 Confirm where the remediation DDL lives (OQ1). If IMPL-0029
  already shipped `remediations`, `remediation_steps`,
  `remediation_events`, both views and the remediator grants in
  `00003_controls_results`, diff them against DESIGN-0032 §Data Model
  and fix drift there; otherwise add them as the OQ1 answer says. The
  DDL is DESIGN-0032's verbatim: the three `CHECK`s on `kind`/`state`,
  `remediations_one_open`, `remediations_slots`, `UNIQUE (remediation_id, seq)`.
- [ ] 1.2 `remediation_due` view: the DESIGN-0032 predicate over
  `repository_policy_state`, `control_assignments` (active, matching
  epoch, `effective_mode = 'remediate'`), `control_results` (status
  `non_compliant`, remediable, `eval_generation > remediated_generation`
  or `intent_revision` differing from the latest remediation's, or
  `observed_head_sha <> acknowledged_head_sha`), and the latest
  remediation (`closed_by_user` eligible only when
  `eval_generation > closed_generation` or `next_eligible_at <= now()`).
  Ordered by `ordinal`.
- [ ] 1.3 `remediation_maintenance` view: rows needing `expire`
  (`reserved` past `reserved_until`), `withdraw` (open PR or
  recommendation whose assignment is gone, excluded, parked, out of
  org, or `effective_mode = 'evaluate'`), `supersede` (recommendation
  whose `intent_revision` moved) and `close_compliant` (open PR whose
  control reads `compliant`; acted on only after a fresh read, 4.10).
- [ ] 1.4 Grants: `rg_remediator` INSERT on `remediations`, UPDATE on
  every column except `observed_head_sha`, `observed_at`,
  `closed_generation`, `next_eligible_at`, DELETE on `reserved` rows
  (a row-level security policy `FOR DELETE TO rg_remediator USING
  (state = 'reserved')`, since a grant cannot restrict rows); INSERT on
  `remediation_steps` and `remediation_events`; UPDATE
  (`remediated_generation`, `hold`, `hold_since`) on `control_results`;
  UPDATE (`last_remediation_at`) on `repository_policy_state`; INSERT on
  `service_runs`; sequence USAGE. UPDATE, DELETE and TRUNCATE revoked
  for both roles on `remediation_steps` and `remediation_events`.
- [ ] 1.5 `internal/store/postgres/remediations.go`:
  `ReserveSlot(ctx, repo, control, epoch, opKey, intent, ttl)` (one
  transaction: `SELECT ... FOR UPDATE` on `repository_policy_state`,
  count `open + reserved` through `remediations_slots`, insert
  `reserved` or return `ErrPRCap`);
  `ClaimOperation(ctx, opKey)` (`SELECT ... FOR UPDATE SKIP LOCKED`,
  returning `ErrOperationHeld` when another attempt holds it; the
  transaction stays open for the activity and is released on return);
  `LatestRemediation`, `AppendStep`, `AppendEvent`, `SetIntent`.
- [ ] 1.6 `RecordRun` (record-is-narrow): `remediated_generation` to
  the generation read at run start, `acknowledged_head_sha` by
  compare-and-set against the head processed, `intent_revision`,
  `last_run_at`, `hold = NULL`; discarded and counted in
  `results_discarded_total{reason="stale_epoch"}` when the assignment
  epoch moved. Idempotent on `operation_key`.
- [ ] 1.7 `SetHold(ctx, repo, control, epoch, hold)` writing
  `control_results.hold`/`hold_since`, and the `remediations.hold =
  'permission'` path for blocked cleanup when the result row is gone.
- [ ] 1.8 `DueRemediations(ctx, batch)` and
  `MaintenanceCandidates(ctx, batch)` over the two views;
  `ExpireReservations(ctx, now)` returning the count for
  `remediation_reservations_expired_total`.
- [ ] 1.9 Pool: `REMEDIATOR_DB_POOL_SIZE` (default
  `REMEDIATOR_CONCURRENCY + 4`); startup refuses a pool smaller than
  the concurrency, since every running operation holds one connection
  for its row lock. `store_pool_wait_seconds{role}` from the pgxpool
  stats.
- [ ] 1.10 `remediation_due` truth table as SQL against the view
  (DESIGN-0032 Testing Strategy): policy state, assignment state and
  epoch, effective mode and reason, status, remediable, generations
  including the first-evaluation `1 > 0` row, intent revision, latest
  state, observed versus acknowledged head, `closed_generation`,
  `next_eligible_at`.
- [ ] 1.11 Grant tests through the extended `pgtest` harness, run as
  `rg_remediator` and never as the owner: every stated transaction
  succeeds (reservation, claim, step, record, hold, expiry); every
  cross-writer operation fails (remediator writing `eval_generation` or
  `observed_head_sha`, deleting an `open` row, updating a step).

#### Success Criteria

- The truth table and grant tests pass as the application roles.
- Sixteen concurrent `ReserveSlot` calls against one repository with
  `max_open_prs = 3` produce exactly three `reserved` rows.
- Two concurrent `ClaimOperation` calls on one key: one gets the row,
  the other `ErrOperationHeld`.

### Phase 2: Evaluator-side hooks: PR observation and starting due remediations

#### Tasks

- [ ] 2.1 PR observation in the evaluate activity, through the
  Evaluation App's `PRObserver`: `ListPullRequests("repo-guardian/")`
  (paginated to completion; a failed page is an observation error, not
  an empty list) plus `GetPullRequest` for each tracked PR not in the
  open list. Matching is by `remediations.pr_number`, never by branch
  name alone.
- [ ] 2.2 Observation writes, as `rg_evaluator` and only the columns it
  holds: `observed_head_sha` and `observed_at` on every open tracked PR;
  `state = 'merged'` with `closed_at`; `state = 'closed_by_user'` with
  `closed_generation = eval_generation` and `next_eligible_at =
  closed_at + reopen_after` (from `repository_policy_state`); a
  `remediation_events` row `observed` with the detail. A PR closed by
  the Remediation App is not reclassified (its row is already
  terminal).
- [ ] 2.3 `pr_event` trigger: a `pull_request` webhook on a
  `repo-guardian/` head signals the evaluation with trigger `pr_event`,
  which runs observation only and touches no evaluation column
  (DESIGN-0032 D24). Priority 2.
- [ ] 2.4 Confirm `control_results.intent_revision` is computed by the
  evaluator as DESIGN-0032 D23 states (hash of epoch, generation,
  `apply`, effective mode, remediability, control revision); add it if
  IMPL-0029 did not.
- [ ] 2.5 `StartDueRemediations` activity: at the end of each
  evaluation, read `remediation_due` for that repository and call
  `client.SignalWithStartWorkflow` once per due control with id
  `remediation/<repository id>/<slug>`, signal `recheck`, task queue
  `repo-guardian-remediate`, priority 3, `WorkflowIDReusePolicy`
  allowing a new run after completion. One activity per evaluation
  whatever the number of due controls (DESIGN-0032 A1).
- [ ] 2.6 Wire `StartDueRemediations` into `EvaluationWorkflow` after
  `Record`; a deferred or discarded evaluation skips it.
- [ ] 2.7 Tests: observation table (open with a new head, merged,
  user-closed, closed by us, a failed second page, a PR authored by
  someone else on a `repo-guardian/` branch, which is ignored);
  signal-with-start into a completing workflow, against the result of
  IMPL-0028's spike; a no-change evaluation starts nothing.

#### Success Criteria

- An evaluation that changes a remediable control's generation in a
  `remediate` assignment starts exactly one `RemediationWorkflow`
  signal for it; a second evaluation while it runs produces a signal,
  not a second execution.
- Observation as `rg_evaluator` cannot write any remediator column
  (grant test).

### Phase 3: RemediationWorkflow, activities and the GitHub fake

#### Tasks

- [ ] 3.1 `internal/workflows/remediation.go`: `RemediationWorkflow`
  with a `recheck` signal channel and a `recheckPending` flag. A run
  loops: run one remediation (activities below), drain signals, re-read
  `eval_generation`, `intent_revision` and `observed_head_sha` through
  a `ReadRemediationState` activity, and loop again if any moved or a
  signal arrived; otherwise complete. ContinueAsNew after 100 loops.
- [ ] 3.2 Register the workflow and its activities only on the
  remediator worker (`repo-guardian-remediate`, worker deployment
  `repo-guardian-remediate`); a depguard or registration test proves
  the evaluator never registers them.
- [ ] 3.3 Activity options: every remediation activity heartbeats and
  carries a `ScheduleToClose` timeout; `StartToClose` below it; retry
  policy non-retryable on `ErrOperationHeld`, `ErrStaleEpoch` and
  holds. The activity exits cleanly when `ClaimOperation` returns
  `ErrOperationHeld`.
- [ ] 3.4 Budget: the remediation run acquires the per-installation
  budget workflow at id `installation/remediate/<installation id>`
  (the app-dimensioned id from IMPL-0028), with `DefaultLeaseTTL`
  sized to cover the remediation activities' `ScheduleToClose`.
- [ ] 3.5 Epoch fence helper `fence(ctx, repo, control, epoch)`:
  re-reads `effective_mode`, `remediable`, the write set and `epoch`
  and returns `ErrStaleEpoch` when any changed. Called immediately
  before every external write in Phases 4 and 5; a lint-style test
  walks the write call sites and fails if one is not preceded by it.
- [ ] 3.6 Classification in the run, in order: `github.AsThrottled`
  (either App) → defer through the controls line's deferral path (a workflow
  timer, never a sleep in an activity); access loss
  → stop with `hold = 'permission'` or let resolution's
  `effective_mode` override take it; then outcomes. A secondary-limit
  403 is a throttle before it is a permission failure.
- [ ] 3.7 Remediator bot identity: resolve the Remediation App's bot
  user id at remediator startup (`GET /app` for the slug, then
  `GET /users/<slug>[bot]` for the id and `type = Bot`), cache it, and
  fail startup when it cannot be resolved. Reuse IMPL-0028's resolver
  if it built one.
- [ ] 3.8 GitHub fake for remediation (`internal/github/ghfake` or the
  package IMPL-0028 created): stateful repositories with real
  three-way merge for update-branch, `createCommitOnBranch`
  `expectedHeadOid` and push semantics, ref create that fails on an
  existing branch, PR authorship by user id and head repository id,
  fork PRs, and a request log so tests can assert no branch delete was
  ever called.
- [ ] 3.9 Replay: capture a `RemediationWorkflow` history from the
  first behaviour test and add it to `internal/workflows/testdata`
  with a replay test, as today's capture does for `EvaluationWorkflow`.
- [ ] 3.10 Tests (time-skipping environment): a signal during a run
  produces one extra loop, not a second run; overlapping attempts of
  one activity act once; a throttle defers without recording.

#### Success Criteria

- The workflow runs end to end against the fake with a no-op
  remediation and completes; the replay test passes in CI.
- The evaluator binary's dependency graph does not reach the
  remediation activities or the `Writer` package (depguard).

### Phase 4: The pull-request path

#### Tasks

- [ ] 4.1 Re-read: assignment, result and latest remediation; stop
  unless `needs_remediation` (the `remediation_due` predicate for this
  one row).
- [ ] 4.2 Find: `GetRef("repo-guardian/<slug>")` and the open PR on it.
  Tracked means a `remediations` row with this branch and a PR number.
- [ ] 4.3 Untracked branch: adopt only when an open PR on it is
  authored by the Remediation App's bot user id with type `Bot` and
  head repository id equal to this repository's. Otherwise hold
  `stale_branch` when the head equals the journaled bot head of a
  `closed_by_user` row, else `foreign_branch`. Commit author text is
  never consulted.
- [ ] 4.4 No branch: `ReserveSlot`; `ErrPRCap` holds `pr_cap`. Write the
  intent (`base_sha` = pinned default-branch head, `branch`,
  `expected_head`, `intent_revision`, `assignment_epoch`) before any
  external step.
- [ ] 4.5 Tracked PR: verify the head descends from the last journaled
  bot commit (`ListCommits` or compare); otherwise hold `conflict`,
  upsert the sticky comment (OQ9), never force-push. When the default
  branch was renamed, `UpdatePullRequestBase` first.
- [ ] 4.6 Behind main: `UpdateBranch` with `expected_head_sha` = the
  observed head. `422` merge conflict → hold `conflict` with the
  comment; `422` head moved → defer; any other `422` → error; `202` →
  defer, and the next run re-pins the head the merge produced.
  Journal `pr_rebased`.
- [ ] 4.7 Fresh `Evaluate` at the pinned default-branch head through the
  Remediation App's `Reader` (D22: never stored status).
- [ ] 4.8 Call the type's `Remediate` (built in IMPL-0029, per its
  OQ2) and `control.ValidateChangeSet` on the result, then the
  self-check (evaluate the changed content; refuse when a `Fixed` rule
  does not pass or a passing rule regresses). A change set with nothing
  in `Fixed` is recorded as a recommendation-style no-op on the PR row
  and not retried until the generation moves (DESIGN-0031 D14).
- [ ] 4.9 Obsolete bot edits: for each path the journal shows the bot
  wrote that the current change set no longer owns, revert it only
  where the current blob equals the journaled `blob_sha`; a human's
  edit to the same path is left alone. Reverts join the same commit.
- [ ] 4.10 Compliant at the fresh read with an open PR: close it as
  `closed_compliant` (sticky comment, `ClosePullRequest`), never delete
  the branch. Drift found by the fresh read keeps it open.
- [ ] 4.11 New branch: `CreateRef` at `base_sha`, journal
  `branch_created`. Commit: `Writer.Commit(branch, expected_head,
  changes, message)`; journal `committed` with every path and its blob
  SHA; `ErrExpectedHeadMismatch` → defer and re-pin.
- [ ] 4.12 PR: `CreatePullRequest`, or on `422` "already exists" list
  by head and adopt the open one authored by the App; otherwise
  `UpdatePullRequest` with the re-rendered title and body. Labels via
  the PR template. Journal `pr_created`/`pr_updated`; the reserved row
  becomes `open` with `pr_number` and `pr_url`.
- [ ] 4.13 PR title, body and labels rendered from the evaluation
  through the control's `pr {}` block and `internal/template`
  (`template.PRVars` with `.Control`, `.Fixed`, `.Manual`, `.Notes`),
  defaults per OQ10; bodies over 65000 characters truncated with the
  existing marker.
- [ ] 4.14 Recovery: a PR create that failed after the commit is
  completed by the next attempt, which finds the branch at exactly the
  journaled `committed_sha` under the same `operation_key`.
- [ ] 4.15 Record (`RecordRun`), then `last_remediation_at`, then the
  workflow's loop check (3.1).
- [ ] 4.16 Behaviour tests, one per DESIGN-0032 Testing Strategy bullet
  under "Remediation workflow": onboarding opens the PR; a no-change
  evaluation does nothing; human edits plus a main merge; main changes
  the same file (mergeable proposal or `conflict`; retired-rule revert;
  human edit kept); human closes (cooldown, `closed_generation`,
  `stale_branch`, fresh branch after a human deletes it); compliant
  close only after a fresh read; merged is terminal with no branch
  delete in the fake's log; expected-head mismatch (concurrent push,
  rewind, delete-and-recreate) defers; update-branch `202`/`422`
  conflict/`422` head-moved; spoofed bot author and a same-named fork
  PR refused; create-after-commit recovery and 422 adoption; every
  failing control at once near the cap with crashes injected between
  reservation, commit, create and record (open count never above the
  cap, reservations reclaimed, `ordinal` order).

#### Success Criteria

- Every behaviour test passes under `-race`.
- No test in the suite records a branch delete or a force push in the
  fake's request log.
- A run killed at any step and re-run completes with exactly one PR
  and one commit per change.

### Phase 5: API remediations: recommend, direct and workflow

#### Tasks

- [ ] 5.1 `recommend` (default): a `kind = 'api'` row in state
  `recommended` with `proposal` JSONB (per resource: key, before,
  after). No GitHub write. Uses the slot only for the one-open index,
  not the PR cap.
- [ ] 5.2 Supersede: a recommendation whose `intent_revision` no longer
  matches becomes `superseded` and a new row runs with the new intent
  (an `apply` toggle with no file change is a new intent, D23).
  `resolved` when the control is compliant at a fresh read.
- [ ] 5.3 `direct` (D14): per resource in `ChangeSet.API`, fence, read,
  write through `Writer` (`UpdateRepository`, `UpsertRuleset` for
  repository-sourced rulesets by id, `SetCustomProperties`,
  `UpsertLabel`, `DeleteLabel`), read back and compare; journal
  `api_write` with `before` and `after`. All done → `applied`; any
  failure → `failed` with the completed steps; the next run resumes
  from the journal and writes only what is not yet journaled `done`.
- [ ] 5.4 `workflow` (D15): only for types implementing
  `WorkflowApply` (`custom_properties`); the change becomes a PR on the
  Phase 4 path carrying `.github/workflows/repo-guardian-<slug>.yml`,
  with property values only through the IMPL-0020 A2 env-indirection
  shape (`yamlq`, `propenv`). A 403 on the workflow path holds
  `permission` with the Workflows permission named in the note.
- [ ] 5.5 Per-write gating: a write whose prerequisite read failed is
  `Blocked` and not attempted, even inside a non-compliant control; a
  property absent from the org schema is `Blocked` while the rest
  syncs.
- [ ] 5.6 Chart and docs: the Remediation App's permission table gains
  Workflows: write only when a policy has a `workflow` control; the
  chart README notes it.
- [ ] 5.7 Tests (DESIGN-0032 "Direct API apply"): fail the second of
  three writes and resume; mutate a target concurrently; toggle
  `apply` from `recommend` to `direct` without a file change
  (supersede, then apply); restore the Remediation App's access; a
  catalog-info parse failure never clears properties; unrelated human
  state survives.

#### Success Criteria

- A partially failed direct apply shows exactly its completed steps
  through the store, and a re-run finishes it without repeating them.
- `recommend` makes zero GitHub writes (fake request log).

### Phase 6: Sweep, lifecycle maintenance and Remediation App discovery

#### Tasks

- [ ] 6.1 `remediation-sweep` schedule (hourly, `repo-guardian-remediate`),
  ensured only by remediator pods: `DueRemediations(REMEDIATION_SWEEP_BATCH)`
  → signal-with-start at priority 4; one `service_runs` row per run.
- [ ] 6.2 Maintenance in the same schedule:
  `MaintenanceCandidates(REMEDIATION_MAINTENANCE_BATCH)` dispatching
  `expire` (`ExpireReservations`, metric), `withdraw` (close the PR
  with a comment as `closed_withdrawn`, or end the recommendation as
  `withdrawn`), `supersede`, and `close_compliant` (a fresh read in the
  same activity; close only when it agrees). A refused write holds
  `permission` and is shown as blocked cleanup.
- [ ] 6.3 `remediation_cleanup_latency_seconds` from the observed
  withdrawal or compliance time to the closing write.
- [ ] 6.4 `controls-discovery-remediate` schedule: lists the
  Remediation App's installations and repositories and refreshes the
  `app = 'remediate'` rows of `app_installations` and
  `app_repository_access` under the row-level policy; a change
  re-resolves the affected repositories (IMPL-0029's resolution
  trigger).
- [ ] 6.5 Role partitioning test: an evaluator pod never ensures
  `remediation-sweep` or `controls-discovery-remediate`; a remediator
  never ensures the evaluation schedules.
- [ ] 6.6 Config: `REMEDIATION_RESERVATION_TTL` (default `1h`),
  `REMEDIATION_SWEEP_BATCH`, `REMEDIATION_MAINTENANCE_BATCH`,
  `REMEDIATOR_CONCURRENCY`, `REMEDIATOR_DB_POOL_SIZE`, with
  validation tests and chart values.
- [ ] 6.7 Tests (DESIGN-0032 "Lifecycle maintenance"): withdraw a
  control, exclude and park its repository, remove its org, switch it
  to evaluate, each with a PR or a recommendation outstanding; every
  artifact reaches its terminal state or blocked cleanup with no fresh
  failing result required.

#### Success Criteria

- With no evaluation running, a withdrawn control's open PR is closed
  as `closed_withdrawn` within one sweep interval.
- A crash between reservation and PR creation frees the slot within
  `REMEDIATION_RESERVATION_TTL` plus one sweep interval.

### Phase 7: Metrics, alerts, API and UI

#### Tasks

- [ ] 7.1 Metrics in `internal/metrics`: `remediation_runs_total{org,control,result}`,
  `remediation_prs_open{org,control,age_bucket}` (leader-published from
  the store, queried with `max by`), `remediation_blocked_total{org,reason}`,
  `remediation_reservations_expired_total{org}`,
  `remediation_cleanup_latency_seconds{org}`,
  `store_pool_wait_seconds{role}`. No repository or PR label.
- [ ] 7.2 Alerts in the generated catalogue (`internal/monitoring/alert`)
  per OQ11, rare-event ones with `alert.firstOrIncrease`; mirror them in
  the chart PrometheusRule; `make monitoring-generate`,
  `make lint-monitoring`, `make lint-alerts`.
- [ ] 7.3 Dashboard panels: remediation backlog, open PRs by age,
  blocked by reason, run results; E4 log matchers for hold and
  adoption log lines, with `TestLogLines_AreStillEmittedByTheBinary`
  covering them.
- [ ] 7.4 API: `/remediations` (filters kind, state, org, control,
  age, hold; open PRs older than N days; recommendations outstanding;
  blocked cleanup); the remediation object (`kind`, `state`,
  `intent_revision`, `observed_head_sha`, `acknowledged_head_sha`,
  `hold`, `proposal`, `steps`) taking the `Remediation` schema name;
  the latest remediation on `/repositories/{id}/controls`; remediation
  events in `/repositories/{id}/events`; remediation backlog on
  `/summary` and `/status`. `apitest` with kin-openapi validation and
  `TestAPIQueries_AreScoped` extended.
- [ ] 7.5 UI: the Remediations view (the Findings view's filters moved
  here per D32), the remediation panel on the repository controls view
  (state, PR link, hold with its human text, e.g. "branch left from a
  closed PR; delete it to resume", direct-apply steps), and the status
  page backlog.

#### Success Criteria

- `make lint-monitoring` and `make lint-alerts` pass with the new
  alerts rendering and promtool-valid.
- Every remediation state and hold renders in the UI against API
  fixtures.

### Phase 8: Cutover code and documentation

#### Tasks

- [ ] 8.1 Confirm `TestPRIdentity_IsFrozen` is gone: IMPL-0029 task 7.2
  retires it with `internal/checker` and `internal/reconciler`. This
  plan only checks that `tools/rgctl` (on `main`) still carries the v1
  literals it needs for cutover steps 3 and 6.
- [ ] 8.2 Parity-test retirement table: list every remaining parity
  scenario and the Phase 4/5/6 test that replaces it; retire only
  scenarios with a named replacement (DESIGN-0032 Migration).
- [ ] 8.3 Rewrite `docs/operations/v2-migration.md` as the fresh-install
  cutover: DESIGN-0032's six steps, rgctl at steps 3 and 6 once per App
  (per OQ6), a fresh database and Temporal namespace, rollback as the
  previous chart against the previous database, and what is not
  restored. Drop every in-place adopt/backfill/verify-shadow claim.
- [ ] 8.4 `v2-overview.md` remediation parts: one PR per control on
  `repo-guardian/<slug>`, `max_open_prs`, `reopen_after`,
  `search_terms`/foreign-PR dropped, `apply` modes, holds, never
  deleting branches.
- [ ] 8.5 `v2-onboarding.md` remediation parts: the Remediation App's
  permissions and events (Workflows only for `workflow` controls), its
  webhook path, the remediator role, `REMEDIATION_*` and
  `REMEDIATOR_*` config, enabling `mode = "remediate"` for one org.
- [ ] 8.6 `docs/operations/rgctl.md`: link the cutover runbook's steps
  3 and 6; note one record per App.
- [ ] 8.7 CLAUDE.md: a remediation contract entry (record-is-narrow
  via grants, observed/acknowledged heads, epoch fence before every
  write, adopt by bot user id, never delete a branch, never
  force-push, nil-vs-terminal recommendation states) and the
  architecture lines for the new packages.
- [ ] 8.8 `docz update impl design` and review the regenerated
  indices.

#### Success Criteria

- `mkdocs build --strict` passes; no operations page describes
  in-place upgrade from v1 or the rc line.
- A reader can run the fresh install from `v2-migration.md` alone.

### Phase 9: Remediation rc and the homelab fresh install

Human-run. The homelab has dev (v2 rc) and prod (v1) managing disjoint
sets of repositories; both stop before the new install, so the split
does not matter.

#### Tasks

- [ ] 9.1 Tag the remediation rc (`dont-release` label, manual
  `v2.0.0-rc.N` tag, chart `2.0.0-rc.N` bumped by hand) per OQ4.
- [ ] 9.2 Create or prepare the Apps per OQ5; mount each key as a file
  from its Secret, never as an env var.
- [ ] 9.3 Stop writers: scale dev's v2 `worker` to zero and prod's v1
  deployment to zero; confirm no old pod runs.
- [ ] 9.4 On dev's Temporal: delete the `discovery` and `snapshot`
  schedules and terminate every running execution on task queue
  `repo-guardian`.
- [ ] 9.5 Record open PRs once per old App: `rgctl prs list --org <org>
  --format json --out <app>-<org>.json` with prod's v1 App credentials
  and again with dev's App credentials, for every org each manages.
- [ ] 9.6 Install the rc fresh: new database (owner plus `rg_evaluator`
  and `rg_remediator` provisioned by the chart), new Temporal
  namespace, both new webhook URLs set on the Apps; every assignment
  starts in `evaluate` mode.
- [ ] 9.7 Verify evaluation: both worker deployments current, discovery
  complete, posture per org against the last v1/rc state, no
  `unmeasurable` surprises, KEDA scaling per queue.
- [ ] 9.8 Remediation in one org (`donaldgifford`): set `mode =
  "remediate"`; watch the first per-control PRs, holds and the cap;
  close that org's old PRs per OQ6.
- [ ] 9.9 Remaining orgs by policy change, per OQ7; close their old PRs
  per OQ6 with `rgctl prs close --from <file> --yes` (add
  `--delete-branch` only by choice; edited PRs are skipped).
- [ ] 9.10 Keep both old databases (and the old charts' values) per
  OQ8; uninstall dev and prod releases once the window passes; then
  uninstall or reduce the old Apps.

#### Success Criteria

- One v2 install manages every repository dev and prod managed; no old
  pod runs.
- Every old open PR is either closed with a pointer comment or left
  deliberately (edited), with the rgctl output kept.
- The first remediation org has had per-control PRs open, updated and
  closed through the full lifecycle with no unexplained hold.

### Phase 10: v2.0.0

#### Tasks

- [ ] 10.1 Chart `version: 2.0.0`, `appVersion: "2.0.0"` (manual), chart
  and root CHANGELOGs regenerated with git-cliff.
- [ ] 10.2 Release per OQ12 with the `dont-release` label and a manual
  `v2.0.0` tag; confirm the push-triggered `release.yml` run creates
  jobs, the images are signed with SLSA provenance on both registries,
  and the chart publishes.
- [ ] 10.3 Upgrade the homelab from the rc to 2.0.0 in place (same
  controls schema chain; no reset needed within the controls line).
- [ ] 10.4 docz statuses: DESIGN-0029 to 0032 → Implemented; IMPL-0028
  to 0030 → Completed; DESIGN-0034 stays Draft; `docz update`.
- [ ] 10.5 Memory and CLAUDE.md: record the ship date and anything the
  fresh install taught.

#### Success Criteria

- `v2.0.0` is published, signed and running in the homelab.
- The docz indices show the four designs Implemented and the three
  plans Completed.

## File Changes

| Path | Change |
| ---- | ------ |
| `internal/store/migrations/00003_controls_results.sql` or a new file (OQ1) | remediation tables, views, grants |
| `internal/store/postgres/remediations.go` (+ tests) | reservation, claim, journal, record, holds, views |
| `internal/store/store.go` | remediation interface methods; `make mocks` |
| `internal/activities/observe.go` | PR observation |
| `internal/activities/start_remediations.go` | `StartDueRemediations` |
| `internal/workflows/remediation.go` (+ tests, testdata) | `RemediationWorkflow` and replay fixture |
| `internal/workflows/evaluation.go` | `StartDueRemediations` wiring |
| `internal/remediation/` (new) | pure run logic: find/adopt/hold, descent, revert selection, lifecycle transitions, so activities stay thin |
| `internal/activities/remediate.go`, `remediate_api.go`, `maintenance.go` | the activities over `internal/remediation` |
| `internal/github/ghfake/` (or IMPL-0028's fake) | stateful fake with merge and commit semantics |
| `internal/metrics/`, `internal/monitoring/` | metrics, alerts, panels; `contrib/generated/` regenerated |
| `internal/api/`, `api/openapi.yaml`, `ui/` | `/remediations`, remediation object, views |
| `internal/config/` | `REMEDIATION_*`, `REMEDIATOR_*` |
| `charts/repo-guardian/` | values, schema, PrometheusRule, README.md.gotmpl, tests |
| `docs/operations/v2-migration.md`, `v2-overview.md`, `v2-onboarding.md`, `rgctl.md` | rewrites (Phase 8) |
| `CLAUDE.md` | remediation contract |

## Testing Plan

- Phase 1: SQL truth table for `remediation_due`, grant tests as both
  application roles, concurrency tests for reservation and claim.
- Phases 2 to 6: the DESIGN-0032 Testing Strategy items under
  observed/acknowledged heads, record-is-narrow, remediation workflow,
  lifecycle maintenance, classification and direct API apply, in the
  Temporal time-skipping environment against the stateful fake.
- Replay of `RemediationWorkflow` histories in CI.
- `apitest` and kin-openapi validation for the API; UI fixture tests.
- `make lint-monitoring`, `make lint-alerts`, helm-unittest for chart
  changes.
- Phase 9's live checks are the homelab acceptance; each result is
  recorded in this document before the GA tag.

## Dependencies

- **IMPL-0028:** the `Writer` package with GraphQL `Commit` and
  `ErrExpectedHeadMismatch`, `CreateRef`, `UpdateBranch` with
  `expected_head_sha`, PR listing with user id and head repository id;
  throttle classification of GraphQL and REST 429; the Remediation App,
  its webhook path, the remediator role, queue and worker deployment;
  `rg_remediator` provisioning and the extended `pgtest`; the
  app-dimensioned budget id; KEDA for the remediate queue; and the
  phase-0 spike results for `createCommitOnBranch`, update-branch and
  signal-with-start into a completing workflow. A spike that
  contradicts DESIGN-0032 changes Phase 4 before it starts.
- **IMPL-0029:** control types' `Remediate` and `ChangeSet`,
  `ValidateChangeSet`, the self-check overlay, `control_results` with
  generations, resolution with `effective_mode` and `ordinal`,
  `EvaluationWorkflow`, the old engine deleted, and the API/UI base the
  remediation views extend.
- **rgctl** (shipped, IMPL-0027) for Phase 9.

## Open Questions

### OQ1: Where do the remediation tables land in the goose chain?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **In `00003_controls_results`, created by IMPL-0029
  before its evaluate-only rc**, as DESIGN-0032 numbers them, so the rc
  schema is already the GA schema and this plan adds behaviour only;
  Task 1.1 then verifies rather than creates.
- (b) A new additive file `00005_remediation` in this plan. Works on an
  rc database in place, but departs from the design's up-front
  numbering.
- (c) Edit `00003` in this plan and discard any evaluate-only rc
  database. Clean chain, but breaks an rc install that already ran
  `00003`.
- other:

### OQ2: Who owns PR observation and the start-due-remediations activity?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **This plan (Phase 2).** Both act on `remediations`
  rows, which do not exist before remediation; IMPL-0029's
  `EvaluationWorkflow` leaves the call site after `Record`.
- (b) IMPL-0029, so `EvaluationWorkflow` is complete when it ships.
- other:

### OQ3: Which control types remediate in v2.0.0?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **All built-in types.** File types through PRs;
  API types `recommend` by default with `direct` and `workflow`
  (custom_properties) available by opt-in, as D14 and D15 decided.
- (b) File types through PRs only; API types `recommend` only, with
  `direct` and `workflow` after GA. Smaller first release, Phase 5
  shrinks to 5.1, 5.2 and 5.7.
- (c) File types only; API types evaluate-only.
- other:

### OQ4: Does remediation ship as an rc before v2.0.0?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **Yes, one or more `v2.0.0-rc.N` tags**
  (`dont-release` plus a manual tag); the homelab fresh install runs on
  the rc, and GA is cut from the same code after Phase 9.
- (b) Cut v2.0.0 directly once Phases 1 to 8 merge and do the fresh
  install on GA.
- other:

### OQ5: Which Apps does the fresh install use?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **Two new Apps** (Evaluation, read-only;
  Remediation, write). New bot identities make the old PRs plainly
  distinct, and both old Apps stay installed until their PRs are closed
  with rgctl, then are uninstalled.
- (b) Reduce prod's v1 App to read-only and make it the Evaluation App
  (D1), and create one new Remediation App. One fewer App, but its
  installations and webhook URL change under live v1 PRs.
- (c) Reuse dev's App as the Remediation App and create a new
  Evaluation App.
- other:

### OQ6: When are the old deployments' PRs closed?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **Per org, when that org turns on `remediate`.**
  Old PRs keep proposing their fixes while the org is evaluate-only,
  and are closed with a pointer comment once per-control PRs replace
  them. rgctl's per-org record files already fit this.
- (b) All at once right after the install is verified (DESIGN-0032 step
  6 as written).
- (c) All at once after every org is on `remediate`.
- other:

### OQ7: What gates turning remediation on for the remaining orgs, and GA?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **`donaldgifford` in `remediate` for 7 days** with
  every hold explained and no PR over the cap, then the remaining orgs;
  GA after 7 more days with no unexplained `conflict`,
  `foreign_branch` or `permission` hold.
- (b) 3 days each.
- (c) No time gate: flip the remaining orgs once the first org's PRs
  look right, and tag GA when they do.
- other:

### OQ8: How long are the old databases kept?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **Until v2.0.0 has run in the homelab for 14
  days**, then a `pg_dump` archive is kept and the databases are
  dropped with the dev and prod uninstall.
- (b) Until the last old PR is closed, then dropped with no archive.
- (c) Indefinitely.
- other:

### OQ9: What does the sticky PR comment look like?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **One sticky comment per PR with marker
  `<!-- repo-guardian:control:v1 -->` on row 1**, edited in place for
  holds (`conflict`), the compliant close and the withdrawn close, with
  a content hash so identical states make no API call (the IMPL-0013
  pattern).
- (b) Reuse v1's `<!-- repo-guardian:reconcile-log:v1 -->` marker.
- (c) A new comment per event.
- other:

### OQ10: Is there a built-in default PR body?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **Yes.** An embedded default title
  (`chore: <control title>`) and body (what it fixes from `.Fixed`, what
  needs a human from `.Manual` and `.Notes`, a link to the
  repository's controls page) in `internal/template`; the enterprise or
  control `pr {}` block overrides it field by field.
- (b) No default: a control without a `pr {}` block fails policy load.
- other:

### OQ11: Which remediation holds alert?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **`foreign_branch`, `permission` and
  `remediation_app_no_access`/`suspended` alert (warning, with
  `firstOrIncrease`); `conflict` alerts only when held over 7 days;
  `pr_cap`, `stale_branch` and `cooldown` never alert** (routine, shown
  in the UI).
- (b) Every blocked reason alerts.
- (c) Only `permission` alerts.
- other:

### OQ12: Where is v2.0.0 tagged?

**Resolved 2026-10-07: (a).**

- (a) ✅ recommended: **Fast-forward `main` to `v2` and tag `v2.0.0` on
  `main`**, with the `dont-release` label on the merge PR and the tag
  pushed by hand, so `main` is the v2 line from GA on.
- (b) Tag on `v2` and merge to `main` later.
- other:

### OQ13: How many PRs?

**Resolved 2026-10-07: (a).** The maintainer's direction: one PR per plan, so the agent does every task it can without waiting on a merge between phases. A task that needs the maintainer (homelab runs, GitHub App registration, approving a large `git rm`, release tags) is marked `deferred - human required` and done by the maintainer, during review or after the merge.

- (a) ✅ recommended: **one PR for the whole plan** into `v2`, with
  `dont-release`; Phases 9 and 10 are `deferred - human required`.
- (b) One PR per phase into `v2`. Smaller reviews, but every phase waits
  on a merge.
- other:

## References

- DESIGN-0032: Evaluation and remediation workflows (Remediation, API
  remediations, Data Model, Failure semantics, Temporal mapping,
  Testing Strategy, Migration / Rollout Plan, D4–D32).
- DESIGN-0031: Control framework (`ChangeSet`, `Writer`,
  `WorkflowApply`, API-remediated types).
- DESIGN-0030: Policy model (`max_open_prs`, `reopen_after`,
  `effective_mode`, epochs).
- DESIGN-0029: Controls and policies.
- INV-0022: Controls designs against the code, GitHub and Temporal.
- IMPL-0028: Controls foundations. IMPL-0029: Controls evaluation.
- IMPL-0027 / DESIGN-0035: rgctl; `docs/operations/rgctl.md`.
- IMPL-0013 (sticky comment pattern), IMPL-0020 A2 (env-indirection),
  IMPL-0022 (deferral contract).
