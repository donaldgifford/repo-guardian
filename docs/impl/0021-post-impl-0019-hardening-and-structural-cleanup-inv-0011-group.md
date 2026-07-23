---
id: IMPL-0021
title: "Post-IMPL-0019 hardening and structural cleanup (INV-0011 Group A Medium + Group B)"
status: Draft
author: Donald Gifford
created: 2026-07-23
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0021: Post-IMPL-0019 hardening and structural cleanup (INV-0011 Group A Medium + Group B)

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-23

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Sequencing](#sequencing)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Custom-properties reconciler correctness (A4, A5)](#phase-1-custom-properties-reconciler-correctness-a4-a5)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Clear-on-file-removal (A3)](#phase-2-clear-on-file-removal-a3)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Policy validation and loader hardening (A6, A8)](#phase-3-policy-validation-and-loader-hardening-a6-a8)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Alert observability (A7)](#phase-4-alert-observability-a7)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Dead-code removal (B3)](#phase-5-dead-code-removal-b3)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Structural refactors (B1, B4)](#phase-6-structural-refactors-b1-b4)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Mock migration (B2)](#phase-7-mock-migration-b2)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Land the six Medium findings (A3–A8) and the four structural-debt items
(B1–B4) from
[INV-0011](../investigation/0011-tech-debt-cleanup-inventory-post-impl-0019.md),
**after** DESIGN-0020/IMPL-0019 has shipped and is proven working in
service. The Group A Mediums are concrete correctness/observability
fixes; Group B is structural cleanup that the standing constraint
("prove the feature before optimizing") deliberately defers to here.

**Implements:** INV-0011 findings A3, A4, A5, A6, A7, A8 and structural
items B1, B2, B3, B4

## Scope

### In Scope

- Group A Mediums: A3 (clear-on-file-removal unreachable), A4 (GHA-mode
  PR staleness), A5 (schema-missing no-op PATCH loop), A6 (property-name
  charset), A7 (unfireable alert window), A8 (typed-null HCL panic in
  `decodeAnnotationProperties`).
- Group B structural: B1 (`engine_policy.go` split), B2 (hand-written
  mocks → mockery), B3 (dead `scheduler/sweep.go`), B4 (standing
  WARNs/fragility).

### Out of Scope

- A1 and A2 (High) — shipped earlier in
  [IMPL-0020](0020-pre-impl-0019-high-severity-fixes-inv-0011-group-a-high.md).
- Any new operator-facing feature.
- A8's *new-code* guard for `decodeWhenBlock` — already folded into
  IMPL-0019 Phase 0. This doc's A8 task is the **existing**
  `decodeAnnotationProperties` instance only.

## Sequencing

- **Hard prerequisite:** IMPL-0019 merged and validated in service
  (Group B especially — the whole reason it was deferred).
- **A3 depends on IMPL-0020's A1 fix** (clear-on-removal is only safe
  once malformed input can no longer masquerade as "clear everything").
- Phases 1–4 (Group A Mediums) are independent of each other and may be
  reordered or parallelized; the ordering below is by blast-radius, not
  dependency.
- Phases 5–7 (Group B) come last and each merges as its own `chore/` PR
  — see Open Question 5 on whether Group B should split into its own
  IMPL entirely.

## Implementation Phases

Run `make fmt` + `make lint` after each task; commit per numbered task
with conventional commits. Packaging into PRs is Open Question 4.

---

### Phase 1: Custom-properties reconciler correctness (A4, A5)

Two independent logic bugs in `internal/reconciler/custom_properties.go`.

#### Tasks

- [ ] 1.1 **A5 (no-op PATCH loop):** filter schema-missing properties
      *before* the drift computation, not only before the PATCH.
      Currently `diffProperties` (`custom_properties.go:164`) compares
      the full managed set while `filterBySchema`
      (`custom_properties.go:313`) trims only the payload — so a
      schema-missing mapped property reports drift forever and re-PATCHes
      the already-correct defined ones each sweep. Move/apply the schema
      filter (or exempt schema-missing names) ahead of `diffProperties`.
- [ ] 1.2 A5 test: with Owner/Component already correct and one mapped
      property absent from the org schema, a second reconcile issues zero
      PATCH calls (stateful mock counts calls across two sweeps).
