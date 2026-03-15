---
id: RFC-0002
title: "HCL-driven Policy Engine for repo-guardian"
status: Draft
author: Donald Gifford
created: 2026-03-14
---
<!-- markdownlint-disable-file MD025 MD041 -->

# RFC 0002: HCL-driven Policy Engine for repo-guardian

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-14

## Summary

Replace repo-guardian's hardcoded file rules, special-cased custom properties
checker, and environment-variable-only configuration with a unified
HCL-driven policy engine. Rules, reconcilers, ignore lists, and
org-level settings are all declared in a configuration file, making it
possible to add new compliance checks, file-change-triggered reconciliation,
and per-repo overrides without code changes.

## Problem Statement

repo-guardian currently has two separate code paths that are conceptually the
same thing at different maturity levels:

1. **File rules** (CODEOWNERS, dependabot, renovate) -- hardcoded Go structs
   in `internal/rules/registry.go`. Adding a new rule means writing Go code,
   recompiling, and redeploying. Rules are binary: the file exists or it
   doesn't. There is no post-merge reconciliation.

2. **Custom properties checker** (`internal/checker/properties.go`) -- a
   completely separate code path that also checks for a file
   (`catalog-info.yaml`), but additionally reads the file's contents and
   reconciles derived state (GitHub custom properties). This is special-cased
   rather than being a capability any rule can have.

**Both follow the same pattern:** ensure a file exists in a repo; if not, PR
it; optionally, when the file exists, read it and reconcile something.

Additional pain points:

- **No ignore/exclusion mechanism.** There is no way to say "skip this rule
  for these repos" or "ignore this repo entirely" without code changes. At
  scale (10k+ repos), there will always be exceptions.

- **No push-event feedback loop.** When repo-guardian creates a PR to add
  `catalog-info.yaml` and the PR is merged, the app has no way to detect the
  change until the next scheduler cycle (default 168h). This is because the
  app doesn't subscribe to `push` events.

- **Env vars don't scale.** Configuration is entirely via environment
  variables. This works for a handful of settings but breaks down for
  structured data like rule definitions, per-repo overrides, field mappings,
  and multi-org support. You cannot express "for repos matching `org/infra-*`,
  skip the renovate rule" in an env var.

- **No content validation.** Rules only check if a file exists. There is no
  way to verify a file's contents are correct -- e.g., CODEOWNERS has an
  actual owner (not a placeholder), or dependabot.yml includes the
  `github-actions` ecosystem.

- **No multi-org support path.** The current architecture assumes a single
  GitHub org. As the tool grows, it needs a way to scope rules and
  configuration per-org.

## Proposed Solution

Introduce an HCL configuration file (`guardian.hcl`) that declaratively
defines:

- **Rules** -- typed compliance checks (file presence, content validation,
  repo settings, branch protection)
- **Reconcilers** -- optional post-change behaviors attached to rules (e.g.,
  read a file and sync derived state somewhere)
- **Ignore lists** -- repos to skip globally or per-rule, with glob support
- **Push event handling** -- which file changes trigger re-evaluation
- **Operational settings** -- worker count, queue size, schedule interval,
  dry-run, etc. (with env var overrides)

### Why HCL

| Feature | HCL | YAML | Env Vars |
|---------|-----|------|----------|
| Typed blocks | Yes | No (flat maps) | No |
| Expressions/functions | Yes | No | No |
| Comments | Yes | Yes | No |
| References between blocks | Yes | No | No |
| Conditionals | Yes | No | No |
| Validation DSL | Via `hcldec` | External | Manual |
| Familiarity | Terraform users | Universal | Universal |
| Nested structure | Natural | Indentation-based | Flat |

HCL is chosen over YAML because it provides typed blocks, expressions,
references between blocks, and composition primitives that map naturally to
policy configuration. It's the same format the team uses with Terraform, so
the learning curve is zero.

## Design

### Configuration File: `guardian.hcl`

