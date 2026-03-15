---
id: DESIGN-0008
title: "Additional Rule Types and Ignore Lists"
status: Draft
author: Donald Gifford
created: 2026-03-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0008: Additional Rule Types and Ignore Lists

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-15

## Overview

Add global and per-rule ignore lists with glob pattern matching, implement
`rule "setting"` and `rule "branch_protection"` types, and build the
`label_sync`, `branch_protection`, and `workflow_sync` reconciler
implementations. This extends the policy engine (DESIGN-0006) and
reconciler interface (DESIGN-0007) to cover the full scope of RFC-0002.

**Implements:** RFC-0002 Phases 4, 6

**Depends on:** DESIGN-0006, DESIGN-0007

## Goals and Non-Goals

### Goals

- Implement global ignore lists (skip repos for all rules)
- Implement per-rule ignore lists (skip specific rules for specific repos)
- Support glob patterns in ignore lists (e.g., `myorg/terraform-*`)
- Implement `rule "setting"` for repo settings (vulnerability alerts,
  default branch, etc.)
- Implement `rule "branch_protection"` for branch protection configuration
- Implement `label_sync`, `branch_protection`, and `workflow_sync`
  reconcilers
- Define fixed HCL schemas for all new rule types and reconcilers

### Non-Goals

- Multi-org support (future work)
- Custom rule type plugins (rules are built-in, config-driven)
- Ignore lists based on repo topics, visibility, or other metadata (only
  repo name matching initially)

## Background

### No Exclusion Mechanism Today

Currently there is no way to skip a rule for specific repos. The only
controls are:

- `skipForks` / `skipArchived` -- global flags that apply to all rules
- `Enabled` field on `FileRule` -- disables a rule entirely

At 10k+ repos, exceptions are inevitable: legacy repos, special-purpose
repos, repos managed by other tooling. Without ignore lists, the only option
is to not install the GitHub App on those repos, which is all-or-nothing.

### Missing Rule Types

The current system only checks file presence. Repo settings like
vulnerability alerts, default branch name, and branch protection rules
require different checking logic that doesn't map to the file rule model.
These are common compliance requirements that belong in the same policy
engine.

## Detailed Design

### Ignore Lists

#### HCL Schema

```hcl
# Global ignore -- skip these repos for ALL rules
ignore {
  repos = [
    "myorg/legacy-monolith",
    "myorg/archived-*",
    "myorg/terraform-*",
  ]
}

# Per-rule ignore -- inside a rule block
rule "file" "codeowners" {
  # ...
  ignore {
    repos = ["myorg/special-case", "myorg/generated-*"]
  }
}
```

#### Go Types

```go
// internal/policy/types.go

type IgnoreConfig struct {
    Repos []string  // exact names or glob patterns
}
```

#### Matching Logic

```go
// internal/policy/ignore.go

// Matches returns true if the given "owner/repo" matches any pattern
// in the ignore list.
func (ic *IgnoreConfig) Matches(ownerRepo string) bool
```

Patterns use Go's `path.Match` for glob matching, which supports:

- `*` matches any sequence of non-separator characters
- `?` matches any single non-separator character
- `[abc]` matches any character in the set

The input is always `owner/repo` format (e.g., `myorg/my-repo`).

#### Evaluation Order

```
CheckRepo(owner, repo)
  |
  global ignore list → match? → skip entire repo
  |
  for each rule:
    per-rule ignore list → match? → skip this rule
    |
    evaluate rule
```

Global ignore is checked once per repo (cheapest). Per-rule ignore is
checked before each rule evaluation.

### Rule Type: `setting`

Repository setting rules check and optionally remediate GitHub repo
settings (properties accessible via the GitHub API).

#### HCL Schema

```hcl
rule "setting" "<name>" {
  enabled   = true            # optional, default true
  property  = "<string>"      # required, GitHub API property name
  expected  = <value>         # required, expected value (bool or string)
  remediate = false           # optional, auto-fix if possible

  ignore {
    repos = [...]
  }
}
```