- [ ] 1.3 **A4 (GHA-mode staleness):** `handleGHAMode`
      (`custom_properties.go:223-226`) returns early when a properties PR
      already exists, so annotation changes never refresh the
      branch/workflow/PR body. Refresh the existing PR's workflow file
      and body when desired state has changed since it was opened
      (reuse the IMPL-0013 refresh pattern; scope per Open Question 2).
- [ ] 1.4 A4 test: open a properties PR, change an annotation, reconcile
      again ⇒ the branch workflow + PR body reflect the new value
      (stateful mock).

#### Success Criteria

- `make lint` and `make test` pass.
- Schema-missing mapped property no longer causes per-sweep PATCH churn
  (A5) — asserted by a zero-call second sweep.
- A GHA-mode properties PR reflects the latest desired state after an
  annotation change (A4).

---

### Phase 2: Clear-on-file-removal (A3)

Make the documented clear-on-removal behavior real. Depends on
IMPL-0020's parse-abort so a malformed file can never trigger a clear.

#### Tasks

- [ ] 2.1 Engine: invoke the `custom_properties` reconciler when the
      rule's configured file is absent, so the reconciler's existing
      (currently production-dead) no-catalog arm runs and clears the
      managed set. Today `runReconcilers`
      (`engine_policy.go:147-149`) skips when `existingPath == ""`.
      Scope carefully against the file-rule double-iteration counter
      contract (CLAUDE.md) and per Open Question 3 (all reconcilers, or
      only clear-capable ones?).
- [ ] 2.2 Confirm the reconciler's `!catalogFound` API-mode branch
      (`custom_properties.go:350`) clears correctly when reached from the
      engine (it is already unit-tested in isolation; this wires the
      engine path that reaches it).
- [ ] 2.3 Multi-sweep test: repo with a synced catalog-info → file
      removed → next reconcile clears the mapped properties (not left
      stale), and a *malformed* file still skips (IMPL-0020 A1 boundary
      re-asserted here).
- [ ] 2.4 Reconcile docs already promise this
      (`policy-reference.md:333-336`,
      `annotation-properties-migration.md:61-68`) — verify wording now
      matches behavior; adjust if the Open Question 3 resolution narrows
      it.

#### Success Criteria

- `make lint` and `make test` pass.
- Removing `catalog-info.yaml` clears the managed properties on the next
  reconcile; the reconciler's no-catalog arm is no longer dead code.
- A malformed file still skips (no clear) — the A1/A3 boundary holds.

---

### Phase 3: Policy validation and loader hardening (A6, A8)

Both in `internal/policy`; independent of each other.

#### Tasks

- [ ] 3.1 **A6 (charset):** correct `githubPropertyNamePattern`
      (`validate.go:144`) to GitHub's actual set —
      `^[a-zA-Z0-9_$#-]{1,75}$` (allow `$` `#`, disallow `.`). Verified
      against GitHub docs in INV-0011. Update the mirrored regex text in
      `policy-reference.md:360-362`.
- [ ] 3.2 A6 tests: `$`/`#` names accepted; a `.` name rejected at load
      with the location-prefixed error.
- [ ] 3.3 A6 migration note: this is load-breaking for any policy already
      using a dotted property name — startup now fails loudly at load
      rather than 422-ing at sync time. Document the upgrade edge.
- [ ] 3.4 **A8 (typed-null panic):** guard
      `decodeAnnotationProperties` (`loader.go:664-695`) with
      `val.IsNull()` / `!val.IsKnown()` before `AsValueMap()`, and the
      per-value `AsString()` likewise, returning a diagnostic instead of
      panicking. (The *new* `decodeWhenBlock` already got this guard in
      IMPL-0019 Phase 0; this fixes the pre-existing instance.)
- [ ] 3.5 A8 test: `annotation_properties = null` and a conditional
      yielding a typed-null map return a clean diagnostic, not a panic.

#### Success Criteria

- `make lint` and `make test` pass.
- Property names with `$`/`#` load; `.` names fail loudly at load.
- A typed-null `annotation_properties` produces a diagnostic; no panic
  path remains in either HCL map decoder.

---

### Phase 4: Alert observability (A7)

Chart-only; the `RepoGuardianPropertySchemaMissing` alert currently
almost never fires.

