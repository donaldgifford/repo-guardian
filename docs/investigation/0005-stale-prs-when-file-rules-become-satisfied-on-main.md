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
- [Testing strategy](#testing-strategy)
  - [Tier 1 — Multi-sweep unit tests (do during the fix)](#tier-1--multi-sweep-unit-tests-do-during-the-fix)
  - [Tier 2 — Diagnostic metric + Prometheus alert (separate small PR, before the fix)](#tier-2--diagnostic-metric--prometheus-alert-separate-small-pr-before-the-fix)
  - [Tier 3 — Integration test scaffold using httptest.Server (medium effort, broader payoff)](#tier-3--integration-test-scaffold-using-httptestserver-medium-effort-broader-payoff)
  - [Tier 4 — End-to-end against a real GitHub org (don't bother for this bug)](#tier-4--end-to-end-against-a-real-github-org-dont-bother-for-this-bug)
  - [What we are NOT proposing](#what-we-are-not-proposing)
  - [Recommendation](#recommendation-1)
- [Observability and operator UX (extended)](#observability-and-operator-ux-extended)
  - [Stale-PR gauge bucketed by rule × age](#stale-pr-gauge-bucketed-by-rule--age)
  - [Reconciliation comments on the PR](#reconciliation-comments-on-the-pr)
  - [Anti-pattern: do not use PR comments for engine state recovery](#anti-pattern-do-not-use-pr-comments-for-engine-state-recovery)
  - [Updated IMPL sequence](#updated-impl-sequence)
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

## Testing strategy

The bug is a state-transition class — single-sweep tests can't catch
it. What matters is what the engine does on sweep N+1 given the
artifacts left by sweep N. Four options, ranked by ROI:

### Tier 1 — Multi-sweep unit tests (do during the fix)

The existing `mockClient` in `internal/checker/engine_test.go`
already models state in maps (`contents`, `branchSHAs`,
`openPRs`, etc.) and lets a test mutate that state between
engine calls. The fix introduces three new interface methods
(`DeleteFile`, `UpdatePullRequest`, `ClosePullRequest`); each
needs a recording field on the mock plus an assertion in the
test. Cheap because we're writing it alongside the fix.

Three scenarios to lock in:

1. **Orphan cleanup** — Sweep 1: rule `codeowners` actionable;
   `.github/CODEOWNERS` written to branch. Mutate mock so the
   file exists on `main`. Sweep 2: assert
   `mock.deletedFiles` contains the target and
   `mock.createdFiles` does NOT include it.
2. **Body refresh on partial satisfaction** — Sweep 1: three
   rules actionable; PR created with body listing all three.
   Mutate mock so one rule's path exists on main. Sweep 2:
   assert `mock.updatedPRBody` reflects the two-rule list, and
   the orphaned target was deleted from the branch.
3. **Auto-close on empty actionable** — Sweep 1: one rule
   actionable; PR created. Mutate mock so the path exists on
   main. Sweep 2: assert `mock.closedPRs` contains the PR
   number AND `mock.deletedBranches` contains
   `repo-guardian/add-missing-files`.

Pattern is the established convention from
`internal/checker/engine_policy_test.go` — no new infrastructure
required.

### Tier 2 — Diagnostic metric + Prometheus alert (separate small PR, before the fix)

Add a counter that increments **exactly when the bug fires**.
Name suggestion: `pr_orphan_left_total{org}` (incremented in
`createOrUpdatePRFromPolicy` whenever a previously-committed file
on the reconcile branch is no longer in `actionable`) and
`pr_open_with_empty_actionable_total{org}` (incremented in
`checkRepoWithPolicy` when `len(actionable) == 0` AND
`findOurPR(openPRs) != nil`).

Value: ship the metric BEFORE the fix to gather production
evidence of how often the bug fires in the homelab. Pair with a
PrometheusRule alert (extends `templates/prometheusrule.yaml`,
which already ships 5 starter alerts from IMPL-0011 P6):

```yaml
- alert: RepoGuardianStalePRLikely
  expr: |
    sum(rate(pr_open_with_empty_actionable_total[1h])) by (org) > 0
  for: 15m
  annotations:
    summary: "Repo Guardian observed satisfied rules with an open PR (org={{ $labels.org }})"
```

Keep the metric post-fix — it stays useful as a regression
canary. If the fix lands and the counter resumes climbing, we
have a smoke detector for that whole class of bug.

### Tier 3 — Integration test scaffold using httptest.Server (medium effort, broader payoff)

Precedent exists in `internal/github/client_test.go`, which uses
`httptest.NewServer` to stand up a fake GitHub API surface. The
existing tests are scoped to one API call per test. A multi-sweep
engine test would extend the pattern:

1. Stand up a `httptest.Server` that records every request and
   serves canned responses from an in-test state map.
2. Construct an installation-scoped `ghclient.Client` pointed at
   the fake's URL.
3. Wire it into `Engine.NewEngineFromPolicy` directly (no
   webhooks, no scheduler, no queue).
4. Drive the engine through N synthetic sweeps, mutating the
   fake's state map between each.
5. Assert on the request log: did the engine call `DeleteFile`,
   `UpdatePullRequest`, `ClosePullRequest` at the right moments?

Cost: roughly 1-2 days to write the harness; ~50 LOC per new
test. Payoff: catches engine-flow bugs that single-method mocks
miss (e.g., a CreateFile-then-DeleteFile that targets the wrong
branch SHA). The harness becomes the canonical test bed for any
future engine-loop change.

Recommendation: scope this in a separate IMPL phase or follow-up
RFC, not in the bug-fix PR. The fix itself is fine with Tier 1.

### Tier 4 — End-to-end against a real GitHub org (don't bother for this bug)

Stand up a sandbox GitHub org with throwaway repos, install
repo-guardian as a real App, drive sweeps end-to-end, assert
via `gh pr view`. This is the "is the whole system actually
working?" tier.

Cost is significant: sandbox org + GitHub App secret management
in CI + rate-limit budget + cleanup between runs. The bug in
INV-0005 is deterministic from code paths — it does not need
real GitHub to surface. E2E would be valuable for chart-deploy
smoke testing (already partially covered by
`charts/repo-guardian/docs/homelab-smoke.md`), but it does not
catch a different failure mode for this bug class than Tier 3
catches more cheaply.

### What we are NOT proposing

- **Property-based / fuzz testing** — possible (the invariant
  "files on branch == targets of actionable rules" is clean) but
  requires the Tier 3 harness first. Re-evaluate after Tier 3
  exists.
- **Mutation testing** — would surface gaps in existing
  coverage, but the gap here is identified already. Useful as a
  separate codebase-wide initiative, not for this bug.
- **Snapshot tests on PR body** — the body changes legitimately
  when operators tune `defaults.pr.body`. Snapshots would
  generate noise. Tier 1's explicit assertions on body content
  are sufficient.

### Recommendation

For the IMPL plan that follows this investigation:

1. Land Tier 2 (diagnostic metric + alert) as a small standalone
   PR before the fix, so we can observe the bug rate in the
   homelab now.
2. Bundle Tier 1 (multi-sweep unit tests) with the fix in the
   same PR — that's the bar for "we believe the fix is
   correct."
3. Open a separate planning issue or RFC for Tier 3 (httptest
   integration scaffold). Don't block the bug fix on it.
4. Skip Tier 4 unless we discover a separate class of bug that
   only manifests against real GitHub.

## Observability and operator UX (extended)

Beyond catching the bug class with tests, two operator-visible
surfaces would make the file-rule reconcile loop legible to humans
and produce fleet-wide insight even when nothing is broken.

### Stale-PR gauge bucketed by rule × age

Today there is no metric that answers "how many repos haven't
merged the `codeowners` rule yet?" or "how many repo-guardian
PRs have been open more than a week?" An operator looking at
fleet health has to manually correlate `gh pr list` output
against the policy file.

Proposed:

```go
// internal/metrics/metrics.go
OpenPRsByRule = promauto.NewGaugeVec(prometheus.GaugeOpts{
    Name: "open_prs_by_rule",
    Help: "Open repo-guardian PRs by org, rule, and PR age bucket. Reset and recomputed every sweep.",
}, []string{"org", "rule", "age_bucket"})
```

Implementation on every sweep:

1. `OpenPRsByRule.Reset()` at the start of `ReconcileAll` (or
   the sweep handler in `internal/checker/sweep.go`).
2. For every open repo-guardian PR (`findOurPR(openPRs) !=
   nil`), compute `age := now.Sub(pr.CreatedAt)` and bucket it
   into one of `0-3d`, `3-7d`, `7-30d`, `30d+`.
3. For every rule still in `actionable` on that sweep,
   `OpenPRsByRule.WithLabelValues(org, rule.Name,
   bucket).Inc()`.

This gives operators three natural views:

```promql
# Bubble-up by rule: how many repos haven't merged each rule.
sum by (rule) (open_prs_by_rule)

# Worst offenders: rules with the most month-plus stale PRs.
sum by (rule) (open_prs_by_rule{age_bucket="30d+"})

# Fleet aging curve over time.
sum by (age_bucket) (open_prs_by_rule)
```

Companion alert in `templates/prometheusrule.yaml`:

```yaml
- alert: RepoGuardianPRsStuckLongTerm
  expr: |
    sum by (org, rule) (open_prs_by_rule{age_bucket="30d+"}) > 0
  for: 1h
  annotations:
    summary: "{{ $labels.rule }} PRs open >30d in {{ $labels.org }}"
    description: |
      Repo-guardian has had open PRs for rule `{{ $labels.rule }}`
      in org `{{ $labels.org }}` for more than 30 days.
      `gh pr list --search 'head:repo-guardian/add-missing-files
      state:open' --json title,url,createdAt`.
```

Cardinality bound: orgs × file rules × 4 buckets. With ~5 rules
and a handful of orgs, that is at most low-hundreds of series —
well within Prometheus comfort zones. `installation_id` is
deliberately not a label (already redundant with `org` per the
IMPL-0009 convention captured in MEMORY.md).

Note on "updated_at" semantics: GitHub's `updated_at` advances on
any commit, comment, label change, or assignee change — including
repo-guardian's own reconcile writes. So a PR repo-guardian
"touches" every sweep would never look stale by that field. Age
from `created_at` is the right input for the staleness gauge.
Separately, a `time_since_last_reconcile_seconds` gauge could
track the inverse if we ever want it, but it duplicates what the
sweep cadence already implies.

### Reconciliation comments on the PR

Today the engine writes the PR body once at creation and never
again. The operator has no inline record of what repo-guardian
has done over the PR's lifetime (added file X, removed orphan Y,
refreshed body because Z became satisfied, etc.).

Proposed: emit a structured PR comment on every state-transition
event. Two design choices to settle in the IMPL plan:

**Option A — append-only comment stream.** Every event is a new
comment. Easy to implement. Maps cleanly to per-event
attribution. Downside: noisy on long-lived PRs (one comment per
sweep that does anything).

**Option B — sticky single comment, edited in place.** First
event creates a comment; subsequent events `UpdatePRComment` the
same comment by ID, appending to its body. Standard GitHub
Actions bot pattern (`actions/github-script` recipes). Find the
existing comment by a marker line:

```
<!-- repo-guardian-reconcile-log v1 -->
```

This stays clean on long-lived PRs. Implementation cost is one
ListComments call per sweep per repo with an open PR (cheap;
already inside the sweep's API budget per IMPL-0011).

**Recommended: Option B.** Operators reading a stale PR want
"what has repo-guardian done to this PR in the past week?" as a
single scrollable artifact, not a wall of bot comments.

Comment body shape (sticky):

```markdown
<!-- repo-guardian-reconcile-log v1 -->

## Repo Guardian reconcile log

| Date (UTC) | Event |
|---|---|
| 2026-05-26 03:00 | Created with rules: `codeowners`, `renovate_config`, `catalog_info` |
| 2026-05-29 03:00 | `catalog_info` rule satisfied on default branch — removed `catalog-info.yaml` from PR |
| 2026-06-02 03:00 | Body refreshed: 2 rules remaining |
```

Engine side: add `client.UpsertPRComment(ctx, owner, repo,
prNumber, markerLine, body)` to the github wrapper. On each
state-transition event in `createOrUpdatePRFromPolicy`, render
the new log row and call upsert.

A new metric records the surface so it's observable:

```go
PRCommentsWrittenTotal = promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "pr_comments_written_total",
    Help: "PR reconcile-log comments written or updated, by org and event type.",
}, []string{"org", "event"})
```

Where `event ∈ {created, file_removed, body_refreshed,
auto_closed}`.

### Anti-pattern: do not use PR comments for engine state recovery

A natural-sounding follow-up — "read the comments back to know
what we did last sweep" — should be explicitly **out of scope**.
Two reasons:

1. The durable Store added in IMPL-0011 (`repo_state` in
   Postgres) is the canonical record of per-repo reconcile
   history. Adding a second authority (PR comments) creates a
   split-brain problem: which is right if they disagree?
2. PR comments are user-editable. An operator who deletes the
   reconcile-log comment, or a third-party bot that mass-deletes
   bot comments, would corrupt engine state if the engine
   trusted comment content.

Comments are the **human-readable** view; `repo_state` is the
**engine-readable** record. If the IMPL plan needs additional
per-PR state (e.g., "files we committed last sweep, for the
orphan-detection step"), add a column to `repo_state` — do not
parse PR comment markdown.

### Updated IMPL sequence

Folding the above back into the recommendation chain at the top:

1. **PR A (tiny)** — diagnostic counters
   (`pr_orphan_left_total`, `pr_open_with_empty_actionable_total`)
   from Tier 2 + the `open_prs_by_rule` gauge from this section.
   Alerts in `templates/prometheusrule.yaml`. No engine behavior
   change. Ship now to see the homelab numbers.
2. **PR B (the actual fix)** — orphan deletion, body
   regeneration, auto-close. Multi-sweep unit tests bundled.
   Reconcile-log PR comments (sticky pattern) emitted on each
   state-transition event. New `PRCommentsWrittenTotal` metric.
3. **PR C (separate, deferrable)** — httptest integration
   scaffold from Tier 3.

The Option B sticky-comment design needs an explicit
`UpsertPRComment` method on `ghclient.Client` and a
corresponding mock. Calling that out so the IMPL plan can size
it correctly.

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
