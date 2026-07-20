---
id: DESIGN-0019
title: "Configurable annotation-sourced custom properties"
status: Approved
author: Donald Gifford
created: 2026-07-19
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0019: Configurable annotation-sourced custom properties

**Status:** Approved
**Author:** Donald Gifford
**Date:** 2026-07-19

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Config surface](#config-surface)
  - [Catalog extraction](#catalog-extraction)
  - [Reconciler sync (both modes)](#reconciler-sync-both-modes)
  - [Schema preflight: warn + filter + metric](#schema-preflight-warn--filter--metric)
  - [Template context](#template-context)
  - [Design decisions (from INV-0008 review)](#design-decisions-from-inv-0008-review)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Phases](#phases)
  - [Phase 0 — Policy config surface](#phase-0--policy-config-surface)
  - [Phase 1 — Generic catalog extraction](#phase-1--generic-catalog-extraction)
  - [Phase 2 — Map-driven reconciler + template context](#phase-2--map-driven-reconciler--template-context)
  - [Phase 3 — Schema preflight (warn + filter + metric)](#phase-3--schema-preflight-warn--filter--metric)
  - [Phase 4 — Observability: alert + Loki contract](#phase-4--observability-alert--loki-contract)
  - [Phase 5 — Docs, examples, migration](#phase-5--docs-examples-migration)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Replace the hardcoded Jira annotation extraction in the `custom_properties`
reconciler with an operator-defined `annotation_properties` map on the
`reconcile "custom_properties"` HCL block. Built-in extraction shrinks to the
two fields guaranteed by the Backstage Component contract (`Owner ←
spec.owner`, `Component ← metadata.name`). Add an org-schema preflight that
warn-logs mapped properties missing from the org's custom-property schema,
filters them out of the values PATCH so defined properties still converge, and
counts occurrences in a new Prometheus metric.

Mapped properties are a **full state sync**: adding, updating, and clearing.
Removing an annotation from catalog-info clears the corresponding property
value on GitHub — the properties exist so downstream apps can consume
accurate state, and stale values defeat the purpose.

Implements the INV-0008 conclusion (open questions resolved 2026-07-19) and
the DESIGN-0019 open-question resolutions (also 2026-07-19; see Open
Questions). Rollout is roll-forward only — this is a feature fix, not a
compatibility project.

## Goals and Non-Goals

### Goals

- Operators declare which catalog-info **annotations** map to which GitHub
  custom **property names**; nothing annotation-sourced is hardcoded.
- `Owner` / `Component` stay built-in, fixed-name, with `Unclassified`
  fallbacks (INV-0008 Q2 → a).
- A repo with annotations mapped to undefined schema properties still gets
  its defined properties synced (no all-or-nothing 422), with a
  Loki-matchable warning and a Prometheus counter (INV-0008 Q3 → a+b).
- Mapped properties are a full state sync — add, update, **and clear on
  removal** — so downstream consumers can trust the values (OQ 3 → b).
- Both reconciler modes (`api`, `github-action`) honor the map (OQ 1 → a).
- Config errors (reserved names, duplicates, empties) fail at policy load
  with location-prefixed diagnostics, not at reconcile time.

### Non-Goals

- Creating or mutating org/enterprise property **schema** definitions —
  repo-guardian remains values-only, least-privilege (INV-0008 Q4 → a).
- Renaming the built-in `Owner`/`Component` property names (Q2 → a).
- Touching property names outside the **managed set** (`{Owner, Component}`
  ∪ mapped names) — values set by humans or other tooling are never diffed,
  written, or cleared.
- Value transformations (prefixing, casing, defaulting per property). The map
  is name→name only; transforms would motivate the sub-block shape rejected
  in INV-0008 Q1.
- Backward compatibility with the removed Jira built-ins — roll-forward only;
  no deprecation window, no compat shims (see Migration / Rollout Plan).

## Background

INV-0008 found the catalog-info → custom-properties mapping is entirely
compile-time: `internal/catalog/catalog.go.Parse` extracts
`jira/project-key` / `jira/label` annotations into `JiraProject` /
`JiraLabel`, and `desiredToPropertyValues`
(`internal/reconciler/custom_properties.go:404-424`) hardcodes the GitHub
property names. Annotations are free-form, deployment-specific metadata — not
part of the Component kind contract — and GitHub's values PATCH is
all-or-nothing, so one undefined property name 422s the whole payload
including `Owner`/`Component`.

Two additional facts surfaced while scoping this design, beyond INV-0008:

1. **The GHA-mode workflow template hardcodes all four properties.**
   `internal/rules/templates/set-custom-properties.tmpl` emits fixed
   `-f 'properties[][property_name]=JiraProject'` pairs and its prerequisites
   comment names all four. Map-driven sync requires rewriting this template
   to range over the resolved property set (OQ 1).
2. **The schema preflight needs a new App permission.** `GET
   /orgs/{org}/properties/schema` (go-github v68:
   `Organizations.GetAllCustomProperties`) requires org-level **Custom
   properties: read** — the App currently needs only repo-level values
   write. The preflight must degrade gracefully when the permission is
   absent (OQ 2).

## Detailed Design

### Config surface

New optional map attribute on the existing reconcile block (INV-0008 Q1 → a):

```hcl
rule "file" "catalog_info" {
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  template = "catalog-info"

  reconcile "custom_properties" {
    mode  = "api"
    watch = true

    # catalog-info annotation key → GitHub custom property name
    annotation_properties = {
      "jira/project-key" = "JiraProject"
      "jira/label"       = "JiraLabel"
    }
  }
}
```

- Absent or empty map ⇒ only `Owner` + `Component` are written. This is the
  new built-in default (`BuiltinDefaults()` and the `CUSTOM_PROPERTIES_MODE`
  back-compat injection both carry an empty map).
- Load-time validation rejects, with location-prefixed diagnostics:
  - property names `Owner` or `Component` (reserved — follows Q2 → a);
  - empty annotation keys or property names;
  - two annotation keys targeting the same property name;
  - property names that don't match GitHub's constraint
    (`^[a-zA-Z0-9_.-]+$`, ≤75 chars — validated so failures surface at load,
    not as a per-repo 422).

Decode follows the existing `decodeReconcileBlock` pattern
(`loader.go:584-617`): add `{Name: "annotation_properties"}` to
`reconcileBodySchema`, iterate `val.AsValueMap()` in the attribute switch.
No `hcl:"-"` traps — plain `map[string]string` on `ReconcilerConfig`.

### Catalog extraction

`internal/catalog/catalog.go` changes:

```go
type Properties struct {
    Owner     string
    Component string
    // Extra maps GitHub property name → value, extracted from
    // metadata.annotations per the operator's annotation_properties map.
    // Only present-and-non-empty annotations produce entries.
    Extra map[string]string
}

func Parse(content string, annotationProps map[string]string) *Properties
```

The `JiraProject`/`JiraLabel` struct fields and the two hardcoded annotation
lookups are deleted. `Parse` iterates the operator map; a missing or empty
annotation simply produces no entry (mirrors today's "only when present and
non-empty" behavior). The `Unclassified` fallbacks for `Owner`/`Component`
are unchanged.

### Reconciler sync (both modes)

`CustomPropertiesReconciler` gains an `annotationProps map[string]string`
field populated from `ReconcilerConfig` in the factory constructor.

The **managed set** is `{Owner, Component}` ∪ the property names in
`annotation_properties`. Reconciliation is a full state sync over the
managed set — and only the managed set:

- `diffProperties` compares every managed property. `Owner`/`Component` are
  never absent (the `Unclassified` fallbacks guarantee a value). For each
  mapped property name: present in `desired.Extra` with a different current
  value ⇒ drift; **absent from `Extra` (annotation removed or empty) while
  the current GitHub value is non-empty ⇒ drift — the value must be
  cleared** (OQ 3 → b). Properties outside the managed set are ignored
  entirely, regardless of what `GetCustomPropertyValues` returns.
- `desiredToPropertyValues` emits `Owner`, `Component`, then every mapped
  property name in **sorted key order** (deterministic payloads; keeps
  GHA-mode rendered workflows and tests stable). Present entries carry
  their value; absent entries carry an explicit **JSON null**, which is
  GitHub's documented mechanism for removing a repo property value.
- The thin wrapper struct `github.CustomPropertyValue.Value` changes from
  `string` to `*string` (nil ⇒ null on the wire); `SetCustomPropertyValues`
  passes nil through to go-github's `interface{}` value field, and
  `GetCustomPropertyValues` maps unset/null back to nil.
- `buildGHABody` / `buildCatalogInfoBody` (PR bodies) render the map
  generically instead of naming Jira fields, including explicit "will
  clear" rows for removals.

Interplay with file assertions is unchanged and orthogonal: removing
`spec.owner` from catalog-info still trips the rule's assertion and flows
through the normal check → PR path, while the property sync writes the
`Unclassified` fallback in the meantime. Clearing only ever applies to
mapped (annotation-sourced) properties.

### Schema preflight: warn + filter + metric

API mode only (GHA mode's PATCH runs inside the target repo's workflow with
`GITHUB_TOKEN`; repo-guardian can't preflight on the workflow's behalf —
the rendered workflow's prerequisites comment carries the warning instead).

Flow in `handleAPIMode`, before `SetCustomPropertyValues`:

1. Fetch the org's defined property names via a new
   `Client.GetOrgPropertySchema(ctx, org) ([]string, error)`, wrapped in a
   **per-org TTL cache** inside the reconciler (`map[string]schemaEntry` +
   `sync.Mutex`, TTL per OQ 4). One API call per org per TTL window — not
   per repo — keeping rate-limit cost flat (INV-0008 recommendation).
2. Partition the payload: entries whose property name is in the schema vs
   not. `Owner`/`Component` are partitioned identically — if the operator
   never defined them, the same warn/filter path reports it instead of a
   permanent silent 422 loop.
3. If the missing set is non-empty:
   - `slog.Warn("custom properties missing from org schema", "org", org,
     "repo", repo, "missing_properties", []string{...})` — stable message
     text and keys form the Loki matching contract (Phase 4).
   - `metrics.CustomPropertyMissingSchemaTotal.WithLabelValues(org,
     property).Inc()` for each missing property.
4. PATCH only the defined subset. If the subset is empty, skip the API call
   entirely (log covers it).
5. **Fail-open:** if the schema fetch errors (403 missing permission, 5xx,
   timeout), log once per org per TTL window and send the **unfiltered**
   payload — exactly today's semantics. The preflight is an enhancement
   layer; its failure must never block syncs that would have succeeded
   (mirrors the `ErrNoSnapshot` fall-open convention in `internal/budget`).

New metric (registered in `internal/metrics`):

```
custom_property_missing_schema_total{org, property}  CounterVec
```

Cardinality: `org` is an established label; `property` is bounded by the
operator's config map (single digits). Safe per INV-0008 Q3 resolution.

### Template context

`tmpl.CatalogInfo` (`internal/template/contexts.go:67-75`) replaces
`JiraProject`/`JiraLabel` with `Properties map[string]string` (property name
→ value, same content as `catalog.Properties.Extra`). Template access:
`{{ index .Catalog.Properties "JiraProject" }}` or
`{{ range $name, $value := .Catalog.Properties }}`.

`set-custom-properties.tmpl` is rewritten to range over the managed set
(OQ 1 → a): present values render as `-f 'properties[][value]=...'` string
fields; clears render as `-F 'properties[][value]=null'` (`gh api -F`
converts the literal `null` to JSON null). The prerequisites comment is
generated from the actual resolved property set instead of naming four
fixed properties.

The `.Catalog.JiraProject`/`.Catalog.JiraLabel` fields are removed in the
same release with no deprecation window (OQ 5 → a, roll-forward): operator
templates referencing them fail at load/render and are fixed by switching
to `index .Catalog.Properties "JiraProject"`.

### Design decisions (from INV-0008 review)

| Decision | Resolution |
|---|---|
| Config shape | map attribute on the reconcile block (Q1 → a) |
| Built-in names renameable | no; `Owner`/`Component` reserved, load-time reject (Q2 → a) |
| Preflight behavior | warn + filter + metric + Loki/Prometheus alerting (Q3 → a+b) |
| Schema creation by the App | never (Q4 → a) |

Plus the DESIGN-0019 open-question resolutions (2026-07-19):

| Decision | Resolution |
|---|---|
| GHA-mode parity | yes — `set-custom-properties.tmpl` rewritten map-driven (OQ 1 → a) |
| Missing org-read permission | fail-open to today's semantics, log once per org per TTL window (OQ 2 → a) |
| Removed annotations | **clear the property value** — full state sync over the managed set (OQ 3 → b) |
| Schema-cache TTL | fixed 30 minutes, unexported constant (OQ 4 → a) |
| Template-context change | Jira fields removed same release, roll-forward, no shims (OQ 5 → a) |

## API / Interface Changes

| Surface | Change |
|---|---|
| HCL policy | `annotation_properties = map(string)` attribute on `reconcile "custom_properties"` blocks (all scopes where reconcile blocks are legal today) |
| `policy.ReconcilerConfig` | + `AnnotationProperties map[string]string` |
| `catalog.Parse` | signature: `Parse(content string, annotationProps map[string]string)`; `Properties.{JiraProject,JiraLabel}` → `Properties.Extra map[string]string` |
| `github.Client` interface | + `GetOrgPropertySchema(ctx context.Context, org string) ([]string, error)` — **mockClient parity: stubs needed in `internal/checker/engine_test.go`, `internal/scheduler/sweep_test.go`, `internal/reconciler/custom_properties_test.go`** |
| `github.CustomPropertyValue` | `Value string` → `Value *string` (nil ⇒ JSON null ⇒ GitHub removes the value); both client methods map accordingly |
| `tmpl.CatalogInfo` | `JiraProject`/`JiraLabel` fields → `Properties map[string]string` (breaking for operator templates; roll-forward, OQ 5 → a) |
| Metrics | + `custom_property_missing_schema_total{org, property}` CounterVec |
| Embedded templates | `set-custom-properties.tmpl` rewritten map-driven |
| App permissions (docs) | org-level **Custom properties: read** documented as optional (enables preflight); absent ⇒ fail-open |

No store, queue, scheduler, webhook, or chart-values changes. The chart is
untouched except docs — the HCL arrives via the existing `policy.config`
ConfigMap path.

## Data Model

None. No store schema, migration, or persisted-state changes. The per-org
schema cache is in-memory, per-process, best-effort (replicas each hold
their own; acceptable because the cache only optimizes a read).

## Phases

Each phase is a separately mergeable PR (or squash-batch) keeping
`make ci` green. Later phases depend on earlier ones in order.

### Phase 0 — Policy config surface

- [ ] Add `AnnotationProperties map[string]string` to
      `policy.ReconcilerConfig` (`internal/policy/types.go`)
- [ ] Add `{Name: "annotation_properties"}` to `reconcileBodySchema` and
      decode via `AsValueMap()` in `decodeReconcileBlock`
      (`internal/policy/loader.go`)
- [ ] Load-time validation: reserved names (`Owner`, `Component`), empty
      keys/values, duplicate property-name targets, GitHub property-name
      charset/length — location-prefixed diagnostics
- [ ] `BuiltinDefaults()` and the `CUSTOM_PROPERTIES_MODE` back-compat
      injection carry an empty map (Owner/Component-only default)
- [ ] Loader tests: happy path, empty map, absent attribute, each
      validation rejection with message assertions
- [ ] `make lint && make test` green

**Success criteria:** a policy declaring `annotation_properties` loads and
round-trips into `ReconcilerConfig`; every invalid shape fails `policy.Load`
with a diagnostic naming the file/line; existing policies (no map) load
unchanged.

### Phase 1 — Generic catalog extraction

- [ ] `catalog.Parse(content, annotationProps)` signature; `Properties.Extra
      map[string]string`; delete `JiraProject`/`JiraLabel` fields and the
      hardcoded `jira/*` lookups (`internal/catalog/catalog.go`)
- [ ] Preserve `Unclassified` fallbacks and the
      apiVersion/kind gate exactly as-is
- [ ] Update catalog unit tests: mapped annotation present/absent/empty,
      nil map, non-Component entity, unparseable YAML
- [ ] Update all `catalog.Parse` call sites to compile (reconciler passes
      its map; tests pass literals)
- [ ] `make lint && make test` green

**Success criteria:** `catalog` package has zero knowledge of any specific
annotation key; `Parse(content, nil)` yields Owner/Component only; all
extraction behavior is driven by the caller's map.

### Phase 2 — Map-driven reconciler + template context

- [ ] `CustomPropertiesReconciler` carries `annotationProps` from
      `ReconcilerConfig`; factory threads it through
- [ ] `github.CustomPropertyValue.Value` → `*string`;
      `SetCustomPropertyValues` maps nil → JSON null (value removal),
      `GetCustomPropertyValues` maps unset/null → nil
- [ ] `diffProperties` and `desiredToPropertyValues` become managed-set
      driven (sorted key order for determinism): present ⇒ value, absent ⇒
      explicit null clear; unmanaged properties never diffed or emitted
- [ ] PR-body builders render the map generically, including "will clear"
      rows for removals
- [ ] `tmpl.CatalogInfo`: `Properties map[string]string` replaces the two
      Jira fields (`internal/template/contexts.go`)
- [ ] Rewrite `set-custom-properties.tmpl` to range over the managed set
      (`-f` for values, `-F 'properties[][value]=null'` for clears);
      prerequisites comment generated from the actual set
- [ ] Reconciler tests: api + gha mode with 0, 1, N mapped annotations;
      clear-on-removal diff and payload; unmanaged-property isolation;
      rendered-workflow golden assertions (including a clear entry)
- [ ] `make lint && make test` green

**Success criteria:** with an empty map, behavior is Owner/Component-only in
both modes; with the Jira map configured and annotations present, rendered
workflows and API payloads are set-identical to today's output; removing a
mapped annotation produces a PATCH that nulls exactly that property; no
payload ever contains a property outside the managed set; no `Jira*`
identifier remains outside tests and docs.

### Phase 3 — Schema preflight (warn + filter + metric)

- [ ] `Client.GetOrgPropertySchema` on the `github.Client` interface +
      `GitHubClient` impl via `Organizations.GetAllCustomProperties`
- [ ] mockClient parity stubs in the three files (`internal/checker/
      engine_test.go`, `internal/scheduler/sweep_test.go`,
      `internal/reconciler/custom_properties_test.go`)
- [ ] Per-org TTL cache in the reconciler (`sync.Mutex` +
      `map[string]schemaEntry`); TTL per OQ 4
- [ ] Partition/filter/warn/metric flow in `handleAPIMode`;
      `custom_property_missing_schema_total{org, property}` registered in
      `internal/metrics`
- [ ] Fail-open path: schema fetch error ⇒ log once per org per TTL window,
      send unfiltered payload
- [ ] Tests: missing-property filter (stateful mock returning schema —
      mock-fidelity rule: list-then-act mocks must reflect configured
      state, not return nil), fail-open on 403, cache hit avoids second
      schema call, counter assertions via `testutil.ToFloat64`
- [ ] `make lint && make test` green

**Success criteria:** a repo with one undefined mapped property still syncs
its defined properties in a single PATCH; the warn line carries `org` +
`missing_properties`; the counter increments once per missing property per
attempt; a 403 on the schema endpoint produces today's exact behavior
(unfiltered PATCH) plus one log line; at most one schema API call per org
per TTL window.

### Phase 4 — Observability: alert + Loki contract

- [ ] Starter alert in `charts/repo-guardian/templates/prometheusrule.yaml`:
      `RepoGuardianPropertySchemaMissing` —
      `rate(custom_property_missing_schema_total[15m]) > 0` for 30m,
      warning severity (tunable, matches existing alert conventions)
- [ ] Document the Loki matching contract (exact message text + structured
      keys `org`, `missing_properties`) and a sample LogQL rule in
      `docs/operations/` (extend the metrics tables where the IMPL-0015
      metrics live)
- [ ] helm-unittest case for the new PrometheusRule entry
- [ ] Chart version patch bump + chart CHANGELOG entry (`dont-release`
      label; chart-only change)
- [ ] `make lint && make test` + helm-unittest green

**Success criteria:** the alert renders in the chart with valid PromQL; the
ops doc names the exact log line and keys so a Loki ruler config can be
written without reading Go source; log message text is asserted in a Go test
so it can't drift silently under the Loki rule.

### Phase 5 — Docs, examples, migration

- [ ] `docs/usage/policy-reference.md`: `annotation_properties` attribute
      (type, default, validation rules, reserved names) + schema-preflight
      behavior + new metric
- [ ] `examples/guardian-full.hcl`: add the Jira map to the
      `catalog_info` reconcile block (preserves current homelab behavior
      through the upgrade)
- [ ] Release-notes entry (roll-forward): removed built-in Jira extraction,
      template-context change
      (`.Catalog.JiraProject` → `index .Catalog.Properties "JiraProject"`),
      clear-on-removal behavior, optional new App permission for preflight
- [ ] `CLAUDE.md`: update reconciler architecture notes (built-ins vs map,
      managed-set clearing, preflight, new client method + mock-parity list)
- [ ] Getting-started walkthrough: refresh the custom-properties demo
      section if it names Jira properties
- [ ] Homelab smoke checklist (operator-side): upgrade with map configured,
      verify no behavior change; remove an annotation, verify the property
      value clears; remove one schema definition, verify
      warn + filter + metric
- [ ] `make lint && make test` green; mkdocs strict build clean

**Success criteria:** an operator can configure the feature from
`policy-reference.md` alone; the example config reproduces pre-change
behavior verbatim; the release notes cover all four behavioral edges
(extraction, template context, clearing, permission).

## Testing Strategy

- **Unit:** catalog parse matrix (Phase 1); loader validation matrix
  (Phase 0); diff/payload construction with sorted-order assertions
  (Phase 2); preflight partition + fail-open + cache (Phase 3).
- **Mock fidelity:** the schema-preflight tests use a stateful mock whose
  `GetOrgPropertySchema` returns configured schema state — never a bare
  `return nil, nil` — per the list-then-act mock-fidelity rule (IMPL-0013
  post-mortem). A call counter asserts the TTL cache (N repos, 1 call).
- **Counters:** `testutil.ToFloat64` on labeled children;
  `metric.Reset()` between cases.
- **Golden:** rendered `set-custom-properties.yml` for 0/1/N mapped
  properties, including one with a clear (`null`) entry.
- **Removal:** mapped annotation deleted ⇒ payload nulls exactly that
  property; a property present on GitHub but outside the managed set never
  appears in any payload or diff.
- **Log contract:** a test asserts the literal warn message text + keys
  (Phase 4 depends on it for Loki).
- **Regression:** with the Jira map configured and annotations present,
  end-to-end reconciler tests must produce API payloads / rendered
  workflows set-identical to the current hardcoded behavior.

## Migration / Rollout Plan

**Roll-forward only.** This is a feature fix, not a compatibility project:
no deprecation window, no compat shims, no rollback support. It is accepted
that a deployment relying on the old built-in Jira extraction — or on the
`.Catalog.Jira*` template fields — breaks or errors after upgrading until
its policy/templates declare the map.

1. Phases 0–3 land as Go changes on one appVersion **minor** bump
   (behavioral change; PR label `minor`). Phase 4 is chart-only
   (`dont-release`, chart patch bump). Phase 5 rides with either.
2. Release notes state the three things an upgrading operator may need to
   do: declare `annotation_properties` where the built-in Jira extraction
   was relied on, rewrite `.Catalog.Jira*` template references to
   `index .Catalog.Properties "..."`, and optionally grant the App
   org-level **Custom properties: read** for the preflight (without it,
   behavior is today's unfiltered PATCH plus a periodic log line).
3. Older binaries reject policies carrying `annotation_properties` at load —
   fix forward, don't roll back.
4. No data migration; no store or webhook impact.

## Open Questions

All resolved 2026-07-19.

1. Should `annotation_properties` work in `github-action` mode in v1? —
   **Decided: (a)**
   - (a) yes — rewrite `set-custom-properties.tmpl` to range over the
     resolved map; both modes stay at feature parity and the template
     rewrite is needed anyway for the Jira-field removal (recommended)
   - (b) api-mode only in v1; declaring the map with
     `mode = "github-action"` fails at policy load with a clear
     diagnostic; GHA parity ships later
   - other:
2. Preflight behavior when the App lacks org-level Custom properties: read? —
   **Decided: (a)**
   - (a) fail-open — log once per org per TTL window, send the unfiltered
     payload (today's exact semantics); document the optional permission
     (recommended)
   - (b) fail-closed — treat as empty schema, filter everything, warn; no
     sync until permission granted (protects against 422s but silently
     stops syncs that work today)
   - (c) require the permission — reconcile errors until granted
   - other:
3. Removed-annotation semantics: an annotation previously synced is deleted
   from catalog-info — what happens to the GitHub property value? —
   **Decided: (b).** The custom properties exist so downstream apps can
   consume accurate state; leaving stale values makes the sync useless.
   Removal clears the value (JSON null). If deleting a catalog field also
   trips a file assertion (e.g. a removed `spec.owner`), that flows through
   the normal assert → check → PR path independently — the two mechanisms
   are orthogonal. Clearing applies only to the managed set.
   - (a) leave it in place — matches today's behavior, non-destructive; a
     future `delete_extra`-style knob can add clearing if wanted
   - (b) clear it (set empty/null) when the mapped annotation is absent —
     true convergence; scoped to the managed set so shared/manually-set
     properties outside the map are never touched
   - other:
4. Schema-cache TTL? — **Decided: (a)**
   - (a) fixed 30 minutes, unexported constant, no config knob — schema
     changes are rare, operators shouldn't need to think about this;
     revisit only if someone hits it (recommended)
   - (b) configurable via HCL (`schema_cache_ttl` on the reconcile block)
   - (c) tie it to `schedule_interval` (one fetch per sweep)
   - other:
5. Template-context migration for `.Catalog.JiraProject` / `.Catalog.JiraLabel`? —
   **Decided: (a)** (follows the roll-forward directive)
   - (a) remove the fields in the same release, document the
     `index .Catalog.Properties` replacement in the release notes — usage
     outside the embedded template (rewritten here anyway) is almost
     certainly zero, and carrying deprecated fields means populating them
     from the map by magic names (recommended)
   - (b) keep the two fields for one minor release, populated when the map
     targets those exact property names; delete in the next release
   - other:

## References

- INV-0008 — Annotation-sourced custom properties should be configurable,
  not hardcoded (parent investigation; open questions resolved 2026-07-19)
- `internal/catalog/catalog.go` — current hardcoded extraction
- `internal/reconciler/custom_properties.go` — reconciler, both modes
- `internal/policy/loader.go:573-617` — reconcile block schema/decode
- `internal/policy/types.go:297-304` — `ReconcilerConfig`
- `internal/template/contexts.go:64-75` — `CatalogInfo` template context
- `internal/rules/templates/set-custom-properties.tmpl` — GHA workflow
  template (hardcodes four properties today)
- `internal/budget` — `ErrNoSnapshot` fall-open convention mirrored by the
  preflight fail-open
- go-github v68 `Organizations.GetAllCustomProperties` — schema read API
- GitHub REST: [repository custom properties](https://docs.github.com/en/rest/repos/custom-properties),
  [organization custom properties](https://docs.github.com/en/rest/orgs/custom-properties)
- DESIGN-0007 / IMPL-0006 — reconciler interface origin
