---
id: IMPL-0019
title: "Absent check mode and conditional file rules"
status: Draft
author: Donald Gifford
created: 2026-07-23
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0019: Absent check mode and conditional file rules

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-23

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Design Decisions Carried In](#design-decisions-carried-in)
- [Implementation Phases](#implementation-phases)
  - [Phase 0: Policy schema, loader, validation](#phase-0-policy-schema-loader-validation)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 1: Engine evaluation — when-gate and absent mode](#phase-1-engine-evaluation--when-gate-and-absent-mode)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 2: Remediation — deletion commits, PR wording, convergence](#phase-2-remediation--deletion-commits-pr-wording-convergence)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 3: Push-event fast convergence](#phase-3-push-event-fast-convergence)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 4: Documentation, examples, release](#phase-4-documentation-examples-release)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Resolved Decisions (Implementation)](#resolved-decisions-implementation)
- [References](#references)
<!--toc:end-->

## Objective

Implement [DESIGN-0020](../design/0020-absent-check-mode-and-conditional-file-rules.md)
(Approved 2026-07-23): a fourth file-rule check mode `check = "absent"`
whose remediation is a file-deletion PR on the existing reconcile branch,
plus a `when { rule_satisfied = "<rule>" }` gating block that makes any
file rule conditional on a sibling rule being satisfied on the default
branch. Driving use case: renovate-first policies — never add Dependabot,
and remove `dependabot.yml` from any repo whose `renovate_config` rule
passes.

**Implements:** DESIGN-0020

## Scope

### In Scope

- `internal/policy`: `CheckAbsent`, `WhenConfig`, HCL schema/decode,
  the full validation matrix including gate-graph cycle detection.
- `internal/checker`: gate evaluation (fail-closed, memoized per
  repo-check), absent-mode evaluation, deletion remediation, inverse-orphan
  restoration, reconcile-log wording, PR body Added/Removed split.
- `internal/template`: additive `Rule.Action` field.
- `internal/metrics`: `files_forbidden_present_total`,
  `rule_gate_closed_total`.
- `internal/policy/watch.go` + `internal/webhook`: watched-set extension
  for gate-referenced paths (DESIGN-0020 Decision 4).
- Operator docs, `examples/guardian-full.hcl`, migration notes.

### Out of Scope

- `when {}` on setting / branch-protection rules (DESIGN-0020 non-goal).
- Same-PR atomicity between the gating rule's addition and the gated
  rule's deletion — the two-PR eventual-consistency flow is accepted.
- Direct-to-default-branch deletes; everything flows through the
  reconcile-branch PR.
- Chart changes (no new values, no new alerts; deletion PRs surface
  through existing PR metrics).

## Design Decisions Carried In

All seven DESIGN-0020 decisions are settled (option a, 2026-07-23) and
are **not** re-opened here: `rule_satisfied`-only gate; content-only
referee evaluation (referee's scope/ignore never affect the gate); new
`files_forbidden_present_total` metric; auto-watch (Phase 3 is
unconditional); delete every matching path in one PR; restore stale
deletions (inverse orphan); `when {}` legal on any check mode. Gate
errors fail closed. The
[Resolved Decisions (Implementation)](#resolved-decisions-implementation)
below are implementation-level only.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Run `make fmt`
and `make lint` after each task; commit per numbered task with
conventional commits.

---

### Phase 0: Policy schema, loader, validation

Everything the engine needs to *see* the new config, failing loudly on
every malformed variant before any engine code exists.

#### Tasks

- [x] 0.1 Add `CheckAbsent CheckMode = "absent"` to
      `internal/policy/types.go` (const block + `CheckMode()` switch) and
      the `validChecks` allowlist in `validate.go.validateFileRule`.
- [x] 0.2 Add `WhenConfig struct { RuleSatisfied string }` and
      `When *WhenConfig` field on `FileRuleConfig` (`types.go`), with doc
      comments stating default-branch-only + content-only + fail-closed
      semantics.
- [x] 0.3 `loader.go`: drop `Required: true` from `target` and `template`
      in `ruleBodySchema` (loader.go:484-491); add `{Type: "when"}` to its
      block list; implement `decodeWhenBlock` (own `hcl.BodySchema` with
      the single `rule_satisfied` attribute so unknown attrs fail load,
      per the INV-0010 guardian-block precedent); wire into
      `decodeRuleSubBlocks`. **Null/unknown guard (INV-0011 A8):** the
      `rule_satisfied` cty value must be checked with `IsNull()` /
      `IsKnown()` and its type asserted to `cty.String` BEFORE calling
      `.AsString()` — a typed-null or conditional-unknown expression
      (`rule_satisfied = null`, or a `? :` yielding null) otherwise
      panics at load instead of returning a diagnostic. This is the
      exact class of bug INV-0011 found in `decodeAnnotationProperties`;
      the new decoder must not repeat it.
- [x] 0.4 `validate.go`: move `target`/`template` requiredness out of the
      HCL schema into `validateFileRule`, conditioned on check mode —
      absent **forbids** `target`, `template`, `assertion` blocks, and
      `reconcile` blocks; all other modes **require** `target` +
      `template` exactly as today.
- [x] 0.5 `validate.go`: new `validateWhenGates(rules []FileRuleConfig)`
      — referenced rule must exist among file rules (error lists known
      rule names), must not be a setting/branch-protection name, must be
      enabled, no self-reference, no cycles of any length (DFS over the
      gate graph), empty `when {}` block is an error.
- [x] 0.6 Loader tests: the DESIGN-0020 HCL-surface example loads clean;
      one test per validation-matrix row asserting the exact
      location-prefixed error; cycle tests at length 2 and 3; unknown
      attribute inside `when {}` fails load; `rule_satisfied = null` and
      a conditional yielding a typed-null return a clean diagnostic
      rather than panicking (INV-0011 A8 regression guard).
- [x] 0.7 `policy.Version` test: adding a `when` block, or flipping a
      rule's check mode to `absent`, changes the hash (`version.go`
      discrimination contract).

#### Success Criteria

- `make lint` and `make test` pass.
- A `guardian.hcl` containing the DESIGN-0020 example parses without
  error; every validation-matrix row has a failing-fixture test.
- `policy.BuiltinDefaults()` is byte-identical (no absent rules ship as
  defaults; defaults tests untouched).
- No engine behavior change yet — an absent rule loads but is inert
  until Phase 1 (verified by an engine test asserting zero actions).

---

### Phase 1: Engine evaluation — when-gate and absent mode

The read-only half of the engine work: which rules fire, which are
gated, and the metrics that make both observable. No mutation paths yet.

#### Tasks

- [x] 1.1 New `internal/checker/gate.go` (Decision 1 — minimal breakup,
      gate helpers only, no wider engine_policy.go refactor):
      `ruleSatisfiedOnDefault(ctx, client, owner, repo, rule)
      (bool, error)` — `evaluateRule` semantics **minus** the
      `hasExistingPRForPolicy` short-circuit, inverted per check mode
      (exists / contains / exact / absent as specified in DESIGN-0020
      §Evaluation flow).
- [x] 1.2 `gate.go`: per-repo-check memoization — a `gateEvaluator`
      struct holding `map[string]gateResult`, constructed inside
      `checkRepoWithPolicy` and threaded to both iteration passes (never
      stored on `Engine`: the engine is shared across worker goroutines).
- [x] 1.3 Wire the gate into `findActionableRules` after the scope and
      ignore gates: closed gate ⇒ Info log (rule, referee, referee
      state) + `RuleGateClosedTotal` increment, rule skipped.
- [x] 1.4 Wire the same gate into `runReconcilers` **silently** (no
      counter — the file-rule double-iteration contract; a comment must
      say why).
- [x] 1.5 Fail-closed error handling: referee evaluation error ⇒ gate
      closed, Warn log, counter with `reason="error"` (Decision 3);
      test with an erroring mock asserting the rule is skipped and no
      remediation is planned.
- [x] 1.6 `evaluateAbsent` arm in `evaluateRule`: actionable iff **any**
      path in `rule.Paths` exists on the default branch (existence-only;
      no content fetch). No path plumbing to remediation (Decision 2).
- [x] 1.7 `internal/metrics/metrics.go`: `FilesForbiddenPresentTotal
      {rule_name, org}` and `RuleGateClosedTotal{rule_name, org, reason}`
      CounterVecs (reusing `labelRuleName`/`labelOrg`/`labelReason`
      consts). Absent-actionable increments the new counter and must NOT
      increment `FilesMissingTotal` (asserted).
- [x] 1.8 Engine tests (`engine_test.go` mock pattern): all five
      DESIGN-0020 semantics-matrix rows; gate-true-despite-open-referee-PR
      (the short-circuit distinction); memoization (mock call counting:
      at most one referee content evaluation per repo-check); metric
      assertions with unique label values.

#### Success Criteria

- `make lint` and `make test` pass.
- Semantics matrix fully covered by table-driven tests.
- Gate evaluation cost: ≤1 referee evaluation per referenced rule per
  repo-check, asserted via mock counters.
- Dry-run only: with `dry_run = true`, an actionable absent rule logs
  its planned deletions and mutates nothing (bridge assertion ahead of
  Phase 2).

---

### Phase 2: Remediation — deletion commits, PR wording, convergence

The mutation half: deletion commits on the reconcile branch, PR text
that describes removals, restoration of stale deletions, and the
multi-sweep convergence suite that proves the lifecycle.

#### Tasks

- [ ] 2.1 `syncActionableFiles` action split (`engine_policy.go:582`):
      absent rules walk **all** `rule.Paths`, `GetContentsOnBranch` each
      on the reconcile branch — exists ⇒ `DeleteFile(branch, path, sha,
      "chore: remove <path> (forbidden by rule \"<name>\")")`; already
      absent ⇒ idempotent skip (mirror of the INV-0003 three-branch
      contract). Add/fix modes unchanged.
- [ ] 2.2 Dry-run detail: the `checkRepoWithPolicy` dry-run arm
      enumerates planned deletions per absent rule (path list in the log
      record) — deletion is the engine's first destructive remediation;
      "would create PR" alone is not reviewable.
- [ ] 2.3 `internal/template/contexts.go`: `Rule.Action string` field
      (`"add"` | `"remove"`), populated in `buildPRVars` from
      `CheckMode()`. Additive — `ValidateZero` strict-mode templates keep
      passing (asserted).
- [ ] 2.4 `buildPRBodyFromPolicy`: split into "Added files" / "Removed
      files" sections; CODEOWNERS-placeholder note renders only when an
      add-mode rule is present.
- [ ] 2.5 Inverse-orphan restoration (`drift.go`): absent arm in
      `discoverOrphans` — for each no-longer-actionable absent rule,
      where default branch has a path and the reconcile branch does not,
      restore via `GetFileContent`(default) + `CreateOrUpdateFile`(branch,
      `"chore: restore <path> (rule \"<name>\" no longer applies)"`).
      Fail-safe: any API error ⇒ leave it, Warn,
      `PROrphanLeftTotal{org}` increment, retry next sweep.
- [ ] 2.6 Reconcile-log wording (`drift.go.buildReconcileLogEvents`):
      absent-aware statuses (`present on main, pending removal` /
      `absent from main`) and gate-closed status (`skipped (when-gate
      closed: <referee> not satisfied)`) — requires threading gate
      outcomes into the event builder. Note in the task commit: status
      strings feed the content hash, so every open PR gets exactly one
      extra comment edit after upgrade.
- [ ] 2.7 Convergence suite (`convergence_test.go`, extending
      `stagedConvergenceState`): renovate-first lifecycle sweep 1
      (add-renovate PR) → merge → sweep 2 (remove-dependabot PR) → merge
      → sweep 3 (converged, no action); hand-deleted dependabot →
      auto-close; gate closes mid-flight in a bundle PR → restoration;
      both `.yml` + `.yaml` present → both deleted in one PR; identical
      re-sweep ⇒ zero mutating API calls.
- [ ] 2.8 Mock-fidelity check per the list-then-act rule: the mock's
      `GetContentsOnBranch` must reflect prior `DeleteFile` /
      `CreateOrUpdateFile` calls on the same mock instance, and the
      idempotency assertions must count mutating calls — an always-static
      mock passes those tests vacuously.

#### Success Criteria

- `make ci` passes.
- The convergence suite demonstrates the full renovate-first lifecycle
  end-to-end against stateful mocks, including auto-close and
  restoration edges.
- A bundle PR mixing one add-rule and one absent-rule renders both body
  sections and produces both commit kinds on the branch.
- Re-running an identical sweep produces zero mutating API calls,
  asserted via mock counters (not just "no error").

---

### Phase 3: Push-event fast convergence

Auto-watch per DESIGN-0020 Decision 4: merging the referee's add-PR
triggers the gated rule's re-check within the webhook path instead of
the next sweep.

#### Tasks

- [ ] 3.1 `internal/policy/watch.go.ExtractWatchedPaths`: for any rule
      carrying a `when` gate, both the referee's paths AND the gated
      rule's own paths join the watched set (Decision 4), unioned with
      the existing reconciler-`watch = true` sources.
- [ ] 3.2 Extend `hasWatchedFileChanges` to scan `commit.Removed` for
      watched paths (Decision 5; today removals are intentionally
      ignored — `internal/webhook/handler.go:314` — which predates
      removals having policy meaning: a removed `renovate.json` flips a
      gate). Update the function's doc comment, which currently
      documents the removed-files exclusion.
- [ ] 3.3 Webhook handler tests: (i) push adding `renovate.json`
      enqueues a re-check for a policy where only the *gated* rule
      references it; (ii) push re-adding `dependabot.yml` (a gated
      rule's own path) enqueues a re-check; (iii) push *removing*
      `renovate.json` enqueues a re-check; (iv) pushes touching
      unwatched paths still enqueue nothing.
- [ ] 3.4 Doc comment on `ExtractWatchedPaths` updated to name all three
      sources (reconciler watch, gate reference, gated rule's own paths).

#### Success Criteria

- `make lint` and `make test` pass.
- In the handler test, the dependabot-removal re-check enqueues on the
  first post-merge push event, not on the next scheduled sweep.
- No webhook behavior change for policies with no `when` blocks
  (existing handler tests untouched and green).

---

### Phase 4: Documentation, examples, release

#### Tasks

- [ ] 4.1 `docs/usage/policy-reference.md`: `absent` check-mode section,
      `when {}` block reference, the validation-matrix table, gate
      fail-closed semantics, and the search-terms collision guidance
      (an absent rule's `search_terms` must not match the add-era PR
      titles for the same file — the example uses `"remove dependabot"`).
- [ ] 4.2 `examples/guardian-full.hcl`: add the `no_dependabot` rule
      (check = "absent" + when gate on `renovate_config`); replace the
      name-glob `ignore { repos = ["myorg/renovate-*"] }` workaround on
      the dependabot rule, keeping the old form as a commented
      alternative; `go test ./examples/...` green.
- [ ] 4.3 `docs/operations/absent-rules-migration.md` (per the
      `*-migration.md` precedent): dry-run-first recipe leading the doc
      (destructive remediation), policy-hash bump ⇒ one-time full
      re-sweep, reconcile-log hash bump ⇒ one comment edit per open PR,
      downgrade behavior (older binary fails loudly at load on the
      unknown check mode), search-terms guidance.
- [ ] 4.4 CLAUDE.md architecture notes: absent mode, when-gate
      fail-closed + content-only semantics, inverse-orphan restoration,
      gate counter only-in-primary-pass reminder, memoization
      per-repo-check-never-on-Engine.
- [ ] 4.5 `docz update impl` / `docz update design` (one type per
      invocation); flip DESIGN-0020 to Implemented and this IMPL to
      Completed (frontmatter + body `**Status:**` line, both); mkdocs
      strict-mode warning count unchanged from the 14-file baseline.
- [ ] 4.6 PR/release mechanics (Decision 6): semver label `minor` (new HCL
      surface, additive binary feature), appVersion bump alongside,
      verifying appVersion against the real tag line per the
      IMPL-0017 post-mortem (Chart.yaml appVersion must equal the tag
      the semver label will actually cut from the latest real release).

#### Success Criteria

- `make ci` passes.
- Policy-reference renders the semantics matrix and validation table;
  the example file loads in `go test ./examples/...`.
- Migration doc exists and leads with the dry-run recipe.
- Docs indices regenerated; statuses flipped; mkdocs baseline unchanged.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/policy/types.go` | Modify | `CheckAbsent`, `WhenConfig`, `When` field |
| `internal/policy/loader.go` | Modify | schema relax, `when` block, `decodeWhenBlock` |
| `internal/policy/validate.go` | Modify | validation matrix, `validateWhenGates` (cycle DFS) |
| `internal/policy/watch.go` | Modify | gate-referenced paths join the watched set |
| `internal/checker/gate.go` | Create | `ruleSatisfiedOnDefault`, `gateEvaluator` memo (Decision 1) |
| `internal/checker/engine_policy.go` | Modify | gate wiring both passes, `evaluateAbsent`, deletion arm in `syncActionableFiles`, body sections, dry-run detail |
| `internal/checker/drift.go` | Modify | inverse-orphan restoration, reconcile-log wording |
| `internal/checker/pr.go` | Modify | `buildPRVars` Action population |
| `internal/template/contexts.go` | Modify | `Rule.Action` (additive) |
| `internal/metrics/metrics.go` | Modify | two new CounterVecs |
| `internal/webhook/handler.go` | Modify | removed-file handling (Decision 5) |
| `internal/checker/convergence_test.go` | Modify | renovate-first lifecycle scenarios |
| `examples/guardian-full.hcl` | Modify | `no_dependabot` worked example |
| `docs/usage/policy-reference.md` | Modify | operator reference |
| `docs/operations/absent-rules-migration.md` | Create | upgrade/migration guide |
| `CLAUDE.md` | Modify | architecture notes |

No `github.Client` interface changes ⇒ no mockClient-parity sweep across
the three canonical stub files.

## Testing Plan

- [ ] Phase 0: validation-matrix fixture tests (one per row, exact
      errors); cycle detection length 2/3; `policy.Version`
      discrimination.
- [ ] Phase 1: semantics-matrix table-driven engine tests; gate
      memoization via mock call counting; fail-closed error test;
      metric assertions with unique labels (parallel-safe).
- [ ] Phase 2: multi-sweep convergence suite (lifecycle, auto-close,
      restoration, multi-path, idempotent re-sweep with zero mutating
      calls); stateful-mock fidelity for `GetContentsOnBranch` after
      writes.
- [ ] Phase 3: webhook handler watched-set tests — gate-referee add,
      own-path re-add, referee removal, unwatched no-op (Decisions 4/5).
- [ ] `go test ./examples/...` covering the updated `guardian-full.hcl`.
- [ ] Homelab smoke (operator-side, checkbox stays open until run):
      dry-run upgrade shows planned deletions only; enable on the test
      org ⇒ removal PR opens against a repo with both renovate +
      dependabot; merge renovate-add PR on a dependabot-only repo ⇒
      removal PR arrives via push path (Phase 3); hand-delete
      dependabot on main ⇒ open removal PR auto-closes.

## Dependencies

- DESIGN-0020 Approved (done, 2026-07-23; PR #165).
- No store/queue/scheduler surface; no chart changes; builds on
  `github.Client` methods that already exist (`DeleteFile`,
  `GetContentsOnBranch`, `GetFileContent`, `CreateOrUpdateFile`).
- Post-v1.9.0 main (IMPL-0017 merged) is the base.

## Resolved Decisions (Implementation)

All six open questions were resolved on 2026-07-23 with the recommended
option (a).

1. **Code placement — new `internal/checker/gate.go`, minimal breakup
   only.** `ruleSatisfiedOnDefault` + `gateEvaluator` live in the new
   file (mirrors the `scope.go` precedent; `engine_policy.go` is already
   1,155 lines); `evaluateAbsent` stays in `engine_policy.go` beside its
   three sibling evaluate arms. **Explicit constraint from review:** do
   NOT expand this into a wider engine_policy.go restructuring — the
   feature ships against the file as it stands, and broader structural
   cleanup is deferred to a separate tech-debt investigation after the
   feature is proven working in service.

2. **No path plumbing to remediation.** `syncActionableFiles` re-probes
   each `rule.Paths` entry on the reconcile branch via
   `GetContentsOnBranch` — needed anyway for the `DeleteFile` blob SHA.
   `findActionableRules` keeps returning `[]policy.FileRuleConfig`; all
   consumer call sites untouched.

3. **`rule_gate_closed_total{rule_name, org, reason}`** with reason ∈
   `not_satisfied` | `error`. Bounded cardinality; `reason="error"` is
   the alertable signal for "API trouble is silently suppressing rules".

4. **Watched-set symmetry** — a gated rule contributes both the
   referee's paths and its own paths to the watched set. A re-added
   `dependabot.yml` triggers the removal PR on the push path.

5. **Honor `commit.Removed` for watched paths** — removals now carry
   policy meaning (a removed `renovate.json` flips a gate; a removed
   `dependabot.yml` is a convergence event). `hasWatchedFileChanges`
   gains one loop over `commit.Removed`; its doc comment loses the
   removed-files exclusion.

6. **Single PR, Phases 0–4, commit-per-task, `minor` label** — the
   IMPL-0017 flow. One release, one migration note, one appVersion bump
   (verified against the real tag line per task 4.6).

## References

- [DESIGN-0020 — Absent check mode and conditional file rules](../design/0020-absent-check-mode-and-conditional-file-rules.md) (Approved; all decisions resolved 2026-07-23)
- IMPL-0013 — orphan cleanup / auto-close / sticky reconcile-log machinery this feature extends; the Q9 fail-safe stance generalized to gates and restoration
- INV-0003 — `CreateOrUpdateFile` three-branch idempotency mirrored by the deletion arm
- IMPL-0017 — single-PR, commit-per-task flow precedent; appVersion-vs-real-tag release gotcha (task 4.6)
- [INV-0011](../investigation/0011-tech-debt-cleanup-inventory-post-impl-0019.md) — A8 (typed-null HCL panic in `decodeAnnotationProperties`) is the same decode-guard class task 0.3 must not repeat in `decodeWhenBlock`
- Audited code paths (as of v1.9.0): `internal/policy/{types,loader,validate,version,watch}.go`, `internal/checker/{engine_policy,drift,pr}.go`, `internal/webhook/handler.go:213-330`, `internal/metrics/metrics.go`
