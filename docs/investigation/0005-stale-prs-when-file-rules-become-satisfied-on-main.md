---
id: INV-0005
title: "Stale PRs when file rules become satisfied on main"
status: Open
author: Donald Gifford
created: 2026-05-26
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0005: Stale PRs when file rules become satisfied on main

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-05-26

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1 — file rules: gap confirmed](#observation-1--file-rules-gap-confirmed)
  - [Observation 2 — setting rules: convergent](#observation-2--setting-rules-convergent)
  - [Observation 3 — branch_protection rules: convergent](#observation-3--branchprotection-rules-convergent)
  - [Observation 4 — reconcilers: idempotent](#observation-4--reconcilers-idempotent)
  - [Observation 5 — legacy engine has the same gap](#observation-5--legacy-engine-has-the-same-gap)
- [Code references](#code-references)
- [Log signatures for live verification](#log-signatures-for-live-verification)
  - [Smoking-gun grep — actionable set is empty but PR is open](#smoking-gun-grep--actionable-set-is-empty-but-pr-is-open)
  - [Partial-update grep — some still missing, others stale](#partial-update-grep--some-still-missing-others-stale)
  - [Negative control — convergent rule types](#negative-control--convergent-rule-types)
  - [Counting the blast radius](#counting-the-blast-radius)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

When a file rule becomes satisfied via a non-guardian path (e.g.,
an operator adds `.github/CODEOWNERS` to `main` via their own PR while
a `repo-guardian/add-missing-files` PR is open), does repo-guardian
reconcile the open PR on its next sweep — drop the orphaned file from
the branch, refresh the PR body, and close the PR when nothing is
left to do?

And more broadly: does the same gap apply to setting rules,
branch_protection rules, and the four built-in reconcilers, or is it
isolated to the PR-mediated file-rule path?

## Hypothesis

The PR-mediated path is leaky in three places:

1. The reconcile branch never has files removed when their rule
   becomes satisfied between sweeps.
2. The PR body is generated at creation time only; it is not
   regenerated when the actionable set shrinks.
3. The PR is never closed when the actionable set becomes empty.

Setting rules, branch_protection rules, and reconcilers should NOT
have this gap because they check current state and short-circuit when
the desired state already matches — no out-of-band intermediate
artifact (like a PR branch) needs cleanup.

## Context

**Triggered by:** Production observation on PR #79
([donaldgifford/repo-guardian#79](https://github.com/donaldgifford/repo-guardian/pull/79)).
The homelab deployment has an open repo-guardian PR proposing three
files (`CODEOWNERS`, `renovate.json`, `catalog-info.yaml`). The
question is what happens if any one of those files is added to `main`
by a different code path before the PR merges:

- Does the next sweep drop that file from the PR?
- Does it close the PR when all three are satisfied externally?
- And: is this a leak only for file rules, or do setting /
  branch_protection rules / reconcilers behave the same way?

Behavior matters because operators frequently land convenience files
manually (especially CODEOWNERS) and expect repo-guardian to converge
on the resulting state without their having to hand-close stale PRs.

## Approach

1. Read `internal/checker/engine_policy.go` to trace the file-rule
   evaluation path end-to-end:
   - `checkRepoWithPolicy` dispatch
   - `findActionableRules` → produces the actionable set
   - `createOrUpdatePRFromPolicy` → mutates the branch and PR
2. Confirm setting rule and branch_protection rule behavior by
   reading `evaluateSettingRule` and `evaluateBranchProtectionRule`.
3. Audit the four built-in reconcilers
   (`internal/reconciler/{custom_properties,label_sync,branch_protection,workflow_sync}.go`)
   for idempotency.
4. Confirm the legacy engine path (`engine.go.findMissingFiles` +
   `createOrUpdatePR`) shares the same gap.
5. Compile log-message signatures emitted on the leaky path so the
   bug can be confirmed in a live deployment via `kubectl logs ...
   | grep ...`.

## Environment

| Component | Version / Value |
|-----------|----------------|
| chart | 0.5.0 |
| appVersion | 1.6.0 |
| Go | 1.25.4 |
| Engine path exercised | policy-based (`NewEngineFromPolicy` — used whenever `GUARDIAN_CONFIG` HCL is present) |
| Reference operator config | `examples/guardian-full.hcl` |
| Trigger surface for the bug | every sweep (`Scheduler.Schedule(name="sweep", ...)`) and every webhook re-check |

## Findings

### Observation 1 — file rules: gap confirmed

The engine's update path **never removes files from the
reconcile branch and never closes the open PR.**

`engine_policy.go:40-49` dispatches based on the size of the
`actionable` set:

```go
switch {
case len(actionable) == 0:
    log.Info("all required files present")
case e.dryRun:
    log.Info("dry run: would create PR", "actionable_rules", policyRuleNames(actionable))
default:
    if err := e.createOrUpdatePRFromPolicy(ctx, client, owner, repo, defaultBranch, actionable, openPRs); err != nil {
        return err
    }
}
```

When the actionable set is **empty** the engine logs a single line
and returns. It does **not** look for an existing repo-guardian PR
to close, and does **not** delete the reconcile branch.

When the actionable set is **non-empty but smaller than the prior
sweep** — meaning some files are now satisfied on `main` while others
are still missing — the call into `createOrUpdatePRFromPolicy`
iterates only the still-actionable rules
(`engine_policy.go:561-590`):

```go
for i := range actionable {
    r := &actionable[i]
    // ... render template ...
    if err := client.CreateOrUpdateFile(ctx, owner, repo, BranchName, r.Target, content, msg); err != nil {
        return fmt.Errorf("creating file %s: %w", r.Target, err)
    }
    log.Info("added file", "path", r.Target)
}
```

The loop **adds and updates** but never **removes**. Files
previously committed to the reconcile branch for a now-satisfied
rule remain on the branch.

`createNewPolicyPR` (`engine_policy.go:605-644`) is the only place
the PR body is rendered — it runs only when no existing PR was
found. The update branch (`engine_policy.go:596-599`) emits
`PRsUpdatedTotal.Inc()` + a single log line and returns; **the PR
body is never regenerated**. So the "Files changed" list in the body
remains the historical at-creation snapshot.

A WARN comment at `engine_policy.go:546-558` already acknowledges a
closely-related risk (branch drifts versus default branch when the
PR sits open for a long time) but does not name the
file-rule-satisfied case directly.

### Observation 2 — setting rules: convergent

`engine_policy.go:731-755`:

```go
if settingMatches(currentValue, rule.Expected) {
    log.Debug("setting matches expected value", "current", currentValue)
    return nil
}

metrics.SettingsMismatchedTotal.WithLabelValues(rule.Name, owner).Inc()
log.Info("setting mismatch", "current", currentValue, "expected", rule.Expected)

if !rule.Remediate {
    return nil
}
// ... remediation ...
```

Setting rules read the current repository value on every sweep and
no-op when it matches. There is no intermediate artifact (no PR, no
branch) that can become stale. **No gap.**

### Observation 3 — branch_protection rules: convergent

`engine_policy.go:944-947`:

```go
mismatches := compareBranchProtection(existing, rule)
if len(mismatches) == 0 {
    log.Debug("branch protection matches expected configuration")
    return nil
}
```

Branch protection rules diff the existing ruleset against the
desired one and short-circuit when there are no mismatches. **No
gap.**

### Observation 4 — reconcilers: idempotent

All four built-in reconcilers check current state and no-op when in
sync:

- `internal/reconciler/custom_properties.go:84` —
  `log.Info("custom properties already correct")` and returns. Also
  `:143` short-circuits when a PR for property sync already exists.
- `internal/reconciler/label_sync.go` — only emits API calls for
  labels that need create / update / rename / delete; an in-sync
  label set produces no mutations.
- `internal/reconciler/branch_protection.go` — same
  diff-then-short-circuit pattern as the BP rule type.
- `internal/reconciler/workflow_sync.go` — observability only, never
  mutates.

**No gap.** Reconcilers are robust against repeated sweeps.

### Observation 5 — legacy engine has the same gap

The legacy non-policy path (`engine.go.findMissingFiles` +
`createOrUpdatePR`) has the equivalent gap with one additional
quirk: `hasExistingPR` (`engine.go:196-210`) skips a rule when an
open PR matches its `PRSearchTerms`. So once a PR exists, that
rule's existence-check result no longer drives any behavior — the
file added in the prior PR sticks around regardless of `main` state.
Same three sub-bugs as the policy path.

Most operators are on the policy path (`GUARDIAN_CONFIG` set), so the
fix will focus on `engine_policy.go`. The legacy path should get the
same treatment but is lower priority.

## Code references

| Concern | File | Lines | What to change |
|---|---|---|---|
| Dispatch when `len(actionable) == 0` does nothing | `internal/checker/engine_policy.go` | 40-49 | Add a branch: if PR exists AND actionable is empty → close PR, delete branch. |
| File-add loop never removes | `internal/checker/engine_policy.go` | 561-590 | Compute "satisfied-since-last-sweep" set; call `client.DeleteFile` for each `r.Target` whose rule is now satisfied. Needs new client method (`DeleteFile`) or reuse of `Repositories.DeleteFile` go-github wrapper. |
| PR body never regenerated | `internal/checker/engine_policy.go` | 592-599 | On the "existing PR" branch, recompute `rendered.Body` via `buildPRBodyFromPolicy(actionable)` and call a new `client.UpdatePullRequest(ctx, owner, repo, prNumber, body)` to push it. |
| Legacy non-policy path has same three sub-bugs | `internal/checker/engine.go` | 103-117, 212-310 | Mirror the fixes here once the policy path lands. |
| Existing WARN comment on related drift | `internal/checker/engine_policy.go` | 546-558 | Update once the rebase question is also resolved. |

## Log signatures for live verification

The leaky behavior produces a specific log signature that an
operator can confirm against a live deployment without code
changes. All entries come from the `checker` subsystem with
`owner=<owner> repo=<repo>` slog fields.

### Smoking-gun grep — actionable set is empty but PR is open

```bash
kubectl logs -n repo-guardian -l app.kubernetes.io/name=repo-guardian --since=24h \
  | grep "all required files present" \
  | grep "owner=<owner> repo=<repo>"
```

If this matches AND the repository has an open PR with head
`repo-guardian/add-missing-files`, the bug is firing: the engine
just confirmed everything is satisfied and walked away from the
stale PR.

Pair it with:

```bash
gh pr list --repo <owner>/<repo> --head repo-guardian/add-missing-files --state open
```

### Partial-update grep — some still missing, others stale

```bash
kubectl logs -n repo-guardian -l app.kubernetes.io/name=repo-guardian --since=24h \
  | grep -E "updated existing PR|added file" \
  | grep "owner=<owner> repo=<repo>"
```

The `"added file"` lines enumerate exactly the rules the engine
considered actionable on that sweep. If the open PR's diff contains
files NOT in that enumeration, those files are orphans from a prior
sweep.

Example correlation:

```
checker  added file  path=.github/dependabot.yml  owner=foo  repo=bar
checker  updated existing PR  pr_number=42  owner=foo  repo=bar
```

If PR #42 also contains `.github/CODEOWNERS`, that's an orphan
addition — CODEOWNERS became satisfied on `main` between the prior
sweep and this one.

### Negative control — convergent rule types

Setting rules log `"setting matches expected value"` (Debug) when
in sync — no behavior to confirm because there's no gap.
Branch protection rules log `"branch protection matches expected
configuration"` (Debug). Reconcilers log e.g. `"custom properties
already correct"` (Info). None of these produce stale state.

To raise verbosity for the convergent paths if you want to confirm
the no-op:

```bash
LOG_LEVEL=debug kubectl set env deploy/repo-guardian LOG_LEVEL=debug -n repo-guardian
```

(reset to `info` after observation)

### Counting the blast radius

```bash
kubectl logs -n repo-guardian -l app.kubernetes.io/name=repo-guardian --since=24h \
  | grep -c "all required files present"
```

Each occurrence is a candidate stale-PR event. Cross-reference
against `gh pr list --search 'head:repo-guardian/add-missing-files
state:open'` across installed repos to count the actual orphans.

## Conclusion

**Answer:** Yes, the gap is real, and it is **isolated to the
file-rule + PR path**. Setting rules, branch_protection rules, and
the four built-in reconcilers all short-circuit cleanly when target
state matches current state.

The PR-mediated path has three distinct sub-bugs that compose:

1. **Orphaned files on the reconcile branch** — files added in a
   prior sweep are never removed when the rule becomes satisfied.
2. **PR body becomes stale** — generated once at PR creation; never
   regenerated when the actionable set shrinks.
3. **PR is never closed** — when the actionable set becomes empty
   the engine logs "all required files present" and walks away,
   leaving the open PR untouched.

The hypothesis is fully confirmed.

## Recommendation

Open an IMPL plan to fix all three sub-bugs together. They share
enough mechanism (the `createOrUpdatePRFromPolicy` rewrite) that
splitting them into separate PRs would just create coordination
overhead.

Sketch of the work, scoped to the policy path first:

1. Add a "satisfied-this-sweep" set derived from
   `policy.FileRules ∖ actionable`, restricted to rules whose
   `Target` was previously committed to the reconcile branch.
2. Add `client.DeleteFile(ctx, owner, repo, branch, path, sha,
   msg)` to the github wrapper (the underlying
   `Repositories.DeleteFile` exists; just needs a thin wrapper).
3. In the update path:
   - For each target in the satisfied-set: `DeleteFile`.
   - Regenerate the PR body via `buildPRBodyFromPolicy(actionable)`
     and call a new `client.UpdatePullRequest(ctx, owner, repo,
     prNumber, &UpdatePR{Body: ...})`.
4. When `len(actionable) == 0` and an existing PR is found: close
   the PR (`client.ClosePullRequest`) and delete the reconcile
   branch (`client.DeleteBranch`).
5. Add a new metric `prs_closed_total{org}` to count auto-closes.
6. Mirror the fix in the legacy `engine.go` path (lower priority).
7. Decide policy on auto-close: do operators want repo-guardian
   closing PRs that humans may be reviewing? Suggested default:
   close with a comment ("All required files now present on
   default branch — closing automatically"). Make it gated by a
   new HCL knob `guardian.auto_close_satisfied = true` (default
   true) for operators who want the legacy behavior.

Open question to discuss before writing IMPL: should
`DeleteFile` work as a separate commit per file (cleaner history)
or as a single squashed commit ("sync to current actionable set")?
The latter is faster on multi-file repos but loses per-file
attribution in the branch history.

## References

- [examples/guardian-full.hcl](../../examples/guardian-full.hcl)
  — the reference config that produced this scenario.
- [INV-0003](0003-pre-existing-branch-422-on-subsequent-reconciles.md)
  — adjacent stale-branch bug; precedent for the
  "investigate then fix in one PR" cadence (PR #69).
- [DESIGN-0007](../design/0007-reconciler-interface-and-push-event-handler.md)
  — push handler for watched paths; relevant because watched-path
  pushes are one way the gap manifests faster than the sweep cadence.
- [PR #79](https://github.com/donaldgifford/repo-guardian/pull/79)
  — the open homelab PR that motivates this investigation.
- WARN comment at
  [`internal/checker/engine_policy.go:546-558`](../../internal/checker/engine_policy.go)
  — already acknowledges a closely-related stale-branch class of bug
  (referencing this conversation in PR #71).
