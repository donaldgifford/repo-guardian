---
id: IMPL-0013
title: "Reconcile open PRs when file rules become satisfied"
status: Draft
author: Donald Gifford
created: 2026-05-28
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0013: Reconcile open PRs when file rules become satisfied

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-28

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Diagnostic metrics and alerts](#phase-1-diagnostic-metrics-and-alerts)
  - [Phase 2: GitHub client surface area](#phase-2-github-client-surface-area)
  - [Phase 3: Engine fix — orphan cleanup, body refresh, auto-close](#phase-3-engine-fix--orphan-cleanup-body-refresh-auto-close)
  - [Phase 4: Sticky reconcile-log PR comment](#phase-4-sticky-reconcile-log-pr-comment)
  - [Phase 5: Operator docs, chart values, runbook](#phase-5-operator-docs-chart-values-runbook)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Resolved Decisions](#resolved-decisions)
- [References](#references)
<!--toc:end-->

## Objective

Close the drift gap surfaced in INV-0005: when a file rule becomes satisfied
on `main` *after* repo-guardian has already opened a PR, the open PR is
left stale — the orphan file stays on the reconcile branch, the PR body
keeps advertising the now-satisfied rule, and the PR is never
auto-closed. Operators must currently close these PRs by hand.

This implementation makes the file-rule + PR path **convergent**, matching
the behaviour of setting rules, branch_protection, and the four
reconcilers. It also ships diagnostic metrics first (as a separate PR)
so we can size the drift before flipping any close behaviour.

**Implements:** [INV-0005](../investigation/0005-stale-prs-when-file-rules-become-satisfied-on-main.md)

## Scope

### In Scope

- Diagnostic metrics + Prometheus alerts shippable on their own (Phase 1, PR A).
- New `github.Client` methods needed for the fix: `DeleteFile`,
  `UpdatePullRequest`, `ClosePullRequest`, `UpsertPRComment`,
  `ListPRComments` (Phase 2).
- Engine fix in `internal/checker/engine_policy.go` for the
  policy-engine path: orphan file deletion on the reconcile branch,
  PR body refresh against the current actionable set, auto-close on
  empty actionable set (Phase 3).
- Sticky reconcile-log PR comment with HTML marker discrimination
  (Phase 4).
- Operator-facing docs: chart values for auto-close gating, runbook
  for what changes after upgrade (Phase 5).

### Out of Scope

- Legacy `NewEngine` (non-policy) path. Production runs are
  policy-driven; the legacy path stays one-way for now. A separate
  DESIGN doc will cover removing the legacy path entirely (and
  legacy Kustomize overlays + `docs/legacy/`); divergence documented
  in `CLAUDE.md` until that DESIGN lands. See Resolved Decisions Q7.
- Setting rules, branch_protection rules, and reconciler convergence.
  Already convergent today; INV-0005 confirmed.
- Persisting comment IDs in the Store for faster sticky-comment
  lookup. List-and-find-by-marker is sub-second for the comment
  counts we'll see in practice. See Resolved Decisions Q10.
- E2E httptest scaffold spanning webhook → engine → mock GitHub. Real
  ROI but big enough to deserve its own IMPL doc (IMPL-0014). See
  Resolved Decisions Q6.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Phase 1 ships
as its own PR (PR A) before Phase 2 starts so we have drift data before
the behavioural change lands.

---

### Phase 1: Diagnostic metrics and alerts

Ship the observability *first*, as a standalone PR. This gives us
production data on how many PRs are stuck in the drift state before we
flip any behaviour — and gives operators a way to detect the bug today
even on versions that don't yet have the fix.

#### Tasks

- [x] Add `PROpenWithEmptyActionableTotal` counter in
  `internal/metrics/metrics.go` labelled `{org}`. Incremented inside
  `checkRepoWithPolicy` when an open repo-guardian PR exists *and*
  the actionable set is empty.
- [x] Add `OpenPRsByRule` `GaugeVec` labelled
  `{org, rule, age_bucket}`. Bucket boundaries hard-coded to
  `<1d`, `1-7d`, `7-30d`, `30d+` (Open Question 8). Set during the
  sweep handler (`internal/checker/sweep.go`) by joining
  `Store.StaleRepos` results against open-PR state.
- [x] Reset `OpenPRsByRule` to zero for `{org, rule}` combinations that
  drop to zero open PRs between sweeps — gauge semantics require this
  to avoid phantom non-zero series.
- [x] Wire `PROpenWithEmptyActionableTotal` increment in
  `engine_policy.go.checkRepoWithPolicy` at the existing
  `len(actionable) == 0 && existingPR != nil` branch (currently
  exits silently — see `engine_policy.go:546-558`).
- [x] Add two Prometheus alerts in
  `charts/repo-guardian/templates/prometheusrule.yaml`:
  - `RepoGuardianStaleOpenPRs`: `OpenPRsByRule{age_bucket="30d+"} > 0`
    for 1h. Severity warning.
  - `RepoGuardianPRDrift`: `rate(PROpenWithEmptyActionableTotal[1h]) > 0`
    for 30m. Severity warning.
- [x] Unit test that `PROpenWithEmptyActionableTotal` increments on the
  exact path described above. Use the `mockClient` state-modelling
  pattern from `internal/checker/engine_test.go` (file present on
  default branch + open PR scenario).
- [x] Unit test that `OpenPRsByRule` is reset to zero for
  `{org, rule}` combinations whose count drops between sweeps. Use
  `testutil.ToFloat64` from the IMPL-0009 pattern.
- [x] Update `contrib/README.md` with example PromQL for both metrics
  (mirror the IMPL-0009 migration recipe style).
- [x] Bump chart appVersion + chart version (patch); changelog entry.

#### Success Criteria

- `make ci` passes.
- Both new metrics surface in `/metrics` against a real installation;
  drift counter increments verifiably in a homelab smoke test (open
  a PR, hand-merge the file to main, wait for next sweep, confirm
  counter ticks).
- Both alerts render in `prometheusrule.yaml` and pass
  `helm-unittest` (Open Question 4 → 4a: hard-code bucket
  boundaries means no values surface).
- Operator can identify the count of stuck PRs in their fleet
  *before* upgrading to the version that fixes them.

---

### Phase 2: GitHub client surface area

The engine fix in Phase 3 needs five new methods on the `github.Client`
interface. Add them on their own first so the mock client updates land
in a contained diff — every test file that constructs a `mockClient`
will need a no-op stub.

#### Tasks

- [x] Add to `internal/github/client.go.Client` interface:
  - [x] `DeleteFile(ctx, owner, repo, path, branch, sha, message) error`
    — wraps `client.Repositories.DeleteFile`. Used to remove orphan
    files from the reconcile branch.
  - [x] `UpdatePullRequest(ctx, owner, repo, number, title, body) error`
    — wraps `PullRequests.Edit` with title/body only.
  - [x] `ClosePullRequest(ctx, owner, repo, number) error` — wraps
    `PullRequests.Edit` with `state="closed"`.
  - [x] `ListPRComments(ctx, owner, repo, number) ([]*Comment, error)`
    — wraps `Issues.ListComments` (PR comments are issue comments
    under the hood). Returns a thin `Comment` type with `ID`, `Body`.
  - [x] `UpsertPRComment(ctx, owner, repo, number, markerKey, body) error`
    — list comments, find the one starting with the marker line, if
    found `Issues.EditComment`, else `Issues.CreateComment`. Marker
    format: `<!-- repo-guardian:reconcile-log:v1 -->` on first line
    (see Open Question 2).
- [x] Implement all five against `go-github` and add unit tests using
  the existing `httptest.Server` pattern in
  `internal/github/client_test.go`.
- [x] Add no-op stubs to existing test mocks:
  - [x] `internal/checker/engine_test.go.mockClient`
  - [x] `internal/scheduler/sweep_test.go.mockClient` (note:
    `internal/checker/sweep_test.go` uses `fakeRateLimit` not
    `mockClient` — no stubs needed there)
  - [x] `internal/reconciler/custom_properties_test.go.mockClient`
    (embedded by `bpMockClient` and `labelMockClient`, so this
    one stub covers the reconciler tests).
- [x] Update mockery-generated mocks if any consume these interfaces
  via `make mocks` (Store/Queue/Scheduler don't; only the legacy
  hand-written mocks).
- [x] Document the marker-comment convention in `CLAUDE.md` under
  the architecture-notes section.

#### Success Criteria

- `make ci` passes.
- All five client methods have unit tests covering happy path + at
  least one error branch.
- All existing tests pass with the new method stubs.
- Marker convention documented; future reconcilers that want sticky
  comments use the same `UpsertPRComment` API.

---

### Phase 3: Engine fix — orphan cleanup, body refresh, auto-close

The behavioural change. Three sub-bugs in `checkRepoWithPolicy`
(`internal/checker/engine_policy.go:40-49`,`561-590`,`605-644`) fixed in
one PR because they share the same control-flow gate
(`len(actionable) == 0 && existingPR != nil`).

#### Tasks

- [x] Refactor `checkRepoWithPolicy` to compute three sets per repo:
  - `actionable`: rules currently failing (existing logic).
  - `previouslyClaimed`: rules whose template file is currently
    committed to the reconcile branch (query `GetContents` on the
    branch for each rule's path).
  - `orphaned = previouslyClaimed - actionable`: files we authored on
    the branch but whose rule is now satisfied on `main`.
- [x] When `len(actionable) > 0 && existingPR != nil`:
  - [x] Delete each file in `orphaned` from the reconcile branch via
    `Client.DeleteFile` (one commit per file — see Open Question 4).
  - [x] Render PR body from the current `actionable` set and call
    `Client.UpdatePullRequest` with the new title/body (also covers
    title drift, e.g., bundle PR becomes a single-rule PR).
  - [x] Skip update if rendered title+body match the existing PR
    (avoid no-op churn — fetch existing via `Client.GetPullRequest`,
    which the client already has).
- [x] When `len(actionable) == 0 && existingPR != nil`:
  - [x] If `cfg.AutoClosePR` (default `true` — Open Question 3),
    post a final sticky comment explaining the close
    (`"all file rules now satisfied on the default branch"`), then
    call `Client.ClosePullRequest`.
  - [x] Increment `PRsClosedTotal{org, reason="satisfied"}` counter
    (new — add alongside the existing `PRsCreated/UpdatedTotal`).
  - [x] If `cfg.AutoClosePR == false`, leave the PR open and only
    upsert the reconcile-log sticky comment (Phase 4) noting the
    convergence state.
- [x] Handle `Client.DeleteFile` partial failure: if N of M orphan
  deletes succeed and one fails, log `slog.Warn`, continue with the
  remaining updates, do NOT increment `PRsUpdatedTotal` (treat as
  drift surface for next sweep).
- [x] Add `PROrphanLeftTotal{org}` counter in
  `internal/metrics/metrics.go`. Incremented on every
  `Client.DeleteFile` error inside the partial-failure handler.
  Matches the `*Total{org}` symmetry of other engine counters.
- [x] Treat `Client.GetContents` error on the reconcile branch as
  "rule is still actionable from our perspective" — see Open
  Question 9 — so a transient API error never causes us to
  *delete* a file or close a PR.
- [x] Add `AutoClosePR bool` to `policy.GuardianConfig` with default
  `true`; HCL key `auto_close_pr` at the top-level `guardian {}`
  block. Plumb through `policy.Load`.
- [x] Add `AUTO_CLOSE_PR` env var override matching the existing
  precedence rules (env wins over HCL — see `applyEnvOverrides`
  pattern).
- [x] Multi-sweep unit tests in
  `internal/checker/convergence_test.go`:
  - [x] Sweep 1: 2 rules fail, PR opened with both files.
  - [x] Sweep 2: Rule A satisfied on main; assert PR updated, file
    A removed from branch, body now describes Rule B only.
  - [x] Sweep 3: Rule B satisfied on main; assert PR closed (when
    `AutoClosePR=true`), close-comment posted, counter incremented.
  - [x] Sweep 3 alt: `AutoClosePR=false`; assert PR stays open,
    no orphan files left, reconcile-log comment notes convergence.
  - [x] `GetContents` error sweep: API returns 500 → assert no
    delete, no close, PR untouched.
  - [x] `DeleteFile` error sweep: API returns 500 → assert
    `PROrphanLeftTotal` increments, sweep continues.
- [x] Update `examples/guardian-full.hcl` to document the
  `auto_close_pr` knob with both values commented in.

#### Success Criteria

- `make ci` passes.
- All multi-sweep tests pass; no flakiness across 10 runs locally
  (`go test -count=10`).
- `PROpenWithEmptyActionableTotal` (from Phase 1) reads zero in
  homelab smoke after deploying this phase, confirming the drift
  surface has actually closed.
- Auto-close behaviour gateable via HCL + env var; default is the
  convergent behaviour.
- No regression in `internal/checker/engine_test.go` — the existing
  single-sweep happy path is preserved.

---

### Phase 4: Sticky reconcile-log PR comment

The reconciler/operator-facing "what did this run do?" surface
proposed in INV-0005. One sticky comment per PR; replaced in place
each reconcile so the comment history stays human-readable.

#### Tasks

- [x] Define the comment body via `renderReconcileLog` in
  `internal/checker/drift.go` (inline string builder; the
  templating renderer was deemed overkill for a markdown table —
  the rendered body is < 1KB and has no operator-tunable surface).
  Renders sections for:
  - Last reconcile timestamp.
  - Per-rule status: "satisfied on main" / "still actionable" /
    "orphan removed from branch".
- [x] Marker line on row 1: `<!-- repo-guardian:reconcile-log:v1 -->`.
  The `v1` suffix lets us evolve the template later without
  breaking the upsert path (see Open Question 2).
- [x] Wire `Client.UpsertPRComment` call into `checkRepoWithPolicy`
  on every reconcile that resolves to "we already have a PR" —
  including both the update-and-still-actionable branch and the
  auto-close branch (final comment before close). Also the
  AutoClosePR=false convergent branch upserts the log so operators
  can see why no progress is happening.
- [x] Render the comment via `renderReconcileLog` (inline,
  per the deviation note above) — `internal/template/` was not
  extended because the comment body has no operator-tunable
  surface today.
- [x] Skip the comment if rendered body matches the existing
  marker-tagged comment (no churn for unchanged state).
- [x] Unit tests in `internal/checker/comments_test.go`:
  - [x] Comment created on first reconcile with existing PR.
  - [x] No comment when no existing PR.
  - [x] Convergent state mentions satisfied rules and auto-close
    footer.
  - [x] `buildReconcileLogEvents` distinguishes orphans, actionable,
    and satisfied rules.
- [x] Document the marker line + the `v1` versioning contract in
  `CLAUDE.md` (covered in Phase 2 architecture note).

#### Success Criteria

- `make ci` passes.
- Operator can read a single PR's comment history and reconstruct
  every reconcile decision repo-guardian made against it.
- Identical reconcile state across two sweeps produces no comment
  churn (verified in unit test).
- Sticky-comment marker format is greppable: a future tool could
  delete all stale repo-guardian comments without hitting
  unrelated bot comments.

---

### Phase 5: Operator docs, chart values, runbook

Wrap the behaviour change with operator-facing surface so upgrading
to this chart version is unsurprising.

#### Tasks

- [ ] Add `autoClosePR: true` value under a new `policy:` block in
  `charts/repo-guardian/values.yaml`. Pass through to the
  Deployment as `AUTO_CLOSE_PR` env var.
- [ ] Document `autoClosePR` in `charts/repo-guardian/README.md.gotmpl`
  (NOT `README.md` directly — IMPL-0012 post-mortem). Include both
  the gating knob and a description of what changes after upgrade.
- [ ] New runbook `docs/operations/pr-convergence-migration.md`:
  - What was broken before (link INV-0005).
  - What changes after upgrade (auto-close on default).
  - How to opt out (`autoClosePR: false` or `AUTO_CLOSE_PR=false`).
  - How to read sticky reconcile-log comments.
  - Smoke checks: confirm `PROpenWithEmptyActionableTotal` drops
    to zero, confirm sticky comments appear on existing PRs,
    confirm any 30-day stale PRs get closed.
- [ ] helm-unittest cases for the new env-var plumbing under
  `charts/repo-guardian/tests/deployment_test.yaml` (both
  default and overridden values).
- [ ] Bump chart minor (`0.5.x → 0.6.0`) and appVersion
  (`1.6.x → 1.7.0`) — behavioural change warrants a minor.
- [ ] CHANGELOG entries in both root and chart `cliff.toml`-driven
  changelogs.
- [ ] Update `mkdocs.yml` nav with the new runbook (docz handles
  this automatically — verify).

#### Success Criteria

- `make ci` passes.
- `helm template charts/repo-guardian -f <values>` renders with and
  without `autoClosePR` set; `AUTO_CLOSE_PR` env var present on
  Deployment.
- Runbook is enough for an operator who's never read INV-0005 to
  understand the upgrade impact.
- Chart published to GHCR via the existing IMPL-0010 workflow with
  cosign signature + SLSA provenance.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/metrics/metrics.go` | Modify | Add `PROpenWithEmptyActionableTotal`, `OpenPRsByRule`, `PRsClosedTotal`, `PROrphanLeftTotal`. |
| `internal/github/client.go` | Modify | Add `DeleteFile`, `UpdatePullRequest`, `ClosePullRequest`, `ListPRComments`, `UpsertPRComment`. |
| `internal/github/client_test.go` | Modify | `httptest.Server` cases for the five new methods. |
| `internal/checker/engine_test.go` | Modify | Add no-op stubs to `mockClient` for the five new methods. |
| `internal/checker/sweep_test.go` | Modify | Same no-op stubs on `mockClient`. |
| `internal/reconciler/custom_properties_test.go` | Modify | Same no-op stubs on `mockClient`. |
| `internal/checker/engine_policy.go` | Modify | Refactor `checkRepoWithPolicy` for orphan cleanup, body refresh, auto-close. |
| `internal/checker/engine_policy_test.go` | Modify | Multi-sweep tests for the convergent paths. |
| `internal/checker/comments_test.go` | Create | Sticky-comment behaviour tests. |
| `internal/checker/sweep.go` | Modify | Populate `OpenPRsByRule` gauge during sweep. |
| `internal/policy/config.go` | Modify | Add `AutoClosePR` field to `GuardianConfig`. |
| `internal/policy/loader.go` | Modify | Decode `auto_close_pr` HCL key. |
| `internal/policy/env.go` | Modify | `AUTO_CLOSE_PR` env override. |
| `internal/template/vars.go` | Modify | Add `ReconcileLogVars` struct. |
| `internal/rules/templates/reconcile-log.tmpl` | Create | Sticky-comment body template. |
| `charts/repo-guardian/values.yaml` | Modify | New `policy.autoClosePR` value. |
| `charts/repo-guardian/templates/deployment.yaml` | Modify | Plumb `AUTO_CLOSE_PR` env var. |
| `charts/repo-guardian/templates/prometheusrule.yaml` | Modify | Two new alerts. |
| `charts/repo-guardian/tests/deployment_test.yaml` | Modify | helm-unittest cases for new env. |
| `charts/repo-guardian/README.md.gotmpl` | Modify | Document `autoClosePR` and sticky comments. |
| `examples/guardian-full.hcl` | Modify | Document `auto_close_pr` knob. |
| `docs/operations/pr-convergence-migration.md` | Create | Upgrade runbook. |
| `contrib/README.md` | Modify | PromQL recipes for new metrics. |
| `CLAUDE.md` | Modify | Document marker convention, auto-close knob, gauge-reset semantics. |
| `mkdocs.yml` | Modify | Auto-updated by docz; verify new runbook nav. |

## Testing Plan

- [ ] Phase 1 metric increments verified by unit test against the exact
  control-flow branch in `engine_policy.go`.
- [ ] Phase 2 client methods: unit tests via `httptest.Server` covering
  happy path + 404 + 500 for each.
- [ ] Phase 3 multi-sweep tests run with `go test -count=10` to catch
  any flakiness (state-modelling mock keeps per-test state).
- [ ] Phase 4 sticky-comment idempotency: same render across two sweeps
  produces zero API calls.
- [ ] Phase 5 helm-unittest: `autoClosePR` env-var plumbing renders
  both with explicit `false` and at default `true`.
- [ ] Homelab smoke (operator-side, documented in the runbook): one
  full reconcile cycle observed across `donaldgifford/logpush` and
  `donaldgifford/repo-guardian-test-repo` — confirm sticky comment
  appears, confirm orphan files removed, confirm any pre-existing
  stuck PRs get closed.

## Dependencies

- INV-0005 (resolved/open) is the source of all motivation and
  diagnosis. This IMPL is its execution arm.
- No new external Go dependencies. All five new client methods are
  thin wrappers around existing `go-github` v68 APIs we already
  import.
- No new Helm chart sub-dependencies. The Prometheus alerts use the
  existing `prometheusrule.yaml` template established in IMPL-0011.
- Phase 1 must merge and be deployed to the homelab *before* Phase 3
  lands so we have baseline drift data.
- Phase 2 must merge before Phase 3 (Phase 3 calls the new methods).
- Phases 4 and 5 can sequence in either order after Phase 3.

## Resolved Decisions

Open questions answered during pre-Phase-1 review (2026-05-28).

| # | Question | Decision | Notes |
|---|---|---|---|
| Q1 | State recovery for memory-backend operators | **(a)** Always query branch tree via `Client.GetContents` for each rule's path. | Stateless; identical behaviour across memory and Postgres backends. |
| Q2 | Sticky comment marker | **(a)** Marker line row 1: `<!-- repo-guardian:reconcile-log:v1 -->`. | Versioned suffix lets us evolve the template without breaking upsert. |
| Q3 | Auto-close behaviour gate | **(a)** Default `true`; gate via HCL `auto_close_pr` and env `AUTO_CLOSE_PR`. Sticky close comment posted before close. | Convergent-by-default matches all other rule types. |
| Q4 | Orphan deletion strategy | **(a)** One commit per file via `Client.DeleteFile`. | Verbose but auditable; partial failure handled by the per-file loop. |
| Q5 | `PROrphanLeftTotal` counter placement | **(b)** Add in Phase 3 (overrode recommendation). | Cheap while touching `metrics.go`; matches `*Total{org}` symmetry. |
| Q6 | E2E httptest scaffold (webhook → engine → mock GitHub) | **(a)** Split into IMPL-0014. | Testing-infra investment applies broadly, not just to this fix. |
| Q7 | Legacy `NewEngine` path | **Out of scope here; separate DESIGN doc covers complete legacy removal** (legacy engine path + `deploy/base` Kustomize overlays + `docs/legacy/`). Divergence documented in `CLAUDE.md` until that DESIGN lands. | DESIGN doc ships on its own branch alongside this IMPL. |
| Q8 | `OpenPRsByRule` age-bucket boundaries | **(a)** Hard-code `<1d`, `1-7d`, `7-30d`, `30d+`. | Tunable boundaries are a cardinality and alert-suppression footgun. |
| Q9 | `Client.GetContents` error behaviour | **(a)** Treat as still actionable — no delete, no close. | Fail-safe: transient API error never triggers destructive action. |
| Q10 | Persist sticky-comment ID in Store | **(a)** List-and-find-by-marker on every reconcile. | Sub-second at the comment volumes we'll see; no Store schema growth. |

## References

- [INV-0005](../investigation/0005-stale-prs-when-file-rules-become-satisfied-on-main.md)
  — the investigation that surfaced and diagnosed all three
  sub-bugs covered by this IMPL.
- [INV-0003](../investigation/0003-pre-existing-branch-422-on-subsequent-reconciles.md)
  — adjacent convergence fix (idempotent file commits). Same
  philosophy: reconcile loops should be re-entrant.
- [DESIGN-0006](../design/0006-hcl-policy-configuration-and-rule-engine.md)
  — the policy engine model these changes operate within.
- [IMPL-0009](0009-per-org-rule-scoping-and-observability.md) —
  prior art for the `{org}` label pattern on Prometheus metrics
  and the `contrib/README.md` migration-recipe convention.
- [IMPL-0011](0011-persistent-reconcile-state-and-multi-replica-coordination.md)
  — established the `StaleSweeper` + per-installation rate-limit
  reserve pattern that Phase 1's gauge integrates with.
- [IMPL-0012](0012-customizable-pr-templates-and-extensible-template-configmap.md)
  — established the `PRTemplate` resolution pattern that the body-
  refresh path reuses.
- `internal/checker/engine_policy.go:40-49` — the dispatch site.
- `internal/checker/engine_policy.go:546-558` — the existing WARN
  comment that already names this drift.
- `internal/checker/engine_policy.go:561-590` — the add/update-only
  loop that Phase 3 rewrites.
- `internal/checker/engine_policy.go:605-644` — `createNewPolicyPR`
  body-render path; Phase 3 reuses this for the update path.