#### Go Types

```go
type SettingRuleConfig struct {
    Name      string
    Enabled   bool
    Property  string
    Expected  interface{}  // bool or string
    Remediate bool
    Ignore    IgnoreConfig
}
```

#### Supported Properties

| Property | Type | GitHub API | Remediate |
|----------|------|-----------|-----------|
| `vulnerability_alerts_enabled` | bool | `GET /repos/{owner}/{repo}/vulnerability-alerts` | `PUT /repos/{owner}/{repo}/vulnerability-alerts` |
| `default_branch` | string | `GET /repos/{owner}/{repo}` → `default_branch` | `PATCH /repos/{owner}/{repo}` |
| `has_issues` | bool | `GET /repos/{owner}/{repo}` → `has_issues` | `PATCH /repos/{owner}/{repo}` |
| `has_wiki` | bool | `GET /repos/{owner}/{repo}` → `has_wiki` | `PATCH /repos/{owner}/{repo}` |
| `delete_branch_on_merge` | bool | `GET /repos/{owner}/{repo}` → `delete_branch_on_merge` | `PATCH /repos/{owner}/{repo}` |
| `allow_merge_commit` | bool | `GET /repos/{owner}/{repo}` → `allow_merge_commit` | `PATCH /repos/{owner}/{repo}` |
| `allow_squash_merge` | bool | `GET /repos/{owner}/{repo}` → `allow_squash_merge` | `PATCH /repos/{owner}/{repo}` |
| `allow_rebase_merge` | bool | `GET /repos/{owner}/{repo}` → `allow_rebase_merge` | `PATCH /repos/{owner}/{repo}` |

#### Check Flow

```
evaluate setting rule
  |
  read current value from GitHub API
  |
  compare against expected
  |-- match → log, increment metric
  `-- mismatch → remediate?
                  |-- yes + not dry_run → set via API, log, increment metric
                  |-- yes + dry_run → log "would remediate"
                  `-- no → log "setting mismatch", increment metric
```

Setting rules don't create PRs -- they either auto-remediate via API or
log the mismatch for manual follow-up.

#### GitHub App Permissions

Setting remediation requires `administration: write` on the GitHub App.
Without this permission, `remediate = true` will fail with a clear error.
The permission requirement is documented but not validated at config load
time (since permissions are checked at runtime).

### Rule Type: `branch_protection`

Branch protection rules check and optionally enforce branch protection
settings on a named branch.

#### HCL Schema

```hcl
rule "branch_protection" "<name>" {
  enabled               = true
  branch                = "main"        # required, branch to protect
  require_pr            = true          # require PRs for merging
  required_approvals    = 1             # minimum approvals
  dismiss_stale_reviews = true          # dismiss approvals on new pushes
  require_status_checks = ["ci/lint"]   # required status checks
  enforce_admins        = false         # apply to admins too
  require_linear_history = false        # require linear history

  ignore {
    repos = [...]
  }

  reconcile "branch_protection" {
    watch = true
  }
}
```

#### Go Types

```go
type BranchProtectionRuleConfig struct {
    Name                  string
    Enabled               bool
    Branch                string
    RequirePR             bool
    RequiredApprovals     int
    DismissStaleReviews   bool
    RequireStatusChecks   []string
    EnforceAdmins         bool
    RequireLinearHistory  bool
    Ignore                IgnoreConfig
    Reconcilers           []ReconcilerConfig
}
```

#### Check Flow

```
evaluate branch_protection rule
  |
  read current protection from GitHub API
  |
  compare each field against expected
  |-- all match → log, increment metric
  `-- mismatch → remediate?
                  |-- yes + not dry_run → update via API
                  |-- yes + dry_run → log "would update"
                  `-- no → log "protection mismatch"
```

#### GitHub App Permissions

Branch protection requires `administration: write` or `organization
administration: write` depending on the ruleset API used.

### Reconciler: `label_sync`

Syncs repository labels from a configuration file (e.g., `labels.yml`).

#### HCL Schema

