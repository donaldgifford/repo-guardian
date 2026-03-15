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
org-level settings are all declared in a single configuration file, making it
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

- **No multi-org support path.** The current architecture assumes a single
  GitHub org. As the tool grows, it needs a way to scope rules and
  configuration per-org.

## Proposed Solution

Introduce an HCL configuration file (`guardian.hcl`) that declaratively
defines:

- **Rules** -- what files to ensure exist, with templates and target paths
- **Reconcilers** -- optional post-merge behaviors attached to rules (e.g.,
  read a file and sync derived state somewhere)
- **Ignore lists** -- repos to skip globally or per-rule, with glob support
- **Push event handling** -- which file changes trigger re-evaluation
- **Operational settings** -- worker count, queue size, schedule interval,
  dry-run, etc.

HCL is chosen over YAML because it provides typed blocks, expressions,
references between blocks, and composition primitives that map naturally to
policy configuration. It's also the format the team already uses with
Terraform.

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

## Design

### Configuration File: `guardian.hcl`

```hcl
# Operational settings (override env vars when set)
guardian {
  dry_run            = false
  schedule_interval  = "168h"
  worker_count       = 5
  queue_size         = 1000
  log_level          = "info"
}

# Global ignore list
ignore {
  repos = [
    "myorg/legacy-monolith",
    "myorg/archived-*",
    "myorg/terraform-*",
  ]
}

# --- File presence rules ---

rule "file" "codeowners" {
  paths    = [".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"]
  template = "codeowners.tmpl"
  target   = ".github/CODEOWNERS"

  pr {
    search_terms = ["codeowners", "CODEOWNERS"]
  }

  ignore {
    repos = ["myorg/special-case"]
  }
}

rule "file" "dependabot" {
  paths    = [".github/dependabot.yml", ".github/dependabot.yaml"]
  template = "dependabot.tmpl"
  target   = ".github/dependabot.yml"

  pr {
    search_terms = ["dependabot"]
  }
}

rule "file" "renovate" {
  enabled = false

  paths = [
    "renovate.json",
    "renovate.json5",
    ".renovaterc",
    ".renovaterc.json",
    ".github/renovate.json",
    ".github/renovate.json5",
  ]

  template = "renovate.tmpl"
  target   = "renovate.json"

  pr {
    search_terms = ["renovate"]
  }
}

rule "file" "catalog_info" {
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  template = "catalog-info.tmpl"
  target   = "catalog-info.yaml"

  pr {
    search_terms = ["catalog-info"]
  }

  # When the file exists (or is added/modified), run this reconciler
  reconcile "custom_properties" {
    mode = "api"  # or "github-action"

    # Watch for changes on the default branch to trigger re-evaluation
    watch = true

    mapping {
      Owner       = "spec.owner"
      Component   = "metadata.name"
      JiraProject = "metadata.annotations.jira/project-key"
      JiraLabel   = "metadata.annotations.jira/label"
    }

    defaults {
      Owner     = "Unclassified"
      Component = "Unclassified"
    }
  }
}
```

### Architecture Overview

```
guardian.hcl
    |
    v
+-------------------+
| HCL Config Loader |  (internal/policy)
| - Parse & validate|
| - Build rule set  |
+-------------------+
    |
    v
+-------------------+     +-------------------+
| Rule Engine       |     | Reconciler        |
| (replaces current |     | Registry          |
|  FileRule registry|     | (new)             |
|  + properties     |     |                   |
|  checker)         |     | - custom_props    |
+-------------------+     | - (future types)  |
    |                      +-------------------+
    v                           |
+-------------------+           |
| Checker Engine    |<----------+
| (refactored)      |
+-------------------+
    |
    v
+-------------------+
| Webhook Handler   |
| + push event      |
| handler (new)     |
+-------------------+
```

### Core Concepts

#### Rule

A rule defines a compliance check. The `"file"` type (the only type
initially) checks for file presence and creates a PR if missing. Future rule
types could check file content, branch protection settings, etc.