```hcl
# Operational settings -- all have built-in defaults, all can be
# overridden by environment variables.
guardian {
  dry_run            = false
  schedule_interval  = "168h"
  worker_count       = 5
  queue_size         = 1000
  log_level          = "info"
}

# Global ignore list -- these repos are skipped for ALL rules.
ignore {
  repos = [
    "myorg/legacy-monolith",
    "myorg/archived-*",
    "myorg/terraform-*",
  ]
}

# --- File presence and content rules ---

rule "file" "codeowners" {
  check  = "contains"   # exists | contains | exact
  paths  = [".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"]
  target = ".github/CODEOWNERS"
  template = "codeowners.tmpl"

  assertion {
    pattern = "^[^#].*@"
    message = "CODEOWNERS must contain at least one owner"
  }

  assertion {
    not_pattern = "@org/CHANGEME"
    message     = "CODEOWNERS still contains the placeholder team"
  }

  pr {
    search_terms = ["codeowners", "CODEOWNERS"]
  }

  ignore {
    repos = ["myorg/special-case"]
  }
}

rule "file" "dependabot" {
  check    = "contains"
  paths    = [".github/dependabot.yml", ".github/dependabot.yaml"]
  target   = ".github/dependabot.yml"
  template = "dependabot.tmpl"

  assertion {
    yaml_path = "updates[*].package-ecosystem"
    contains  = "github-actions"
    message   = "dependabot must include github-actions ecosystem"
  }

  pr {
    search_terms = ["dependabot"]
  }
}

rule "file" "renovate" {
  enabled = false

  check = "exists"
  paths = [
    "renovate.json",
    "renovate.json5",
    ".renovaterc",
    ".renovaterc.json",
    ".github/renovate.json",
    ".github/renovate.json5",
  ]
  target   = "renovate.json"
  template = "renovate.tmpl"

  pr {
    search_terms = ["renovate"]
  }
}

rule "file" "catalog_info" {
  check    = "exists"
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  target   = "catalog-info.yaml"
  template = "catalog-info.tmpl"

  pr {
    search_terms = ["catalog-info"]
  }

  # When the file exists or is modified, reconcile custom properties
  reconcile "custom_properties" {
    mode  = "api"   # or "github-action"
    watch = true    # trigger on push events

    owner       = "spec.owner"
    component   = "metadata.name"
    jira_project = "metadata.annotations.jira/project-key"
    jira_label   = "metadata.annotations.jira/label"

    defaults {
      owner     = "Unclassified"
      component = "Unclassified"
    }
  }
}

rule "file" "ci_lint" {
  check    = "exact"
  paths    = [".github/workflows/lint.yml"]
  target   = ".github/workflows/lint.yml"
  template = "lint-workflow.tmpl"

  pr {
    search_terms = ["lint workflow"]
  }

  reconcile "workflow_sync" {
    watch = true
  }
}

# --- Repository setting rules ---

rule "setting" "vulnerability_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = true
}

rule "setting" "default_branch" {
  property = "default_branch"
  expected = "main"
}

# --- Branch protection rules ---

rule "branch_protection" "main" {
  branch                = "main"
  require_pr            = true
  required_approvals    = 1
  dismiss_stale_reviews = true
  require_status_checks = ["ci/lint", "ci/test"]

  reconcile "branch_protection" {
    watch = true
  }
}
```

### Config File Loading

**Supports both single file and directory loading.** The `GUARDIAN_CONFIG`
env var (default: `/etc/repo-guardian/guardian.hcl`) can point to either:

- A single `.hcl` file
- A directory containing one or more `.hcl` files

When pointing to a directory, all `.hcl` files are loaded and merged. HCL
handles block merging natively. This allows splitting large configurations
into logical files (e.g., `rules-files.hcl`, `rules-settings.hcl`,
`ignores.hcl`).

### Architecture Overview

```
guardian.hcl (file or directory)
    |
    v
+-------------------+
| HCL Config Loader |  (internal/policy)
| - Parse & validate|
| - Build rule set  |
| - Merge env vars  |
+-------------------+
    |
    v
+-------------------+     +-------------------+
| Rule Engine       |     | Reconciler        |
| (replaces current |     | Registry          |
|  FileRule registry|     |                   |
|  + properties     |     | - custom_props    |
|  checker)         |     | - label_sync      |
+-------------------+     | - branch_protect  |
    |                     | - workflow_sync   |
    v                     +-------------------+
+-------------------+           |
| Checker Engine    |<----------+
| (refactored)      |
+-------------------+
    |
    v
+-------------------+
| Webhook Handler   |
| + push event      |
|   handler         |
+-------------------+
```

