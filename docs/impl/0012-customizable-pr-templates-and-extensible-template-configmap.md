---
id: IMPL-0012
title: "Customizable PR templates and extensible template ConfigMap"
status: Completed
author: Donald Gifford
created: 2026-05-03
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0012: Customizable PR templates and extensible template ConfigMap

**Status:** Completed (in-repo phases 1–7); Phase 7.4 homelab smoke operator-side
**Author:** Donald Gifford
**Date:** 2026-05-03

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: internal/template/ package foundation](#phase-1-internaltemplate-package-foundation)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Migrate TemplateStore to compiled templates](#phase-2-migrate-templatestore-to-compiled-templates)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Rewrite embedded templates to dotted-path syntax](#phase-3-rewrite-embedded-templates-to-dotted-path-syntax)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: HCL pr {} grammar + PRTemplate](#phase-4-hcl-pr--grammar--prtemplate)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Engine PR creation integration + render-time behavior](#phase-5-engine-pr-creation-integration--render-time-behavior)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Chart values surface](#phase-6-chart-values-surface)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Examples + migration docs + smoke](#phase-7-examples--migration-docs--smoke)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement the unified templating system described in DESIGN-0013: one
Go `text/template`-based renderer in `internal/template/` that serves
file-template rendering, PR titles, and PR bodies. Replace the legacy
`OWNER_VALUE`-style substitution in reconcilers with dotted-path Go
template syntax. Add HCL `pr {}` blocks at three scopes (defaults,
rule, reconcile) with explicit `inherits` control. Replace the chart's
hardcoded `templates.codeowners` / `dependabot` / `renovate` slots
with a generic `templates.files` map plus `existingConfigMap` escape
hatch and `templating.vars` env-var pass-through.

**Implements:** DESIGN-0013

## Scope

### In Scope

- New Go package `internal/template/` with `Renderer`, `Compiled`,
  variable contexts (`Common`, `FileVars`, `PRVars`), and curated
  helpers (`env`, `default`, `join`, `lower`, `upper`, `title`).
- Migration of `internal/rules/registry.go.TemplateStore` so
  `Get(name)` returns `*template.Compiled` (parsed at load time).
- Removal of `internal/reconciler/reconciler.go.renderTemplate` and
  every caller in favor of `template.Renderer.Render(...)`.
- Rewrite of embedded templates (`catalog-info.tmpl`,
  `set-custom-properties.tmpl`, `renovate.tmpl`) to dotted-path syntax.
- HCL grammar additions: `pr {}` block at policy `defaults`, per-rule,
  and per-reconciler scopes; new `inherits` boolean field.
- Engine resolution order for PR title/body/labels:
  reconciler → rule → defaults → built-in, with field-by-field
  inheritance and `inherits = false` skipping parents.
- Multi-rule bundled-PR title fallback to `defaults.pr.title` on
  conflict.
- PR body truncation to ~65000 chars with visible marker + `slog.Warn`.
- `STRICT_TEMPLATES=true` env var (and `--strict-templates` CLI flag)
  for opt-in zero-value-context render-check at policy load.
- Chart `values.templates.files` (map),
  `values.templates.existingConfigMap` (string), `values.templating.vars`
  (map → Deployment env vars).
- Removal of legacy `values.templates.codeowners` / `dependabot` /
  `renovate` chart slots.
- helm-unittest cases for the new map-driven ConfigMap and
  existingConfigMap passthrough.
- `examples/guardian-full.hcl` updated to demonstrate
  `defaults { pr {} }` and per-rule `pr {}` blocks.
- Migration documentation in chart README, `docs/ADDING_RULES.md`,
  CHANGELOG entries for binary AND chart.
- Homelab smoke validation with a Jira-style title using
  `env "JIRA_PROJECT"`.

### Out of Scope

- Conditionals, loops over arbitrary data, or full Sprig in templates
  (V1 ships the small curated helper set).
- Markdown linting or rendering of bodies.
- Localization / i18n of PR text.
- Per-installation PR-template overrides (covered by per-org scope
  blocks in DESIGN-0010, not duplicated here).
- A backward-compat shim for `OWNER_VALUE`-style placeholders.
- Per-rule "force separate PR" flag (`pr.bundle = "separate"`
  deferred to V2 per DESIGN-0013 Resolved Q3).
- Replacing the embedded fallback templates (binary still ships
  defaults; chart consumer just gets richer override surface).
- Env-var allow-listing for the `env` template helper (operator owns
  the binary's runtime env; document the risk).

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all
its tasks are checked off and its success criteria are met. Commit at
the end of every numbered task with a conventional commit message.

DESIGN-0013 ships as one coordinated change set because the unified
renderer touches both PR creation and reconciler file rendering;
splitting them would leave the codebase mid-migration with two
templating systems active at once. The phase numbering here tracks
development order within that single PR (or stacked-PR sequence on a
single feature branch); each phase ends in a CI-green state but the
chart and binary aren't released until phase 7.

---

### Phase 1: `internal/template/` package foundation

Pure addition. New package with renderer, contexts, helpers, and tests.
Nothing yet calls it; CI must stay green throughout.

#### Tasks

- [x] Add `internal/template/template.go` with:
  - `Renderer` struct holding the curated `template.FuncMap`.
  - `NewRenderer()` constructor.
  - `Renderer.Parse(name, body string) (*Compiled, error)` —
    compiles a Go `text/template` and returns a `*Compiled`.
  - `Compiled.Render(vars any) (string, error)` — executes against
    any typed context. Nil receiver returns `ErrNilCompiled`
    sentinel.
- [x] Add `internal/template/contexts.go` with:
  - `Common` struct (`Owner`, `Repo`, `DefaultBranch`, `Date`).
  - `FileVars` struct embedding `Common` (`Rule`,
    `Catalog *CatalogInfo`, `Org` alias).
  - `PRVars` struct embedding `Common` (`Rule Rule`, `Rules []Rule`,
    `Files []string`, `Reconciler string`).
  - `Rule` struct (`Name`, `Target`).
  - `CatalogInfo` struct (`Owner`, `Component`, `JiraProject`,
    `JiraLabel`).
- [x] Add `internal/template/helpers.go` with the curated helper set:
  `env`, `default`, `join`, `lower`, `upper`, `title`. Each helper
  has godoc on the exported binding documenting the signature and
  edge cases.
- [x] Add `internal/template/strict.go` with
  `ValidateZero[T any](c *Compiled) error` — runs the compiled
  template against a zero-value of context type T. Generic version
  preferred over `reflect.Type` (cleaner call site,
  compile-time-checked). Used for opt-in strict mode in Phase 5.
- [x] Add `internal/template/template_test.go`:
  - Parse-error positive and negative cases.
  - Each helper has positive + edge-case tests.
  - Render against a zero-value `FileVars` and `PRVars` to catch nil
    pointer derefs.
  - Render against populated contexts proves byte-equivalence to a
    known expected string.
  - Confirm the same `Renderer` serves both `FileVars` and `PRVars`.
  - Coverage: 97.1% (target was ≥85%).
- [x] Doc comment at the top of `template.go` documents the security
  posture of the `env` helper (no allow-list; operator-trusted).

#### Success Criteria

- `go build ./...` succeeds.
- `make test` green (new package has ≥85% coverage).
- `make lint` and `make fmt` green.
- No production code calls the new package yet — verifiable via
  `grep -r 'internal/template' internal/ cmd/` returning only the
  package's own files.

---

### Phase 2: Migrate `TemplateStore` to compiled templates

Replace the legacy `strings.ReplaceAll`-based renderer with the new
`internal/template` package. CI must stay green throughout.

#### Tasks

- [x] Update `internal/rules/registry.go`:
  - `TemplateStore` holds a `Renderer *template.Renderer` and a map
    of name → `*template.Compiled` (parsed once at load) plus a
    parallel `raw` map for byte-exact CheckExact comparisons.
  - `TemplateStore.Get(name)` now returns
    `(*template.Compiled, error)`.
  - `TemplateStore.Raw(name)` returns `(string, error)` for the
    CheckExact byte-comparison case.
  - `LoadTemplates(...)` parses every embedded + filesystem-loaded
    `.tmpl` file via `Renderer.Parse`. Parse errors fail the load
    with location context (`"compiling template %q: %v"`).
  - GHA-expression templates (containing `${{`) are stored as
    raw-passthrough via `Renderer.Raw` since their `{{` markers
    are GHA syntax, not Go template syntax.
- [x] Delete `internal/reconciler/custom_properties.go.renderTemplate`
  and every internal caller (the function lived in
  custom_properties.go, not reconciler.go as the IMPL doc
  originally stated). Inline migration:
  - `internal/reconciler/custom_properties.go` — both render sites
    (set-custom-properties and catalog-info) now call
    `compiled.Render(template.FileVars{Common: ..., Catalog: ...})`.
- [x] Update the engine's file-creation paths:
  - `internal/checker/engine.go` (legacy path L260) — render via
    `compiled.Render(FileVars{...})` with full Common context.
  - `internal/checker/engine_policy.go` (policy path L559) — same
    treatment, plus CheckExact paths (L166, L384) switched to
    `Raw()` for byte comparison.
- [x] Update `internal/checker/engine_policy_test.go` (six sites) to
  call `Raw()` for fileContents fixtures since they need the
  pre-render template body. Hand-written reconciler mocks stay;
  custom_properties_test.go works unchanged.
- [x] Confirm rendered output is byte-equivalent to the old renderer
  via the legacy-syntax translator
  (`translateLegacyPlaceholders`): templates with no `{{` markers
  are pre-translated from `OWNER_VALUE`-style to dotted-path before
  Parse, so the rendered output matches the legacy substitution
  output exactly.
- [x] Phase-2-specific shim deletion list captured as a `CLAUDE:`
  directive comment on `translateLegacyPlaceholders` in
  `registry.go` so it's visible at code review time.

#### Success Criteria

- `make ci` green.
- All existing reconciler integration tests pass with no expected
  output changes.
- `internal/rules/templates/*.tmpl` files unchanged (deferred to
  Phase 3).
- Search for `renderTemplate` returns no hits in `internal/`.

---

### Phase 3: Rewrite embedded templates to dotted-path syntax

Delete the legacy-syntax shim from Phase 2 and rewrite the three
embedded templates.

#### Tasks

- [x] Rewrite `internal/rules/templates/catalog-info.tmpl`:
  - `REPO_NAME` → `{{ .Repo }}`
  - `ORG_NAME` → `{{ .Owner }}`
- [x] Rewrite `internal/rules/templates/set-custom-properties.tmpl`:
  - `OWNER_VALUE` → `{{ .Catalog.Owner }}`
  - `COMPONENT_VALUE` → `{{ .Catalog.Component }}`
  - `JIRA_PROJECT_VALUE` → `{{ .Catalog.JiraProject }}`
  - `JIRA_LABEL_VALUE` → `{{ .Catalog.JiraLabel }}`
- [x] Rewrite `internal/rules/templates/renovate.tmpl`:
  - `ORG_NAME` → `{{ .Owner }}`
- [x] Delete the legacy-syntax shim added in Phase 2's adapter step.
- [x] Update reconciler tests' expected output to match the dotted-
  path rendered result (should be byte-equivalent to the old
  output; only the template syntax changed).
- [x] Add a regression test in `internal/rules/registry_test.go`
  that loads each embedded template against a known fixture context
  and asserts the rendered output contains expected substrings
  (golden-style assertions live next to the TemplateStore tests).
- [x] Add a test that loads each embedded template against a
  zero-value `FileVars` (with `Catalog: nil`) and asserts a clear
  error message for the catalog-info-aware templates.
- [x] Rewrite `internal/rules/templates/renovate-workflow.tmpl` to
  escape every GHA `${{ ... }}` expression inside a backtick-raw-string
  Go template action so the template parses cleanly without the
  `Renderer.Raw` passthrough routing.

#### Success Criteria

- `make ci` green.
- Goldens match across all three templates.
- Search for `OWNER_VALUE` / `REPO_NAME` / `ORG_NAME` /
  `COMPONENT_VALUE` / `JIRA_PROJECT_VALUE` / `JIRA_LABEL_VALUE`
  returns no hits inside `internal/rules/templates/`.

---

### Phase 4: HCL `pr {}` grammar + `PRTemplate`

Add the HCL grammar. PR template resolution wired but not yet
consumed by the engine.

#### Tasks

- [x] Extend HCL types in `internal/policy/types.go`:
  - Add `PRTemplate` struct (with `*template.Compiled` Title/Body,
    `[]string` Labels, `bool` Inherits — default true).
  - Extend existing `PRConfig` HCL struct with `Title`, `Body`,
    `Labels`, `Inherits` fields tagged for HCL decoding (kept
    name `PRConfig` to avoid renaming the existing
    `SearchTerms`-only block).
  - Add `DefaultsConfig` for the new top-level `defaults { }`
    block; add `PR *PRConfig` to `ReconcilerConfig` for
    `reconcile { pr { } }` sub-blocks.
- [x] Update `internal/policy/loader.go`:
  - Decode `defaults { pr {} }` at the top level via
    `decodeDefaultsBlock`.
  - Decode `pr {}` inside `rule "file"` blocks (extended existing
    `decodePRBlock` to read `title`, `body`, `labels`, `inherits`).
  - Decode `pr {}` inside `reconcile { ... }` blocks (added
    `reconcileBodySchema` so the decoder accepts the `pr`
    sub-block).
  - Compile each `Title`/`Body` to `*template.Compiled` via the
    package-level `Renderer` in a post-decode pass
    (`compilePolicyTemplates`). Parse errors fail policy load
    with location context (`defaults.pr.title`,
    `rule "name".pr.body`,
    `rule "name".reconcile "type".pr.title`).
- [x] Add `internal/policy/pr.go`:
  - Two resolution entry points, both feeding the same
    field-by-field merge logic (Open Q3 resolution):
    - `ResolveRulePR(rule, defaults *PRTemplate) *PRTemplate`
      for the rule's own PR.
    - `ResolveReconcilerPR(reconciler, defaults *PRTemplate) *PRTemplate`
      for reconciler-opened PRs. Skips `rule.pr` deliberately
      (Open Q4 resolution).
  - Field-by-field merge: each of (Title, Body, Labels)
    independently inherits if unset and `Inherits=true`;
    `Inherits=false` falls through directly to the engine
    built-in.
  - HCL presence-vs-absence: empty string `body = ""` and empty
    list `labels = []` are explicit overrides (do NOT inherit).
    `PRConfig` uses `*string` for Title/Body and a sidecar
    `LabelsSet bool` populated by the loader to detect explicit
    `labels = []`. (Open Q2 / Q9 resolutions.)
  - `(*PolicyConfig).DefaultsPR()`, `RulePR(name)`, and
    `ReconcilerPR(rule, type)` provide direct lookup for engine
    callers without re-walking the rule list.
- [x] Add `internal/policy/pr_test.go`:
  - Resolution-order tests covering every combination of
    set/unset/inherits=false at each level.
  - Field-by-field merge test (rule sets only `title`; body and
    labels inherited from defaults).
  - Parse-error test surfaces the right error path with location
    prefix at all three scopes.
  - End-to-end Load tests exercise the full HCL decode →
    compile → resolve pipeline for defaults, rule, and
    reconciler scopes.

#### Success Criteria

- `make ci` green.
- HCL fixtures for the three scopes parse and resolve correctly.
- Policy load fails with location-prefixed error on a bad template.

---

### Phase 5: Engine PR creation integration + render-time behavior

Wire `PRTemplate` into actual PR creation. Multi-rule bundle
resolution, body truncation, strict-mode flag.

#### Tasks

- [x] Update `internal/checker/engine_policy.go`:
  - Build `template.PRVars` from the repo + actionable rules + files
    via `buildPRVars` in `internal/checker/pr.go`.
  - Resolve `*PRTemplate` for the firing rule(s) via
    `policy.ResolveRulePR` (single rule) or per-rule resolution
    inside `bundleTitle` (multi-rule).
  - Render `Title`, `Body` into strings via the unified renderer.
  - Pass labels through to the new `Client.AddLabelsToPR` GitHub
    API method (added to `internal/github/github.go`).
  - For multi-rule bundles, populate `vars.Rules` and leave
    `vars.Rule` zero-valued. Single-rule keeps `Rule` set and
    `Rules` nil.
- [x] Multi-rule bundled-PR title resolution: if rules disagree on
  the rendered title, fall back to `defaults.pr.title`'s rendered
  output (or built-in `PRTitle` const if defaults unset). Emit
  `slog.Info` listing the ignored rule titles.
- [x] Multi-rule bundled-PR body resolution: when
  `len(actionable) > 1`, skip per-rule `pr.body` resolution
  entirely and resolve `Body` from `defaults.pr.body` only (or
  engine built-in if defaults unset). Per-rule `pr.body` is
  implicitly single-rule. (Open Q5 resolution.)
- [x] Body truncation: if the rendered body exceeds 65000 chars,
  truncate to (65000 - len(marker)) chars and append the marker
  `<!-- truncated by repo-guardian: original length=N chars, max=65535 -->`.
  Emit `slog.Warn` with the original length and the rule/repo
  identity.
- [x] Update reconciler PR creation paths
  (`internal/reconciler/custom_properties.go`) to consume the
  resolved `*PRTemplate` carried on `ReconcileParams.PRTemplate`.
  The engine pre-resolves via `policy.ReconcilerPR(rule, type)`
  before invoking each reconciler. Both PR creation sites
  (handleGHAMode workflow PR + createCatalogInfoPR) now use the
  helper `resolveReconcilerPR` and apply labels via `applyLabels`.
- [x] Add `STRICT_TEMPLATES` env var AND `--strict-templates` CLI
  flag to `cmd/repo-guardian/main.go`. CLI flag wins over env var
  via `flag.Bool` default-value semantics. When true,
  `policy.ValidatePRTemplates` is invoked after policy load; it
  walks every compiled `Title`/`Body` and runs
  `tmpl.ValidateZero[tmpl.PRVars]`. Failures are aggregated into
  a single error with location prefixes for each scope.
  (Open Q10 resolution.)
- [x] Engine-integration tests for new behaviors
  (`internal/checker/pr_test.go`):
  - Rule with custom title → PR has rendered title.
  - Rule with custom labels → labels applied to PR.
  - Bundled PR with conflicting titles → falls back to defaults.
  - `inherits = false` on a rule short-circuits to built-in.
  - Body truncation triggers and marker present.
- [x] Strict-mode tests (`internal/policy/strict_test.go`):
  - Safe templates pass.
  - `.Catalog.Owner` reference at rule-pr scope flagged.
  - Empty config passes trivially.
  - Multiple failures aggregated.
- [x] Update existing reconciler tests under customized policy.
  `TestGHAMode_CustomizedPRTemplate` in
  `internal/reconciler/custom_properties_test.go` supplies a
  non-nil `ReconcileParams.PRTemplate` and asserts the rendered
  title, body (with `.Reconciler` interpolated), and labels make
  it onto the PR. The default-policy path is already covered by
  the existing `TestGHAMode_*` tests — they hit the fallback
  strings (byte-identical to the previous hardcoded constants)
  through the new `resolveReconcilerPR` helper.

#### Success Criteria

- `make ci` green.
- Engine integration tests cover all six new behaviors above.
- Strict-mode flag is documented in `cmd/repo-guardian/main.go`
  help text.

---

### Phase 6: Chart values surface

Replace the chart's hardcoded template slots with the generic map
and escape hatch. helm-unittest covers the matrix.

#### Tasks

- [x] Update `charts/repo-guardian/values.yaml`:
  - **Add** `templates.files: {}` (map of `<filename>: <content>`).
  - **Add** `templates.existingConfigMap: ""`.
  - **Add** `templating.vars: {}` (map of env-var key → value).
  - **Add** `templating.strict: false` — sets `STRICT_TEMPLATES`
    env var on the Deployment. (Open Q10 resolution.)
  - **Remove** `templates.codeowners`, `templates.dependabot`,
    `templates.renovate`. Migration documented in the values.yaml
    doc comments.
- [x] Rewrite `charts/repo-guardian/templates/configmap.yaml`:
  - If `.Values.templates.existingConfigMap` is non-empty: skip
    rendering the chart's ConfigMap entirely.
  - Else: `range $name, $content := .Values.templates.files` and
    emit a key per entry.
  - `namespace: {{ .Release.Namespace }}` stamped (chart 0.3.2
    invariant from PR #67).
- [x] Update `charts/repo-guardian/templates/deployment.yaml`:
  - When `existingConfigMap` is set, mount the named ConfigMap at
    `TEMPLATE_DIR` (`/etc/repo-guardian/templates`).
  - Else mount the chart-rendered ConfigMap.
  - Append `templating.vars` keys to the env-var list.
  - Reserved-name validation runs at the top of the Deployment
    template via `repo-guardian.validateTemplatingVars` helper —
    fails the render with a clear list of offenders.
    (Open Q6 resolution.)
  - `STRICT_TEMPLATES` env var emitted when
    `.Values.templating.strict` is true.
- [x] Update `charts/repo-guardian/templates/NOTES.txt` with a
  clear upgrade message for users who had the legacy slots
  populated:

  > **Breaking change in chart 0.4.0**: `templates.codeowners`,
  > `templates.dependabot`, and `templates.renovate` have been
  > removed. Move existing values into `templates.files` with the
  > `.tmpl` suffix, e.g.:
  >
  > ```yaml
  > templates:
  >   files:
  >     codeowners.tmpl: |
  >       * @platform-team
  > ```

- [x] Updated `charts/repo-guardian/tests/configmap_test.yaml`
  cases:
  - `templates.files` empty → chart ConfigMap renders with empty
    `data: {}` and binary falls back to embedded defaults at
    runtime.
  - `templates.files` populated → ConfigMap has every named key
    with matching content.
  - `templates.existingConfigMap=foo` → chart skips ConfigMap
    rendering entirely (assertion: `hasDocuments: count: 0`);
    Deployment mounts `foo`.
- [x] Add `charts/repo-guardian/tests/deployment_env_test.yaml`:
  - `templating.vars: {JIRA_PROJECT: "PLAT"}` → Deployment env list
    contains `name: JIRA_PROJECT, value: PLAT`.
  - `templating.vars: {GITHUB_ORG: "anything"}` → helm template
    fails with reserved-name error. (Open Q6 resolution.)
  - `templating.strict: true` → Deployment env list contains
    `name: STRICT_TEMPLATES, value: "true"`. (Open Q10 resolution.)
  - `templates.existingConfigMap=my-shared-templates` → Deployment
    volume mount points to the supplied name.
- [x] Add `repo-guardian.reservedEnvVars` helper template to
  `charts/repo-guardian/templates/_helpers.tpl` enumerating every
  chart-managed env var name plus
  `repo-guardian.validateTemplatingVars` invoking `fail` on
  collision. Used by deployment.yaml's preamble.
  (Open Q6 resolution.)
- [x] Bump chart `version` from `0.3.3` to `0.4.0` —
  chart-breaking (legacy slots removed).
- [x] Bump chart `appVersion` from `1.4.1` to `1.5.0` to track
  the binary release that ships this work.
- [x] Chart `CHANGELOG.md` is auto-regenerated by git-cliff at
  publish time from commit messages; the breaking-change note
  lands via the Phase 6 commit body.
- [x] Run `helm template ... | kubectl apply --dry-run=client` for
  the standard + customized configurations. Verified output:
  ServiceAccount, Secret, ConfigMap, Service, Deployment all
  apply cleanly.
- [x] `helm unittest charts/repo-guardian` green: 8 suites, 55
  tests pass after Phase 6 changes.

#### Success Criteria

- `make ci` green.
- helm-unittest matrix green.
- `ct lint` and `ct install` against a kind cluster green.
- Chart README updated.
- Manual `helm template` produces a kubectl-applyable document set
  for a representative config.

---

### Phase 7: Examples + migration docs + smoke

End-to-end validation that the new templating system actually
delivers on the customization promises.

#### Tasks

- [x] Update `examples/guardian-full.hcl`:
  - Top-level `defaults { pr {} }` block uses
    `[{{ env "JIRA_PROJECT" | default "GUARDIAN" }}]` in the title,
    a `range .Files` body, and a labels list of
    `["automated", "guardian"]`.
  - Per-rule `pr {}` override on the `codeowners` rule sets only
    `title`; body and labels inherit from defaults.
  - Per-reconciler `pr {}` override on
    `catalog_info.reconcile.custom_properties` sets
    `inherits = false` plus its own title and labels — opts out
    of the compliance-flavored defaults entirely.
- [x] Update `docs/ADDING_RULES.md` with a "Customizing PR text"
  section. Includes resolution-chain table noting reconciler PRs
  skip `rule.pr` (Open Q4) plus the
  "What NOT to do" warning about secret env vars (Open Q8).
- [x] "Security considerations" section added to
  `charts/repo-guardian/README.md` covering `templating.vars`
  + `env` helper exposure plus the `STRICT_TEMPLATES`
  recommendation. (Open Q8 resolution.)
- [x] Top-level `CHANGELOG.md` is git-cliff-generated from commit
  messages; the breaking-change note lands via Phase 6 commit
  body. No manual edit needed.
- [x] `CLAUDE.md` Architecture section already lists the unified
  `internal/template/` package and Phase 4-5 entries describe the
  resolution chain. No further edits.
- [x] MEMORY.md retains the Phase 3 GHA-escape entry; Phase 4-5
  patterns live in CLAUDE.md (the architecture is permanent
  rather than a transient learning).
- [ ] Homelab smoke deploy — **Pending operator action**, can't
  be performed from a code session. Operator-runnable runbook
  scaffolded at
  [`charts/repo-guardian/docs/homelab-smoke.md`](../../charts/repo-guardian/docs/homelab-smoke.md);
  follow it end-to-end. Acceptance criteria:
  - Set `templating.vars.JIRA_PROJECT: PLAT` in chart values.
  - Set a per-rule
    `pr.title = "[{{ env \"JIRA_PROJECT\" }}-CHORE] add CODEOWNERS"`
    on the codeowners rule.
  - Trigger a reconcile against
    `donaldgifford/repo-guardian-test-repo`.
  - Confirm the resulting PR title is `[PLAT-CHORE] add CODEOWNERS`.
  - Confirm bundled PRs (multiple rules firing) fall back to the
    `defaults.pr.title` cleanly.
  - Confirm a body that exceeds 65000 chars is truncated with the
    marker.
- [x] Operator-facing migration guide added at
  `docs/operations/template-migration.md` covering:
  - Old-syntax → dotted-path mapping for any custom `.tmpl` files.
  - Chart values delta (legacy slots → `templates.files`).
  - Validation steps (`STRICT_TEMPLATES=true` for CI).
  - Rollback recipe.

#### Success Criteria

- `examples/guardian-full.hcl` parses cleanly via the loader.
- Homelab smoke confirms the Jira-style PR title in production.
- Migration guide passes a colleague-review (or self-review with a
  fresh eye, given solo maintainership).
- All CHANGELOG and MEMORY updates committed.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/template/template.go` | Create | `Renderer`, `Compiled`, `Parse`, `Render` |
| `internal/template/contexts.go` | Create | `Common`, `FileVars`, `PRVars`, `Rule`, `CatalogInfo` |
| `internal/template/helpers.go` | Create | `env`, `default`, `join`, `lower`, `upper`, `title` |
| `internal/template/strict.go` | Create | Zero-value-context validation |
| `internal/template/template_test.go` | Create | Renderer + helper tests + goldens |
| `internal/policy/types.go` | Modify | Add `PRTemplate`, `PRBlock` types |
| `internal/policy/loader.go` | Modify | Decode `pr {}` blocks at three scopes |
| `internal/policy/pr.go` | Create | `Resolve(...)` for the inheritance chain |
| `internal/policy/pr_test.go` | Create | Resolution-order tests |
| `internal/rules/registry.go` | Modify | `TemplateStore.Get` returns `*template.Compiled` |
| `internal/reconciler/reconciler.go` | Modify | Delete `renderTemplate`; callers use `template.Renderer` |
| `internal/reconciler/custom_properties.go` | Modify | Use compiled templates; populate `Catalog` context |
| `internal/checker/engine_policy.go` | Modify | Resolve & render PR title/body, multi-rule fallback, body truncation |
| `internal/checker/engine_test.go` | Modify | New context shape in mocks |
| `internal/rules/templates/catalog-info.tmpl` | Modify | Dotted-path rewrite |
| `internal/rules/templates/set-custom-properties.tmpl` | Modify | Dotted-path rewrite |
| `internal/rules/templates/renovate.tmpl` | Modify | Dotted-path rewrite |
| `cmd/repo-guardian/main.go` | Modify | `STRICT_TEMPLATES` flag wiring |
| `internal/config/config.go` | Modify | `STRICT_TEMPLATES` env var |
| `charts/repo-guardian/values.yaml` | Modify | Add `templates.files`, `templates.existingConfigMap`, `templating.vars`; remove legacy slots |
| `charts/repo-guardian/templates/configmap.yaml` | Modify | Range over `templates.files`; honor `existingConfigMap` |
| `charts/repo-guardian/templates/deployment.yaml` | Modify | Mount existingConfigMap or chart-rendered; emit `templating.vars` env vars |
| `charts/repo-guardian/templates/NOTES.txt` | Modify | Migration message |
| `charts/repo-guardian/tests/configmap_test.yaml` | Modify | New test cases |
| `charts/repo-guardian/tests/deployment_env_test.yaml` | Create | `templating.vars` env coverage |
| `charts/repo-guardian/Chart.yaml` | Modify | Bump to 0.4.0 + appVersion |
| `charts/repo-guardian/CHANGELOG.md` | Modify | Release-notes entry |
| `charts/repo-guardian/README.md` | Modify | Document the new template values surface |
| `examples/guardian-full.hcl` | Modify | `defaults.pr` + per-rule `pr` + per-reconciler `pr` examples |
| `docs/ADDING_RULES.md` | Modify | "Customizing PR text" section |
| `docs/operations/template-migration.md` | Create | Old-syntax → dotted-path mapping; chart values delta |
| `CHANGELOG.md` | Modify | Binary breaking-change entry |
| `CLAUDE.md` | Modify | Architecture / patterns updates |

## Testing Plan

- [x] Unit tests for `internal/template/` (Phase 1) covering renderer
  + every helper + zero-value contexts + the `Validate` path.
- [x] Golden-file tests for every embedded template under
  `internal/rules/templates/` (Phase 3).
- [x] HCL fixtures exercising `defaults.pr`, per-rule `pr`,
  per-reconciler `pr`, and `inherits=false` (Phase 4).
- [x] `internal/policy/pr_test.go` resolution-order test matrix
  (Phase 4).
- [x] Engine integration tests for: rule with custom title, bundled
  PR title fallback, reconciler with custom title, body truncation,
  strict-mode validation failure, `inherits=false` short-circuit
  (Phase 5).
- [x] Reconciler test fixtures updated to assert the new PR shape
  (Phase 5).
- [x] helm-unittest cases for `templates.files`,
  `templates.existingConfigMap`, `templating.vars` (Phase 6).
- [ ] Homelab smoke at production with a Jira-style title and
  multi-rule bundle (Phase 7) — **Pending operator action**.

## Dependencies

- DESIGN-0013 (this IMPL implements it).
- DESIGN-0006 (HCL Policy Configuration — the grammar this design
  extends).
- DESIGN-0007 (Reconciler Interface — the reconciler pattern whose
  PR creation this design also customizes).
- IMPL-0011 (multi-replica work) coordinates with this for chart
  version bumps but does not block. **IMPL-0012 ships first as
  chart 0.4.0; IMPL-0011 follows as chart 0.5.0.**
- Go `text/template` (stdlib).
- No new external Go dependencies.

## Open Questions

All resolved. Captured here for the audit trail.

1. **Polymorphic vs typed `Render` signature.** **Resolved.**
   Polymorphic `Render(vars any) (string, error)`. Matches stdlib
   `text/template.Execute(w, data any)` exactly; anyone reading
   our code who knows Go templates already knows this shape.
   Typed `RenderFile(FileVars)` / `RenderPR(PRVars)` would couple
   the `Compiled` struct to the call site, fighting the
   "one renderer for all three contexts" goal. Runtime fail-loud
   is acceptable: wrong-type usage surfaces as a clear template
   execution error.
2. **Empty-string vs unset HCL field semantics.** **Resolved.**
   Empty-string is explicit override (does NOT inherit). HCL
   distinguishes presence-of-attribute from absence, so we
   mechanically detect both states. Implementation: `PRBlock`
   uses `*string` for nullable fields where presence matters, or
   a sidecar `bool` to track presence. Sets a clean rule that
   extends to Q9 (empty labels list).
3. **Field-by-field vs all-or-nothing inheritance merge.**
   **Resolved.** Field-by-field merge. Each of `Title`, `Body`,
   `Labels` independently inherits if unset and `Inherits=true`.
   DESIGN-0013's "unset fields cascade up the chain" wording
   directly supports this; all-or-nothing would force operators
   to re-paste body/labels into every rule, defeating the purpose
   of `defaults.pr`.
4. **Reconciler-PR resolution chain.** **Resolved.** Skip
   `rule.pr` — chain is `reconciler → defaults → built-in`. The
   `rule.pr` is scoped to the file rule's compliance subject; the
   `reconcile.pr` is scoped to whatever the reconciler does
   (install action, sync labels, set properties). Different
   artifact, different audience, different title language. Walking
   `rule.pr` for reconciler PRs would mean a custom_properties
   reconciler PR could inherit "add catalog-info.yaml" as its
   title, which is misleading. Documented loudly in
   `docs/ADDING_RULES.md`. Implementation: two resolution paths in
   `policy.Resolve` — one for rule PRs, one for reconciler PRs —
   both feeding the same field-by-field merge logic.
5. **Bundled-PR body customization.** **Resolved.** Option (a) —
   bundled PR body is always rendered from `defaults.pr.body`
   (or built-in), never per-rule body. Per-rule body templates
   are implicitly single-rule. Predictable for operators ("if the
   bundle fires, you get one consistent body shape"); same logic
   that drove the title fallback to defaults on conflict.
   Operator escape hatch is built-in: `defaults.pr.body` uses
   `.Rules` slice to format the bundled list. Implementation:
   when `len(actionable) > 1`, skip per-rule `pr.body` resolution
   entirely and resolve `Body` from `defaults.pr.body` only.
   Edge case: `defaults.pr.body` unset → falls through to engine
   built-in (existing hardcoded Markdown summary; no regression).
6. **`templating.vars` env-var collision policy.** **Resolved.**
   Option (a) — `helm template` fails with a clear error listing
   reserved names. Silent override (b) is the worst path
   (debugging requires `kubectl exec`); silent strip (c) loses
   operator intent. Implementation: maintain a reserved-name
   constant in `_helpers.tpl`:

   ```yaml
   {{- define "repo-guardian.reservedEnvVars" -}}
   GITHUB_APP_ID GITHUB_INSTALLATION_ID GITHUB_PRIVATE_KEY
   WEBHOOK_SECRET STORE_DSN QUEUE_VALKEY_DSN STORE_BACKEND
   QUEUE_BACKEND SCHEDULER_BACKEND STRICT_TEMPLATES TEMPLATE_DIR
   GUARDIAN_CONFIG ...
   {{- end }}
   ```

   Before emitting `templating.vars` env entries, intersect keys
   against this list and `{{ fail "..." }}` with the offending
   names. The reserved list lives in the chart (not the binary)
   because it's about chart-managed env vars; new chart releases
   that introduce a reserved env var update the list.
7. **Chart version bump strategy and order.** **Resolved.**
   **IMPL-0012 ships first as chart 0.4.0**; IMPL-0011 follows as
   chart 0.5.0. Sequential, not combined. Reasoning for putting
   IMPL-0012 ahead: smaller scope (~3-4× faster cycle time, 1 Go
   package + HCL grammar + chart values changes vs 7-phase
   multi-replica plumbing); lower blast radius (no new
   operational components like Postgres / Valkey backends);
   day-1 visible improvement for operators (PR titles change
   immediately, multi-replica is invisible until N>1 replicas);
   production isn't acutely hurting (chart 0.3.3 with the
   INV-0003 engine fix is working in homelab today, cold-start
   API burn is theoretical until repo counts hit thousands);
   merge-conflict economics favor landing the smaller chart
   delta (this one) first so IMPL-0011's bigger delta merges
   against a settled base. Sequential (vs combined) keeps each
   chart rev single-concern for rollback, validation, and
   CHANGELOG readability. Operator workload is similar either
   ordering — two MINOR upgrades vs one combined upgrade.
8. **`env` helper security posture.** **Resolved.** Option (a) —
   no allow-list, trust the operator. The operator already
   provisions every env var on the Deployment, writes the policy
   HCL, and controls the templates that render PR bodies; the
   `env` helper reading those same vars is not privilege
   escalation. The "allow-list constrained to `templating.vars`"
   variant (c) is forward-compatible if a real concern surfaces.
   Documentation requirements: chart README "Security
   considerations" section warns explicitly that
   `templating.vars` and the `env` helper give the policy full
   read access to env vars on the Deployment;
   `docs/ADDING_RULES.md` "Customizing PR text" includes a
   one-liner showing what NOT to do.
9. **Empty labels list semantics.** **Resolved.** Explicit empty
   list = override to no labels. Same rule as Q2: presence-of-
   attribute = override; absence = inherit. Concrete case: a
   per-rule `pr { labels = [] }` against
   `defaults.pr.labels = ["compliance"]` strips compliance from
   that PR while still inheriting `title` and `body` from
   defaults if `inherits=true`. Operators get two ways to express
   "no labels": `labels = []` (surgical, keeps other inheritance)
   or `inherits = false` (this PR is a totally different vibe).
10. **Strict-templates flag — env var, CLI, or both?**
    **Resolved.** Both, with CLI flag taking precedence over env
    var. Env var (`STRICT_TEMPLATES=true`) is the chart-friendly
    path: helm operators set `templating.strict: true` once in
    `values.yaml` and the chart pipes it into the Deployment. CLI
    flag (`--strict-templates`) is for one-off CI runs — an
    operator validates HCL against `STRICT_TEMPLATES=true`
    semantics before merging without touching a Deployment.
    Standard Go convention: explicit command-line arg overrides
    ambient environment. Implementation: `cmd/repo-guardian/main.go`
    reads env var first, then `flag.Parse()` overrides if CLI
    arg is passed. Chart values surface adds
    `templating.strict: false` (default).

## References

- DESIGN-0013 — the design this implements.
- DESIGN-0006 — HCL grammar this extends.
- DESIGN-0007 — reconciler interface, customized here.
- DESIGN-0010 — per-org scope blocks (the multi-installation
  customization story we intentionally don't duplicate).
- IMPL-0011 — coordinates on chart version bump cadence.
- Go `text/template`: <https://pkg.go.dev/text/template>
- Sprig template helpers (NOT included; reference only):
  <https://masterminds.github.io/sprig/>
- GitHub PR body length limit (empirical, 65535 chars).