Every rule has:
- `paths` -- where to look (any match satisfies the rule)
- `template` -- what to create if missing
- `target` -- where to create it
- `enabled` -- toggle (default true)
- `pr` -- PR detection settings
- `ignore` -- per-rule repo exclusions
- Zero or more `reconcile` blocks

#### Reconciler

A reconciler is an optional behavior that runs when a rule's file **exists**.
It reads the file and takes action based on its contents. The first (and
currently only) reconciler type is `custom_properties`, which:

1. Parses the file (e.g., `catalog-info.yaml`)
2. Extracts values via a configurable `mapping`
3. Diffs against current GitHub custom properties
4. Sets them via API or creates a PR with a GHA workflow

The reconciler interface is generic:

```go
type Reconciler interface {
    // Name returns the reconciler identifier (e.g., "custom_properties").
    Name() string

    // Reconcile is called when the rule's file exists in the repo.
    // content is the file's raw content.
    Reconcile(ctx context.Context, client github.Client, owner, repo, defaultBranch, content string) error
}
```

Future reconciler types could include: `label_sync` (read labels from a file
and sync to GitHub), `branch_protection` (read settings from a file and
apply), etc.

#### Ignore Lists

Two levels:
- **Global** -- `ignore { repos = [...] }` at the top level skips repos for
  all rules
- **Per-rule** -- `ignore { repos = [...] }` inside a rule block skips that
  specific rule for those repos

Patterns support glob matching (e.g., `myorg/terraform-*`).

#### Push Event Handling

When a rule has `reconcile { watch = true }`, repo-guardian subscribes to
`push` events and checks if the rule's `paths` were modified. If so, it
re-enqueues the repo for evaluation.

The push handler:
1. Checks the push is to the default branch
2. Scans `commits[].added` and `commits[].modified` for watched file paths
3. Enqueues the repo if a match is found

This closes the feedback loop where repo-guardian creates a
`catalog-info.yaml` PR, and after merge, immediately reconciles the custom
properties rather than waiting for the weekly scheduler.

### Config Loading and Env Var Interaction

The HCL config file is optional. When absent, the system falls back to the
current env-var-only behavior, preserving full backward compatibility.

When present, the loading order is:

1. Built-in defaults (same as today)
2. `guardian.hcl` overrides defaults
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

Add a new ConfigMap to mount `guardian.hcl` into the pod:

```yaml
# values.yaml
policy:
  # -- Inline HCL policy configuration
  config: ""
  # -- Use an existing ConfigMap for the policy file
  existingConfigMap: ""
```

When `policy.config` is set, the chart creates a ConfigMap with the HCL
content and mounts it at `/etc/repo-guardian/guardian.hcl`.

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
`hashicorp/hcl/v2`. Write a config loader that merges HCL + env vars.

**Deliverable:** `internal/policy` package that can parse `guardian.hcl` and
produce a typed config struct. Comprehensive tests for parsing, validation,
and env var override behavior.

### Phase 2: Rule Engine Refactor

Replace the hardcoded `FileRule` registry with a generic rule engine that
reads from the parsed policy config. The checker engine accepts rules from
either the policy config or the built-in defaults (when no config file is
present).

**Deliverable:** Existing file rules (CODEOWNERS, dependabot, renovate) work
identically whether configured via HCL or built-in defaults. All existing
tests pass.

### Phase 3: Reconciler Interface and Custom Properties Migration

Define the `Reconciler` interface. Migrate the existing custom properties
checker into a `custom_properties` reconciler. The reconciler is attached to
the `catalog_info` rule and runs when `catalog-info.yaml` exists.

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

### Phase 6: Helm Chart and Documentation

Update the Helm chart to support `guardian.hcl` via ConfigMap. Update
documentation, migration guide, and examples.

**Deliverable:** Users can deploy with HCL config via Helm values. README
and chart values document the new configuration model.

