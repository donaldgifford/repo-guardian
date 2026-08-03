---
id: INV-0014
title: "Orphan cleanup deletes files the default branch legitimately owns"
status: Concluded
author: Donald Gifford
created: 2026-08-03
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0014: Orphan cleanup deletes files the default branch legitimately owns

**Status:** Concluded
**Author:** Donald Gifford
**Date:** 2026-08-03

<!--toc:start-->
- [Question](#question)
- [What it looks like in practice](#what-it-looks-like-in-practice)
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
- [Blast radius](#blast-radius)
- [Recommendation](#recommendation)
  - [R1. Fix the test fake first, and let it reproduce the bug](#r1-fix-the-test-fake-first-and-let-it-reproduce-the-bug)
  - [R2. Fix discoverOrphans](#r2-fix-discoverorphans)
  - [R3. Operational, ahead of the code](#r3-operational-ahead-of-the-code)
  - [Scope still to decide](#scope-still-to-decide)
- [Testing strategy: breaking the circularity](#testing-strategy-breaking-the-circularity)
  - [The circularity](#the-circularity)
  - [The fix: one contract suite, two implementations](#the-fix-one-contract-suite-two-implementations)
  - [Make the fake's blind spots loud, not plausible](#make-the-fakes-blind-spots-loud-not-plausible)
  - [The existing CLAUDE.md rule is too narrow](#the-existing-claudemd-rule-is-too-narrow)
  - [Tiered plan](#tiered-plan)
- [What shipped](#what-shipped)
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

## What it looks like in practice

Take a repository that already has `.github/CODEOWNERS` and is missing
`.github/dependabot.yml`.

**First sweep**

1. CODEOWNERS is present → the rule is satisfied, nothing to do.
2. dependabot.yml is missing → actionable.
3. repo-guardian creates `repo-guardian/add-missing-files` **from main**.
   A new branch is a copy of main, so the branch now contains
   `.github/CODEOWNERS` — not because repo-guardian added it, but
   because main has it.
4. It writes `dependabot.yml` to the branch and opens the PR.

Correct so far.

**Second sweep** — the PR is open, so orphan cleanup runs. For every
rule that is *not* actionable it asks one question:

> Is this rule's file sitting on the PR branch even though the rule is
> satisfied? Then I must have added it in an earlier sweep and it is no
> longer needed — delete it.

It asks whether `.github/CODEOWNERS` is on the branch. It is. So it
deletes it, and the PR now reads *"add dependabot.yml, delete
CODEOWNERS."*

The file is on the branch because **the branch is a copy of main**, not
because repo-guardian put it there — and nothing in the code can tell
those two apart. repo-guardian never recorded "I added this file," so
"it is on the branch" is the only evidence available, and that evidence
does not mean what the code takes it to mean.

**The case the feature still needs to handle.** A repository with no
CODEOWNERS anywhere: repo-guardian writes `.github/CODEOWNERS` to the
branch and opens a PR; a human then commits `CODEOWNERS` at the repo
root straight to main. Now the rule is satisfied by the root file,
`.github/CODEOWNERS` is on the branch, and it is **not** on main — so
repo-guardian is the only party that could have put it there. Deleting
it is correct, and the PR stops proposing a duplicate. Any fix has to
keep this working.

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

## Recommendation

Two changes, in this order. The ordering is not cosmetic: done this way
the first change *proves* the bug and the second fixes it, so the
regression test is non-vacuous by construction rather than by
assertion.

### R1. Fix the test fake first, and let it reproduce the bug

Finding D established that the failing state — a file on the reconcile
branch that repo-guardian did not write — is inexpressible in the
current fake. Until that changes, **any regression test written for
this bug would pass before the fix**, which is the definition of a
vacuous test.

Three ways to address it:

**Option A — seed `branchContents` directly in the new test.** One line
of test setup, no fake change.

*Rejected.* It makes exactly one test correct and leaves the trap armed
for everyone else. Nobody wrote that line for the existing convergence
tests precisely because nobody knew the invariant existed; a fix that
depends on future authors knowing it does not hold.

**Option B — teach `GetContentsOnBranch` that branches inherit from the
default branch.** *Recommended.*

```go
func (m *mockClient) GetContentsOnBranch(_ context.Context, owner, repo, path, branch string) (string, bool, error) {
	if m.getContentsOnBranchErr != nil {
		return "", false, m.getContentsOnBranchErr
	}

	key := owner + "/" + repo + "/" + branch + "/" + path

	// A tombstone means the path was deleted on this branch and must NOT
	// be inherited back from the default branch.
	if m.branchDeleted[key] {
		return "", false, nil
	}

	if sha, ok := m.branchContents[key]; ok {
		return sha, true, nil
	}

	// The reconcile branch is cut from the default branch, so anything on
	// default is visible on it unless the branch overrode or deleted it.
	// Modelling this is what makes INV-0014's failing state expressible.
	if m.contents[owner+"/"+repo+"/"+path] {
		return "sha-inherited-" + path, true, nil
	}

	return "", false, nil
}
```

The tombstone map is load-bearing, not incidental. `DeleteFile`
currently does a bare `delete(m.branchContents, key)`
(`engine_test.go:338`); with an inheritance fallback and no tombstone, a
file deleted from the branch would immediately reappear from the default
branch, breaking the `restoreInverseOrphans` tests — which depend on
exactly the state "deleted on branch, present on default". So
`DeleteFile` must set the tombstone and `CreateOrUpdateFile` must clear
it.

One more required edit: `GetContentsOnBranch` currently early-returns
`false` when `branchContents == nil` (`engine_test.go:320`). That guard
has to go, or every test that leaves the map nil silently skips
inheritance.

**Expect existing tests to fail, and read that as the reproduction.**
Most convergence tests never populate `branchContents`, so today they
run in a world where the reconcile branch is empty except for what
repo-guardian wrote. Giving the fake inheritance puts them in the real
world, and any that assert an orphan deletion against a file the repo
owns will go red. That red is INV-0014 reproducing under test. Triage
each failure as either "this is the bug" or "this fixture now needs
adjusting"; do not blanket-adjust fixtures to restore green.

**Option C — replace the string maps with a small git-shaped model**
(default tree + per-branch overlay of adds/deletes).

Defensible, and strictly more faithful. Rejected *for this fix* because
CLAUDE.md records six behaviours across three fake sites that depend on
current semantics (IMPL-0021 task 7.1), and rebuilding them under
time pressure on a data-destructive bug trades a small risk for a large
one. Worth revisiting later on its own schedule.

### R2. Fix `discoverOrphans`

**Option 1 — probe the default branch before the reconcile branch.**
*Recommended.* Mirrors `restoreInverseOrphans` (Finding E'), which
already does this correctly twelve lines away.

```go
// A path that exists on the default branch is never an orphan: deleting
// it from the reconcile branch makes the PR propose removing a file the
// repository legitimately owns (INV-0014).
onDefault, err := client.GetContents(ctx, owner, repo, r.Target)
if err != nil {
	log.Warn("orphan discovery: default-branch probe failed, treating as still-actionable",
		"rule", r.Name, "path", r.Target, "err", err)

	continue
}

if onDefault {
	continue
}

// ... existing GetContentsOnBranch probe unchanged
```

The rule is unconditional and easy to state: **if the default branch has
the path, never delete it.** There is no case where deleting a
default-branch file from the reconcile branch is the right action —
doing so always produces a PR that proposes removing a real file.

It still catches the genuine orphan. Rule satisfied by a root-level
`CODEOWNERS` on main, `.github/CODEOWNERS` written to the branch by an
earlier sweep, `.github/CODEOWNERS` absent from main: not on default →
still an orphan → deleted. That is the convergence case IMPL-0013 Phase
3 was built for, and it is preserved.

Ordering matters for cost: probing default *first* means the common case
(rule satisfied because the file is on main) costs one API call instead
of two. Genuine orphans cost two. The probe only runs for
non-actionable rules on repos that already have an open PR, so fleet-wide
impact is small.

Fail-safe direction is the same as everywhere else in this file: a probe
error means "do not delete."

**Option 2 — compare blob SHAs between branch and default.** *Rejected,
and worth recording because it is the tempting "more precise" answer.*

The idea is that a differing SHA proves repo-guardian authored the branch
copy. It does — and it still must not delete. If main holds a human-
authored `.github/CODEOWNERS` and the branch holds repo-guardian's
version, deleting from the branch proposes removing main's file. SHA
comparison is more precise about provenance and wrong about the action.
Presence on the default branch, not authorship, is the correct predicate.

**Option 3 — record provenance properly** (track which files
repo-guardian wrote to the branch, in the store or via a commit-message
convention).

The "real" fix for the root cause named in Finding A. Rejected as the
immediate remedy: it needs persistent state or branch-history parsing,
and Option 1 makes the destructive outcome unreachable without it. Worth
a follow-up if orphan cleanup ever needs to distinguish cases Option 1
deliberately collapses.

**Option 4 — gate orphan cleanup behind a config flag, defaulting off.**

**Accepted (operator decision, 2026-08-03) — ships alongside Option 1,
not instead of it.**

The risk/benefit is lopsided: the feature prevents a stale PR body, and
its failure mode deletes a real file. Option 1 makes the destructive
path unreachable, so the flag is defence in depth on a fleet that has
already been burned once — an operator who does not trust the fix can
turn the whole behaviour off without downgrading.

Shape follows the existing `auto_close_pr` precedent exactly, which is
the closest analogue (a `guardian {}` bool gating a PR-mutating
behaviour, default-on, env override): an `orphan_cleanup` attribute on
the `guardian {}` block plus an `ORPHAN_CLEANUP` env override. Per
INV-0010, adding a `GuardianConfig` field needs **three** edits in
lockstep — `guardianBodySchema`, a `setGuardianAttr` case, and a
`mergeGuardianConfig` carry — and missing the merge is exactly how
`auto_close_pr` silently did nothing. Default is **on**, because with
Option 1 in place the behaviour is correct and turning it off by default
would regress the INV-0005 convergence fix for every operator.

### R3. Operational, ahead of the code

1. Close any open repo-guardian PR carrying a `chore: remove ...`
   commit. Confirmed closed already per the operator, pending the scan.
2. Ship as a patch release once R1+R2 land. Every repo in a fleet meets
   the precondition on its second sweep, so this is not a "wait for the
   next feature release" fix.
3. Correct the misattributed `PR #71` comment at the
   `updateReconcileBranch` call site while in the file (Finding B).

### Scope still to decide

- Do the `scheduler` and `reconciler` fakes need the same inheritance
  fix, or is `internal/checker` the only package whose tests exercise
  branch-vs-default semantics? Current reading is checker-only, but that
  should be checked rather than assumed.
- Whether Option 4's flag ships in the same patch or not at all.

## Testing strategy: breaking the circularity

R1 fixes the fake's model of branch inheritance. That corrects *this*
wrong belief and leaves the mechanism that produced it fully intact. The
mechanism is worth naming, because it will produce the next one.

### The circularity

The fake is a hand-written encoding of our mental model of GitHub. Our
model said *"a branch contains what we put on it."* GitHub says *"a
branch contains what main had, plus what we put on it."* Every test then
validated the engine against the model — so the tests agreed with the
code because both were built from the same wrong belief. **Nothing
inside that loop is capable of disagreeing.**

Note the shape: the tests were not sloppy and the assertions were not
weak. A correct assertion evaluated against a fictional world is still
fiction. No amount of additional testing *of that kind* would have found
this.

### The fix: one contract suite, two implementations

State the GitHub behaviours we depend on **once**, as tests, and run
them against both the fake and real GitHub. If the fake drifts from
reality the shared test fails, so the fake cannot quietly encode a
fantasy — it is held to the same contract as the real thing.

```go
// Same bodies, two drivers.
func contractBranchInheritsDefaultContents(t *testing.T, c ghclient.Client)
func contractDeleteOnBranchLeavesDefaultUntouched(t *testing.T, c ghclient.Client)
func contractUpdateBranchMergesDefaultIn(t *testing.T, c ghclient.Client)
func contractFileAddedToBranchIsNotOnDefault(t *testing.T, c ghclient.Client)
```

- **Against the fake** — runs in `make test`, milliseconds, every PR.
- **Against real GitHub** — `//go:build ghcontract`, a scratch org and a
  throwaway repo, run nightly or pre-release.

The real-GitHub driver is the only true oracle anywhere in this
codebase; every other test asks us what we believe. It is also small,
because it does not test the engine — it tests *our assumptions about
the API*. "Does creating a branch from main make main's files readable
on that branch?" is a single test, and it would have caught INV-0014
before IMPL-0013 shipped.

There is direct precedent: Postgres and Valkey both have real-backend
integration suites under a build tag, with contract assertions living in
each backend's own test file (CLAUDE.md § single-backend contract test
convention). GitHub is currently the only major dependency with **no
real-thing test at all** — the `httptest.Server` suite in
`internal/github/client_test.go` tests our request/response plumbing
against handlers we also wrote, which is the same circularity one layer
down.

### Make the fake's blind spots loud, not plausible

`GetContentsOnBranch` returned `false` for a state it could not
represent: a confident, plausible, wrong answer. The repo has already
learned this lesson once — the generated mocks panic on un-overridden
methods, explicitly because "a silent zero value is how the IMPL-0013 P4
vacuous-assertion bug happened" (CLAUDE.md). The same reasoning applies
here and was never carried across.

Concretely: if a test never declares the repository's default-branch
contents, the fake should not assume "empty." It should fail and force
the test to say what the repo contains. A fake that cannot represent a
state must refuse to answer questions about it.

### The existing CLAUDE.md rule is too narrow

The rule written after the last occurrence of this failure shape:

> when a production code path is shaped like "list existing → decide to
> skip-or-act → mutate," the mock's list method MUST return prior
> writes **from the same mock instance**.

That covers state **the test** created. INV-0014 is state **the world**
created — a file that existed before repo-guardian ever ran. Identical
failure shape, entirely outside the rule's scope, which is why
following the rule did not prevent it.

Proposed extension, to land with the fix:

> ...and MUST also return state the mock never wrote but the real system
> would have — repository state that pre-existed the test. Where a fake
> cannot represent such state, it MUST fail loudly rather than return a
> plausible default.

### Tiered plan

**Now, shipping with the fix**

1. Fake inheritance + tombstones (R1).
2. The four contract tests above, running against the fake.
3. A safety-net assertion in the checker test harness: no test may
   finish having deleted a path that exists on the default branch. This
   catches the whole class rather than this one instance, and it is
   stated in domain terms, so it keeps working as the fake improves.
4. The CLAUDE.md rule extension above.

**Next, deferred out of the patch (operator decision, 2026-08-03)**

5. The same contract tests against a scratch org under `//go:build
   ghcontract`. **Deferred**: the operator will verify this fix manually
   against real repositories so the patch can ship promptly, rather than
   holding a data-destructive fix behind new test infrastructure.

   This remains the step that actually breaks the circle. Items 1-4 are
   still our model checking our model, and manual verification confirms
   *this* fix without leaving anything behind that would catch the next
   one. Track it as follow-up work, not as closed.

**Later, only if it earns it**

6. Replace the interface-level fake with an HTTP-level fake GitHub that
   stores refs and trees, so branch semantics become structurally
   impossible to get wrong rather than correct-by-maintenance. Real
   work; explicitly not under this bug's time pressure.

## What shipped

| Commit | Change |
|---|---|
| `26c5869` | **Prevention** — `discoverOrphans` probes the default branch first; a path the default branch owns is never an orphan (R2 option 1) |
| `6840117` | **Kill switch** — `orphan_cleanup` HCL attribute, `ORPHAN_CLEANUP` env, `policy.orphanCleanup` chart value, default on (R2 option 4) |
| `34005a8` | **Repair** — `restoreInverseOrphans` generalised beyond absent rules, so branches already carrying the deletion self-heal on the next sweep |

The fake-fidelity work in R1 shipped inside `26c5869`: `GetContentsOnBranch`
now inherits from the default branch, with a tombstone map so deletions
stick. Fixing it reproduced the bug immediately with production code
untouched — `TestConvergence_RenovateFirst_RemovesDependabot` began deleting
`renovate.json`, the reported failure on a different rule.

Every behavioural change was neutralise-verified. Three of the tests written
during this work were vacuous on first draft and were rebuilt after the
neutralisation showed them passing against broken code — the same failure
shape as the original defect, one level up.

**Not shipped, tracked as follow-up:** the real-GitHub contract suite
(Testing strategy, tier 2). Without it the repo is still in the position
described under "The circularity" — our model checking our model — and
nothing yet catches the next wrong belief about the GitHub API.

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