#### Tasks

- [ ] 4.1 Fix the alert window in
      `charts/repo-guardian/templates/prometheusrule.yaml:128-129`: the
      `rate(...[15m]) > 0` + `for: 30m` pairing resets the pending alert
      before it can fire (an isolated mismatch stays positive ≤ ~15 min).
      Rework to a window that outlives the `for` (e.g.
      `increase(...[1h]) > 0` with a shorter `for`, or align windows) —
      mechanism per Open Question 1.
- [ ] 4.2 Fix the same flaw in the LogQL example
      (`docs/operations/scaling.md:229-238`).
- [ ] 4.3 helm-unittest: the alert renders with the corrected
      expression; existing enable/disable/override cases still pass.
- [ ] 4.4 Chart version bump (`dont-release`, chart-only cadence).

#### Success Criteria

- `make helm-unittest` passes with the corrected expression.
- A single isolated schema-missing event would drive the alert to firing
  under the default freshness (validated by reasoning/promtool, noted in
  the PR).

---

### Phase 5: Dead-code removal (B3)

Zero-risk deletion of unreferenced legacy code. Held until IMPL-0019
proves out per the standing constraint (Open Question 6 revisits whether
it may ride earlier).

#### Tasks

- [ ] 5.1 Delete `internal/scheduler/sweep.go` (`Sweeper`, `NewSweeper`,
      `ReconcileAll`, deprecated `Start`) and its tests — confirmed
      referenced only within its own file/tests; the IMPL-0015 Phase 1
      "repurpose as Discoverer" never consumed it (`Discoverer` shipped
      as its own type).
- [ ] 5.2 Sweep for other post-IMPL-0015/0016/0017 leftovers (unused
      config plumbing for the removed `sweep` schedule, orphaned
      helpers) via `deadcode`/`staticcheck U1000` and a reference scan.
- [ ] 5.3 Confirm `make ci` green after deletion (no dangling
      references, no coverage-threshold regression from removed tests).

#### Success Criteria

- `make ci` passes with the dead code removed.
- `deadcode ./...` reports no new unreachable exported symbols in the
  scheduler package.

---

### Phase 6: Structural refactors (B1, B4)

Mechanical, behavior-preserving. Each is its own commit; PR grouping per
Open Question 4.

#### Tasks

- [ ] 6.1 **B1 (`engine_policy.go` split):** extract into
      `engine_settings.go`, `engine_branch_protection.go`, and
      `engine_pr.go` (the three concerns cohabiting the 1,155-line file
      alongside file-rule evaluation). Pure moves — no behavior change,
      no signature change; verified by an unchanged test suite.
