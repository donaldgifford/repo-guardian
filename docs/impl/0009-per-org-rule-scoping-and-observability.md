---
id: IMPL-0009
title: "Per-org rule scoping and observability"
status: Draft
author: Donald Gifford
created: 2026-04-25
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0009: Per-org rule scoping and observability

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-04-25

## Objective

Implement DESIGN-0010: optional top-level `scope { orgs = [...] }`
block engages strict mode where every rule must declare its own
scope; absence preserves current behavior unchanged (legacy mode).
Plus an `org` label on per-rule and per-repo Prometheus metrics,
plus a new `repo_guardian_out_of_scope_total` counter. Bundled
`contrib/` Grafana dashboard and Prometheus alerts are updated;
a new `contrib/README.md` catalogs every exposed metric.

**Implements:** DESIGN-0010

## Scope

### In Scope

- `ScopeConfig` type and matcher (`internal/policy/scope.go`)
- `Scope *ScopeConfig` field on `PolicyConfig`, `FileRuleConfig`,
  `SettingRuleConfig`, `BranchProtectionRuleConfig`
- HCL loader support: top-level `scope {}` block + per-rule
  `scope {}` sub-blocks on all three rule types
- Strict-mode validation (load-time errors and warnings)
- Two new evaluation gates in the policy engine (policy-level and
  rule-level)
- New `OutOfScopeTotal` counter; `org` label on existing
  per-rule / per-repo metrics
- Promotion of `PRsCreatedTotal` and `PRsUpdatedTotal` from
  `Counter` to `CounterVec` with `["org"]`
- Updated `contrib/grafana/repo-guardian-dashboard.json` and
  `contrib/prometheus/alerts.yaml` reflecting new labels
- New `contrib/README.md` cataloging every exposed metric with
  example queries
- Unit, loader, engine, and metrics tests covering legacy and
  strict modes
- HCL examples (single-file and directory layouts) and
  documentation updates
- Backward-compatibility verification — every existing test passes
  unchanged in legacy mode

### Out of Scope

- Multi-App support (DESIGN-0010 non-goal)
- Forgejo / non-GitHub forge support (INV-0002, separate work)
- Per-repo inverse `ignore` (DESIGN-0010 non-goal)
- Repo-name as a metric label (DESIGN-0010 non-goal)
- Changes to `check_duration_seconds` histogram or rate-limit
  metrics (DESIGN-0010 explicit deferral)
- Helm chart changes (none required)

## Implementation Phases

Each phase builds on the previous one. A phase is complete when
all its tasks are checked off and its success criteria are met.

---

### Phase 1: Data Model and Matcher

Define the `ScopeConfig` type and its glob matcher. This phase
introduces no behavior change — the type is unused until later
phases wire it into the loader and engine.

#### Tasks

- [x] Add `ScopeConfig` struct to `internal/policy/types.go` next
      to `IgnoreConfig` (line ~109). Single field
      `Orgs []string` with `hcl:"orgs,optional"` tag
- [x] Add `Scope *ScopeConfig` field to `PolicyConfig` (line ~24)
      with `hcl:"scope,block"` tag
- [x] Add `Scope *ScopeConfig` field to `FileRuleConfig` (line ~52)
      with `hcl:"scope,block"` tag
- [x] Add `Scope *ScopeConfig` field to `SettingRuleConfig`
      (line ~115) with `hcl:"scope,block"` tag
- [x] Add `Scope *ScopeConfig` field to `BranchProtectionRuleConfig`
      (line ~146) with `hcl:"scope,block"` tag
- [x] Create `internal/policy/scope.go` with:
  - `(*ScopeConfig) Matches(owner string) bool` — returns `true`
    if `owner` matches any pattern in `Orgs`. Returns `false` for
    nil or empty `Orgs` (mirror `IgnoreConfig.Matches` shape but
    inverted polarity)
  - `(*ScopeConfig) HasUniversal() bool` — returns `true` if
    `Orgs` contains the literal `"*"`
