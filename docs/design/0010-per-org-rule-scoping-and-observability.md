---
id: DESIGN-0010
title: "Per-org rule scoping and observability"
status: Implemented
author: Donald Gifford
created: 2026-04-25
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0010: Per-org rule scoping and observability

**Status:** Implemented
**Author:** Donald Gifford
**Date:** 2026-04-25

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Architectural framing](#architectural-framing)
  - [Two modes: legacy and strict](#two-modes-legacy-and-strict)
  - [scope block](#scope-block)
  - [Strict-mode validation](#strict-mode-validation)
  - [Evaluation order](#evaluation-order)
  - [Metrics labels](#metrics-labels)
- [API / Interface Changes](#api--interface-changes)
  - [HCL schema](#hcl-schema)
  - [Engine](#engine)
  - [Loader](#loader)
  - [Metrics](#metrics)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [Resolved Questions](#resolved-questions)
- [References](#references)
<!--toc:end-->

## Overview

repo-guardian's HCL policy engine treats every rule as global — every
enabled rule is evaluated against every repository the configured GitHub
App can see. When that App is installed across multiple orgs, there is no
way to apply a rule to only some of those orgs. This design adds an
optional top-level `scope { orgs = [...] }` block that declares the set
of orgs the policy is intended for, plus a per-rule `scope { orgs = [...] }`
block that targets a subset (or `["*"]` for all top-level orgs). Presence
of the top-level block engages **strict mode**: every rule must declare
its own scope explicitly. Absence preserves today's behavior unchanged
(legacy mode). The design also adds an `org` label to Prometheus
metrics so operators can slice work and errors per org, and ships
updated `contrib/` Grafana dashboard and Prometheus alerts (plus a
new `contrib/README.md` cataloging every exposed metric).

## Goals and Non-Goals

### Goals

- Let multi-org operators declare which orgs the policy targets, and
  which rules apply to which orgs, with no implicit inheritance.
- Preserve every existing single-org HCL config unchanged. Adding the
  feature must not silently change behavior for anyone.
- Surface per-org slicing in metrics so dashboards and alerts can be
  cut by org. (Installation IDs travel through structured logs for
  GitHub audit-log correlation; they are not added as a metric label
  because `(App, org)` is 1:1 with installation, making the label
  redundant with `org`.)
- Reuse the glob-matching, lowercase-normalize semantics from
  `IgnoreConfig` so users learn one matching model.

### Non-Goals

- **Multiple distinct GitHub Apps in one repo-guardian instance.** Out
  of scope. repo-guardian remains a single-App service. Org isolation
  comes from the App's installations, not from running multiple Apps.
- **Forgejo / non-GitHub forge support.** Tracked in [INV-0002][inv-0002];
  separate work.
- **Per-repo (not per-org) inverse `ignore`.** "Apply only to these
  repos" beyond what `ignore` already covers is a YAGNI hazard.
- **Repo-name as a metric label.** Org and installation ID are
  bounded by App-installation count (small). Repo name would push
  counter cardinality into the thousands and is not added here.

## Background

repo-guardian uses a single GitHub App. That App's installations
naturally span multiple orgs — the scheduler at
`internal/scheduler/scheduler.go:71-80` already calls
`ListInstallations()` and iterates each, the work queue carries
`RepoJob.InstallationID` (`internal/checker/queue.go:35`), and
`CreateInstallationClient` (`internal/github/client.go:294`) hands back
a client scoped to the right installation. The runtime is
multi-org-aware.

What's *not* there:

- **Rules can't target a subset of orgs.** Every enabled rule
  evaluates against every repo. Operationally, this means you either
  run multiple repo-guardian instances (one per org) or accept that
  all orgs share the exact same policy.
- **Metrics carry no org dimension.** `internal/metrics/metrics.go`
  exposes 20+ counters and histograms; none include `org`. An
  operator looking at `repo_guardian_errors_total` or
  `repo_guardian_files_missing_total{rule_name="codeowners"}` cannot
  tell which org the work belongs to.

The investigation that triggered this design is [INV-0002][inv-0002];
the ignore-list pattern this proposal mirrors is
[DESIGN-0008][design-0008].

## Detailed Design

### Architectural framing

repo-guardian is a **single-GitHub-App service**. The App can be
installed on multiple orgs; rules can be scoped to a subset of those
orgs. Anything beyond that — multiple distinct Apps, non-GitHub
forges — is out of scope for this design.

### Two modes: legacy and strict

The presence of a top-level `scope {}` block selects the mode. There
is no other switch.

**Legacy mode** — no top-level `scope {}` block in the HCL config:

- Behaves exactly like today. Every enabled rule applies to every
  repo the App can reach.
- Per-rule `scope {}` blocks are ignored if present (with a load-time
  warning). Mixing per-rule scope with no top-level scope is almost
  certainly a mistake; the warning surfaces it without breaking
  the load.
- No HCL changes required for any existing config. Single-org
  deployments never need to think about this feature.
- **Legacy mode is a permanent, supported configuration**, not a
  deprecation runway. Single-org users (the majority case) will
  always be able to omit the top-level `scope {}` block and run
  without ever interacting with this feature. The "legacy" label
  refers to the lack of explicit scoping, not to the lifespan of
  the mode.

**Strict mode** — top-level `scope {}` block present:

- The top-level scope declares the **universe of orgs** the policy
  knows about. Repos in orgs not matching the top-level scope are
  skipped wholesale (out-of-scope, before any rule is evaluated).
- Every rule **must** declare its own `scope { orgs = [...] }` block.
  A rule without one is a load-time error.
- A rule's scope must be either `["*"]` (shorthand for "all orgs in
  the top-level scope") or one or more patterns identifying a subset
  of the top-level orgs.
- A rule with `scope { orgs = ["*"] }` is the explicit "applies to
  every org this policy targets" idiom. There is no implicit
  applies-everywhere.

### `scope` block

Top-level (declares the universe):

```hcl
scope {
  orgs = ["myorg-prod", "myorg-staging"]
}
```

Per-rule (subset or all):

```hcl
rule "file" "codeowners" {
  scope { orgs = ["*"] }   # applies to all top-level orgs
  check    = "exists"
  paths    = ["CODEOWNERS", ".github/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"
}

rule "file" "experimental" {
  scope { orgs = ["myorg-staging"] }   # staging only
  check    = "exists"
  paths    = [".github/experimental.yml"]
  target   = ".github/experimental.yml"
  template = "experimental"
}
```

Pattern matching: `path.Match` glob (`*`, `?`, `[abc]`), lowercase
normalize on both sides — identical to `IgnoreConfig.Matches`. Invalid
patterns are silently skipped (matching the existing convention).

The literal `"*"` is reserved at the rule level to mean "all top-level
scope orgs" rather than `path.Match`'s glob `*` (which would be
identical here, but the explicit semantic makes the intent
self-documenting).

Splitting across files is supported via the existing `loadDirectory`
behavior. A typical multi-org layout:

```
guardian/
  scope.hcl          # top-level scope { orgs = [...] }
  shared.hcl         # rules with scope { orgs = ["*"] }
  prod-only.hcl      # rules with scope { orgs = ["myorg-prod"] }
  staging-only.hcl   # rules with scope { orgs = ["myorg-staging"] }
```

`hcl.MergeFiles` flattens all files into one body before decoding;
the top-level scope is decoded once, regardless of which file it
appears in. Multiple top-level scope blocks across files are a
load-time error (see strict-mode validation below).

### Strict-mode validation

Engaged when the top-level `scope {}` block is present.

| Condition | Result |
|-----------|--------|
| Top-level `scope.orgs` is empty | Load-time error: "top-level scope must declare at least one org" |
| Two or more top-level `scope {}` blocks across the merged config | Load-time error: "only one top-level scope block allowed" |
| A rule has no `scope {}` block | Load-time error: "rule '<name>' must declare scope in strict mode" |
| A rule's `scope.orgs` is empty | Load-time error: "rule '<name>' scope.orgs must not be empty" |

Deliberately **not** validated at load time:

- Whether a rule's scope patterns are actually a subset of the
  top-level scope. Pattern containment for general globs is
  computationally awkward and the runtime gate handles it correctly
  anyway: a rule whose scope matches an org outside the top-level
  scope simply never runs (the top-level gate rejects the repo
  first). Worst case is a typo'd pattern that does nothing, which
  is a debugging nuisance but not a correctness issue.

### Evaluation order

For every `(owner, repo, rule)` triple:

1. **Top-level scope gate** — if a top-level scope is set and `owner`
   does not match it, skip the entire repo (not just this rule).
   Increment
   `repo_guardian_out_of_scope_total{level="policy", org=owner}`
   **once per enabled rule that would have run** (see "Counter
   semantics" below).
2. **Rule scope gate** — if this rule's scope (in strict mode,
   guaranteed non-nil; in legacy mode, ignored) does not match
   `owner`, skip the rule. Increment
   `repo_guardian_out_of_scope_total{level="rule", org=owner}` once.
3. **Ignore gate** — if the rule's `ignore.repos` matches
   `owner/repo`, skip and log. Increment existing
   `repo_guardian_ignored_total` (with new `org` label per below).
4. **Evaluate the rule** as today.

Order matters. A repo that is both out-of-scope and on the ignore
list is recorded as out-of-scope at the highest applicable level.
This gives operators a clear "why was this not enforced?" chain when
debugging.

**Counter semantics for `out_of_scope_total`:** both `level="policy"`
and `level="rule"` count *rule evaluations skipped because of scope*,
not *repos skipped*. When the policy-level gate rejects one repo, the
counter increments by N (the number of enabled rules that would have
run on that repo). This keeps the two levels in the same units, so
operators can sum them or break them down without thinking about an
asymmetry. Cost is a tiny loop per skipped repo; negligible.

### Metrics labels

Add an `org` label to per-repo / per-rule metrics. Values are
derived from the owner segment of `owner/repo`, lowercased — known
at every callsite that increments these metrics.

Installation IDs are deliberately *not* added as a metric label.
For a given GitHub App, the `(App, org)` pair is 1:1 with an
installation, so `installation_id` would be redundant with `org`.
Installation IDs continue to flow through structured logs
(`RepoJob.InstallationID` is already plumbed to logger fields) for
GitHub audit-log correlation.

Label additions:

| Metric | New labels |
|--------|------------|
| `repo_guardian_repos_checked_total` | `org` |
| `repo_guardian_files_missing_total` | `org` |
| `repo_guardian_settings_checked_total` | `org` |
| `repo_guardian_settings_mismatched_total` | `org` |
| `repo_guardian_settings_remediated_total` | `org` |
| `repo_guardian_branch_protection_checked_total` | `org` |
| `repo_guardian_branch_protection_remediated_total` | `org` |
| `repo_guardian_ignored_total` | `org` (in addition to existing `scope`) |
| `repo_guardian_errors_total` | `org` |
| `repo_guardian_prs_created_total` | `org` (was unlabeled `Counter`; promoted to `CounterVec`) |
| `repo_guardian_prs_updated_total` | `org` (was unlabeled `Counter`; promoted to `CounterVec`) |

Plus one new metric:

```
repo_guardian_out_of_scope_total{level="policy|rule", org="..."}
```

Counts repos / rules skipped by the top-level or per-rule scope
gates. Distinct from `ignored_total` — out-of-scope means "the
policy doesn't apply here at all"; ignored means "the policy applies
but we're explicitly excluding this repo."

Deliberately *not* relabeled:

- `repo_guardian_check_duration_seconds` — histogram. Adding
  high-cardinality labels to histograms blows up storage. Keep
  unlabeled.
- `repo_guardian_github_rate_remaining` (gauge) and
  `repo_guardian_github_rate_limit_*` — reflect the App-level
  rate limit; not cleanly attributable to one installation when
  the App transport is involved. Leave for a follow-up.

Cardinality bound: orgs count = installations count. For typical
deployments this is < 10; even an unusually large multi-org App
would land < 100. Safe.

## API / Interface Changes

### HCL schema

- New top-level optional `scope {}` block (defines the policy
  universe).
- New optional `scope {}` block on `rule "file"`, `rule "setting"`,
  and `rule "branch_protection"` (selects a subset within the
  universe).
- Both block forms have the same shape: `orgs = [string]`. No new
  attributes.

### Engine

- New helper `ScopeConfig.Matches(owner string) bool` returns `true`
  if `owner` matches any pattern in `Orgs`. Note: returns `true`
  for **applies**, opposite of `IgnoreConfig.Matches`.
- New helper `ScopeConfig.HasUniversal() bool` returns `true` if
  `Orgs` contains the literal `"*"`. Used at the rule level to
  short-circuit to "applies to every top-level org."
- Two new gates in evaluation:
  1. Policy-level gate: `cfg.Scope == nil || cfg.Scope.Matches(owner)`.
     If false, skip the entire repo.
  2. Rule-level gate: `r.Scope.HasUniversal() || r.Scope.Matches(owner)`.
     If false, skip the rule. (In legacy mode, skipped — `r.Scope` is
     ignored.)
- Inserted before the existing `r.Ignore != nil && r.Ignore.Matches(...)`
  check at:
  - `internal/checker/engine_policy.go:78` (file rules)
  - `internal/checker/engine_policy.go:241` (file rules — second pass)
  - `internal/checker/engine_policy.go:603` (setting rules)
  - `internal/checker/engine_policy.go:792` (branch protection rules)

### Loader

- Decode top-level `scope {}` block in `decodeBody` /
  `hclConfigToPolicy` (`internal/policy/loader.go:147,664`).
- Add `scope` block decoding in:
  - File rule decoder (alongside existing `ignore` / `pr` / `reconcile`)
  - Setting rule decoder (around `loader.go:580`)
  - Branch protection rule decoder (around `loader.go:632`)
- Add `validateStrictScope(cfg *PolicyConfig) error` called at the
  end of `Load` when `cfg.Scope != nil`. Enforces the table in
  [Strict-mode validation](#strict-mode-validation).
- In legacy mode, emit a single `slog.Warn` at load time if any
  rule defines a per-rule `scope` block. ("Per-rule scope ignored:
  no top-level scope declared.")

### Metrics

- Update each `promauto.NewCounterVec` listed above to include
  `org` in the constructor's label slice.
- Promote `PRsCreatedTotal` and `PRsUpdatedTotal` from
  `prometheus.Counter` to `prometheus.CounterVec` with `["org"]`.
  This is a Prometheus-schema change — see Migration / Rollout.
- Add new `OutOfScopeTotal` counter vec with labels `level` and `org`.
- Update every callsite to provide the new label. `org` is derived
  from `ownerRepo` already in scope at every callsite via
  `strings.SplitN(ownerRepo, "/", 2)[0]`.

## Data Model

```go
// ScopeConfig holds org match patterns. Used in two places:
//   - PolicyConfig.Scope: top-level universe declaration. Presence
//     engages strict mode.
//   - FileRuleConfig.Scope / SettingRuleConfig.Scope /
//     BranchProtectionRuleConfig.Scope: rule-level subset.
//
// Patterns use the same glob model as IgnoreConfig (path.Match,
// lowercase normalization).
type ScopeConfig struct {
    Orgs []string `hcl:"orgs,optional"`
}

// Matches reports whether owner matches any pattern in Orgs.
// Returns false if sc is nil or Orgs is empty (i.e., "matches
// nothing"). Note: opposite polarity from IgnoreConfig.Matches,
// which returns true for "skip."
func (sc *ScopeConfig) Matches(owner string) bool {
    if sc == nil || len(sc.Orgs) == 0 {
        return false
    }
    normalized := strings.ToLower(owner)
    for _, pattern := range sc.Orgs {
        ok, err := path.Match(strings.ToLower(pattern), normalized)
        if err != nil {
            continue
        }
        if ok {
            return true
        }
    }
    return false
}

// HasUniversal reports whether Orgs contains the literal "*",
// used at the rule level as the explicit "applies to all
// top-level scope orgs" idiom.
func (sc *ScopeConfig) HasUniversal() bool {
    if sc == nil {
        return false
    }
    for _, p := range sc.Orgs {
        if p == "*" {
            return true
        }
    }
    return false
}
```

The struct lives in `internal/policy/types.go` next to `IgnoreConfig`.
The matcher lives in a new `internal/policy/scope.go` next to
`ignore.go`.

`PolicyConfig` gains a `Scope *ScopeConfig` field. Each rule type's
config struct gains the same field.

## Testing Strategy

- **Unit tests** in `internal/policy/scope_test.go` mirroring
  `ignore_test.go`: exact match, glob wildcard, character set,
  multiple patterns, empty list, nil receiver, case-insensitive,
  invalid pattern. Plus tests for `HasUniversal`.
- **Loader tests** for legacy mode:
  - No top-level scope: rule without scope decodes, applies
    everywhere (regression).
  - No top-level scope + rule has per-rule scope: warning logged,
    behavior unchanged from today.
- **Loader tests** for strict mode validation (each is a load-time
  error):
  - Top-level scope present, rule without scope.
  - Top-level scope with empty orgs.
  - Two top-level scope blocks across two files in a directory load.
  - Rule with empty `scope.orgs`.
- **Engine tests** in `engine_policy_test.go`:
  - Legacy: rule with no scope applies to all orgs (regression).
  - Strict: top-level scope `["myorg-*"]` rejects `otherorg/repo`
    before any rule evaluates.
  - Strict: rule with `scope { orgs = ["*"] }` applies to every
    org in the top-level scope.
  - Strict: rule with `scope { orgs = ["myorg-prod"] }` applies
    to `myorg-prod/repo` and skips `myorg-staging/repo`.
  - Strict: rule with both `scope` and `ignore` — out-of-scope wins
    when applicable; ignore wins when in scope.
  - Strict: `out_of_scope_total{level=...}` increments correctly at
    each level.
- **Metrics tests** — every relabeled metric still registers and
  accepts the new label values; counter increments produce the
  expected label set.

Coverage target: same 60% project floor; new code in
`policy/scope.go` should land closer to 95% (pure logic).

## Migration / Rollout Plan

Backward compatibility is the primary constraint:

- **Existing single-org HCL configs** — no changes. Legacy mode,
  identical behavior. The four `BuiltinDefaults` rules
  (`codeowners`, `dependabot`, `renovate_config`, `renovate_workflow`)
  also stay in legacy form since `BuiltinDefaults()` does not set a
  top-level scope.
- **Multi-org operators opt in** by adding a top-level `scope {}`
  block. They must then add `scope { orgs = ["*"] }` to every rule
  they want to keep applying everywhere. The load-time error makes
  the migration mechanical: errors point at each rule that needs
  attention.
- **Existing metrics consumers** — adding `org` to `CounterVec`s
  affects queries that don't aggregate over the new label.
  Operators with custom dashboards / alerts should use
  `sum without (org) (...)` to recover pre-existing behavior. Two
  schema changes are riskier and called out separately:
  - `repo_guardian_prs_created_total` and
    `repo_guardian_prs_updated_total` were unlabeled `Counter`s.
    They become `CounterVec`s with one label. Existing scalar
    queries (`repo_guardian_prs_created_total`) return multiple
    time series instead of one; consumers must wrap in
    `sum(...)`.
  - The new `repo_guardian_out_of_scope_total` is additive, not a
    schema change.
- **Bundled contrib/ dashboard and alerts updated.** The
  repo-guardian Grafana dashboard and Prometheus alerts shipped
  in `contrib/` are updated to use the new labels and added
  metric. A `contrib/README.md` is added documenting every
  exposed metric with examples and copy-paste-friendly queries
  for operators starting fresh.
- **Helm chart** — no changes needed.

Ship in one release. No feature flag — the change is small, the
modes are deterministic from config presence, and defaults preserve
current behavior at the policy layer.

Documentation work shipped with the change:
- New `examples/guardian-multi-org.hcl` and a corresponding
  directory-layout example (`examples/guardian-multi-org/`)
  demonstrating split files.
- README + `docs/ADDING_RULES.md` updates with a "Multi-org"
  section.
- Updated `contrib/grafana/repo-guardian-dashboard.json` and
  `contrib/prometheus/alerts.yaml` reflecting new labels and the
  `out_of_scope_total` metric.
- New `contrib/README.md` cataloging every exposed metric, its
  labels, and example queries.
- Release notes calling out the metric label change and the
  `Counter`-to-`CounterVec` promotion for PR metrics.

## Open Questions

None at this time.

## Resolved Questions

1. **Wildcard semantics at the rule level** — `scope { orgs = ["*"] }`
   means "all orgs declared in the top-level scope," not "literally
   any org." The top-level scope is the universe.
2. **Mixed mode within a directory load** — once a top-level scope
   block is present anywhere in the merged config, every rule must
   declare its own scope block. Missing per-rule scope is a
   load-time error.
3. **Legacy mode lifespan** — permanent. Single-org users will
   always be able to omit the top-level scope and run without
   interacting with this feature.
4. **`out_of_scope_total` counter units** — both `level="policy"`
   and `level="rule"` count rule evaluations skipped, not repos
   skipped. The policy-level gate increments the counter once per
   enabled rule that would have run on the rejected repo, keeping
   the two levels in the same units.
5. **Legacy-mode warning text** — `"per-rule scope ignored: no
   top-level scope { } block declared. Add a top-level scope block
   to enable strict mode, or remove per-rule scope blocks."`
6. **Policy-level scope gate location** — runs inside `CheckRepo`
   (engine layer), not in `processJob` (queue layer). Keeps all
   scope-gating logic in one file alongside the rule-level gates.
7. **`installation_id` as a metric label** — not added. `(App, org)`
   is 1:1 with installation, so the label would be redundant with
   `org`. Installation IDs continue to be logged via structured
   log fields for GitHub audit-log correlation.
8. **`PRsCreatedTotal` / `PRsUpdatedTotal` schema break** — accepted.
   These are promoted from `Counter` to `CounterVec` with `["org"]`.
   The bundled `contrib/` Grafana dashboard and Prometheus alerts
   are updated to use the new schema. A new `contrib/README.md`
   documents the metric set and provides copy-pasteable queries
   so external operators can adopt the new schema with minimal
   friction.

## References

- [INV-0002][inv-0002] — Multi-org and Forgejo support investigation
- [DESIGN-0006][design-0006] — HCL policy engine
- [DESIGN-0008][design-0008] — Additional rule types and ignore
  lists (the pattern this design mirrors)
- `internal/policy/ignore.go` — implementation reference for the matcher
- `internal/policy/ignore_test.go` — test reference

[inv-0002]: ../investigation/0002-multi-org-and-forgejo-support-for-repo-guardian.md
[design-0006]: 0006-hcl-policy-configuration-and-rule-engine.md
[design-0008]: 0008-additional-rule-types-and-ignore-lists.md
