---
id: DESIGN-0020
title: "Absent check mode and conditional file rules"
status: Draft
author: Donald Gifford
created: 2026-07-23
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0020: Absent check mode and conditional file rules

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-23

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [Driving use case: Renovate-first orgs](#driving-use-case-renovate-first-orgs)
  - [What the engine supports today](#what-the-engine-supports-today)
  - [Existing building blocks](#existing-building-blocks)
- [Detailed Design](#detailed-design)
  - [HCL surface](#hcl-surface)
  - [Semantics matrix](#semantics-matrix)
  - [Evaluation flow](#evaluation-flow)
  - [Remediation flow (deletion commits)](#remediation-flow-deletion-commits)
  - [Convergence: orphans, auto-close, and the inverse orphan](#convergence-orphans-auto-close-and-the-inverse-orphan)
  - [Validation rules](#validation-rules)
  - [Metrics](#metrics)
  - [PR text, commit messages, and the reconcile log](#pr-text-commit-messages-and-the-reconcile-log)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Implementation Phases](#implementation-phases)
  - [Phase 0 — Policy schema, loader, validation](#phase-0--policy-schema-loader-validation)
  - [Phase 1 — Engine evaluation: when-gate and absent mode](#phase-1--engine-evaluation-when-gate-and-absent-mode)
  - [Phase 2 — Remediation: deletion commits, PR wording, convergence](#phase-2--remediation-deletion-commits-pr-wording-convergence)
  - [Phase 3 — Push-event fast convergence (conditional on OQ 4)](#phase-3--push-event-fast-convergence-conditional-on-oq-4)
  - [Phase 4 — Documentation and examples](#phase-4--documentation-and-examples)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Add a fourth file-rule check mode, `check = "absent"`, whose remediation is a
PR that **deletes** the matching file(s), plus a `when {}` gating block that
makes any file rule conditional on the state of another rule in the same
policy. Together these express "renovate-first" policies: never add a
Dependabot config, and actively remove one from any repo that already
satisfies the Renovate rule.

## Goals and Non-Goals

### Goals

- Express "this file must NOT exist" as a first-class file rule whose
  remediation is a file-deletion PR on the existing reconcile branch.
- Express "this rule only applies when another rule is satisfied on the
  default branch" (cross-file conditionality) without duplicating path
  lists or assertions between rules.
- Full convergence parity with existing modes: orphan handling, auto-close,
  sticky reconcile-log comment, and the drift metrics all behave sensibly
  for deletion-based rules.
- Pure policy-schema extension: no new rule *type* (stays `rule "file"`),
  no engine constructor changes, no new deployment surface.

### Non-Goals

- Conditionality on anything other than sibling file rules (no expressions,
  no repo-property conditions, no org-level conditions — `scope`/`ignore`
  already cover the org/repo-name axes).
- Same-PR atomicity between the gating rule's addition and the gated rule's
  deletion (see [Convergence](#convergence-orphans-auto-close-and-the-inverse-orphan);
  the two-sweep eventual-consistency flow is deliberate).
- Deleting files outside a PR flow (no direct-to-default-branch deletes,
  matching the existing "everything goes through a reviewable PR" stance).
- Generalizing `when {}` to setting or branch-protection rules (can follow
  later; file rules are the only ones with cross-file interplay today).

## Background

### Driving use case: Renovate-first orgs

The org standard is Renovate (`renovate_config` + `renovate_workflow` rules
in `examples/guardian-full.hcl`). Dependabot and Renovate overlap; running
both produces duplicate PR noise. The desired policy:

| Repo has renovate config | Repo has dependabot.yml | Desired action |
|---|---|---|
| yes | yes | PR removing `.github/dependabot.yml` |
| yes | no | nothing (converged) |
| no | either | dependabot rule inert; `renovate_config` rule adds Renovate |

Today's engine cannot express either half:

1. **No deletion remediation.** All three check modes (`exists`,
   `contains`, `exact` — `internal/policy/types.go:15-25`) remediate by
   *adding or fixing* a file via `CreateOrUpdateFile`
   (`engine_policy.go.syncActionableFiles`). Nothing authors a deletion.
2. **No content-based conditionality.** The only per-rule gates are
   `scope` (org globs) and `ignore` (repo-name globs). The
   `ignore { repos = ["myorg/renovate-*"] }` workaround in
   `guardian-full.hcl` is name-based — it cannot see whether a repo
   actually contains a renovate config.

### What the engine supports today

Verified against the code (post-IMPL-0017, appVersion 1.9.0):

- `findActionableRules` (`engine_policy.go:221`) iterates
  `policy.FileRules`, applies scope → ignore gates, then `evaluateRule`
  which short-circuits on `hasExistingPRForPolicy` (search-terms match)
  before checking file state via `findExistingFile` (first matching path
  only).
- `syncActionableFiles` (`engine_policy.go:582`) renders each actionable
  rule's template and commits it to the deterministic reconcile branch
  (`repo-guardian/add-missing-files`) with `chore: add <target>`.
- `discoverOrphans`/`cleanupOrphans` (`drift.go:71,128`) remove files from
  the reconcile branch when their rule is no longer actionable — fail-safe
  per IMPL-0013 Q9 (API error ⇒ treat as still actionable, never delete).
- `autoClosePR` (`drift.go:305`) closes the PR and deletes the branch when
  the actionable set is empty.
- The rule HCL schema requires `target` and `template`
  (`loader.go:484-491`, `Required: true`) and validation requires both
  non-empty plus `paths` (`validate.go:96-106`).

### Existing building blocks

The expensive plumbing already exists — this design is mostly wiring:

- `github.Client.DeleteFile(ctx, owner, repo, branch, path, sha, msg)` —
  shipped in IMPL-0013 Phase 2 for orphan cleanup; exactly the primitive a
  deletion commit needs.
- `GetContentsOnBranch(ctx, owner, repo, path, branch) (sha, exists, err)`
  — provides the blob SHA required by the Contents-API delete.
- The reconcile branch + PR + labels + sticky-comment machinery is
  action-agnostic; a branch whose commits are deletions flows through
  `CreatePullRequest`/`UpdatePullRequest` unchanged.
- Per-rule `pr {}` templating (DESIGN-0013) gives removal PRs their own
  title/body/labels without new schema.

## Detailed Design

### HCL surface

```hcl
# Renovate-first: dependabot config is forbidden wherever Renovate
# is already set up. Never adds dependabot anywhere.
rule "file" "no_dependabot" {
  check = "absent"
  paths = [".github/dependabot.yml", ".github/dependabot.yaml"]

  # Rule is in force only when the named sibling rule is satisfied
  # on the default branch (its paths exist AND its assertions pass).
  # Otherwise this rule is skipped entirely.
  when {
    rule_satisfied = "renovate_config"
  }

  pr {
    search_terms = ["remove dependabot"]
    title        = "chore({{ .Repo }}): remove dependabot config (repo uses Renovate)"
  }
}
```

Grammar additions:

- `check = "absent"` — new `CheckMode` constant `CheckAbsent`. The rule is
  **actionable when any path in `paths` exists** on the default branch.
  `target` and `template` are meaningless for deletions and are **rejected**
  if present (see [Validation rules](#validation-rules)); the HCL schema's
  `Required: true` on both attributes is relaxed to optional, with
  per-check-mode requiredness enforced in `validateFileRule`.
- `when { rule_satisfied = "<rule-name>" }` — new optional block on
  `rule "file"` blocks (any check mode, not just `absent` — see OQ 7).
  References a sibling file rule by its HCL label. The gate is true when
  the referenced rule is **satisfied on the default branch**: at least one
  of its `paths` exists and (for `contains`) its assertions pass / (for
  `exact`) content matches its template. Gate false ⇒ the gated rule is
  skipped for this repo on this sweep (not actionable, not an orphan
  producer, reconcilers skipped).

The referenced-rule form (`rule_satisfied`) is preferred over an inline
path list (`any_exists = [...]`) because it reuses the sibling rule's
`paths` *and* its assertions — a repo with a `renovate.json` that doesn't
extend the org preset is not yet "using Renovate", and a duplicated path
list is exactly the copy-paste divergence this repo's post-mortems keep
catching (see OQ 1).

### Semantics matrix

For `no_dependabot` gated on `renovate_config`:

| renovate satisfied on default | dependabot file present | Result |
|---|---|---|
| yes | yes | actionable → deletion commit on reconcile branch → PR |
| yes | no | gate open, nothing to delete → not actionable (converged) |
| no (missing) | yes | gate closed → rule skipped; `renovate_config` itself is actionable and adds Renovate |
| no (assertion fails) | any | gate closed → rule skipped; `renovate_config` opens its own fix PR |
| no | no | gate closed → rule skipped |

The dependabot-only repo converges over **two PR cycles**: sweep 1 opens
the add-renovate PR; after a human merges it, a later sweep sees the gate
open and opens the remove-dependabot PR. This is deliberate — the gate
evaluates the **default branch**, never the reconcile branch, so
repo-guardian never deletes Dependabot before Renovate is actually live
and reviewed (see Non-Goals).

### Evaluation flow

Changes concentrate in `findActionableRules` and a new shared helper:

1. **`ruleSatisfiedOnDefault(ctx, client, owner, repo, rule) (bool, error)`**
   — extracted from the existing `evaluateRule` logic *minus* the
   `hasExistingPRForPolicy` short-circuit (that short-circuit exists to
   avoid duplicate PRs; reusing it here would make the gate flip merely
   because a PR is open, which is wrong). Semantics per check mode:
   - `exists`: any path in `paths` exists.
   - `contains`: a path exists AND `EvaluateAssertions` passes.
   - `exact`: a path exists AND content matches the template.
   - `absent`: no path exists. (Gating on an absent rule is legal but
     unusual; defined for completeness.)
2. **Per-repo-check memoization.** Gate evaluation duplicates API calls
   the referenced rule's own evaluation already makes (`GetContents` per
   path, `GetFileContent` for assertions). A small
   `map[ruleName]satisfiedResult` cache scoped to the single
   `checkRepoWithPolicy` invocation caps the overhead at one evaluation
   per referenced rule per repo-check. No cross-repo or cross-sweep
   caching — repo state changes between checks.
3. **Gate placement.** In `findActionableRules`, after the scope and
   ignore gates and before `evaluateRule`. Gate-closed skips increment a
   new counter (see [Metrics](#metrics)) and log at Info with the
   referenced rule and its evaluation. Per the file-rule double-iteration
   contract (CLAUDE.md), `runReconcilers` must apply the same gate
   **silently** (no counter increment) — forgetting this double-counts.
4. **Gate error handling — fail closed.** If evaluating the referenced
   rule errors (API glitch), the gate is treated as **closed** (rule
   skipped this sweep, Warn log). Rationale: the gated action is
   typically destructive (deletion); a transient error must never cause a
   delete PR against a repo whose Renovate state is unknown. This mirrors
   the IMPL-0013 Q9 fail-safe stance.
5. **`evaluateAbsent`.** New evaluation arm: walk **all** of `paths`
   (unlike `findExistingFile`, which stops at the first hit) and collect
   every existing path. Actionable when the set is non-empty. The
   collected paths ride along to remediation (a repo can have both
   `dependabot.yml` and `dependabot.yaml`; deleting only the first leaves
   the rule permanently actionable — see OQ 5).

### Remediation flow (deletion commits)

`syncActionableFiles` gains an action split per rule:

- **Add/fix modes (existing):** render template → `CreateOrUpdateFile` —
  unchanged.
- **Absent mode (new):** for each existing matching path:
  1. `GetContentsOnBranch(branch)` → blob SHA on the reconcile branch
     (the branch was cut from default, so the file is there).
  2. `DeleteFile(branch, path, sha, msg)` with
     `chore: remove <path> (forbidden by rule "<name>")`.
  3. Idempotency: if `GetContentsOnBranch` reports the file already absent
     on the branch (a prior sweep deleted it), skip — mirrors the
     skip-if-identical arm of `CreateOrUpdateFile` (INV-0003).

Deletions and additions coexist on the same branch/PR naturally (e.g., a
repo missing CODEOWNERS *and* carrying dependabot+renovate gets one PR
that adds `.github/CODEOWNERS` and removes `.github/dependabot.yml`).

### Convergence: orphans, auto-close, and the inverse orphan

- **Empty actionable + open PR** — unchanged: `autoClosePR` posts the
  final sticky comment, closes, deletes the branch. Deleting the branch
  discards pending deletion commits, which is exactly right (e.g., someone
  hand-deleted dependabot on main, or removed renovate so the gate
  closed).
- **Existing orphan discovery is deletion-blind but safe.**
  `discoverOrphans` looks for a rule's `Target` file *present* on the
  branch; an absent rule has no `Target` and its branch state is a
  *missing* file, so it never matches — no change needed to avoid false
  positives.
- **The inverse orphan (new).** In a bundle PR, if the absent rule stops
  being actionable (gate closed or file gone from main) while another
  rule keeps the PR open, the branch still carries a stale deletion
  commit — the PR diff would delete a file the policy no longer wants
  deleted. Mitigation: `discoverOrphans` grows an absent-rule arm — for a
  no-longer-actionable absent rule, check each of its `paths` on the
  branch; where default-branch has the file but the branch does not,
  **restore** it (`GetFileContent` from default →
  `CreateOrUpdateFile` on branch, `chore: restore <path> (rule "<name>"
  no longer applies)`). Same fail-safe stance: any API error ⇒ leave it,
  Warn, retry next sweep (see OQ 6).

### Validation rules

Added to `validateFileRule` (`internal/policy/validate.go`):

| Condition | Result |
|---|---|
| `check = "absent"` + `template` set | error — nothing to render |
| `check = "absent"` + `target` set | error — deletions operate on `paths` |
| `check = "absent"` + `assertion` blocks | error — no content to assert |
| `check = "absent"` + `reconcile` blocks | error — reconcilers consume file content that must not exist |
| `check != "absent"` + `target`/`template` missing | error — preserves today's contract (requiredness moves from HCL schema to validation) |
| `when.rule_satisfied` names a nonexistent rule | error, with the list of known rule names |
| `when.rule_satisfied` names a `setting`/`branch_protection` rule | error — file rules only |
| `when.rule_satisfied` names a disabled rule | error — a permanently-closed gate is a policy bug, fail loud at load |
| `when.rule_satisfied` self-reference | error |
| `when` cycles (A gates on B, B gates on A; any length) | error — detected via DFS over the gate graph at load time |
| Empty `when {}` block (no `rule_satisfied`) | error |

The check-mode allowlist in `validateFileRule` and the `CheckMode()`
switch in `types.go` both gain `absent`. Strict-templates validation
(`ValidatePRTemplates`) is unaffected — absent rules may carry `pr {}`
blocks and those validate exactly as today.

### Metrics

- New `files_forbidden_present_total{rule_name, org}` CounterVec —
  incremented when an absent rule is actionable. `files_missing_total`
  is **not** incremented for absent rules; its name would invert its
  meaning (see OQ 3).
- New `rule_gate_closed_total{rule_name, org}` CounterVec — incremented
  in the primary pass only when a `when` gate skips a rule. Distinct from
  `OutOfScopeTotal`/`IgnoredTotal` because gate state is repo-content
  -driven and expected to flip; operators alert on scope/ignore anomalies
  but *watch* gate counts.
- `prs_created_total` / `prs_updated_total` / `prs_closed_total` /
  `pr_orphan_left_total` — unchanged; deletion PRs are just PRs.
- File restoration (inverse orphan) failures reuse
  `pr_orphan_left_total{org}` — same "branch content is stale, next sweep
  retries" meaning.

### PR text, commit messages, and the reconcile log

- **`tmpl.Rule` gains `Action string`** (`"add"` | `"remove"`), populated
  in `buildPRVars`; `PRVars.Files` continues to list every touched path
  (additions and deletions). Additive, so `ValidateZero` strict-mode
  passes and existing operator templates keep rendering unchanged;
  templates that want per-action wording use `{{ if eq .Rule.Action
  "remove" }}`.
- **Fallback body** (`buildPRBodyFromPolicy`) splits the file list into
  "Added files" and "Removed files" sections; the CODEOWNERS-placeholder
  note renders only when an add-mode rule is present.
- **Reconcile-log statuses** (`drift.go`) get absent-aware wording:
  actionable absent rule → `present on main, pending removal`; satisfied →
  `absent from main`; gate-closed → `skipped (when-gate closed:
  <referenced rule> not satisfied)`. The status strings feed the
  content-hash tag, so new wording changes hashes exactly once per PR —
  acceptable (single extra comment edit after upgrade).
- **`hasExistingPRForPolicy` guidance, not code:** an absent rule's
  `search_terms` must not collide with terms from the era when the same
  file was being *added* (e.g., bare `"dependabot"` would match an old
  "add dependabot" PR title and permanently suppress the removal). The
  docs and example use `"remove dependabot"`.

## API / Interface Changes

- **`internal/policy`**: new `CheckAbsent CheckMode = "absent"`; new
  `WhenConfig struct { RuleSatisfied string }` + `When *WhenConfig` field
  on `FileRuleConfig`; `ruleBodySchema` adds the `when` block type and
  drops `Required: true` from `target`/`template`; new decode + validation
  paths per above.
- **`internal/checker`**: `evaluateAbsent`, `ruleSatisfiedOnDefault` (+
  per-check memo), gate wiring in `findActionableRules` **and**
  `runReconcilers`, action split in `syncActionableFiles`, absent arm in
  `discoverOrphans`, wording changes in `buildPRBodyFromPolicy` /
  reconcile-log builders.
- **`internal/template`**: `Rule.Action` field (additive).
- **`internal/metrics`**: two new CounterVecs.
- **`internal/github`**: no interface changes — `DeleteFile`,
  `GetContentsOnBranch`, `GetFileContent`, `CreateOrUpdateFile` cover
  everything. No mockClient-parity sweep needed.
- **Chart**: no new values; no new alerts required (deletion PRs surface
  through the existing PR metrics). Chart README/docs only.

## Data Model

No store schema changes. `policy.Version` hashes the JSON-serialized
config (`version.go:32`); adding the `When` field and new check-mode
string changes the hash for **every** loaded policy on upgrade, which
marks all `repo_state` rows drifted and triggers one full re-sweep. This
is the established behavior for any policy-struct change (same thing
happened on IMPL-0017's schema additions) and is harmless — but the
migration doc must state it so a fleet-wide sweep right after upgrade
isn't mistaken for a bug.

## Implementation Phases

### Phase 0 — Policy schema, loader, validation

Tasks:

- [ ] Add `CheckAbsent` to `types.go` (`CheckMode` const + `CheckMode()`
      switch) and the `validateFileRule` allowlist.
- [ ] Add `WhenConfig` type and `When *WhenConfig` field to
      `FileRuleConfig`.
- [ ] Relax `target`/`template` to optional in `ruleBodySchema`; add
      `{Type: "when"}` to its block list; decode via new
      `decodeWhenBlock` in `loader.go` (wired into `decodeRuleSubBlocks`).
- [ ] Move `target`/`template` requiredness into `validateFileRule`,
      conditioned on check mode (absent forbids, others require).
- [ ] Implement the full validation matrix (template/target/assertions/
      reconcilers forbidden on absent; referenced-rule existence, type,
      enabled, self-reference, cycle detection via DFS; empty `when`).
- [ ] Loader tests: happy-path absent rule, gated rule, every validation
      error with location-prefixed messages, cycle of length 2 and 3.
- [ ] `policy.Version` test: adding a `when` block or flipping check mode
      to absent changes the hash.

Success criteria: `make lint` and `make test` pass; a `guardian.hcl`
containing the [HCL surface](#hcl-surface) example loads without error;
each row of the validation matrix has a test asserting its exact error;
`policy.BuiltinDefaults()` is untouched (no absent rules ship as
defaults).

### Phase 1 — Engine evaluation: when-gate and absent mode

Tasks:

- [ ] Implement `ruleSatisfiedOnDefault` with the per-repo-check
      memoization map; unit tests per check mode including the
      no-PR-short-circuit distinction (gate must evaluate true even when
      an open PR exists for the referenced rule).
- [ ] Wire the gate into `findActionableRules` (counter + Info log) and
      `runReconcilers` (silent), honoring the double-iteration contract.
- [ ] Fail-closed gate error handling with Warn log; test with an
      erroring mock client asserting the rule is skipped and no deletion
      is planned.
- [ ] Implement `evaluateAbsent` collecting **all** existing matching
      paths; actionable iff non-empty.
- [ ] Add `files_forbidden_present_total` and `rule_gate_closed_total`
      metrics; assert increments via `testutil.ToFloat64` with unique
      label values (parallel-safe per the established convention).
- [ ] Verify `FilesMissingTotal` is not incremented for absent rules.

Success criteria: `make lint` and `make test` pass; table-driven engine
tests cover all five rows of the [semantics matrix](#semantics-matrix)
using the existing `mockClient` pattern in `internal/checker/engine_test.go`;
gate evaluation issues at most one content fetch per referenced rule per
repo-check (asserted via mock call counting).

### Phase 2 — Remediation: deletion commits, PR wording, convergence

Tasks:

- [ ] Action split in `syncActionableFiles`: absent rules delete each
      collected path on the reconcile branch via
      `GetContentsOnBranch` + `DeleteFile`, with the already-absent
      idempotent skip.
- [ ] `tmpl.Rule.Action` field + `buildPRVars` population; fallback-body
      Added/Removed sections; CODEOWNERS note gated on add-mode presence.
- [ ] Inverse-orphan restoration arm in `discoverOrphans`/`cleanupOrphans`
      per the design, reusing `pr_orphan_left_total` on failure.
- [ ] Reconcile-log absent-aware status strings.
- [ ] Multi-sweep convergence tests in `internal/checker/convergence_test.go`
      following the IMPL-0013 pattern: sweep 1 renovate-add PR → merge →
      sweep 2 dependabot-remove PR → merge → sweep 3 converged;
      hand-deleted dependabot → auto-close; gate closes mid-flight →
      restoration; both `.yml` and `.yaml` present → both deleted.
- [ ] Stateful-mock fidelity check: the mock's branch-contents state must
      reflect prior `DeleteFile`/`CreateOrUpdateFile` calls on the same
      mock, per the list-then-act mock-fidelity rule (CLAUDE.md) — an
      always-static mock would vacuously pass the idempotency tests.

Success criteria: `make ci` passes; convergence suite demonstrates the
full renovate-first lifecycle end-to-end against mocks; a bundle PR mixing
one add-rule and one absent-rule renders both sections and both commit
kinds; re-running an identical sweep produces zero mutating API calls
(idempotency, asserted via mock counters).

### Phase 3 — Push-event fast convergence (conditional on OQ 4)

Tasks:

- [ ] Extend watched-path extraction so a rule referenced by any
      `when.rule_satisfied` contributes its `paths` to the webhook
      handler's watched set (merging renovate's own add-PR then triggers
      an immediate re-check instead of waiting for the sweep cadence).
- [ ] Webhook handler test: push touching `renovate.json` on the default
      branch enqueues a re-check for a policy where only the *gated* rule
      watches it.
- [ ] Confirm no watched-path change when OQ 4 lands as (b); delete this
      phase from the plan in that case.

Success criteria: `make lint` and `make test` pass; the dependabot-removal
PR opens on the first post-merge webhook delivery in the handler test, not
on the next scheduled sweep.

### Phase 4 — Documentation and examples

Tasks:

- [ ] `docs/usage/policy-reference.md`: `absent` check mode section,
      `when {}` block reference, validation-matrix table, search-terms
      collision guidance.
- [ ] `examples/guardian-full.hcl`: replace the name-glob
      `ignore { repos = ["myorg/renovate-*"] }` workaround on the
      dependabot rule with the `no_dependabot` + `when` form; keep the
      old form nearby as a commented alternative; `go test ./examples/...`
      must pass.
- [ ] Migration note (upgrade doc or release notes per the
      `docs/operations/*-migration.md` precedent): policy-hash bump ⇒
      one-time full re-sweep; reconcile-log hash bump ⇒ one comment edit
      per open PR; search-terms guidance.
- [ ] CLAUDE.md architecture notes: absent mode, when-gate fail-closed
      semantics, inverse-orphan restoration, and the
      counter-only-in-primary-pass reminder extended to the gate.
- [ ] `docz update design` / index regeneration; mkdocs strict-mode
      warning count unchanged from the 14-file baseline.

Success criteria: `make ci` passes; policy-reference renders the full
semantics matrix; the example file loads in the example test; docs build
clean.

## Testing Strategy

Unit tests live beside each phase (see per-phase tasks). The
cross-cutting strategies, named here so phases can reference them:

- **Mock fidelity for list-then-act paths** — deletion idempotency and
  inverse-orphan restoration are exactly the shape the IMPL-0013 Phase 4
  post-mortem warns about: the mock's `GetContentsOnBranch` must reflect
  prior writes to the same mock or the skip-arm assertions are vacuous.
- **Multi-sweep convergence** — extend `convergence_test.go`, which
  already models sweep sequences with a stateful mock; the renovate-first
  lifecycle is a new scenario family, not new infrastructure.
- **Metric assertions** — unique label values per test
  (`testutil.ToFloat64` + targeted `Reset()`), per the established
  parallel-safety convention.
- **No integration-tag additions** — nothing here touches store/queue/
  scheduler backends.

## Migration / Rollout Plan

Additive and opt-in: policies that never write `check = "absent"` or
`when {}` parse and behave identically. Rollout:

1. Ship binary + docs (minor appVersion bump; new HCL surface).
2. On upgrade, `policy.Version` changes ⇒ one-time full re-sweep
   (documented; expected).
3. Operators adopt by editing `guardian.hcl`; recommended first step is
   `dry_run = true` (or the `DRY_RUN` env) to review planned deletions in
   logs before letting removal PRs open — deletion is the first
   destructive remediation the file engine ships, so the migration doc
   leads with the dry-run recipe.
4. Rollback = remove the new blocks from HCL (or pin the previous
   appVersion; unknown check mode fails validation at load, so a
   downgraded binary with an absent-rule policy fails loudly at startup
   rather than silently ignoring the rule — this is the desired
   fail-loud behavior, called out in the migration doc).

## Open Questions

1. **Gate mechanism — what does `when {}` accept?**
   - (a) **Recommendation:** `rule_satisfied = "<name>"` only. Reuses the
     referenced rule's paths *and* assertions (a renovate.json that fails
     the org-preset assertion isn't "using Renovate"); no duplicated path
     lists to drift. Cycle detection is straightforward on a named graph.
   - (b) `any_exists = ["path", ...]` only. Simpler engine (no rule graph,
     no cycles), but duplicates path lists and cannot see assertions —
     re-introduces the copy-paste divergence class of bug.
   - (c) Both, mutually exclusive per block. Maximum flexibility, larger
     validation surface and two behaviors to document.
   - other:

2. **Does the gate respect the referenced rule's own scope/ignore?**
   - (a) **Recommendation:** No — content-only evaluation. The referenced
     rule serves purely as a named bundle of paths+assertions; the gated
     rule's *own* scope/ignore already control where the gated rule
     applies. Mixing in the referee's gates makes the gate's value depend
     on org/repo-name state in ways that are hard to reason about (e.g.,
     renovate rule ignored on one repo would silently flip the dependabot
     gate there).
   - (b) Yes — gate is false wherever the referenced rule is
     scoped/ignored out. More "consistent" with the referee as a whole
     rule, but produces the surprising cross-effects above.
   - other:

3. **Metrics shape for absent-rule actionability?**
   - (a) **Recommendation:** New `files_forbidden_present_total{rule_name,
     org}` counter; `files_missing_total` untouched for absent rules. The
     existing counter's name would mean its opposite; dashboards keying on
     it (contrib/) stay truthful.
   - (b) Reuse `files_missing_total` (rule label disambiguates). No new
     metric, but "missing" counting "present" is a standing operator
     footgun.
   - other:

4. **Auto-watch the referenced rule's paths for push-triggered re-checks
   (Phase 3)?**
   - (a) **Recommendation:** Yes. Merging the renovate-add PR fires a push
     touching `renovate.json`; auto-watching means the removal PR opens
     minutes later instead of at the next weekly sweep. Cost is a slightly
     larger watched-path set in the webhook handler.
   - (b) No — sweep-cadence convergence only. Simpler; the two-PR flow
     already spans a human merge, so hours-to-days latency arguably
     doesn't matter.
   - other:

5. **When multiple `paths` entries exist in a repo (e.g., both
   `dependabot.yml` and `dependabot.yaml`), what does remediation
   delete?**
   - (a) **Recommendation:** Every existing matching path, in one PR. The
     rule's contract is "no file at any of these paths"; deleting only one
     leaves the rule actionable forever.
   - (b) First match only (parity with `findExistingFile`), converging
     over successive PRs. Simpler diff per PR, but N PRs for N variants
     and a confusing intermediate state.
   - other:

6. **Stale deletion in a bundle PR (gate closes / file removed on main
   while another rule keeps the PR open) — restore or leave?**
   - (a) **Recommendation:** Restore the file on the reconcile branch from
     default-branch content (the inverse-orphan arm), fail-safe on errors.
     Leaves the PR diff always equal to current policy intent — the same
     invariant orphan cleanup enforces for additions.
   - (b) Leave the stale deletion until the whole PR auto-closes. No new
     code path, but a reviewer merging the bundle would delete a file the
     policy no longer targets — a correctness hole, not just cosmetics.
   - other:

7. **Is `when {}` allowed on non-absent rules?**
   - (a) **Recommendation:** Yes — the gate is orthogonal to check mode
     (e.g., "require dependabot config *unless* renovate is satisfied"
     wants a when-gated `exists` rule as the mirror-image policy). The
     validation matrix and engine wiring are check-mode-agnostic anyway;
     restricting it buys nothing.
   - (b) No — absent-only for the first release; loosen later if asked.
     Smaller doc surface now, breaking-change-shaped loosening later.
   - other:

## References

- [DESIGN-0013 — PR templates](0013-customizable-pr-templates.md) — per-rule `pr {}` inheritance the removal PRs reuse
- IMPL-0013 — orphan cleanup, auto-close, sticky reconcile-log; the Q9 fail-safe stance this design extends to gates and restoration
- INV-0003 — `CreateOrUpdateFile` three-branch idempotency the deletion path mirrors
- `examples/guardian-full.hcl` — the name-glob `ignore` workaround this design replaces
- `internal/policy/types.go:15-25`, `internal/policy/loader.go:484-499`, `internal/policy/validate.go:79-127`, `internal/checker/engine_policy.go:221-302,582-621`, `internal/checker/drift.go:71-156,305-338` — code paths audited for this design (as of appVersion 1.9.0)