### Rule Types

Rules use the two-label HCL block syntax: `rule "<type>" "<name>"`. The type
determines which schema validates the block, and the name is the unique
identifier for logging, metrics, and ignore lists.

| Rule Type | Purpose | Check Modes |
|-----------|---------|-------------|
| `file` | Ensure a file exists with correct content | `exists`, `contains`, `exact` |
| `setting` | Ensure a repo setting has expected value | boolean match, string match |
| `branch_protection` | Ensure branch protection rules are set | structured comparison |

#### File Rule Check Modes

| Mode | Behavior | Use Case |
|------|----------|----------|
| `exists` | File is present, content ignored | Renovate config, catalog-info.yaml |
| `contains` | File is present and passes assertions | CODEOWNERS has a real owner, dependabot includes ecosystems |
| `exact` | File must match template (semantic diff for YAML, byte diff for plaintext) | CI workflows, standardized configs |

#### Content Assertions

Assertions are typed by which field is used:

- **`pattern`** / **`not_pattern`** -- regex matching for unstructured files
  (CODEOWNERS, Makefile, etc.)
- **`yaml_path`** + **`contains`** / **`equals`** -- YAML-aware path
  matching, parser inferred from file extension
- **`json_path`** (future) -- JSON-aware path matching

For `exact` mode, YAML files use semantic comparison (ignore
comment/whitespace differences) rather than byte-level diff.

### Reconciler Types

Reconcilers are optional behaviors attached to rules. They run when a rule's
target file exists (or is modified, if `watch = true`). Each reconciler type
has a fixed schema validated at config load time.

| Reconciler Type | Purpose | Fixed Schema Fields |
|-----------------|---------|---------------------|
| `custom_properties` | Read file, extract values, sync to GitHub custom properties | `mode`, `owner`, `component`, `jira_project`, `jira_label`, `defaults` |
| `label_sync` | Read labels from file, sync to GitHub repo labels | TBD |
| `branch_protection` | Read settings from file, apply branch protection | TBD |
| `workflow_sync` | Ensure workflow file matches template | TBD |

The reconciler interface in Go:

```go
type Reconciler interface {
    // Name returns the reconciler identifier (e.g., "custom_properties").
    Name() string

    // Reconcile is called when the rule's file exists in the repo.
    // content is the file's raw content.
    Reconcile(ctx context.Context, client github.Client,
        owner, repo, defaultBranch, content string) error
}
```

Adding a new reconciler type requires:

1. Go code for the reconciler implementation
2. HCL schema definition for the reconciler's fixed fields
3. No changes to the rule engine or config loader

### Ignore Lists

Two levels:

- **Global** -- `ignore { repos = [...] }` at the top level skips repos for
  all rules
- **Per-rule** -- `ignore { repos = [...] }` inside a rule block skips that
  specific rule for those repos

Patterns support glob matching (e.g., `myorg/terraform-*`).

Evaluation order:

1. Global ignore list checked first (cheapest)
2. Per-rule ignore list checked before rule evaluation

### Push Event Handling

When a rule has `reconcile { watch = true }`, repo-guardian handles `push`
events for the rule's file paths.

The push handler:

1. Checks the push is to the default branch
2. Scans `commits[].added` and `commits[].modified` for watched file paths
3. Enqueues the repo if a match is found

This closes the feedback loop where repo-guardian creates a
`catalog-info.yaml` PR, and after merge, immediately reconciles the custom
properties rather than waiting for the weekly scheduler.

The GitHub App must subscribe to the `push` event (one-time manual config).
No new permissions are required.

### Config Loading and Env Var Interaction

The HCL config file is optional. When absent, the system falls back to the
current env-var-only behavior, preserving full backward compatibility.

When present, the loading order is:

1. Built-in defaults (same as today)
2. `guardian.hcl` overrides defaults (all settings including operational)
3. Environment variables override `guardian.hcl`

This means env vars always win, which is important for Kubernetes deployments
where secrets (webhook secret, private key) should never be in the HCL file.

The config file path is set via `GUARDIAN_CONFIG` env var (default:
`/etc/repo-guardian/guardian.hcl`).

### Migration from Current System

The refactoring is designed to be non-breaking:

