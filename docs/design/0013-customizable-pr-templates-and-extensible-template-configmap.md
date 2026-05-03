---
id: DESIGN-0013
title: "Customizable PR templates and extensible template ConfigMap"
status: Draft
author: Donald Gifford
created: 2026-05-03
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0013: Customizable PR templates and extensible template ConfigMap

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-03

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

repo-guardian's PR titles and bodies are hardcoded constants in the
engine, which means orgs that require Jira/Linear ticket prefixes,
conventional-commit style titles, or links to internal documentation
in the body cannot adopt the tool without forking. Separately, the
chart's template ConfigMap exposes only three hardcoded slots
(`codeowners`, `dependabot`, `renovate`), so adding a sixth or
seventh templated rule requires both an HCL change and a chart
release. This design lets operators customize PR titles and bodies
per-rule via Go-template expressions in HCL, and replaces the
hardcoded chart slots with a generic `templates.files` map (plus an
`existingConfigMap` escape hatch) so adding new rule templates is a
config-only operation.

## Goals and Non-Goals

### Goals

- **Per-rule PR title and body customization.** Each rule (and each
  reconciler that opens its own PR) can override the engine defaults
  with a Go-template string.
- **Global defaults at the policy level.** Orgs that want a uniform
  prefix or footer set it once at `defaults.pr {}` and all rules
  inherit unless they override.
- **Useful template variables.** Repo identity, rule identity, the
  list of files added in this PR, the current date, plus an `env`
  helper for runtime substitution (Jira project ID, support URL,
  etc.).
- **Generic template ConfigMap.** Chart's `values.templates.files`
  is a map of `<filename>: <content>` so any template name is
  expressible without chart changes. Existing hardcoded slots
  (`codeowners`, `dependabot`, `renovate`) remain accepted for one
  release as a deprecation runway.
- **`templates.existingConfigMap` escape hatch.** Mirrors
  `policy.existingConfigMap`. Operator points at a ConfigMap they
  manage themselves (out-of-band, GitOps-managed, etc.) and the
  chart skips creating its own.
- **Safe-by-default templating.** Template parse errors at config
  load time, not at PR-creation time. A bad template should fail
  the policy load with a clear message, not silently default to the
  built-in title.

### Non-Goals

- **A general-purpose templating language.** No conditionals, no
  loops over arbitrary data — just variable substitution + a small
  set of helpers (`env`, `join`, `default`). Anything that wants
  more belongs in a reconciler, not a PR title.
- **Markdown linting / rendering of bodies.** Operators write
  whatever Markdown they want; the engine only substitutes variables
  and forwards the result to GitHub.
- **Localization / i18n.** Single language per policy.
- **Per-installation overrides.** A given policy file is one
  configuration. Multi-org / multi-installation customization
  remains the responsibility of the per-org scope blocks
  (DESIGN-0010).
- **Replacing the embedded fallback templates.** The binary still
  ships embedded defaults so a chart with no `templates.files`
  configured still works for the built-in rules.

## Background

Today's engine state for PR generation:

- `internal/checker/engine_policy.go` defines `PRTitle = "chore: add
  missing files"` as a constant, and `buildPRBodyFromPolicy(actionable)`
  hardcodes a Markdown summary of the rules being reconciled.
- Reconcilers that open their own PRs also have hardcoded titles:
  `PropertiesPRTitle`, `CatalogInfoPRTitle` in
  `internal/reconciler/custom_properties.go`.
- HCL `rule` blocks accept a `pr {}` sub-block today, but its only
  field is `search_terms` (used for finding existing PRs to update).
- No template variable substitution exists in the binary.

Today's chart state for templates:

- `charts/repo-guardian/templates/configmap.yaml` builds a ConfigMap
  with three hardcoded keys (`CODEOWNERS.tmpl`, `dependabot.yml.tmpl`,
  `renovate.json.tmpl`), each populated from
  `.Values.templates.<name>` if set.
- `cfg.TemplateDir` (env `TEMPLATE_DIR`, default
  `/etc/repo-guardian/templates`) on the binary side already supports
  loading any number of `*.tmpl` files from a directory and falling
  back to embedded defaults — so the binary is fully extensible.
- The bottleneck is the chart's hardcoded values surface, not the
  binary.

## Detailed Design

### HCL grammar additions

A new `pr {}` block grammar, valid at three scopes:

```hcl
# 1. Policy-level defaults — applies to every rule that doesn't override
defaults {
  pr {
    title  = "chore: {{ .Rule.Name }}"
    body   = <<-EOT
      Generated by repo-guardian.
      Files added:
      {{ range .Files }}- `{{ . }}`
      {{ end }}
    EOT
    labels = ["compliance", "automated"]
  }
}

# 2. Per-rule override
rule "file" "codeowners" {
  check    = "exists"
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  pr {
    title = "[{{ env \"JIRA_PROJECT\" }}-CHORE] add CODEOWNERS"
    body  = "Adds CODEOWNERS per [SEC-001](https://jira.example.com/browse/SEC-1)"
    # labels not set → inherits ["compliance", "automated"] from defaults
  }
}

# 3. Per-reconciler override (reconcilers that open their own PRs)
rule "file" "catalog_info" {
  check    = "contains"
  target   = "catalog-info.yaml"
  template = "catalog-info"

  reconcile "custom_properties" {
    mode  = "github-action"
    watch = true

    pr {
      title = "ci: install custom-properties workflow"
      body  = "Installs the GitHub Action that syncs Backstage metadata to repo properties."
    }
  }
}
```