## Risks and Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| HCL dependency adds complexity | Medium | Low | `hashicorp/hcl/v2` is a single, stable, well-maintained dependency. It's the same library Terraform uses. |
| Breaking existing deployments | High | Low | HCL config is optional. Existing env-var-only deployments work unchanged. Migration is incremental. |
| Push event volume overwhelms the system | Medium | Medium | Filter in the webhook handler (no API calls). Work queue drops jobs when full. Can remove `push` event subscription as a kill switch. |
| Over-engineering for current scale | Low | Medium | Phase the implementation. Ship HCL parsing and rule refactor first. Reconcilers and push events come later. Each phase delivers standalone value. |
| Config file drift between environments | Medium | Medium | Env vars override HCL, so secrets and per-env settings stay in Kubernetes Secrets/ConfigMaps. The HCL file is the same across envs. |

## Open Questions

1. **Should `guardian.hcl` support multiple files or includes?** For large
   organizations, a single file may become unwieldy. HCL supports file
   merging natively -- should we support loading a directory of `.hcl` files
   (e.g., `/etc/repo-guardian/policy.d/*.hcl`)? Or keep it simple with one
   file initially?

2. **Should operational settings (worker_count, schedule_interval, etc.) move
   into the HCL file or stay as env vars only?** Putting them in HCL means
   one config surface, but env vars are the standard for container
   configuration and are easier to override per-environment. The current
   design proposes HCL + env var override, but we could keep operational
   settings env-var-only and use HCL purely for policy/rules.

3. **Should the `reconcile` block support arbitrary key-value `mapping` or
   use a fixed schema per reconciler type?** A generic mapping
   (`Owner = "spec.owner"`) is flexible but loosely typed. A fixed schema per
   reconciler type is safer but requires Go code changes to add new
   mappings. The current custom properties checker has 4 fixed fields.

4. **What reconciler types do we foresee beyond `custom_properties`?** If
   `custom_properties` is likely the only reconciler for a long time, the
   interface may be premature abstraction. If there are concrete near-term
   use cases (label sync, branch protection, etc.), the interface pays for
   itself.

5. **Should rules support content validation beyond presence checks?** For
   example: "dependabot.yml must include `github-actions` ecosystem." This
   would make rules significantly more powerful but also more complex. Is
   this a near-term need or future work?

6. **Should we support `rule "file"` as the only rule type initially, or
   design the HCL schema to accommodate future rule types from the start?**
   For example, `rule "branch_protection" "main"` or
   `rule "setting" "vulnerability_alerts"`. If we only support `"file"`,
   we can simplify the schema. If we design for extensibility, the schema
   is slightly more complex but doesn't need breaking changes later.

7. **How should the config file be delivered in Kubernetes?** Options:
   a) Inline in Helm values (`policy.config: |`)
   b) External ConfigMap (`policy.existingConfigMap: my-config`)
   c) Baked into the Docker image (not recommended but possible)
   The current design proposes both (a) and (b) via Helm values.

## Success Criteria

- Adding a new file presence rule requires only a `guardian.hcl` change --
  no Go code, no recompile, no redeploy (just a ConfigMap update + rollout)
- Ignoring a repo or rule for specific repos is a one-line config change
- `catalog-info.yaml` changes are detected within seconds of merge (push
  event handler) rather than up to 168h (scheduler)
- Existing deployments with env-var-only configuration continue to work
  unchanged
- The `custom_properties` reconciler produces identical behavior to the
  current special-cased checker

## References

- [RFC-0001: Repo Compliance App](0001-repo-compliance-app-repo-guardian.md)
- [DESIGN-0001: Custom Properties from Backstage](../design/0001-custom-properties-from-backstage.md)
- [hashicorp/hcl v2](https://github.com/hashicorp/hcl)
- [GitHub Push Event Payload](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push)
- Current rule registry: `internal/rules/registry.go`
- Current custom properties checker: `internal/checker/properties.go`
- Current webhook handler: `internal/webhook/handler.go`