1. The existing `DefaultRules` in `internal/rules/registry.go` become the
   built-in defaults when no HCL config is present
2. The existing env vars continue to work unchanged
3. The custom properties checker logic moves into a `custom_properties`
   reconciler but the behavior is identical
4. The `CUSTOM_PROPERTIES_MODE` env var continues to work, configuring the
   built-in `catalog_info` rule's reconciler

A deployment can migrate incrementally: start using `guardian.hcl` for new
rules and overrides while keeping existing env vars for operational settings.

### Helm Chart Changes

Add support for mounting `guardian.hcl` via ConfigMap:

```yaml
# values.yaml
policy:
  # -- Inline HCL policy configuration
  config: ""
  # -- Use an existing ConfigMap for the policy file
  existingConfigMap: ""
```

Both inline and external ConfigMap are supported. When `policy.config` is
set, the chart creates a ConfigMap with the HCL content and mounts it at
`/etc/repo-guardian/guardian.hcl`. When `policy.existingConfigMap` is set,
the chart mounts that ConfigMap instead.

## Alternatives Considered

### 1. YAML Configuration File

YAML is more universally known, but it lacks HCL's expressiveness for this
use case. Policy rules with nested blocks, optional reconcilers, per-rule
overrides, and glob patterns are awkward in YAML. YAML also doesn't support
comments in a standardized way across all parsers, and doesn't have
expressions or references. Since the team already uses HCL for Terraform,
the learning curve is zero.

### 2. CUE

CUE provides stronger validation and typing than HCL, but it's less
mainstream, has a steeper learning curve, and the Go ecosystem support is
less mature. It would be overkill for a configuration file that's essentially
a list of rules with optional blocks.

### 3. Keep Env Vars, Add a JSON/YAML Rules File

Split the problem: keep env vars for operational config, add a separate
rules file for compliance checks. This avoids the HCL dependency but means
two configuration mechanisms that don't compose well. Per-rule overrides and
ignore lists would still be awkward in YAML.

### 4. Go Plugin System (No Config File)

Keep rules in Go code but make them more pluggable via interfaces. This
maintains type safety but doesn't solve the "add a rule without recompiling"
problem. It also doesn't address ignore lists or per-repo overrides.

## Implementation Phases

### Phase 1: HCL Config Parser and Policy Types

Define the Go types that represent the HCL schema (`PolicyConfig`, `Rule`,
`Reconciler`, `IgnoreList`). Implement the HCL parser using
`hashicorp/hcl/v2`. Write a config loader that supports single file and
directory loading, and merges HCL settings with env var overrides.

**Deliverable:** `internal/policy` package that can parse `guardian.hcl` and
produce a typed config struct. Comprehensive tests for parsing, validation,
env var override behavior, and directory loading.

### Phase 2: Rule Engine Refactor

Replace the hardcoded `FileRule` registry with a generic rule engine that
reads from the parsed policy config. The checker engine accepts rules from
either the policy config or the built-in defaults (when no config file is
present). Support all three check modes (`exists`, `contains`, `exact`).

**Deliverable:** Existing file rules (CODEOWNERS, dependabot, renovate) work
identically whether configured via HCL or built-in defaults. Content
assertions work for both regex and YAML path matching. All existing tests
pass.

### Phase 3: Reconciler Interface and Custom Properties Migration

Define the `Reconciler` interface. Migrate the existing custom properties
checker into a `custom_properties` reconciler with a fixed schema. The
reconciler is attached to the `catalog_info` rule and runs when
`catalog-info.yaml` exists.

**Deliverable:** Custom properties behavior is identical but driven by the
reconciler interface. The `CUSTOM_PROPERTIES_MODE` env var continues to work
as a shorthand for configuring the built-in reconciler.

### Phase 4: Ignore Lists

Implement global and per-rule ignore lists with glob matching. The checker
engine checks ignore lists before evaluating rules. Ignore lists are
configured in `guardian.hcl`.

**Deliverable:** Repos and rules can be excluded via HCL config. Tests cover
exact match, glob patterns, global vs per-rule scoping.

### Phase 5: Push Event Handler

Add `push` event handling to the webhook handler. When a rule has
`reconcile { watch = true }`, pushes to the default branch that modify the
rule's watched files trigger re-evaluation.