Resolution order (most specific wins):

1. Reconciler-level `reconcile { pr {} }` (only for reconciler PRs)
2. Rule-level `rule { pr {} }` (only for rule PRs)
3. Policy-level `defaults { pr {} }`
4. Engine built-in (current behavior)

### Template variables

Available in both `title` and `body`:

| Variable | Type | Notes |
|---|---|---|
| `.Owner` | string | Repo owner / org login |
| `.Repo` | string | Repo name |
| `.DefaultBranch` | string | Default branch name |
| `.Rule.Name` | string | HCL rule label (e.g., `codeowners`) |
| `.Rule.Target` | string | Target file path (e.g., `.github/CODEOWNERS`) |
| `.Files` | []string | All file paths included in this PR |
| `.Date` | string | RFC3339 timestamp at PR creation |
| `.Reconciler` | string | Empty for rule PRs; reconciler name for reconciler PRs |

Helpers (Sprig-style subset):

- `env "VAR"` — read environment variable; empty string if unset
- `join sep slice` — string-join a slice
- `default fallback value` — fallback if value is empty
- `lower`, `upper`, `title` — case helpers

The full Sprig library is **not** included — we want a minimal,
auditable surface. Adding a helper requires a code change and tests.

### Multi-rule PR title resolution

When multiple file rules fire on the same sweep against the same
repo, the engine bundles them into a single PR on
`repo-guardian/add-missing-files`. With per-rule titles, this
creates ambiguity. Resolution:

- If two or more rules with different `pr.title` values fire
  together: use the policy-level `defaults.pr.title` (or built-in
  if no default). The per-rule titles are ignored for the bundled
  PR. The engine emits a `slog.Info` log noting the rules whose
  titles were ignored.
- If exactly one rule fires (or all firing rules share the same
  resolved title): use that title.
- Future option: `pr.bundle = "separate"` on a rule could force
  it into its own PR (its own branch, its own title), at the cost
  of more open PRs per repo. Out of scope for V1.

The body is always built from a default template that lists *all*
included rules and files, but the body template can reference
`.Rules` (slice) instead of `.Rule.Name` to format the bundled list.

### Chart values surface

`values.yaml` schema change:

```yaml
templates:
  # Generic map: any filename, any content.
  # Filename WITHOUT .tmpl suffix matches the rule's `template = "..."` reference.
  # Filename WITH .tmpl suffix is what gets written into the ConfigMap.
  files: {}

  # Escape hatch: point at a ConfigMap managed outside the chart.
  # When set, the chart does NOT create its own template ConfigMap.
  existingConfigMap: ""

  # DEPRECATED in this design's release; removed two minor releases later.
  # Behavior preserved: setting these still populates `files` under the hood.
  codeowners: ""
  dependabot: ""
  renovate: ""
```

Example filled-in:

```yaml
templates:
  files:
    codeowners.tmpl: |
      * @platform-team
    dependabot.yml.tmpl: |
      version: 2
      updates:
        - package-ecosystem: github-actions
          directory: /
          schedule:
            interval: weekly
    custom-properties.tmpl: |
      apiVersion: v1
      kind: ConfigMap
      ...
    wf-security-scan.tmpl: |
      name: Security Scan
      on: { push: { branches: [main] } }
      jobs: { ... }
```

`templates/configmap.yaml` rewrite:

- If `templates.existingConfigMap` is set: skip rendering the
  ConfigMap. Mount the named ConfigMap on the Deployment.
- Else: `range $name, $content := .Values.templates.files` and
  emit a key per entry.
- Legacy slots (`codeowners`, `dependabot`, `renovate`) fold into
  `files` at chart-template time with a deprecation warning emitted
  via `helm.sh/hook-failure-policy` or simpler: a NOTES.txt block.

## API / Interface Changes

HCL grammar (added):

- `defaults { pr {} }` block at top level
- `pr {}` block inside `rule "file"` (extends existing)
- `pr {}` block inside `reconcile { ... }` (new)
- Fields: `title`, `body`, `labels`

Go (added):

- `internal/policy/pr.go` — `PRTemplate` struct, `Render(vars *PRVars) (title, body string, err error)`, validation at policy-load time.
- `internal/checker/engine_policy.go` — wire `PRTemplate.Render` into PR creation, fall through to defaults.
- `internal/reconciler/custom_properties.go` (and any reconciler that opens PRs) — accept a `*PRTemplate` from the reconciler config and invoke it.

Chart `values.yaml` (changed):

- Add `templates.files` (map)
- Add `templates.existingConfigMap` (string)
- Mark `templates.codeowners` / `dependabot` / `renovate` as deprecated in helm-docs

