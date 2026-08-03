---
id: INV-0014
title: "Orphan cleanup deletes files the default branch legitimately owns"
status: In Progress
author: Donald Gifford
created: 2026-08-03
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0014: Orphan cleanup deletes files the default branch legitimately owns

**Status:** In Progress
**Author:** Donald Gifford
**Date:** 2026-08-03

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [A. discoverOrphans has no provenance signal](#a-discoverorphans-has-no-provenance-signal)
  - [B. UpdatePRBranch guarantees the false positive](#b-updateprbranch-guarantees-the-false-positive)
  - [C. The blast radius is Target-shaped, not Paths-shaped](#c-the-blast-radius-is-target-shaped-not-paths-shaped)
  - [D. Why the test suite cannot express this](#d-why-the-test-suite-cannot-express-this)
  - [E. Latent since IMPL-0013 Phase 3](#e-latent-since-impl-0013-phase-3)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [Open questions for the operator](#open-questions-for-the-operator)
- [References](#references)
<!--toc:end-->

## Question

On a second sweep against a repository that already has a
`repo-guardian/add-missing-files` PR open, repo-guardian commits a
deletion of `.github/CODEOWNERS` with the message
`chore: remove .github/CODEOWNERS (rule "codeowners" satisfied on default branch)`
— even though the file exists on the default branch and the rule is
therefore satisfied and should be a no-op.

Why does a *satisfied* rule produce a *deletion*, and what is the blast
radius?

## Hypothesis

`discoverOrphans` cannot distinguish a file repo-guardian itself wrote
to the reconcile branch in an earlier sweep (a true orphan) from a file
that is visible on that branch only because the branch was cut from —
and is continuously merged with — the default branch. Any rule that
(a) is not actionable and (b) has its `Target` path present on the
default branch is therefore misclassified as an orphan and deleted.

## Context

Reported from production. Timeline as described by the operator: an
initial run behaved correctly, an upgrade was applied, and the following
weekend's run began emitting delete commits against repositories whose
`CODEOWNERS` file already existed.

This is a **data-destructive** defect, not a cosmetic one: the deletion
lands on the reconcile branch of an *open* PR. Merging that PR removes
`.github/CODEOWNERS` from the default branch. Because CODEOWNERS drives
review routing and is frequently a branch-protection input, the
downstream effect of a merge is silent loss of required-reviewer
enforcement.

**Triggered by:** production report, 2026-08-03. Related to IMPL-0013
Phase 3 (orphan cleanup) and INV-0011 B4 / PR #71 (reconcile-branch
freshening).

## Approach

1. Trace the commit message back to its producer to identify the code
   path with certainty rather than by inference.
2. Read `discoverOrphans` and determine what evidence it uses to decide
   a file is an orphan.
3. Establish whether the preceding branch-freshening step makes the
   misclassification possible, likely, or certain.
4. Determine which rules and which repository layouts are affected, and
   which are not.
5. Explain why the existing convergence test suite passes.
6. Identify the release in which the defect became reachable.

## Environment

| Component | Version / Value |
|-----------|-----------------|
| Branch analysed | `main` @ `b1bd32e` |
| Defect introduced | `ee80603` (IMPL-0013, PR #82) |
| First affected release | appVersion 1.7.0 / chart 0.6.0 |
| Affected rule (built-in defaults) | `codeowners`, `Target = .github/CODEOWNERS` |
| Code paths | `internal/checker/drift.go`, `internal/checker/engine_pr.go` |

## Findings

### A. `discoverOrphans` has no provenance signal

`internal/checker/drift.go:101` is the whole decision:

```go
sha, exists, err := client.GetContentsOnBranch(ctx, owner, repo, r.Target, BranchName)
...
if !exists {
    continue
}

orphans = append(orphans, orphanFile{RuleName: r.Name, Path: r.Target, SHA: sha})
```

The predicate is *"the rule is not actionable **and** its target path
exists on the reconcile branch."* That is presumed to mean "we wrote
this file in an earlier sweep and it is no longer needed." It does not.
The reconcile branch is cut from the default branch, so **every** file
the default branch has is present on the reconcile branch. The
predicate matches:

1. a file repo-guardian committed in an earlier sweep — a true orphan;
2. a file that exists on the default branch and always did — a false
   positive, and the reported bug.

The two are indistinguishable from the branch alone. Provenance is
never recorded anywhere, and the branch contents cannot supply it.

The corresponding cleanup at `drift.go:139` is the commit the operator
observed:

```go
msg := fmt.Sprintf("chore: remove %s (rule %q satisfied on default branch)", o.Path, o.RuleName)
```

The message is accurate about *why* the rule stopped being actionable
and wrong about what should follow from it. "Satisfied on the default
branch" is precisely the state in which the file must be left alone.

### B. `UpdatePRBranch` guarantees the false positive

`engine_pr.go:161-163` runs immediately before orphan discovery:

```go
if existingPR != nil {
    updateReconcileBranch(ctx, log, client, owner, repo, existingPR.Number)
}
```

This merges the default branch into the reconcile branch — the INV-0011
B4 fix (PR #71), added so the file sync always operates on current
content and cannot silently revert a manual edit on merge.

That fix is correct in isolation, and it converts this defect from
*possible* to *certain*. Any file on the default branch is merged onto
the reconcile branch a few lines before `discoverOrphans` asks whether
that path exists on the reconcile branch. The answer is now
unconditionally yes. Two individually-sound behaviours compose into a
deletion.

### C. The blast radius is `Target`-shaped, not `Paths`-shaped

The satisfaction check and the orphan check use different fields.
`policy.BuiltinDefaults()` (`internal/policy/defaults.go:118-129`):

```go
Paths:  []string{pathCodeownersRoot, pathCodeownersGitHub, "docs/CODEOWNERS"},
Target: pathCodeownersGitHub,   // ".github/CODEOWNERS"
```

`evaluateRule` satisfies the rule if **any** of the three paths exist.
`discoverOrphans` only ever looks at `Target`. So:

| Repository layout | Rule state | Orphan check | Outcome |
|---|---|---|---|
| `.github/CODEOWNERS` present | satisfied | `Target` found | **file deleted** |
| root `CODEOWNERS` only | satisfied | `Target` absent | safe |
| `docs/CODEOWNERS` only | satisfied | `Target` absent | safe |
| no CODEOWNERS | actionable | rule in actionable set, skipped | safe |

This yields a sharp, falsifiable prediction: **only repositories whose
CODEOWNERS lives at `.github/CODEOWNERS` are affected.** Repositories
with a root-level `CODEOWNERS` see nothing. That prediction is the
cheapest way to confirm this diagnosis against the real fleet.

The same reasoning applies to every non-actionable file rule with a
`Target` that the default branch happens to contain — `codeowners` is
not special, it is merely the one whose `Target` most often already
exists. `dependabot`, `renovate_config` and any operator-defined rule
are subject to the identical failure.

Two further preconditions, both matching the report:

- An open repo-guardian PR must already exist — orphan discovery is
  only reachable from the `existingPR != nil` branch. This is why the
  first run was clean and the second was not; it is a second-sweep-only
  defect by construction.
- At least one *other* rule must still be actionable. With an empty
  actionable set the code takes the `autoClosePR` path instead and no
  deletion occurs.

### D. Why the test suite cannot express this

`internal/checker/engine_test.go:315` backs `GetContentsOnBranch` with
a `branchContents` map, and the only writer to that map is the fake's
own `CreateOrUpdateFile` (`engine_test.go:167-172`). The fake models
"repo-guardian wrote this file to this branch" and nothing else. The
fake's `UpdatePRBranch` (`engine_test.go:364`) appends to a call-log and
does not touch `branchContents` at all.

So the fake has no representation of the failing state — *a file present
on the reconcile branch that repo-guardian did not put there*. The
convergence tests are not weak here; they are describing a world in
which the bug cannot occur. Every assertion in
`convergence_test.go` about orphan cleanup passes because in the fake's
model the only files on the branch **are** orphans.

This is a textbook instance of the mock-fidelity rule already in
CLAUDE.md ("when a production path is shaped like list → decide → mutate,
the mock's list method must return what production's list would
return"). The existing rule is stated in terms of *the mock's own prior
writes*; this case shows it needs to extend to **state the mock never
wrote** — inherited default-branch content.

### E. Latent since IMPL-0013 Phase 3

`internal/checker/drift.go` was added whole in `ee80603` (IMPL-0013,
PR #82), shipped as appVersion 1.7.0 / chart 0.6.0. The defect has been
present since that release. It requires a second sweep against a repo
with an open PR and a `Target`-path file on the default branch, which is
why it can sit unnoticed across many deployments and then appear all at
once when a fleet reaches its second sweep — matching "ran it again over
the weekend."

## Conclusion

**Answer:** Hypothesis confirmed.

`discoverOrphans` treats "path exists on the reconcile branch" as proof
that repo-guardian authored it. Because the reconcile branch inherits
the default branch — and is force-freshened from it by
`updateReconcileBranch` immediately beforehand — a satisfied rule whose
`Target` exists on the default branch is misclassified as an orphan and
deleted, producing an open PR that proposes removing a legitimate file.

The bug is real, destructive, and reachable in the default
configuration. It is not caused by the recent upgrade in the sense of
being newly introduced by it — it has been latent since 1.7.0 — but an
upgrade that restarted sweeps, or a fleet reaching its second sweep,
would surface it exactly as reported.

## Recommendation

Not yet decided — this section is the next working session. The leading
candidate is to make the orphan predicate compare against the default
branch rather than the reconcile branch alone: a path that exists on the
default branch is by definition not an orphan, because deleting it
proposes removing a file the repository legitimately owns. That is a
small, local change to `discoverOrphans` with an obvious fail-safe
direction (when in doubt, do not delete), and it composes correctly with
`updateReconcileBranch` rather than fighting it.

Whatever fix is chosen, three things are required alongside it:

1. **Operator guidance now, before the fix ships.** Any open
   repo-guardian PR containing a `chore: remove ...` commit must not be
   merged. This needs to go out ahead of the code change.
2. **A test fake that can represent inherited content.** Until
   `branchContents` (or its replacement) models default-branch
   inheritance, no regression test for this can be non-vacuous — it
   would pass before the fix. Per standing practice the regression test
   must be verified by neutralising the fix and watching it fail.
3. **A blast-radius query.** Identify already-open PRs across the fleet
   that carry an orphan-deletion commit, so remediation is not limited
   to whatever the operator happened to notice.

## Open questions for the operator

1. Do the affected repositories keep CODEOWNERS at `.github/CODEOWNERS`
   rather than at the repository root? Finding C predicts only the
   former are affected — a cheap confirmation of the whole diagnosis.
2. Which appVersion was running before the upgrade, and which is running
   now? If the prior version was < 1.7.0, that fully explains "first run
   fine, second run bad" without any second mechanism.
3. Were any of these delete-commit PRs merged? That determines whether
   this is a "stop the bleeding" situation or a "restore deleted files"
   one.
4. Are the affected repos ones where some *other* rule (dependabot,
   renovate) is still actionable? The model says they must be; a
   counter-example would mean there is a second path to investigate.

## References

- IMPL-0013 — orphan cleanup and PR convergence (PR #82, `ee80603`)
- INV-0011 B4 / PR #71 — reconcile-branch freshening via
  `UpdatePRBranch`
- INV-0005 — the PR-drift surface that motivated IMPL-0013 Phase 3
- `internal/checker/drift.go` — `discoverOrphans`, `cleanupOrphans`
- `internal/checker/engine_pr.go` — `createOrUpdatePRFromPolicy`,
  `updateReconcileBranch`
- CLAUDE.md § "Mock-fidelity rule for list-then-act idempotency tests"