- [x] Create `internal/policy/scope_test.go` mirroring
      `ignore_test.go`:
  - `TestScopeConfig_Matches_ExactMatch`
  - `TestScopeConfig_Matches_GlobWildcard`
  - `TestScopeConfig_Matches_NoMatch`
  - `TestScopeConfig_Matches_CharacterSet`
  - `TestScopeConfig_Matches_CaseInsensitive`
  - `TestScopeConfig_Matches_EmptyOrgs`
  - `TestScopeConfig_Matches_NilConfig`
  - `TestScopeConfig_Matches_MultiplePatterns`
  - `TestScopeConfig_Matches_InvalidPattern`
  - `TestScopeConfig_Matches_QuestionMark`
  - `TestScopeConfig_HasUniversal_True`
  - `TestScopeConfig_HasUniversal_False`
  - `TestScopeConfig_HasUniversal_NilReceiver`
- [x] `make test` passes — all new tests green, every existing
      test untouched

#### Success Criteria

- New types compile and existing tests still pass (no behavior
  change yet)
- `scope_test.go` reaches > 95% coverage on `scope.go`
- `make lint` passes — no `gocyclo`, `prealloc`, `gocritic`
  hits on the new code

---

### Phase 2: Top-level Scope Loader

Wire HCL decoding for the **top-level** `scope {}` block. Per-rule
scope is added in Phase 3.

#### Tasks

- [x] Update `decodeBody` schema in `internal/policy/loader.go`
      (line ~150) to add `{Type: "scope"}` to the top-level
      `BodySchema.Blocks`
- [x] Add `decodeScopeBlock(block *hcl.Block, ctx *hcl.EvalContext)
      (*ScopeConfig, hcl.Diagnostics)` next to
      `decodeIgnoreBlock` (line ~314). Reads the `orgs` attribute
      as a list of strings, identical to `decodeIgnoreBlock`'s
      `repos` handling
- [x] Update `decodeBlock` (line ~209) to dispatch
      `block.Type == "scope"` to `decodeScopeBlock`. Handle
      multiple top-level scope blocks: collect all into a slice
      on `hclConfig` so Phase 4 validation can detect duplicates
- [x] Add `Scope *ScopeConfig` and `ScopeBlocks []*ScopeConfig`
      fields to internal `hclConfig` struct (line ~24). The
      slice is for duplicate detection; the singleton `Scope`
      is what gets promoted into `PolicyConfig`
- [x] Update `hclConfigToPolicy` (line ~670) to copy the
      top-level scope into `cfg.Scope` if present
- [x] Add loader tests in `internal/policy/loader_test.go`:
  - `TestLoad_TopLevelScope_Decoded` — top-level
    `scope { orgs = ["myorg-*"] }` populates `cfg.Scope.Orgs`
  - `TestLoad_NoTopLevelScope_NilScope` — config with no scope
    block leaves `cfg.Scope == nil`
  - `TestLoad_TopLevelScope_AcrossDirectoryFiles` — directory
    with `scope.hcl` containing the block; verify decode
- [x] `make test` passes

#### Success Criteria

- Top-level `scope {}` block decodes correctly when present
- Configs without a top-level scope still load identically to
  before (regression coverage)
- Directory loads collect duplicate top-level scope blocks for
  Phase 4 to reject

---

### Phase 3: Per-rule Scope Loader

Wire HCL decoding for the per-rule `scope {}` sub-block on file,
setting, and branch-protection rules.

#### Tasks

- [x] Update file-rule body schema (`ruleBodySchema`, line ~331)
      to allow `scope` sub-block alongside `ignore`, `pr`,
      `assertion`, `reconcile`
- [x] Update `decodeRuleSubBlocks` (line ~409) to handle
      `sub.Type == "scope"`: call `decodeScopeBlock`, set
      `r.Scope`. Place next to the existing `ignore` handler
      (line ~429)
- [x] Update setting-rule body schema (`settingRuleBodySchema`,
      line ~529) to allow `scope` sub-block
- [x] Update setting-rule decoder (line ~579 area) to dispatch
      `scope` sub-blocks to `decodeScopeBlock` and set `sr.Scope`
- [x] Update branch-protection body schema
      (`branchProtectionBodySchema`, line ~590) to allow `scope`
      sub-block
- [x] Update branch-protection decoder (line ~631 area) to set
      `bp.Scope`
