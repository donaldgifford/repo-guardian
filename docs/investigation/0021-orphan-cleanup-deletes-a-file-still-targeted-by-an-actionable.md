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

## Conclusion

**Answer:** The hypothesis is confirmed. Org scoping is applied correctly. `discoverOrphans` treats a path as orphaned when its *rule* is not actionable, even though another actionable rule wrote that path earlier in the same sweep. Any two file rules that share a `target` and differ in actionability trigger it.

## Recommendation

1. **Fix (#199, `main`, `patch`).** In `discoverOrphans`, build the set of paths actionable rules will write (`plannedWrites(actionable)`, the helper `restoreInverseOrphans` already uses). Skip any candidate whose `Target` is in it. This should also skip the two GitHub API probes for that rule.
2. **Tests.**
   - Two rules share a target, and the sibling is out of scope. Variants: ignored, gate-closed.
   - The shared file must survive the sweep, with zero `DeleteFile` calls and no "orphan removed from branch" row.
   - Route the test through `parityCheckRepo` on `v2`.
   - Neutralize the fix and confirm the test fails.
3. **Merge forward (#200).** Merge into `v2`. The parity goldens should change only where a bogus `DeleteFile` disappears; regenerate deliberately if so.
4. **Workaround until shipped.** Set `policy.orphanCleanup: false` in chart values, not in the HCL (Observation 6).
5. **Policy guidance.** Document in `docs/usage/policy-reference.md` that rules may share a `target`. Note that their templates then compete for the same file: whichever actionable rule syncs last wins. A `contains` rule layered on an `exists` rule should use the same template, or the `contains` rule's template should be a superset.

## References

- #199: v1 fix
- #200: v2 merge-forward
- INV-0014: orphan cleanup deleted files the default branch owns (#174)
- IMPL-0013 Phase 3: orphan cleanup and convergence
- IMPL-0019 Phase 2: inverse-orphan restoration (`restoreInverseOrphans`)
- `internal/checker/drift.go`, `internal/checker/engine_pr.go`