**Deliverable:** Merging a `catalog-info.yaml` PR triggers an immediate
custom properties rescan. Metric: `repos_checked_total{trigger="push"}`.

### Phase 6: Additional Rule Types

Implement `rule "setting"` and `rule "branch_protection"` types. Define
their HCL schemas, checker logic, and reconciler implementations
(`label_sync`, `branch_protection`, `workflow_sync`).

**Deliverable:** Setting and branch protection rules can be declared in
`guardian.hcl` and evaluated by the checker engine.

### Phase 7: Helm Chart and Documentation

Update the Helm chart to support `guardian.hcl` via inline values or
external ConfigMap. Update documentation, migration guide, and examples.

**Deliverable:** Users can deploy with HCL config via Helm values. README
and chart values document the new configuration model.

## Risks and Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| HCL dependency adds complexity | Medium | Low | `hashicorp/hcl/v2` is a single, stable, well-maintained dependency. It's the same library Terraform uses. |
| Breaking existing deployments | High | Low | HCL config is optional. Existing env-var-only deployments work unchanged. Migration is incremental. |
| Push event volume overwhelms the system | Medium | Medium | Filter in the webhook handler (no API calls). Work queue drops jobs when full. Can remove `push` event subscription as a kill switch. |
| Over-engineering for current scale | Low | Medium | Phase the implementation. Each phase delivers standalone value. |
| Config file drift between environments | Medium | Medium | Env vars override HCL, so secrets and per-env settings stay in Kubernetes Secrets/ConfigMaps. The HCL file is the same across envs. |

## Resolved Questions

1. **Single file vs directory loading:** Support both from the start. A
   single file or a directory of `.hcl` files. `GUARDIAN_CONFIG` can point
   to either.

2. **Operational settings location:** All settings (including operational
   ones like `worker_count`, `schedule_interval`) are declared in the HCL
   file with defaults. Environment variables override any HCL setting.

3. **Reconciler mapping style:** Fixed schema per reconciler type. Each
   reconciler defines its own typed fields (e.g., `owner`, `component`,
   `jira_project` for `custom_properties`). Adding new fields requires
   Go code changes, but validation happens at config load time.

4. **Reconciler types:** Four concrete types planned: `custom_properties`,
   `label_sync`, `branch_protection`, `workflow_sync`. The interface is
   justified.

5. **Content validation:** Yes, three check modes: `exists` (presence only),
   `contains` (presence + content assertions), `exact` (must match
   template). Assertions support regex (`pattern` / `not_pattern`) for
   unstructured files and YAML path matching (`yaml_path` + `contains` /
   `equals`) for structured files. Parser is inferred from file extension.

6. **Rule types:** Design the schema for multiple rule types from the
   start: `rule "file"`, `rule "setting"`, `rule "branch_protection"`.
   The two-label HCL block syntax (`rule "<type>" "<name>"`) naturally
   accommodates this.

7. **Config delivery in Kubernetes:** Both inline Helm values
   (`policy.config`) and external ConfigMap (`policy.existingConfigMap`).

## Success Criteria

- Adding a new file presence rule requires only a `guardian.hcl` change --
  no Go code, no recompile, no redeploy (just a ConfigMap update + rollout)
- Content assertions catch files that exist but are incorrect (placeholder
  owners, missing ecosystems)
- Ignoring a repo or rule for specific repos is a one-line config change
- `catalog-info.yaml` changes are detected within seconds of merge (push
  event handler) rather than up to 168h (scheduler)
- Existing deployments with env-var-only configuration continue to work
  unchanged
- The `custom_properties` reconciler produces identical behavior to the
  current special-cased checker
- Setting and branch protection rules can be declared and enforced via HCL

## References

- [RFC-0001: Repo Compliance App](0001-repo-compliance-app-repo-guardian.md)
- [DESIGN-0001: Custom Properties from Backstage](../design/0001-custom-properties-from-backstage.md)
- [hashicorp/hcl v2](https://github.com/hashicorp/hcl)
- [GitHub Push Event Payload](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push)
- Current rule registry: `internal/rules/registry.go`
- Current custom properties checker: `internal/checker/properties.go`
- Current webhook handler: `internal/webhook/handler.go`