- [x] Add loader tests in `loader_test.go`:
  - `TestLoad_FileRuleScope_Decoded`
  - `TestLoad_SettingRuleScope_Decoded`
  - `TestLoad_BranchProtectionRuleScope_Decoded`
  - `TestLoad_RuleScopeWithUniversal` — `scope { orgs = ["*"] }`
    decodes to `Orgs: ["*"]` (no special-cased expansion at
    load time; that's a runtime concept)
- [x] `make test` passes

#### Success Criteria

- Per-rule `scope {}` blocks decode on all three rule types
- Wildcard `"*"` is preserved verbatim in `Orgs` (not expanded
  at load time)
- Existing rule-decoder tests still pass

---

### Phase 4: Strict-mode Validation

Add load-time validation that engages when the top-level scope is
present, plus a legacy-mode warning when per-rule scope is set
without a top-level block.

#### Tasks

- [x] Add `validateStrictScope(cfg *PolicyConfig) error` to
      `internal/policy/validate.go` (or new `validate_scope.go`):
  - Return error if `cfg.Scope.Orgs` is empty
  - Return error if multiple top-level scope blocks were
    collected by the loader (use `ScopeBlocks` slice from
    Phase 2)
  - Return error for each `FileRule` / `SettingRule` /
    `BranchProtectionRule` whose `Scope == nil`
  - Return error for each rule whose `Scope.Orgs` is empty
  - Error messages match DESIGN-0010 strict-mode validation
    table verbatim
- [x] Wire into `Load` (`loader.go:73` area): call
      `validateStrictScope(cfg)` only when `cfg.Scope != nil`,
      after the existing `Validate(cfg)` call
- [x] Add legacy-mode warning: in `Load`, when `cfg.Scope == nil`,
      walk all rule types; if any rule has `r.Scope != nil`,
      emit a single `slog.Warn` at load time with the resolved
      warning text from DESIGN-0010
      (`"per-rule scope ignored: no top-level scope { } block
      declared. Add a top-level scope block to enable strict
      mode, or remove per-rule scope blocks."`)
- [x] Add loader tests for each strict-mode error path
      (table-driven preferred):
  - `TestLoad_StrictMode_TopLevelEmptyOrgs`
  - `TestLoad_StrictMode_DuplicateTopLevelScope_Directory`
  - `TestLoad_StrictMode_FileRuleMissingScope`
  - `TestLoad_StrictMode_SettingRuleMissingScope`
  - `TestLoad_StrictMode_BranchProtectionRuleMissingScope`
  - `TestLoad_StrictMode_RuleEmptyScopeOrgs`
- [x] Add legacy-mode warning test:
      `TestLoad_LegacyMode_PerRuleScopePresent_Warns` — captures
      log output, asserts warning fires exactly once even when
      multiple rules have scope blocks
- [x] `make test` passes

#### Success Criteria

- Every error path in the DESIGN-0010 strict-mode validation
  table is exercised
- Legacy-mode warning fires exactly once per load (not once per
  rule)
- Configs without top-level scope and without per-rule scope
  load silently (no warnings)
- Error messages match DESIGN-0010 verbatim

---

### Phase 5: Engine Evaluation Gates

Add the policy-level and rule-level scope gates to the policy
engine. Before this phase the field exists but is never
consulted at runtime.

#### Tasks

- [ ] Add `policyScopeAllows(cfg *policy.PolicyConfig, owner
      string) bool` helper. Returns `true` when
      `cfg.Scope == nil` (legacy) or `cfg.Scope.Matches(owner)`
- [ ] Add `ruleScopeAllows(rs *policy.ScopeConfig, owner string,
      strictMode bool) bool` helper:
  - In legacy mode (`!strictMode`), always returns `true`
  - In strict mode: `rs.HasUniversal() ||
    rs.Matches(owner)`. Note: top-level gate already passed,
    so `HasUniversal()` is sufficient for "applies to all"
- [ ] In `engine_policy.go` `CheckRepo`: insert the policy-level
      gate at the top of the function, before any rule iteration.
      If it returns `false`, increment
      `OutOfScopeTotal{level="policy", org=owner}` once per
      enabled rule across all three rule types (sum of counts)
      and return early
- [ ] Insert rule-level gate at each ignore-check site, **before**
      the existing `r.Ignore.Matches(...)` call:
  - `engine_policy.go:78` (file rules — reconciler pass)
  - `engine_policy.go:241` (file rules — first pass)
  - `engine_policy.go:603` (setting rules)
  - `engine_policy.go:792` (branch protection rules)
  - When the gate returns `false`: increment
    `OutOfScopeTotal{level="rule", org=owner}` and `continue`
- [ ] Verify the legacy `engine.go` (non-policy registry path)
      is not affected — it has no `policy.PolicyConfig` so
      neither gate applies. Confirm by reading `NewEngine` flow
- [ ] Add engine tests in `engine_policy_test.go`:
  - `TestPolicyEngine_LegacyMode_NoScopeFiltering` — top-level
    nil, rules without scope, all repos processed (regression)
  - `TestPolicyEngine_StrictMode_PolicyScopeRejectsRepo` —
    `cfg.Scope.Orgs = ["myorg-*"]`, repo `otherorg/repo`
    skipped before any rule evaluates; counter increments by N
    where N is enabled-rule count
  - `TestPolicyEngine_StrictMode_RuleUniversalApplies` — rule
    with `scope.orgs = ["*"]` evaluates against every in-scope
    repo
  - `TestPolicyEngine_StrictMode_RuleSubsetApplies` — rule
    with `scope.orgs = ["myorg-prod"]` skips
    `myorg-staging/repo`, evaluates `myorg-prod/repo`
  - `TestPolicyEngine_StrictMode_OutOfScopeWinsOverIgnore` —
    rule with both `scope` and `ignore` matching the same repo:
    out-of-scope counter increments, ignore counter does not
  - `TestPolicyEngine_StrictMode_InScopeButIgnored` — repo in
    scope but in `ignore.repos`: ignore counter increments,
    out-of-scope does not
  - `TestPolicyEngine_StrictMode_OutOfScopeCounterByLevel` —
    verifies policy-level vs rule-level label values are correct
    and counts match expectations
- [ ] `make test` passes

#### Success Criteria

- Both gates evaluate in the order specified by DESIGN-0010
- Legacy mode: zero behavior change (regression tests prove this)
- Strict mode: out-of-scope short-circuits cleanly; counters
  match the "rule-evaluations skipped" semantic at both levels
- Legacy-path engine (`NewEngine`) is unaffected

---

### Phase 6: Metrics Relabeling

Add `org` label to per-rule / per-repo metrics, plus the new
`OutOfScopeTotal` counter. Installation IDs are not added as labels
(they're 1:1 with org for a given App and would be redundant);
they continue to flow through structured logs.

#### Tasks

- [ ] Update `internal/metrics/metrics.go`:
  - `ReposCheckedTotal`: labels become `["trigger", "org"]`
  - `FilesMissingTotal`: labels become `["rule_name", "org"]`
  - `SettingsCheckedTotal`: labels become
    `["rule_name", "org"]`
  - `SettingsMismatchedTotal`: labels become
    `["rule_name", "org"]`
  - `SettingsRemediatedTotal`: labels become
    `["rule_name", "org"]`
  - `BranchProtectionCheckedTotal`: labels become
    `["rule_name", "org"]`
  - `BranchProtectionRemediatedTotal`: labels become
    `["rule_name", "org"]`
  - `IgnoredTotal`: labels become `["scope", "org"]`
  - `ErrorsTotal`: labels become `["operation", "org"]`
  - `PRsCreatedTotal`: gain label `["org"]` (was unlabeled
    counter; promote to `CounterVec`)
  - `PRsUpdatedTotal`: gain label `["org"]` (same)
  - Add `OutOfScopeTotal` as
    `prometheus.NewCounterVec(..., []string{"level", "org"})`
- [ ] Update every callsite. Reference list (verified via
      research):
  - `engine_policy.go:255` — `FilesMissingTotal`
  - `engine_policy.go:546` — `PRsCreatedTotal`
  - `engine_policy.go:549` — `PRsUpdatedTotal`
  - `engine_policy.go:626` — `SettingsCheckedTotal`
  - `engine_policy.go:638` — `SettingsMismatchedTotal`
  - `engine_policy.go:654` — `SettingsRemediatedTotal`
  - `engine_policy.go:815` — `BranchProtectionCheckedTotal`
  - `engine_policy.go:870` — `BranchProtectionRemediatedTotal`
  - `engine_policy.go:243`, `engine_policy.go:605`,
    `engine_policy.go:794` — `IgnoredTotal` (rule-scope) — add
    `org`
  - `engine.go:86` — `IgnoredTotal` (global scope) — add `org`
  - `engine.go:167` — `FilesMissingTotal` (legacy path) — add
    `org`
  - `engine.go:283` — `PRsCreatedTotal` (legacy path) — add
    `org`
  - `engine.go:286` — `PRsUpdatedTotal` (legacy path) — add
    `org`
  - `queue.go:182` — `ReposCheckedTotal` — add `org` (from
    `job.Owner`)
  - `queue.go:169` — `ErrorsTotal` (`create_install_client`) —
    add `org` (derive from `job.Owner`)
  - `queue.go:176` — `ErrorsTotal` (`check_repo`) — add `org`
- [ ] Plumb `org` to engine-internal callsites by deriving from
      the `ownerRepo` string already in scope at every site
      (`strings.SplitN(ownerRepo, "/", 2)[0]`). No new function
      parameters needed
- [ ] Update `internal/metrics/metrics_test.go` to verify each
      relabeled metric registers and accepts the new label set
- [ ] Update existing engine tests to provide expected label
      values when asserting counter increments
- [ ] `make test` passes

#### Success Criteria

- All relabeled metrics emit with the new label values
- `OutOfScopeTotal` registers and increments correctly at both
  `level="policy"` and `level="rule"`
- No metric callsite calls `WithLabelValues` with the wrong
  arity (would panic at runtime)
- Coverage for `metrics.go` matches or exceeds the pre-change
  level

---

### Phase 7: contrib/ Updates and Metrics Catalog

Update the bundled Grafana dashboard and Prometheus alerts to use
the new labels, and add a comprehensive `contrib/README.md`
documenting every exposed metric with example queries. This ships
the breaking-change migration story for external operators.

#### Tasks

- [ ] Update `contrib/prometheus/alerts.yaml`:
  - Wrap all references to `repo_guardian_prs_created_total` and
    `repo_guardian_prs_updated_total` in `sum(...)` to handle the
    `Counter` → `CounterVec` promotion
  - Audit every alert expression that uses a relabeled metric and
    ensure it still produces the intended series shape
    (`sum without (org) (...)` where pre-existing behavior is
    desired; `sum by (org) (...)` where per-org alerting is
    desired)
  - Add at least one new alert exercising the new
    `repo_guardian_out_of_scope_total` metric (e.g., warn if
    `level="rule"` is consistently > 0 for a rule, suggesting a
    misconfiguration where a rule applies to no actual orgs)
- [ ] Update `contrib/grafana/repo-guardian-dashboard.json`:
  - Audit every panel for queries that reference relabeled
    metrics; update to aggregate appropriately
  - Add an "Org" template variable populated from
    `label_values(repo_guardian_repos_checked_total, org)` so
    operators can filter the dashboard per org
  - Add a panel for `repo_guardian_out_of_scope_total` broken
    out by `level`
  - Add a panel showing PRs created/updated rates per org (uses
    the newly labeled metrics)
- [ ] Validate the updated alerts file with `promtool check rules
      contrib/prometheus/alerts.yaml`
- [ ] Validate the dashboard JSON parses (e.g., `jq . <
      contrib/grafana/repo-guardian-dashboard.json > /dev/null`)
- [ ] Create `contrib/README.md` cataloging every exposed metric:
  - Metric name, type (Counter / CounterVec / Histogram / Gauge),
    labels, what it measures
  - At least one example PromQL query per metric
  - A "Common queries" section showing per-org rate, error rate,
    PR creation rate, out-of-scope rate, etc.
  - A "Migration from pre-DESIGN-0010" section showing the
    `sum without (org) (...)` recipe for operators preserving
    older query behavior
  - Pointer to the Grafana dashboard and alerts file as starting
    points
- [ ] `make lint` passes (yamllint on alerts file)

#### Success Criteria

- `promtool check rules` exits 0 on the updated alerts file
- The Grafana dashboard JSON is valid and renders the same panel
  set after import (no orphaned queries)
- New `contrib/README.md` documents every metric currently exposed
  by `internal/metrics/metrics.go` (plus `OutOfScopeTotal`)
- An operator with no prior context can import the dashboard,
  apply the alerts, and read `contrib/README.md` to understand
  what they're getting

---

### Phase 8: Documentation and Examples

Ship HCL examples (single-file and directory) plus README /
ADDING_RULES updates.

#### Tasks

- [ ] Create `examples/guardian-multi-org.hcl` — single-file
      example showing top-level scope, `["*"]` rules, and
      org-specific rules
- [ ] Create `examples/guardian-multi-org/` directory with split
      files matching DESIGN-0010 §scope block:
  - `scope.hcl` — top-level scope
  - `shared.hcl` — rules with `scope { orgs = ["*"] }`
  - `prod-only.hcl` — `myorg-prod`-scoped rules
  - `staging-only.hcl` — `myorg-staging`-scoped rules
- [ ] Update `examples/README.md` table to list the new examples
- [ ] Update `examples/examples_test.go` with parse tests:
  - `TestExampleHCL_MultiOrg` — single-file example loads
  - `TestExampleHCL_MultiOrgDirectory` — directory example
    loads via `Load(directory)`
- [ ] Add "Multi-org" section to `docs/ADDING_RULES.md` after
      the reconcilers section, covering:
  - When to use legacy vs strict mode
  - The `scope { orgs = ["*"] }` idiom
  - How to split rules across files
  - Strict-mode error explanations and how to fix them
- [ ] Update `README.md` configuration table to mention the
      top-level `scope` block under the HCL policy example
- [ ] Add a CHANGELOG / release note draft mentioning:
  - The added `org` label on per-rule / per-repo metrics
    (recommend `sum without (org) (...)` to recover pre-existing
    query semantics)
  - The `Counter` → `CounterVec` promotion of `prs_created_total`
    and `prs_updated_total` (recommend wrapping scalar queries
    in `sum(...)`)
  - The new `repo_guardian_out_of_scope_total` metric
  - Pointer to the updated `contrib/` dashboards and alerts as
    a known-good starting point
- [ ] `docz update` to refresh README index tables
- [ ] `make lint` passes

#### Success Criteria

- Both example forms parse cleanly via `policy.Load`
- Single-org users reading the README do not have to learn
  about scope
- Multi-org users get a copy-pasteable starting config
- Migration guidance is shipped, not left for future me

---

### Phase 9: Final Validation

Full CI pipeline, coverage check, and doc status update.

#### Tasks

- [ ] `make ci` passes — lint + test + build all green
- [ ] Coverage for changed packages (`internal/policy`,
      `internal/checker`, `internal/metrics`) is at or above the
      pre-change baseline
- [ ] Manual sanity check: build the binary, run with a sample
      `guardian.hcl` from `examples/guardian-multi-org.hcl`,
      verify `/metrics` exposes labeled metrics
- [ ] Manual sanity check: run with no HCL config (legacy
      defaults), verify behavior identical to current main
- [ ] No new TODO / FIXME comments left in the changed code
- [ ] Update IMPL-0009 status from "Draft" to "Completed"
- [ ] Update DESIGN-0010 status from "Draft" to "Implemented"
- [ ] `docz update` to refresh indexes

#### Success Criteria

- `make ci` exits 0
- Coverage stays at or above baseline; new files (`scope.go`)
  hit > 95%
- Both manual sanity checks pass
- DESIGN-0010 marked Implemented; IMPL-0009 marked Completed

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/policy/types.go` | Modify | Add `ScopeConfig` and `Scope` field to `PolicyConfig` and three rule structs |
| `internal/policy/scope.go` | Create | Matcher and `HasUniversal` helper |
| `internal/policy/scope_test.go` | Create | Unit tests mirroring `ignore_test.go` |
| `internal/policy/loader.go` | Modify | Top-level scope schema + decoder; per-rule scope decoding on three rule types |
| `internal/policy/loader_test.go` | Modify | Loader tests for legacy + strict modes, all error paths |
| `internal/policy/validate.go` | Modify | Add `validateStrictScope` |
| `internal/checker/engine_policy.go` | Modify | Two gates (policy-level + rule-level) at four call sites; counter increments |
| `internal/checker/engine_policy_test.go` | Modify | Engine tests for legacy + strict modes |
| `internal/checker/engine.go` | Modify | Add `org` label to legacy-path metric callsites |
| `internal/checker/queue.go` | Modify | Add `org` to `ReposCheckedTotal` and `ErrorsTotal` callsites |
| `internal/metrics/metrics.go` | Modify | Relabel 9 counters with `org`; promote `PRsCreatedTotal` / `PRsUpdatedTotal` from `Counter` to `CounterVec[org]`; add `OutOfScopeTotal` |
| `internal/metrics/metrics_test.go` | Modify | Verify new label sets |
| `examples/guardian-multi-org.hcl` | Create | Single-file example |
| `examples/guardian-multi-org/{scope,shared,prod-only,staging-only}.hcl` | Create | Directory example |
| `examples/README.md` | Modify | Document new examples |
| `examples/examples_test.go` | Modify | Parse tests for multi-org examples |
| `docs/ADDING_RULES.md` | Modify | New "Multi-org" section |
| `README.md` | Modify | Mention top-level scope under HCL example |
| `contrib/prometheus/alerts.yaml` | Modify | Update queries for relabeled and promoted metrics; add `out_of_scope_total` alert |
| `contrib/grafana/repo-guardian-dashboard.json` | Modify | Update panel queries; add `org` template variable; add panels for `out_of_scope_total` and per-org PR rates |
| `contrib/README.md` | Create | Catalog of all exposed metrics with example queries and migration recipes |

## Testing Plan

- [ ] Unit tests for `ScopeConfig.Matches` and `HasUniversal`
      (mirror `ignore_test.go` cases)
- [ ] Loader tests for legacy mode (regression) and strict-mode
      decoding
- [ ] Loader tests for every error path in the strict-mode
      validation table
- [ ] Loader test for legacy-mode warning (fires once, log
      capture)
- [ ] Engine tests for legacy mode (regression — current
      behavior preserved)
- [ ] Engine tests for strict-mode policy-level gate
- [ ] Engine tests for strict-mode rule-level gate (universal +
      subset)
- [ ] Engine tests for `out_of_scope_total` counter at both
      levels with correct rule-evaluation semantics
- [ ] Engine tests for scope+ignore interaction (out-of-scope
      wins)
- [ ] Metrics test for every relabeled metric registering and
      accepting new labels
- [ ] Example parse tests for both single-file and directory
      multi-org configs

## Resolved Questions

(All eight questions from DESIGN-0010 are already resolved and
locked in; reproduced here for reference.)

1. **Wildcard semantics** — `["*"]` at rule level = "all
   top-level orgs"
2. **Mixed mode** — top-level scope set ⟹ every rule needs
   scope; load-time error otherwise
3. **Legacy mode lifespan** — permanent
4. **Counter units** — both levels count rule evaluations
   (policy-level increments by N where N = enabled rule count)
5. **Legacy-mode warning text** — fixed string per DESIGN-0010
6. **Policy-level scope gate location** — runs inside `CheckRepo`
   alongside the rule-level gates
7. **`installation_id` as a metric label** — not added.
   `(App, org)` is 1:1 with installation; the label would be
   redundant with `org`. Installation IDs continue to be logged
   via structured log fields for GitHub audit-log correlation
8. **`PRs*` schema break accepted** — `PRsCreatedTotal` and
   `PRsUpdatedTotal` are promoted from `Counter` to
   `CounterVec[org]`. Bundled `contrib/` Grafana dashboard and
   Prometheus alerts are updated; new `contrib/README.md`
   provides operators copy-pasteable starting queries

## Open Questions

None at this time.

## References

- [DESIGN-0010](../design/0010-per-org-rule-scoping-and-observability.md) — parent design
- [INV-0002](../investigation/0002-multi-org-and-forgejo-support-for-repo-guardian.md) — investigation that motivated this
- [DESIGN-0008](../design/0008-additional-rule-types-and-ignore-lists.md) — ignore-list pattern this design mirrors
- `internal/policy/ignore.go` — implementation reference
- `internal/policy/ignore_test.go` — test reference
