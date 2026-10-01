---
id: INV-0021
title: "Orphan cleanup deletes a file still targeted by an actionable rule"
status: Open
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0021: Orphan cleanup deletes a file still targeted by an actionable rule

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: writes happen before orphan cleanup, in the same sweep](#observation-1-writes-happen-before-orphan-cleanup-in-the-same-sweep)
  - [Observation 2: orphan status is decided per rule name](#observation-2-orphan-status-is-decided-per-rule-name)
  - [Observation 3: the sweep, outside the wiz org](#observation-3-the-sweep-outside-the-wiz-org)
  - [Observation 4: the inverse path already has the right guard](#observation-4-the-inverse-path-already-has-the-right-guard)
  - [Observation 5: v2 is affected identically](#observation-5-v2-is-affected-identically)
  - [Observation 6: the HCL kill switch is shadowed by the chart](#observation-6-the-hcl-kill-switch-is-shadowed-by-the-chart)
- [Engine review: where overlapping rules bite](#engine-review-where-overlapping-rules-bite)
  - [R1: shared target, one rule not actionable (this bug), HIGH, confirmed](#r1-shared-target-one-rule-not-actionable-this-bug-high-confirmed)
  - [R2: an add rule and an absent rule on the same path, HIGH, confirmed, and both shipped examples have it](#r2-an-add-rule-and-an-absent-rule-on-the-same-path-high-confirmed-and-both-shipped-examples-have-it)
  - [R3: two actionable rules with the same target, MEDIUM-HIGH, confirmed](#r3-two-actionable-rules-with-the-same-target-medium-high-confirmed)
  - [R4: paths versus target, MEDIUM, confirmed](#r4-paths-versus-target-medium-confirmed)
  - [R5: removing or disabling a rule strands its change in the open PR, MEDIUM, confirmed](#r5-removing-or-disabling-a-rule-strands-its-change-in-the-open-pr-medium-confirmed)
  - [R6: the same rule name in two kinds collides in posture, MEDIUM, confirmed](#r6-the-same-rule-name-in-two-kinds-collides-in-posture-medium-confirmed)
  - [R7: rulesets and settings are matched by target, not ownership, MEDIUM, confirmed](#r7-rulesets-and-settings-are-matched-by-target-not-ownership-medium-confirmed)
  - [R8: foreign-PR matching is a substring match, LOW-MEDIUM, confirmed](#r8-foreign-pr-matching-is-a-substring-match-low-medium-confirmed)
  - [R9: misleading reconcile-log status, LOW, confirmed](#r9-misleading-reconcile-log-status-low-confirmed)
  - [Not affected](#not-affected)
  - [Validation at load today](#validation-at-load-today)
- [Testing that would have caught this](#testing-that-would-have-caught-this)
  - [T1: a more realistic fake](#t1-a-more-realistic-fake)
  - [T2: an exhaustive pairwise overlap matrix](#t2-an-exhaustive-pairwise-overlap-matrix)
  - [T3: the shipped examples converge](#t3-the-shipped-examples-converge)
  - [T4: validation at policy load](#t4-validation-at-policy-load)
  - [T5: one ownership map per sweep (a design change, not a test)](#t5-one-ownership-map-per-sweep-a-design-change-not-a-test)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Why do open repo-guardian PRs in every org except one gain a commit that deletes CODEOWNERS after a second, single-org CODEOWNERS rule was added to the policy? And is the policy's org scoping being ignored?

## Hypothesis

Scoping works. Orphan cleanup decides authorship per *rule name*, not per *path*. So when two rules share a `target` and only one of them is actionable, the non-actionable rule's "orphan" is the file the actionable rule just wrote.

## Context

A CODEOWNERS `.wiz` rule (`check = "contains"`, `target = ".github/CODEOWNERS"`) was added to a strict-mode policy (top-level `scope {}`, every rule scoped) and scoped to a single org. The existing `codeowners` rule (`check = "exists"`, same `target`) applies to every org.

After rollout, open repo-guardian PRs outside the wiz org started proposing to delete CODEOWNERS.

This is the same family as INV-0014, where orphan cleanup deleted files the default branch owned. The INV-0014 fix (#174) made absence from the default branch a precondition for orphan status. That precondition holds here, because the file really is absent from the default branch. So the INV-0014 guard does not cover this case.

**Triggered by:** #199 (v1), #200 (v2).

## Approach

1. Read `createOrUpdatePRFromPolicy` (`internal/checker/engine_pr.go`) for the order of writes and deletes on an existing PR.
2. Read `discoverOrphans` (`internal/checker/drift.go`) for how a rule qualifies as an orphan.
3. Walk one sweep of a repository outside the wiz org with an open PR.
4. Compare with `restoreInverseOrphans`, which already guards against touching a path an actionable rule claims.
5. Check `v2` for the same code.

## Environment

| Component | Version / Value |
| --------- | --------------- |
| Binary | v1.11.1 (`66cac25` lineage, post-INV-0014) |
| Chart | `1.0.0-rc.11` |
| Policy | strict mode (top-level `scope {}`); `codeowners` (`exists`, `orgs = ["*"]`) and a wiz `contains` rule (one org), both `target = ".github/CODEOWNERS"` |
| `policy.orphanCleanup` | `true` (default) |

## Findings

### Observation 1: writes happen before orphan cleanup, in the same sweep

On an existing PR, `createOrUpdatePRFromPolicy` does the following:

1. `syncActionableFiles` (`engine_pr.go:167`) commits every actionable rule's template to `repo-guardian/add-missing-files`.
2. Only then does it call `discoverOrphans(ctx, log, client, e.policy.FileRules, actionable, owner, repo)` (`engine_pr.go:185`).
3. It passes the result to `cleanupOrphans`.

So a file written in step 1 is on the branch by the time step 2 probes it.

### Observation 2: orphan status is decided per rule name

`discoverOrphans` iterates **every** enabled file rule and skips only those whose **name** is in the actionable set (`drift.go:101`). For each remaining rule with a `Target`, it applies the INV-0014 authorship test: the target is absent from the default branch and present on the reconcile branch.

Nothing asks whether another, still-actionable rule targets the same path.

### Observation 3: the sweep, outside the wiz org

| Step | `codeowners` | wiz rule |
| ---- | ------------ | -------- |
| Scope | in scope (`*`) | out of scope (rule-level) |
| Actionable | yes (no CODEOWNERS on default) | no |
| `syncActionableFiles` | writes `.github/CODEOWNERS` | — |
| `discoverOrphans` | skipped (actionable) | not on default ✓, on branch ✓ → **orphan** |
| `cleanupOrphans` | — | **deletes `.github/CODEOWNERS`** |

The next sweep writes the file again and deletes it again. The PR's net diff has no CODEOWNERS, and the sticky reconcile-log comment reports the wiz rule as "orphan removed from branch".

Inside the wiz org both rules are actionable, so neither is an orphan candidate. That is why the behaviour looks like scoping is ignored everywhere except one org: the deletion happens precisely where scoping *excludes* the rule.

The same thing happens for any reason a rule is not actionable while a sibling sharing its target is: rule-level `ignore {}`, a closed `when {}` gate, or the rule simply being satisfied under different paths.

### Observation 4: the inverse path already has the right guard

`restoreInverseOrphans` builds a `claimed` set from `plannedDeletions(actionable)` and `plannedWrites(actionable)` (`drift.go:256-263`). It then refuses to touch a claimed path (`drift.go:320`).

`discoverOrphans` has no equivalent. The two halves of drift handling disagree on whether a path's owner is a rule or the set of actionable rules.

### Observation 5: v2 is affected identically

`internal/checker/drift.go` on `v2` has the same `discoverOrphans`. v2's `activities.CheckRepo` drives the same engine. v2 does record `not_applicable` outcomes for out-of-scope rules, but those still are not actionable, so they still qualify.

### Observation 6: the HCL kill switch is shadowed by the chart

`orphan_cleanup = false` in `guardian {}` has no effect under the chart. `deployment.yaml:97` always sets `ORPHAN_CLEANUP` from `policy.orphanCleanup`, and `applyEnvOverrides` lets the env var win. The operator workaround must therefore go through chart values.

## Engine review: where overlapping rules bite

The root cause generalises. The engine has several actors that decide what happens to a path:

- `syncActionableFiles`
- `removeForbiddenFiles`
- `discoverOrphans`
- `restoreInverseOrphans`
- `Engine` evaluation, which reads `Paths` but writes `Target`

Each builds its own idea of who owns that path, keyed by rule name, by path, or not at all. Policy validation assumes rules are disjoint but never checks it.

Findings below are from a read of `main` and are ordered by severity. **Confirmed** means the code path was traced.

### R1: shared target, one rule not actionable (this bug), HIGH, confirmed

`discoverOrphans` (`drift.go:87-103`) excludes by name. The non-actionable sibling triggers it whenever it is:

- out of scope (`engine_policy.go:303`)
- ignored (`:310`)
- gate-closed (`:322`)
- matched by a foreign PR's `search_terms` (`:385`)
- `exists`/`contains`/`exact`, satisfied through a *different* entry in its `paths`

A disabled sibling does not trigger it, because `IsEnabled` is checked first.

Cleanup runs only on the existing-PR path, so sweep 1 opens a correct PR. Every later sweep commits `CreateOrUpdateFile` then `DeleteFile`: two commits per sweep, forever. GitHub's read-after-write lag means the branch probe can miss the fresh write, so the deletion is intermittent, which makes the bug harder to reproduce.

### R2: an add rule and an absent rule on the same path, HIGH, confirmed, and both shipped examples have it

`examples/guardian-full.hcl` pairs `dependabot` (`exists`, **no gate**, target `.github/dependabot.yml`) with `no_dependabot` (`absent`, same path, gated on `renovate_config`). Its comment says to "add Dependabot everywhere and let the gated absent rule remove it". `examples/guardian-enterprise.hcl` has the same pair.

In a Renovate repo the cycle is:

1. Dependabot is present, so the absent rule is actionable and its PR deletes it. The PR merges.
2. The next sweep makes the `exists` rule actionable, and its PR re-adds the file. That PR merges.
3. Repeat forever.

DESIGN-0020's convergence table claims "renovate yes / dependabot no → converged". The `renovateFirstPolicy` fixture (`convergence_test.go:68`) leaves out the `exists` rule, so no test exercises the real pairing.

There is also a within-sweep variant: a failing `contains` rule on X plus an actionable `absent` rule on X. `removeForbiddenFiles` (`engine_pr.go:278-297`) checks no claim set, so the outcome depends on policy order:

- write then delete: churn on every sweep;
- delete then write: net "modify", and the absent rule never converges.

### R3: two actionable rules with the same target, MEDIUM-HIGH, confirmed

The last writer in policy order wins (`engine_pr.go:223`). `CreateOrUpdateFile` skips only identical content, so while the PR is open, each sweep writes A's content and then B's.

After merge, if A is `contains`/`exact` and B's content fails A's check, A goes actionable on its own and opens a new PR. With incompatible templates this never converges. The PR body and `PRVars.Files` also list the path twice.

This is exactly the wiz layout inside the wiz org. It works only if the two templates agree, which nothing enforces.

### R4: `paths` versus `target`, MEDIUM, confirmed

Evaluation reads the first `paths` entry that exists on the default branch (`engine_policy.go:396, 554-563`). Remediation writes `target`.

- **`target` not in `paths`:** the rule's own fix can never satisfy it, so it stays actionable after merge. The next PR then has no diff, and `CreatePullRequest` fails every sweep. GitHub's 422 response for that case was not traced.
- **Path order:** the built-in `codeowners` paths are root, `.github`, `docs` (`defaults.go:120`), but GitHub uses `.github/CODEOWNERS` first. A `contains`/`exact` rule over those paths keeps reading a stale root `CODEOWNERS` after its fix lands in `.github/`.

### R5: removing or disabling a rule strands its change in the open PR, MEDIUM, confirmed

`discoverOrphans` and `restoreInverseOrphans` both iterate current, enabled rules (`drift.go:94-99, 267-272`). A file written by a since-deleted rule, or a deletion made by a since-disabled absent rule, stays on the branch. The refreshed PR body omits it, and merging ships it.

A rename that keeps the same target is safe, because the probe is by path.

### R6: the same rule name in two kinds collides in posture, MEDIUM, confirmed

The `rule_state` primary key is `(installation_id, owner, repo, rule_name)` with no kind (`0003_rule_state.up.sql:25`). The upsert overwrites `rule_kind`, so a `file` rule and a `setting` rule named alike overwrite each other's posture row, and compliance groups by name.

`Validate` rejects duplicates only within a kind. On v2 the findings store is keyed by (kind, name); see the IMPL-0025 Phase 5 note.

### R7: rulesets and settings are matched by target, not ownership, MEDIUM, confirmed

`findMatchingRuleset` (`engine_branch_protection.go:148-165`) and the reconciler's `findRulesetForBranch` return the *first* ruleset whose include pattern matches the branch. They then update it under `repo-guardian-<rule>`.

So two `branch_protection` rules on one branch, or a rule plus the `branch_protection` reconciler, overwrite each other every sweep. Each reports itself compliant because it "remediated". A ruleset a human created for `main` gets overwritten and renamed.

Two `setting` rules on the same property have the same shape. Nothing validates either.

### R8: foreign-PR matching is a substring match, LOW-MEDIUM, confirmed

`search_terms` match a PR's title or head branch by substring (`engine_pr.go:55-76`). `renovate_config`'s `["renovate"]` matches every Renovate dependency PR, assuming Renovate's default `renovate/` branch prefix (not traced). While one is open the rule counts as compliant, and as R1 shows, that makes it a deletion trigger for any rule sharing its target.

### R9: misleading reconcile-log status, LOW, confirmed

- `reconcileStatus` (`drift.go:514-549`) has no scope or ignore case, so out-of-scope and ignored rules read "satisfied on main".
- `autoClosePR` passes a nil gate, so gate-closed rules read the same.
- Absent-rule PR bodies list every path, not only the ones present.

### Not affected

Reconciler-written files. Reconcilers use their own branches (`custom_properties.go:55-58`), so neither orphan cleanup nor inverse-orphan restoration touches them. `ExtractWatchedPaths` is a set of paths with no ownership decision.

Plausible, not traced: two `label_sync` reconcilers with `delete_extra` undo each other.

### Validation at load today

`Validate` (`validate.go:19-30`) checks:

- duplicate names within each kind
- `when`-gate references: the target exists, is a file rule, is enabled, is not self, and there are no cycles
- per-rule fields, such as absent rules forbidding `target`/`template`
- duplicate `annotation_properties` targets

It does **not** check:

- duplicate targets
- `target` not in `paths`
- an add rule's target inside an absent rule's `paths`
- overlapping `paths`
- the same name across kinds
- a duplicate setting property
- a duplicate branch-protection branch

Paths are never normalised (no `path.Clean`), so `./x` and `x` are distinct.

## Testing that would have caught this

The existing tests cover one rule at a time, plus a few hand-picked pairs. The bug lives in the *interaction* between rules, and in sweeps after the first. These additions target that.

### T1: a more realistic fake

The `mockClient` in `engine_test.go` discards file content, has no merge, and has no per-sweep call log. Add:

- content per (branch, path), with skip-if-identical like the real `CreateOrUpdateFile`;
- `mergeOurPR()`, which applies the branch to the default branch's contents, closes the PR and deletes the branch;
- `resetCalls()`, so each sweep's calls are asserted on their own.

Keep the fake's list-then-act semantics (CLAUDE.md, mock-fidelity rule).

### T2: an exhaustive pairwise overlap matrix

`internal/checker/overlap_matrix_test.go`, built on the convergence helpers (`newStagedConvergenceWithPolicy`, `sweep`, `openOurPR`, `satisfyOnMain`). Enumerate rule pairs (A, B) over:

- **mode:** `exists`, `contains` passing, `contains` failing, `exact`, `absent`
- **relation:** same target; A's target in B's `paths`; target not in its own `paths`; disjoint (the control)
- **B's actionability:** actionable, out of scope, ignored, gate closed, foreign PR, satisfied via another path, disabled, removed from policy between sweeps
- **PR state:** none or open
- `staleBranchReads`: on or off

Assert these invariants on every sweep:

- **I1:** no path is both written and deleted in one sweep.
- **I2:** never `DeleteFile` a path in `plannedWrites(actionable)`; never write a path in `plannedDeletions(actionable)`. This catches R1 and the within-sweep half of R2.
- **I3 (idempotence):** a second identical sweep makes zero writes, deletes or PR updates. This catches the churn in R1 and R3.
- **I4 (convergence):** repeat sweep → merge for at most 4 rounds; the result must be no actionable rule and no open PR. On failure, print the cycle trace. This catches R2, R3 and R4.
- **I5:** the branch's diff from default equals the planned writes and deletions for the current actionable set. This catches R5.
- **I6:** after merge, every rule that was actionable is satisfied by its own fix. This catches R4.

A pair that cannot converge (R2 without gating, R3 with incompatible templates) should become a load-time error (T4), not an expected test failure.

### T3: the shipped examples converge

Load every `examples/*.hcl` and run the I4 loop against a small grid of synthetic repositories: Renovate present or absent × Dependabot present or absent × CODEOWNERS in each location. This catches R2 directly, and keeps the examples honest, since operators copy them.

### T4: validation at policy load

`validateFileRuleOverlaps` in `validate.go`, table-tested in `validate_test.go`. Normalise every path with `path.Clean` first, then:

- **error** when `target` is not in `paths`;
- **error** when an add rule's `target` is in an absent rule's `paths`, unless a `when` gate makes them mutually exclusive;
- **error, or warn and require identical templates,** when two non-absent rules share a `target`;
- **warn** when a `contains`/`exact` rule's `target` is not its first `paths` entry;
- **error** on the same name across kinds (v1);
- **error** on a duplicate setting property or branch-protection branch where scopes overlap.

### T5: one ownership map per sweep (a design change, not a test)

Build `path → owning actionable rule` once per check and pass it to `syncActionableFiles`, `removeForbiddenFiles`, `discoverOrphans` and `restoreInverseOrphans`. All four actors then answer "who owns this path" from one source of truth, and I1/I2 hold by construction rather than by each function remembering to check.

## Conclusion

**Answer:** The hypothesis is confirmed. Org scoping is applied correctly. `discoverOrphans` treats a path as orphaned when its *rule* is not actionable, even though another actionable rule wrote that path earlier in the same sweep. Any two file rules that share a `target` and differ in actionability trigger it.

## Recommendation

1. **Fix R1 now (#199, `main`, `patch`).** In `discoverOrphans`, skip any candidate whose `Target` is in `plannedWrites(actionable)`, the helper `restoreInverseOrphans` already uses. This also saves that rule's two API probes. Tests:
   - The R1 variants from the T2 axes: out of scope, ignored, gate closed, foreign PR, satisfied via another path.
   - Assert I2 and I3: zero `DeleteFile` calls, and a second sweep makes no writes.
   - Neutralize the fix and confirm the tests fail.
2. **Merge forward (#200).** Merge the fix into `v2`, routing the new tests through `parityCheckRepo`. Goldens should change only where a bogus `DeleteFile` disappears.
3. **Workaround until shipped.** Set `policy.orphanCleanup: false` in chart values, not in the HCL (Observation 6). For the wiz rule specifically, use the same `codeowners` template in both rules, so R3 cannot flip-flop inside the wiz org.
4. **Follow-up issues**, one each, in this order:
   - R2: fix both example policies (gate `dependabot` on `renovate_config` not being satisfied, or remove it), plus T3.
   - T4: load-time overlap validation, which turns R2, R3 and R4 into load errors.
   - T1 + T2: the overlap matrix.
   - T5: the per-sweep ownership map, so the R1/R2 class holds by construction.
   - R5: stranded changes from removed rules.
   - R7: ruleset and setting ownership.
   - R6: v1-only; v2 keys by kind.
   - R8, R9: cosmetic or low.
5. **Policy guidance.** Add a "rules that share files" section to `docs/usage/policy-reference.md`:
   - a shared `target` needs identical or compatible templates;
   - an add rule and an absent rule on one path need mutually exclusive gates;
   - list `target` first in `paths`.

## References

- #199: v1 fix
- #200: v2 merge-forward
- INV-0014: orphan cleanup deleted files the default branch owns (#174)
- IMPL-0013 Phase 3: orphan cleanup and convergence
- IMPL-0019 Phase 2: inverse-orphan restoration (`restoreInverseOrphans`)
- `internal/checker/drift.go`, `internal/checker/engine_pr.go`