```hcl
rule "file" "labels" {
  check    = "exists"
  paths    = [".github/labels.yml", ".github/labels.yaml"]
  target   = ".github/labels.yml"
  template = "labels.tmpl"

  reconcile "label_sync" {
    watch         = true
    delete_extra  = false    # delete labels not in the file
  }
}
```

#### Go Types

```go
type LabelSyncConfig struct {
    Watch       bool
    DeleteExtra bool
}
```

#### Reconcile Flow

1. Parse the labels file (YAML: list of `{name, color, description}`)
2. List current repo labels via GitHub API
3. Diff desired vs current
4. Create missing labels, update changed labels
5. If `delete_extra = true`, delete labels not in the file

### Reconciler: `branch_protection`

Applies branch protection settings from a config file. This is distinct
from the `rule "branch_protection"` type -- the rule checks protection
settings directly, while the reconciler reads settings from a file in the
repo.

#### HCL Schema

```hcl
rule "file" "branch_protection_config" {
  check    = "exists"
  paths    = [".github/branch-protection.yml"]
  target   = ".github/branch-protection.yml"
  template = "branch-protection.tmpl"

  reconcile "branch_protection" {
    watch = true
  }
}
```

The reconciler reads the YAML file and applies the settings via the
GitHub branch protection API. The file schema mirrors the HCL
`rule "branch_protection"` fields.

### Reconciler: `workflow_sync`

Ensures a workflow file matches a template. Unlike `exact` mode on file
rules (which creates a PR on mismatch), the workflow_sync reconciler can
take additional actions like triggering the workflow or updating dependent
configurations.

#### HCL Schema

```hcl
rule "file" "ci_lint" {
  check    = "exact"
  paths    = [".github/workflows/lint.yml"]
  target   = ".github/workflows/lint.yml"
  template = "lint-workflow.tmpl"

  reconcile "workflow_sync" {
    watch = true
  }
}
```

The reconciler is primarily useful for the `watch` capability -- detecting
when someone modifies a managed workflow and triggering a rescan to
restore it to the template.

### Metrics

New metrics for setting and branch protection rules:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `repo_guardian_settings_checked_total` | Counter | `rule_name` | Setting rules evaluated |
| `repo_guardian_settings_remediated_total` | Counter | `rule_name` | Settings auto-remediated |
| `repo_guardian_settings_mismatched_total` | Counter | `rule_name` | Settings that don't match expected |
| `repo_guardian_branch_protection_checked_total` | Counter | `rule_name` | Branch protection rules evaluated |
| `repo_guardian_branch_protection_remediated_total` | Counter | `rule_name` | Branch protection auto-remediated |
| `repo_guardian_ignored_total` | Counter | `scope` | Repos/rules skipped by ignore lists (`scope=global` or `scope=rule`) |

## API / Interface Changes

### GitHub Client Interface

New methods on the `github.Client` interface:

```go
// Setting rules
GetVulnerabilityAlertsEnabled(ctx, owner, repo) (bool, error)
EnableVulnerabilityAlerts(ctx, owner, repo) error
DisableVulnerabilityAlerts(ctx, owner, repo) error
UpdateRepository(ctx, owner, repo, opts) error

// Branch protection
GetBranchProtection(ctx, owner, repo, branch) (*BranchProtection, error)
UpdateBranchProtection(ctx, owner, repo, branch, opts) error

// Labels
ListLabels(ctx, owner, repo) ([]*Label, error)
CreateLabel(ctx, owner, repo, label) error
UpdateLabel(ctx, owner, repo, name, label) error
DeleteLabel(ctx, owner, repo, name) error
```

### GitHub App Permissions

| Permission | Access | Required For |
|------------|--------|-------------|
| `administration` | Write | Setting remediation, branch protection |
| `issues` | Write | Label sync (labels are under issues API) |

These are only needed if using the corresponding rule types with
`remediate = true`. Rules that only check (no remediation) need read-only
access.

## Data Model

No persistent storage changes. All state is read from GitHub on each
evaluation.

## Testing Strategy

