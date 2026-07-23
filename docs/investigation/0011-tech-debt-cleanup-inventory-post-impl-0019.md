---
id: INV-0011
title: "Tech debt cleanup inventory post IMPL-0019"
status: Open
author: Donald Gifford
created: 2026-07-23
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0011: Tech debt cleanup inventory post IMPL-0019

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-07-23

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Group A — Verified defects in the shipped IMPL-0017 surface](#group-a--verified-defects-in-the-shipped-impl-0017-surface)
  - [Group B — Structural debt (deferred by explicit constraint)](#group-b--structural-debt-deferred-by-explicit-constraint)
  - [Review-verification notes](#review-verification-notes)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Question

What defects and structural debt exist in the current codebase
(post-IMPL-0017, appVersion 1.9.0), and how should fixes be sequenced
given the standing constraint that the DESIGN-0020 / IMPL-0019 feature
must be built and proven working before structural optimization begins?

## Hypothesis

The operator-provided review findings (8 items, reviewed at PR-#164 head
`79fd16a`) are real and reproducible on current `main`; the four
structural areas scoped via review discussion (engine_policy.go size,
hand-written mocks, dead/legacy code, known WARNs) are documentable now
and safely deferrable; the High-severity items are not deferrable.

## Context

IMPL-0019 Decision 1 explicitly deferred any `engine_policy.go`
restructuring to "a separate tech-debt investigation after the feature
is proven working in service" — this is that investigation. Separately,
an operator code review of the IMPL-0017 surface produced 8 findings
(2 High, 6 Medium) that needed verification against current `main` and
a triage decision: which are bugs to fix now vs debt to schedule.

**Triggered by:** IMPL-0019 (Resolved Decision 1), DESIGN-0019/IMPL-0017
post-ship review

## Approach

1. Re-verify each of the 8 operator review findings against current
   `main` (`04683f8`, post-#164) at the cited file:line locations.
2. Cross-check finding 6's charset claim against GitHub's documentation
   for custom-property naming constraints.
3. Audit the four structural areas: line counts and lint-ceiling
   pressure for `engine_policy.go`; the mockClient parity-sweep cost;
   reference-scan for dead legacy code; inventory of standing WARN
   comments.
4. Triage into: immediate fixes (High), scheduled hardening (Medium),
   deferred structural work (post-IMPL-0019 per constraint).

## Environment

| Component | Version / Value |
|-----------|----------------|
| main | `04683f8` (feat!: IMPL-0017, PR #164) |
| appVersion / chart | 1.9.0 / 1.0.0-rc.6 |
| Review baseline | PR #164 head `79fd16a` (operator review) |

## Findings

### Group A — Verified defects in the shipped IMPL-0017 surface

All 8 operator findings **confirmed on current main**. Numbering
preserved from the review.

**A1 (High): Invalid catalog YAML destructively clears mapped
properties.** `internal/catalog/catalog.go:57` — `Parse` returns
`defaults()` (Owner/Component = "Unclassified", no Extra map) for BOTH
unparseable YAML and non-Component entities, indistinguishable from a
valid file with no annotations. Under API-mode full state sync
(`custom_properties.go.desiredToPropertyValues`), a temporarily
malformed `catalog-info.yaml` immediately PATCHes `null` for every
mapped property and overwrites Owner/Component with "Unclassified".
Parse failure should abort reconciliation (skip + Warn + metric), never
be presented as desired state.

**A2 (High, security): Generated workflow interpolates annotation
values into shell without escaping.**
`internal/rules/templates/set-custom-properties.tmpl:31` — `.Catalog.Owner`,
each `annotation_properties` value, and `.Catalog.Component` render
inside single-quoted `gh api -f` arguments in the generated GitHub
Actions workflow. These values come from repo-controlled
`catalog-info.yaml`: an apostrophe breaks the workflow; a value like
`x'$(command)'` achieves command execution with the workflow's
`GITHUB_TOKEN` once the PR merges. Same remediation class as the
release-pipeline post-mortem's bake-metadata fix: pass values through
`env:` (never shell-parsed) rather than inline interpolation, and/or
reject hostile characters at parse time.

**A3 (Medium): Documented clear-on-file-removal is unreachable code.**
`internal/checker/engine_policy.go:147-149` skips all reconcilers when
no configured path exists (`existingPath == ""`), and
`getFileContentForReconciler` returning `""` also skips. Verification
found this is *stronger* than the review stated: the reconciler already
ships the no-catalog arm (`resolveCatalogContent` re-reads the repo when
`params.Content` is empty; `handleAPIMode`'s `!catalogFound` branch
exists and is tested) — but the engine never invokes reconcilers without
file content, so the arm is production-dead. Contradicts
`docs/usage/policy-reference.md:333-336` and
`docs/operations/annotation-properties-migration.md:61-68`, which
promise clears when the whole file is removed.

**A4 (Medium): GHA-mode PRs freeze stale property values.**
`internal/reconciler/custom_properties.go:223-226` — `handleGHAMode`
returns as soon as `findPropertiesPR` finds an existing PR. Annotation
changes after PR creation never refresh the branch/workflow/PR body;
merging applies the obsolete values. (Contrast: the file-rule PR path
gained exactly this refresh machinery in IMPL-0013.)

**A5 (Medium): Schema-missing properties cause a permanent no-op PATCH
loop.** Drift is computed at `custom_properties.go:164`
(`diffProperties`, full managed set, *before* mode dispatch) but the
schema filter runs only on the PATCH payload
(`custom_properties.go:313-314`). A mapped property absent from the org
schema never appears in GitHub's current values → `diffProperties`
reports drift forever → every reconcile PATCHes the already-correct
defined properties. Filter (or exempt schema-missing names) before the
diff, not just before the PATCH.

**A6 (Medium): Property-name validation uses the wrong GitHub charset.**
`internal/policy/validate.go:141-144` uses `^[a-zA-Z0-9_.-]{1,75}$`.
Verified against GitHub's docs (managing custom properties): the allowed
set is `a-z A-Z 0-9 _ - $ #`, max 75 — periods are NOT permitted, `$`
and `#` ARE. We accept invalid names (`.`— fails later at PATCH time)
and reject valid ones (`$`, `#`). The same wrong expression is
documented at `docs/usage/policy-reference.md:360-362`. Note the fix is
load-behavior-breaking for any existing policy using dotted names
(fail-loud at load replaces a 422 at sync time — an improvement, but it
needs a migration note).

**A7 (Medium): `RepoGuardianPropertySchemaMissing` usually cannot
fire.** `charts/repo-guardian/templates/prometheusrule.yaml:128-129`
pairs `rate(counter[15m]) > 0` with `for: 30m`. An isolated mismatch
keeps the rate positive ≤ ~15 minutes, so the pending alert resets
before firing; with default 24h freshness and jittered sweeps, only
large fleets with continuous re-hits would ever alert. The LogQL example
at `docs/operations/scaling.md:229-238` shares the flaw. Fix shape:
widen the rate window past the `for` duration (e.g.
`increase(...[1h]) > 0` with a shorter `for`, or align windows).

**A8 (Medium): Typed-null HCL maps can panic at startup.**
`internal/policy/loader.go:664-695` — `decodeAnnotationProperties`
checks `IsObjectType()/IsMapType()` but not `val.IsNull()` /
`!val.IsKnown()` before `AsValueMap()`; per-value `AsString()` has the
same gap. A conditional HCL expression yielding a typed-null object
passes the type check and panics instead of producing a diagnostic.
(The type-check itself was hardened for List/Tuple during IMPL-0017
review — this is the remaining null/unknown edge of the same guard.)

### Group B — Structural debt (deferred by explicit constraint)

Per IMPL-0019 Decision 1 and review direction, none of this starts until
the DESIGN-0020/IMPL-0019 feature is proven working in service.

**B1: `engine_policy.go` structure.** 1,155 lines pre-IMPL-0019; the
new feature adds evaluation/remediation arms to it. Contains four
separable concerns: file-rule evaluation, PR create/update machinery,
setting-rule evaluation, branch-protection evaluation. gocyclo/funlen
pressure is recurring (two extraction refactors already forced by lint:
`syncActionableFiles`, `createNewPolicyPR`). Candidate split:
`engine_settings.go`, `engine_branch_protection.go`, `engine_pr.go` —
mechanical moves, no behavior change.

**B2: Hand-written `github.Client` mocks → mockery.** Every interface
addition costs a three-file parity sweep
(`internal/checker/engine_test.go`, `internal/scheduler/sweep_test.go`,
`internal/reconciler/custom_properties_test.go` + embedders), documented
as a standing convention in CLAUDE.md and paid again in IMPL-0017
(`GetOrgPropertySchema` stubs). DESIGN-0012 already sanctioned the
migration as a follow-up. Complication to scope honestly: several mocks
are *stateful* (recording, list-then-act fidelity) — mockery replaces
the boilerplate but the stateful behaviors need hand-kept wrappers or
expectation plumbing; this is not a pure win and needs a small design
pass first.

**B3: Dead/legacy code.** Confirmed by reference scan:
`internal/scheduler/sweep.go` (`Sweeper`, `NewSweeper`, `ReconcileAll`,
deprecated `Start`) is referenced only within its own file and tests —
the IMPL-0015 Phase 1 plan kept it "to repurpose as Discoverer.Discover",
but `Discoverer` shipped as its own type; the repurpose never consumed
it. Delete file + tests. Sweep should also catch any other
post-IMPL-0015/0016/0017 leftovers (e.g. unused config plumbing for the
removed schedule).

**B4: Standing WARNs / known fragility.**
- Reconcile-branch base-drift WARN (`engine_policy.go:529-541`, PR #71):
  stale branch + auto-merge can overwrite manual default-branch edits.
  Mitigations sketched in the comment (rebase-before-reconcile,
  close/reopen aged PRs) — needs a decision, not just a comment.
- `refreshPolicyPR` body-compare limitation (`engine_policy.go:655-662`):
  body not exposed on the PR struct, so title-stable sweeps PATCH
  unconditionally — one wasted PATCH per touched repo per sweep.
- `hasExistingPRForPolicy` substring matching on `search_terms`
  (`engine_policy.go:438-456`): fragile against title collisions;
  becomes sharper-edged with IMPL-0019's removal PRs (an absent rule's
  terms must not match add-era titles — currently guidance-only).

### Review-verification notes

- Review baseline was PR #164 head `79fd16a`; all findings re-verified
  on merged `main` (`04683f8`) — line numbers cited above are current.
- The review's CI note (Security Scan failing on GO-2026-5970,
  `golang.org/x/text v0.37.0`) was already resolved before merge:
  `6ae1e1f` bumped x/text to v0.39.0; the advisory-vs-scan-timing
  behavior is documented in the 2026-07-22 session notes.
- Targeted race tests for catalog/policy/github/reconciler packages
  passed at review time; nothing in Group A is a race — all are logic
  and contract defects.

## Conclusion

**Answer: Confirmed.** All 8 review findings reproduce on current main
(one — A3 — is worse than reported: the promised behavior exists but is
unreachable). The four structural areas are real, documented, and safely
deferrable. Two findings (A1, A2) are High severity — A2 is a
repo-controlled command-injection vector into a `GITHUB_TOKEN`-bearing
workflow — and should not wait for the post-IMPL-0019 cleanup window.

## Recommendation

Three tracks, sequenced around the IMPL-0019 constraint:

1. **Immediate fix PR (before IMPL-0019 implementation starts):** A1 +
   A2 as a single `fix/` PR with the `patch` label. Small blast radius
   (catalog.go signature + one template + reconciler skip-path), highest
   severity, and both are regressions of operator trust in a feature
   that just shipped.
2. **Hardening fix PR (can land during IMPL-0019 work):** A3, A4, A5,
   A6, A8 (`patch`), plus a chart-only PR for A7 (`dont-release`).
   Packaging per Open Question 4.
3. **Structural cleanup (post-IMPL-0019, feature proven):** B1–B4, each
   as its own `chore/` PR, scoped by a short plan per area (B2 needs the
   stateful-mock design pass first). This INV stays Open until those
   land; flip to Resolved when the last track-3 item merges or is
   explicitly descoped.

These tracks are now planned as two IMPL docs:
[IMPL-0020](../impl/0020-pre-impl-0019-high-severity-fixes-inv-0011-group-a-high.md)
covers track 1 (the A1+A2 High fixes, before IMPL-0019);
[IMPL-0021](../impl/0021-post-impl-0019-hardening-and-structural-cleanup-inv-0011-group.md)
covers tracks 2 and 3 (Group A Mediums + Group B structural, after
IMPL-0019). Open Question 5 in IMPL-0021 revisits whether Group B should
later split into its own IMPL.

## Open Questions

1. **Sequencing of the High fixes (A1+A2) relative to IMPL-0019?**
   - (a) **Recommendation:** dedicated `fix/` PR first, before IMPL-0019
     implementation begins — A2 is a live injection vector in every
     generated workflow PR and A1 destroys operator data on a malformed
     commit; both are small, self-contained fixes (INV-0003 precedent:
     investigation → tiny fix PR without a full IMPL doc).
   - (b) Fold into IMPL-0019 Phase 0 — one less PR, but couples security
     fixes to an unrelated feature timeline and delays them by however
     long Phase 0 review takes.
   - (c) Defer to the hardening PR — leaves the injection vector open
     longest; rejected unless the GHA mode is confirmed unused in every
     live deployment.
   - other:

2. **A1 fix semantics — what does a parse failure mean?**
   - (a) **Recommendation:** abort the reconcile for that repo: skip
     with `slog.Warn` + new counter
     (`catalog_parse_failed_total{org}`), leaving GitHub state
     untouched. Malformed input is unknowable intent — never synthesize
     a destructive default from it. `Parse` returns an error instead of
     silently defaulting (signature change; the non-Component-entity
     case stays a clean "no properties" result since that IS knowable
     intent).
   - (b) Fall back to last-known-good desired state — requires
     persisting prior desired state per repo (new Store surface); more
     machinery than the problem warrants.
   - (c) Keep defaults but log loudly — still destroys data, just
     audibly.
   - other:

3. **A2 fix mechanism for the generated workflow?**
   - (a) **Recommendation:** both layers — render values into the
     workflow's `env:` block and reference them as `"$VAR"` inside the
     `run:` script (env values are never shell-parsed; the exact
     pattern the release-pipeline post-mortem established for bake
     metadata), AND tighten annotation-value validation at policy load
     to reject control characters. Defense in depth; the env fix alone
     is complete, the validation catches garbage earlier.
   - (b) env-indirection only — sufficient, minimal.
   - (c) Shell-escape values at template render — fragile (quoting
     escaping quoting) and every future template edit can reintroduce
     the bug.
   - other:

4. **Packaging of the Medium hardening fixes (A3–A8)?**
   - (a) **Recommendation:** one `fix/impl-0017-hardening` PR for
     A3+A4+A5+A6+A8 with `patch` label and a migration note covering
     A6's charset change; separate chart-only PR for A7 with
     `dont-release` (chart cadence is independent). Two PRs total,
     coherent review scope ("everything the post-ship review found").
   - (b) One PR per finding — maximal isolation, five-plus PRs of
     review overhead for changes that are each < ~100 lines.
   - (c) Fold all into IMPL-0019 — couples shipped-feature fixes to new
     feature risk; rejected for the same reason as 1(b).
   - other:

5. **Does A3 get fixed in code or in docs?**
   - (a) **Recommendation:** code — implement the documented behavior.
     The reconciler's no-catalog arm already exists and is tested; the
     change is an engine-side path that invokes the custom_properties
     reconciler when the rule's file is absent (scoped carefully against
     the double-iteration counter contract). Docs and migration guide
     already promise this; A1's fix (parse-failure abort) makes the
     clear path safe to enable.
   - (b) Docs retreat — document that file removal does NOT clear.
     Cheaper, but it breaks the DESIGN-0019 full-state-sync contract and
     leaves the tested reconciler arm permanently dead.
   - other:

6. **May the dead-code deletion (B3) ride earlier than the rest of
   Group B?**
   - (a) **Recommendation:** no — hold ALL of Group B until after
     IMPL-0019 proves out, per the explicit review constraint. Deleting
     `sweep.go` touches the scheduler package IMPL-0019 doesn't, but
     "no structural churn during feature work" is simpler to hold as a
     bright line than to litigate per-file.
   - (b) Allow B3 only in the hardening PR — it's zero-risk deletion of
     unreferenced code and shrinks future review noise; the bright line
     costs us carrying dead code a few more weeks.
   - other:

## References

- [IMPL-0019](../impl/0019-absent-check-mode-and-conditional-file-rules.md) — Resolved Decision 1 defers B1 here
- [DESIGN-0020](../design/0020-absent-check-mode-and-conditional-file-rules.md) — the feature that must prove out before Group B starts
- [DESIGN-0019](../design/0019-configurable-annotation-sourced-custom-properties.md) / IMPL-0017 — the surface Group A hardens
- [INV-0003](0003-pre-existing-branch-422-on-subsequent-reconciles.md) — precedent for investigation → small fix PR without a full IMPL
- GitHub docs — [Managing custom properties for repositories](https://docs.github.com/en/organizations/managing-organization-settings/managing-custom-properties-for-repositories-in-your-organization) (property-name charset verified 2026-07-23)
- Operator code review of PR #164 head `79fd16a` (8 findings, folded in verbatim as Group A)
