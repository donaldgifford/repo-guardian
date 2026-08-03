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
  - [B. The branch inherits the file; UpdatePRBranch only widens the window](#b-the-branch-inherits-the-file-updateprbranch-only-widens-the-window)
  - [C. The blast radius is Target-shaped, not Paths-shaped](#c-the-blast-radius-is-target-shaped-not-paths-shaped)
  - [D. Why the test suite cannot express this](#d-why-the-test-suite-cannot-express-this)
  - [E'. The fix already exists in this file, one function down](#e-the-fix-already-exists-in-this-file-one-function-down)
  - [E. Latent since IMPL-0013 Phase 3](#e-latent-since-impl-0013-phase-3)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [Blast radius](#blast-radius)
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
initial run behaved correctly, updates were applied, and the following
weekend's run began emitting delete commits against repositories whose
`CODEOWNERS` file already existed. The operator reports running v1.10.1
and confirms the affected repositories keep CODEOWNERS at
`.github/CODEOWNERS`.

This is a **data-destructive** defect, not a cosmetic one: the deletion
lands on the reconcile branch of an *open* PR. Merging that PR removes
`.github/CODEOWNERS` from the default branch. Because CODEOWNERS drives
review routing and is frequently a branch-protection input, the
downstream effect of a merge is silent loss of required-reviewer
enforcement.

**Triggered by:** production report, 2026-08-03. Related to IMPL-0013
Phase 3 (orphan cleanup, PR #82) and IMPL-0021 (reconcile-branch
freshening, PR #169) — see Finding B on the misattributed PR number in
the source comment.

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
| Operator-reported running version | v1.10.1 |
| `updateReconcileBranch` introduced | `49a6424` / PR #169 / v1.10.1 |
| Repo layout confirmed by operator | `.github/CODEOWNERS` (matches Finding C) |
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

### B. The branch inherits the file; `UpdatePRBranch` only widens the window

The reconcile branch is created from the default branch HEAD —
`engine_pr.go:143` passes `baseSHA` to `CreateBranch`, which is a plain
`Git.CreateRef`. So from the instant it exists, the branch contains
every file the default branch had, `.github/CODEOWNERS` included. No
merge step is required for the false positive: **it is already certain
for any repo that had the file at branch-creation time**, and has been
since IMPL-0013 shipped.

`engine_pr.go:161-163` then runs immediately before orphan discovery:

```go
if existingPR != nil {
    updateReconcileBranch(ctx, log, client, owner, repo, existingPR.Number)
}
```

This merges the default branch into the reconcile branch so the file
sync always operates on current content and cannot silently revert a
manual edit on merge. It is correct in isolation, and it *widens* the
trigger rather than creating it: it additionally captures repos where
the file appeared on the default branch **after** the reconcile branch
was cut. Without it those repos would escape; with it they do not.

Provenance note, because the source comment is misleading: that call
site is annotated `(INV-0011 B4, PR #71)`, but `git log -S` puts its
introduction at `49a6424` — PR **#169**, IMPL-0021 — which
`git describe --contains` resolves to **v1.10.1**. Anyone dating the
regression from the comment will land on the wrong release.

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

### E'. The fix already exists in this file, one function down

`restoreInverseOrphans` — the absent-mode mirror added later by
IMPL-0019 — does the check `discoverOrphans` is missing.
`restoreRulePaths` (`drift.go:214-235`) probes the **default branch
first** and only then the reconcile branch:

```go
onDefault, err := client.GetContents(ctx, owner, repo, path)   // default-branch-only helper
...
_, onBranch, err := client.GetContentsOnBranch(ctx, owner, repo, path, BranchName)
```

Both probes fail safe: any API error leaves the path untouched.

So the newer mirror function establishes the correct pattern — never
act on a path from branch state alone, always cross-reference the
default branch — while the older function it was explicitly modelled on
never had it. This is strong corroboration that the missing probe is an
oversight in `discoverOrphans` rather than a deliberate asymmetry, and
it means the fix has a working in-repo reference implementation twelve
lines away.

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
configuration. It has been latent since 1.7.0 and is present in the
operator's reported v1.10.1; no version change is needed to explain the
timeline. The single precondition that separates "first run fine" from
"second run destructive" is the existence of an open repo-guardian PR,
which only holds from the second sweep onward.

Finding C's prediction — that only `.github/CODEOWNERS` repos are
affected — was confirmed by the operator against the real fleet, which
moves this from a plausible reading of the code to a diagnosis.

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

## Blast radius

Operator reports the affected PRs were **closed, not merged**. If that
holds fleet-wide this is containment, not recovery: the deletion never
reached a default branch, and closing the PR is sufficient. It is worth
verifying rather than trusting recall, because the failure mode is
silent — a merged orphan-deletion looks like an ordinary chore commit in
the repo's history.

`scripts/inv-0014-scan.sh ORG [ORG...]` measures it. Strictly read-only:
no merges, no closes, no branch deletions. For every PR in the org whose
head is `repo-guardian/add-missing-files` it reports the PR state, every
path removed by a `chore: remove ...` commit, and — the question that
actually matters — whether that path exists on the default branch
**today**:

| PR state | Path on default | Meaning |
|---|---|---|
| merged | absent | confirmed data loss; restore from history |
| open | present | containment; close before anyone merges |
| closed | present | no action — the expected state per the report |
| any | probe failed | indeterminate, never assumed safe |

Both fragile parts were verified before use: the commit-message regex
against the exact format `cleanupOrphans` emits (and against add-mode,
restore-mode and merge commits, which must not match), and the
200/404 status extraction against a public repository.

## Open questions for the operator

1. ~~Do the affected repositories keep CODEOWNERS at
   `.github/CODEOWNERS`?~~ Confirmed — matches Finding C's prediction.
2. ~~Which appVersion was running?~~ v1.10.1, which contains the
   defect. No version change is needed to explain the timeline.
3. ~~Were any of these delete-commit PRs merged?~~ Operator reports
   closed, not merged — containment rather than recovery. To be
   confirmed fleet-wide with `scripts/inv-0014-scan.sh`.
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