Env vars: none. The PR-template feature is policy-driven, not env-driven.

## Data Model

No persistence. Everything is config-resolved at policy load time.

`PRTemplate` struct (sketch):

```go
package policy

type PRTemplate struct {
    Title  string   // raw template string
    Body   string   // raw template string
    Labels []string // applied verbatim, no templating

    titleTpl *template.Template // parsed at load time
    bodyTpl  *template.Template
}

type PRVars struct {
    Owner         string
    Repo          string
    DefaultBranch string
    Rule          PRVarRule
    Rules         []PRVarRule  // populated only for bundled-PR bodies
    Files         []string
    Date          string
    Reconciler    string
}

type PRVarRule struct {
    Name   string
    Target string
}

func (p *PRTemplate) Render(v *PRVars) (title, body string, err error)
```

Templates are parsed once at policy load. Render-time errors are
treated as engine errors (logged, PR creation aborts for that repo).

## Testing Strategy

- **Unit tests** in `internal/policy/pr_test.go`:
  - Parse-time errors caught at policy load (bad template → fail
    policy validation)
  - Render with all variables / helpers
  - Resolution-order tests (reconciler > rule > defaults > built-in)
- **Integration tests** in `internal/checker/engine_policy_test.go`:
  - Rule with custom title → PR created with rendered title
  - Bundled PR with conflicting titles → falls back to defaults
  - Reconciler with custom title → reconciler PR uses it
- **Helm-unittest** for chart changes:
  - `templates.files` map renders into ConfigMap with all keys
  - `templates.existingConfigMap` skips ConfigMap rendering and
    mounts the named one
  - Legacy slots still work
- **Smoke test in homelab** with at least one rule using a Jira-style
  title and `env "JIRA_PROJECT"` substitution.

## Migration / Rollout Plan

1. **Land HCL grammar + Go renderer** in a single PR. Existing
   policies parse unchanged because all new HCL blocks are optional.
2. **Land chart `templates.files` map and `existingConfigMap`** in
   a separate PR. Legacy slots still work; helm-docs emits
   deprecation warnings.
3. **Update `examples/guardian-full.hcl`** to demonstrate the new
   `defaults { pr {} }` and per-rule `pr {}` blocks.
4. **Document migration recipe** in chart README and `docs/ADDING_RULES.md`.
5. **Deprecate legacy chart slots** two minor chart releases after
   `templates.files` ships. Rip them out one minor release later.

Rollback: revert the chart values change (legacy slots still work),
or revert the engine PR (HCL parser ignores unknown blocks with a
warning, so old binary against new HCL is graceful).

## Open Questions

1. **Sprig subset or full Sprig?** Including all of Sprig is one
   import line and gives users `now`, `quote`, `regexReplaceAll`,
   etc. for free. Argument against: surface area, security review
   cost (Sprig has filesystem helpers we'd need to disable),
   templates that drift into mini-programs. Lean toward small
   curated subset; revisit if users complain.
2. **`labels` templating.** Should the `labels` list itself accept
   templates (e.g., `labels = ["jira/{{ env \"JIRA_PROJECT\" }}"]`)
   or stay literal? Lean literal — labels are rarely dynamic.
3. **Multi-rule PR — opt-in to separate PRs?** A `pr.bundle =
   "separate"` flag would force a rule into its own branch / PR
   when bundling would lose its custom title. Cleaner, but doubles
   the PR count. Probably yes for V2; out of scope for V1.
4. **Reconciler PR config inheritance.** Should reconciler PRs
   inherit from `defaults.pr` even though they're for a different
   purpose? Right now the doc says yes. Counter-argument: a
   reconciler PR might want a different default body (different
   rule context). Could add `defaults.reconciler.pr {}` if that
   matters.
5. **Legacy chart slot removal cadence.** Two minor releases is
   pretty fast for a breaking values change. Three? Four? Tied to
   the chart's overall versioning policy.
6. **`existingConfigMap` validation.** Should the chart fail the
   install if the named ConfigMap doesn't exist, or let k8s emit
   a CreateContainerConfigError when the pod tries to mount?
   k8s's native behavior is fine; document it.
7. **Body length limits.** GitHub caps PR bodies at 65535 chars.
   Should we truncate-with-warning or fail at render time? Lean
   truncate-with-warning since orgs sometimes accidentally embed
   large blobs.
8. **Global Markdown linter on bodies?** Probably no — operator
   responsibility — but a doc note recommending standard Markdown
   conventions wouldn't hurt.

## References

- DESIGN-0006 (HCL Policy Configuration and Rule Engine — the
  grammar this design extends)
- DESIGN-0007 (Reconciler Interface — the reconciler pattern whose
  PR creation this design also customizes)
- DESIGN-0010 (Per-org rule scoping — the multi-installation
  customization story this design intentionally does not duplicate)
- Go `text/template` package:
  https://pkg.go.dev/text/template
- Sprig template helpers:
  https://masterminds.github.io/sprig/
- GitHub PR body length limit (empirical, undocumented but
  consistent at 65535 chars)