### Unit Tests -- Ignore Lists

- **Exact match:** `myorg/my-repo` matches `myorg/my-repo`
- **Glob wildcard:** `myorg/terraform-vpc` matches `myorg/terraform-*`
- **No match:** `myorg/my-repo` doesn't match `myorg/other-*`
- **Global vs per-rule:** Global ignore skips all rules, per-rule skips
  only that rule
- **Empty ignore list:** No repos are skipped
- **Case sensitivity:** Matching is case-sensitive (GitHub repo names are
  case-insensitive -- should we normalize?)

### Unit Tests -- Setting Rules

- **Setting matches expected:** No action taken
- **Setting mismatches, remediate=false:** Logs mismatch
- **Setting mismatches, remediate=true:** Calls API to fix
- **Setting mismatches, remediate=true, dry_run:** Logs "would remediate"
- **Unsupported property:** Validation error at config load time

### Unit Tests -- Branch Protection Rules

- **Protection matches:** No action
- **Protection mismatches:** Logs differences
- **Protection with remediation:** Updates via API
- **Branch doesn't exist:** Logs warning, no error

### Unit Tests -- Reconcilers

- **label_sync:** Creates missing labels, updates changed, optionally
  deletes extra
- **branch_protection:** Reads YAML file, applies settings via API
- **workflow_sync:** Detects file change, triggers rescan

## Migration / Rollout Plan

1. Ship ignore lists first -- lowest risk, immediately useful for repos
   that need exemptions
2. Ship setting rules -- read-only first (`remediate = false`), enable
   remediation after validation
3. Ship branch protection rules -- same approach, read-only then remediate
4. Ship remaining reconcilers as they're implemented
5. Each addition is opt-in via `guardian.hcl` -- no impact on existing
   deployments

## Open Questions

1. **Should ignore lists support repo metadata beyond name?** For example,
   ignoring all repos with a specific topic, or all private repos, or all
   repos in a specific team. Name-based matching covers most cases, but
   metadata-based filtering would be more powerful. This could be a future
   enhancement: `ignore { topics = ["archived", "deprecated"] }`.

2. **Should setting rules support custom properties as a `property` type?**
   Currently custom properties are handled by the `custom_properties`
   reconciler attached to the `catalog_info` file rule. Should there also
   be a way to set custom properties directly via `rule "setting"` without
   needing a `catalog-info.yaml` file? This would be useful for properties
   that don't come from Backstage.

3. **Should `rule "branch_protection"` use the legacy branch protection
   API or the newer repository rulesets API?** The rulesets API
   (`/repos/{owner}/{repo}/rulesets`) is more powerful and is GitHub's
   recommended path forward, but it requires different permissions and has
   a different data model. The legacy API
   (`/repos/{owner}/{repo}/branches/{branch}/protection`) is simpler and
   more widely used. Should we support both, or pick one?

4. **Should ignore list changes require a restart or be hot-reloadable?**
   Currently the HCL config is loaded at startup. If ignore lists change
   frequently (e.g., adding repos during an incident), a restart is
   required. Hot-reload (watching the config file for changes) adds
   complexity but improves operational agility.

5. **Should `label_sync` reconciler handle label renames?** If a label is
   renamed in the config file, should the reconciler detect the old label
   and rename it (preserving issues assigned to it), or delete the old and
   create the new (losing the association)?

## References

- [RFC-0002: HCL-driven Policy Engine](../rfc/0002-hcl-driven-policy-engine-for-repo-guardian.md)
- [DESIGN-0006: HCL Policy Configuration and Rule Engine](0006-hcl-policy-configuration-and-rule-engine.md)
- [DESIGN-0007: Reconciler Interface and Push Event Handler](0007-reconciler-interface-and-push-event-handler.md)
- [GitHub Branch Protection API](https://docs.github.com/en/rest/branches/branch-protection)
- [GitHub Repository Rulesets API](https://docs.github.com/en/rest/repos/rules)
- [GitHub Labels API](https://docs.github.com/en/rest/issues/labels)