- [ ] 6.2 **B4 — reconcile-branch base-drift** (`engine_policy.go:529`,
      PR #71 WARN): decide and implement a mitigation
      (rebase-before-reconcile, or close/reopen aged PRs, or documented
      "don't auto-merge repo-guardian branches") — Open Question 7.
- [ ] 6.3 **B4 — `refreshPolicyPR` body compare**
      (`engine_policy.go:655-662`): the PR body isn't exposed on the PR
      struct, so title-stable sweeps PATCH unconditionally. Expose/cache
      the body (or hash it) to skip no-op PATCHes.
- [ ] 6.4 **B4 — `hasExistingPRForPolicy` search-terms fragility**
      (`engine_policy.go:438-456`): substring matching collides across
      rules and across add-vs-remove eras (sharper-edged now that
      IMPL-0019 ships removal PRs). Tighten matching (scoped/anchored
      terms, or a structured marker) — coordinate with whatever
      convention IMPL-0019 landed for removal-PR search terms.

#### Success Criteria

- `make ci` passes; the B1 split changes no behavior (test suite
  unchanged and green).
- Each B4 item either fixed or explicitly downgraded to a documented
  WARN with a rationale.

---

### Phase 7: Mock migration (B2)

Migrate the hand-written `github.Client` mocks to mockery. Gated on a
short design pass first because several mocks are stateful.

#### Tasks

- [ ] 7.1 Design pass: catalog the stateful mock behaviors
      (recording, list-then-act fidelity in
      `custom_properties_test.go`, `convergence_test.go`) and decide how
      they map onto mockery (expectation plumbing vs hand-kept wrapper
      types) — this is the "not a pure win" risk flagged in INV-0011 B2.
- [ ] 7.2 Migrate the three canonical stub sites
      (`engine_test.go`, `sweep_test.go` — or its replacement after
      Phase 5, `custom_properties_test.go`) to generated mocks, keeping
      stateful behavior intact.
- [ ] 7.3 `make mocks` wired and documented; CLAUDE.md updated to drop
      the "add stubs to three files in lockstep" convention once the
      generated mocks are authoritative.

#### Success Criteria

- `make ci` passes with generated mocks.
- Adding a `github.Client` method no longer requires a manual three-file
  stub sweep (verified by regenerating).
- Stateful test behaviors (idempotency, list-then-act) still hold.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/reconciler/custom_properties.go` | Modify | A5 filter-before-diff, A4 PR refresh |
| `internal/checker/engine_policy.go` | Modify | A3 reconciler-on-absence; B1 split source; B4 items |
| `internal/checker/engine_settings.go` | Create | B1 extraction |
| `internal/checker/engine_branch_protection.go` | Create | B1 extraction |
| `internal/checker/engine_pr.go` | Create | B1 extraction |
| `internal/policy/validate.go` | Modify | A6 charset |
| `internal/policy/loader.go` | Modify | A8 null/unknown guard |
| `charts/repo-guardian/templates/prometheusrule.yaml` | Modify | A7 alert window |
| `charts/repo-guardian/tests/prometheusrule_test.yaml` | Modify | A7 assertion |
| `internal/scheduler/sweep.go` | Delete | B3 dead code |
| `internal/{checker,scheduler,reconciler}/*_test.go` | Modify | B2 mockery migration |
| `docs/usage/policy-reference.md` | Modify | A6 charset text |
| `docs/operations/scaling.md` | Modify | A7 LogQL example |
| `CLAUDE.md` | Modify | A6 migration, B2 convention drop |

## Testing Plan

- [ ] Phase 1: A5 zero-call second sweep; A4 PR-refresh after annotation
      change.
- [ ] Phase 2: clear-on-removal multi-sweep; malformed-file-still-skips
      boundary.
- [ ] Phase 3: A6 `$`/`#` accept + `.` reject; A8 typed-null diagnostic
      (no panic).
- [ ] Phase 4: helm-unittest alert render; firing-window reasoning noted.
- [ ] Phase 5: `make ci` + `deadcode` clean after removal.
- [ ] Phase 6: unchanged test suite green after B1 split; B4 items
      covered or documented.
- [ ] Phase 7: generated mocks pass full suite incl. stateful behaviors.

## Dependencies

- IMPL-0019 merged and proven in service (hard prerequisite for Group B).
- IMPL-0020 merged (A1 parse-abort) — prerequisite for Phase 2 (A3).
- B4 task 6.4 coordinates with IMPL-0019's removal-PR search-terms
  convention.

## Open Questions

1. **A7 alert-window fix shape?**
   - (a) **Recommendation:** `increase(repo_guardian_custom_property_missing_schema_total[1h]) > 0`
     with `for: 5m` — a window comfortably longer than `for`, so a single
     isolated mismatch still fires; `increase` over `rate` reads more
     naturally for a "did this happen at all recently" alert. Apply the
     same shape to the LogQL example.
   - (b) Keep `rate` but widen to `[1h]` and drop `for` to `0m` — fires
     faster but loses the debounce, noisier on flapping.
   - (c) Leave the alert; document it as "only meaningful on large
     fleets with sustained mismatches." Cheapest, but ships a starter
     alert that misleads small operators into thinking it works.
   - other:

2. **A4 GHA-mode refresh scope?**
   - (a) **Recommendation:** refresh the workflow file + PR body on the
     existing branch when desired state changed (mirror the IMPL-0013
     file-rule refresh); do NOT add auto-close for GHA-mode properties
     PRs in this pass. Closes the staleness bug without importing the
     full convergence machinery.
   - (b) Full parity with file-rule PRs including auto-close when the
     desired set becomes empty — larger, and GHA-mode PRs are
     merge-once-and-delete by design, so auto-close is lower value.
   - other:

3. **A3 — run all reconcilers on file-absence, or only clear-capable
   ones?**
   - (a) **Recommendation:** only invoke reconcilers whose contract
     includes a clear/absence behavior (today: `custom_properties`).
     `label_sync`/`branch_protection`/`workflow_sync` have no meaningful
     "file removed" semantics and running them on absence risks
     surprising side effects. A small reconciler capability flag
     (`RunsOnAbsence() bool`) gates it.
   - (b) Invoke all reconcilers on absence and let each no-op — simpler
     wiring, but couples every reconciler to a new invocation context
     they weren't designed for and muddies the double-iteration counter
     contract.
   - other:

4. **PR packaging for the Group A Mediums (Phases 1–4)?**
   - (a) **Recommendation:** one `fix/impl-0017-hardening` PR for the
     code Mediums (A3, A4, A5, A6, A8) with `patch` + an A6 migration
     note; a separate chart-only PR for A7 (`dont-release`, independent
     cadence). Two PRs, coherent scope ("everything the review found,
     minus the High fixes already shipped").
   - (b) One PR per finding — maximal isolation, 5+ PRs of overhead for
     sub-100-line changes.
   - other:

5. **Should Group B (Phases 5–7) split into its own IMPL doc?**
   - (a) **Recommendation:** yes — spin Group B into `IMPL-00XX
     (structural cleanup)` when Phases 1–4 are done and IMPL-0019 is
     proven, keeping this doc as the Group-A-Medium hardening record.
     Group B is refactor/migration work with different review shape and
     its own sequencing; bundling it here makes this doc span two very
     different kinds of change and risks it staying open for months.
   - (b) Keep everything in this one doc — single tracking artifact for
     "all non-High INV-0011 work," at the cost of a long-lived doc mixing
     fixes and refactors.
   - other:

6. **May the dead-code deletion (Phase 5 / B3) ride earlier than the
   rest of Group B?**
   - (a) **Recommendation:** no — hold all of Group B until IMPL-0019 is
     proven, per the standing constraint. "No structural churn during
     feature work" is a simpler bright line to hold than to litigate
     per-file, and B3 is zero-urgency.
   - (b) Allow B3 in the Phase 1–4 hardening window — it's risk-free
     deletion of unreferenced code and shrinks review noise; the bright
     line costs us carrying dead code a few more weeks.
   - other:

7. **B4 reconcile-branch base-drift (task 6.2) — which mitigation?**
   - (a) **Recommendation:** rebase the reconcile branch onto current
     default-branch HEAD before syncing files each reconcile. Directly
     eliminates both the base-drift and content-drift risks the PR #71
     WARN describes, and keeps PRs mergeable without manual intervention.
   - (b) Close+reopen repo-guardian PRs older than N days — simpler, but
     churns PR numbers and notification noise.
   - (c) Document "don't enable auto-merge on repo-guardian/* branches"
     and leave the code — zero code, but relies on operator discipline
     and doesn't fix content-drift.
   - other:

## References

- [INV-0011](../investigation/0011-tech-debt-cleanup-inventory-post-impl-0019.md) — source findings A3–A8 (Group A) and B1–B4 (Group B), verified on `b3350ef`
- [IMPL-0020](0020-pre-impl-0019-high-severity-fixes-inv-0011-group-a-high.md) — the High fixes shipped before IMPL-0019; A1 is a prerequisite for Phase 2 (A3)
- [IMPL-0019](0019-absent-check-mode-and-conditional-file-rules.md) — hard prerequisite (proven in service) for Group B; its removal-PR search-terms convention coordinates with B4 task 6.4
- IMPL-0013 — the PR-refresh + convergence machinery reused by A4 and referenced by B4
- DESIGN-0012 — sanctioned the mockery migration (B2) as a follow-up
- GitHub docs — [Managing custom properties](https://docs.github.com/en/organizations/managing-organization-settings/managing-custom-properties-for-repositories-in-your-organization) (A6 charset)
- Audited code (as of `b3350ef`): `internal/reconciler/custom_properties.go:{164,223,313,350}`, `internal/checker/engine_policy.go:{147,438,529,655}`, `internal/policy/{validate.go:144,loader.go:664}`, `internal/scheduler/sweep.go`, `charts/repo-guardian/templates/prometheusrule.yaml:128`
