---
id: IMPL-0017
title: "Configurable annotation-sourced custom properties"
status: Draft
author: Donald Gifford
created: 2026-07-20
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0017: Configurable annotation-sourced custom properties

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-20

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 0: Policy config surface](#phase-0-policy-config-surface)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 1: Generic catalog extraction](#phase-1-generic-catalog-extraction)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 2: Map-driven reconciler, client value type, templates](#phase-2-map-driven-reconciler-client-value-type-templates)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 3: Schema preflight (warn + filter + metric)](#phase-3-schema-preflight-warn--filter--metric)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 4: Chart observability (alert + Loki contract)](#phase-4-chart-observability-alert--loki-contract)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 5: Docs, examples, release notes](#phase-5-docs-examples-release-notes)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Rollback](#rollback)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement DESIGN-0019: replace the hardcoded Jira annotation extraction in
the `custom_properties` reconciler with an operator-defined
`annotation_properties` map, make mapped properties a full state sync over
the managed set (add / update / **clear on removal**), and add the org-schema
preflight (warn + filter + metric, fail-open). Roll-forward only — no compat
shims.

**Implements:** DESIGN-0019 (all open questions resolved 2026-07-19; parent
investigation INV-0008)

## Scope

### In Scope

- HCL `annotation_properties` map attribute + load-time validation
- Generic `catalog.Parse` (`Properties.Extra` map; Jira fields deleted)
- Managed-set diff/payload with JSON-null clears in both reconciler modes
- `github.CustomPropertyValue.Value` → `*string`; new
  `Client.GetOrgPropertySchema` method
- Per-org 30-minute schema cache; preflight partition/warn/filter/metric
- `custom_property_missing_schema_total{org, property}` metric, chart alert,
  Loki log contract
- Docs: policy reference, example config, release notes, CLAUDE.md

### Out of Scope

- Schema definition creation/mutation by the App (DESIGN-0019 non-goal)
- Renaming built-in `Owner`/`Component` property names
- Touching properties outside the managed set
- Value transforms; backward compatibility with the removed Jira built-ins
- Web UI / status API work (INV-0009 track)

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met. Run `make lint && make
test` after each task; `make ci` before each PR.

PR grouping (OQ 1 → a): **PR-1** = Phases 0–3 + Phase 5 docs on one feature
branch, label `minor`; **PR-2** = Phase 4 chart-only, `dont-release`, after
PR-1 merges so the metric names are final.

---

### Phase 0: Policy config surface

Decode and validate the new map attribute end-to-end through `policy.Load`.
Inert on its own — nothing consumes the field yet — so it merges safely.

#### Tasks

- [x] Add `AnnotationProperties map[string]string` to `ReconcilerConfig`
      (`internal/policy/types.go:297-304`); godoc states key = annotation,
      value = GitHub property name
- [x] Add `{Name: "annotation_properties"}` to `reconcileBodySchema` and a
      decode case in `decodeReconcileBlock` (`internal/policy/loader.go:573-617`)
      using `val.AsValueMap()` guarded by `val.CanIterateElements()`;
      collect diags with the existing location-prefix pattern
- [x] Validation in `internal/policy/validate.go`, called from the existing
      validate pass, rejecting: reserved property names (`Owner`,
      `Component` — **case-insensitive**, OQ 3 → a), empty annotation keys,
      empty property names, duplicate property-name targets
      (case-insensitive), and names violating GitHub's constraint
      (`^[a-zA-Z0-9_.-]+$`, ≤75 chars)
- [x] Confirm `BuiltinDefaults()` and the `CUSTOM_PROPERTIES_MODE`
      back-compat injection (`internal/policy/defaults.go`) leave the map
      empty/nil (Owner/Component-only default)
- [x] Loader tests: map decodes; absent attribute ⇒ nil map; empty map ⇒
      empty; non-string values ⇒ diagnostic. Validate tests: one case per
      rejection with message-content assertions
- [x] `make lint && make test` green (watch gci ordering + godot on new
      comments)

#### Success Criteria

- A policy declaring `annotation_properties` loads and round-trips into
  `ReconcilerConfig.AnnotationProperties`
- Every invalid shape fails `policy.Load` with a diagnostic naming file/line
- Existing policies (no map) load byte-for-byte identically; full test suite
  unaffected

---

### Phase 1: Generic catalog extraction

Remove all annotation knowledge from `internal/catalog`; extraction is
driven entirely by the caller's map.

#### Tasks

- [x] Change signature to `Parse(content string, annotationProps
      map[string]string) *Properties`; replace `JiraProject`/`JiraLabel`
      fields with `Extra map[string]string` (property name → value);
      delete the `jira/*` lookups (`internal/catalog/catalog.go`)
- [x] Populate `Extra` only for present-and-non-empty annotations; keep
      `Unclassified` fallbacks and the apiVersion/kind gate exactly as-is;
      `defaults()` returns nil/empty `Extra`
- [x] Update the single production call site
      (`internal/reconciler/custom_properties.go:76`) to pass the
      reconciler's map (field added properly in Phase 2 — a temporary nil
      argument here is acceptable if Phase 1 merges alone) — landed
      together with Phase 2's `annotationProps` field since the existing
      reconciler tests directly depended on the hardcoded Jira extraction
      and a standalone nil-argument commit would leave them failing
- [x] Rewrite catalog tests: mapped annotation present / absent / empty
      value / nil map / non-Component entity / unparseable YAML; delete
      Jira-specific cases
- [x] `make lint && make test` green

#### Success Criteria

- `internal/catalog` contains no annotation-key literals
- `Parse(content, nil)` yields Owner/Component only, fallbacks intact
- All extraction behavior proven map-driven by tests

---

### Phase 2: Map-driven reconciler, client value type, templates

The behavioral core: managed-set state sync with clears, in both modes.

#### Tasks

- [x] `github.CustomPropertyValue.Value` → `*string`
      (`internal/github/client.go`): `SetCustomPropertyValues` passes nil
      through to go-github's `interface{}` (JSON null ⇒ removal);
      `GetCustomPropertyValues` keeps the `fmt.Sprintf("%v")` coercion for
      non-nil values of any type, nil stays nil (OQ 4 → a); fix all
      compiling call sites and test literals
- [x] Add `annotationProps map[string]string` field to
      `CustomPropertiesReconciler`; populate from
      `ReconcilerConfig.AnnotationProperties` in
      `NewCustomPropertiesReconciler`; thread the map into `catalog.Parse`
- [x] Rewrite `diffProperties` managed-set driven: `Owner`/`Component`
      compare; each mapped name — present ⇒ value compare, absent while
      current non-empty ⇒ drift (clear needed); unmanaged names ignored
- [x] Rewrite `desiredToPropertyValues`: `Owner`, `Component`, then mapped
      names in sorted key order; present ⇒ value, absent ⇒ nil (clear)
- [x] Update dry-run logs in `handleAPIMode` (`custom_properties.go:232-240`)
      to enumerate sets vs clears; update the "custom properties need
      update" log similarly
- [x] Clears observability (OQ 2 → a): register
      `CustomPropertyClearedTotal` CounterVec
      (`repo_guardian_custom_property_cleared_total`, label `org`) in
      `internal/metrics/metrics.go`; increment per cleared property on a
      successful PATCH; add a `cleared_properties` field to the "set custom
      properties via API" log line (empty slice omitted)
- [x] PR-body builders (`buildGHABody`/`buildCatalogInfoBody`/
      `buildPropertiesPRBody`) render the map generically with "will clear"
      rows; drop Jira-specific lines
- [x] `tmpl.CatalogInfo`: replace `JiraProject`/`JiraLabel` with
      `Properties map[string]string` (`internal/template/contexts.go:64-75`);
      fix template-context tests
- [x] Rewrite `internal/rules/templates/set-custom-properties.tmpl`: range
      the managed set — `-f 'properties[][value]=...'` for values,
      `-F 'properties[][value]=null'` for clears; prerequisites comment
      generated from the actual property set (keep the backtick-escape
      wrappers for `${{ ... }}`)
- [x] Reconciler tests: api + gha with 0/1/N mapped annotations;
      clear-on-removal diff + payload; unmanaged-property isolation;
      cleared-counter assertion (`testutil.ToFloat64`);
      rendered-workflow golden assertions including a clear entry;
      regression case proving Jira-map-configured output matches the old
      hardcoded output set-for-set
- [x] `make lint && make test` green (hugeParam: keep passing
      `ReconcilerConfig` per existing convention; map adds one word)

#### Success Criteria

- Empty map ⇒ Owner/Component-only behavior in both modes
- Removing a mapped annotation produces a PATCH nulling exactly that
  property; no payload ever contains an unmanaged property
- With the Jira map configured and annotations present, payloads/workflows
  are set-identical to pre-change output
- `grep -ri jira internal/ --include='*.go' | grep -v _test` returns nothing

---

### Phase 3: Schema preflight (warn + filter + metric)

#### Tasks

- [x] `GetOrgPropertySchema(ctx, org) ([]string, error)` on the
      `github.Client` interface + `GitHubClient` impl via
      `Organizations.GetAllCustomProperties` (names only)
- [x] mockClient parity: no-op stubs in `internal/checker/engine_test.go`,
      `internal/scheduler/sweep_test.go`,
      `internal/reconciler/custom_properties_test.go` (embedded by
      `bpMockClient`/`labelMockClient` — one stub covers reconciler tests)
- [x] Per-org cache in `CustomPropertiesReconciler`: `sync.Mutex` +
      `map[string]schemaEntry{names map[string]struct{}, fetchedAt}`;
      unexported `schemaCacheTTL = 30 * time.Minute`; error results also
      cached for the TTL window (drives log-once-per-org fail-open)
- [x] Partition step in `handleAPIMode` before `SetCustomPropertyValues`:
      split payload on schema membership; empty defined-subset skips the
      PATCH; missing set ⇒ `slog.Warn("custom properties missing from org
      schema", "org", ..., "repo", ..., "missing_properties", [...])` +
      per-property counter increment
- [x] Fail-open: schema fetch error (403/5xx/timeout) ⇒ log once per org
      per TTL window, send unfiltered payload (today's exact semantics)
- [x] `CustomPropertyMissingSchemaTotal` CounterVec
      (`repo_guardian_custom_property_missing_schema_total`,
      labels `org`, `property`) in `internal/metrics/metrics.go` following
      the `EnqueueGatedByBudgetTotal` pattern
- [x] Tests: stateful schema mock (mock-fidelity rule — returns configured
      schema, never bare nil) with call counter asserting one fetch per org
      per TTL; filter case; fail-open-on-403 case; counter assertions via
      `testutil.ToFloat64` + `Reset()` between cases; literal warn-message
      text asserted (Loki contract)
- [x] `make lint && make test` green

#### Success Criteria

- Repo with one undefined mapped property syncs its defined properties in a
  single PATCH; warn line carries `org` + `missing_properties`; counter
  increments once per missing property per attempt
- 403 on the schema endpoint reproduces today's behavior (unfiltered PATCH)
  plus exactly one log line per org per TTL window
- N repos in one org within the TTL ⇒ exactly one schema API call

---

### Phase 4: Chart observability (alert + Loki contract)

Chart-only PR (`dont-release`), chart `1.0.0-rc.4` → `1.0.0-rc.5`.

#### Tasks

- [ ] `RepoGuardianPropertySchemaMissing` alert in
      `charts/repo-guardian/templates/prometheusrule.yaml`:
      `rate(repo_guardian_custom_property_missing_schema_total[15m]) > 0`
      for 30m, `severity: warning`, annotation linking the policy-reference
      preflight section (match existing alert style, lines 28-115)
- [ ] Loki matching contract in `docs/operations/` (extend where the
      IMPL-0015 metric tables live): exact message text, structured keys
      (`org`, `missing_properties`), sample LogQL ruler rule
- [ ] helm-unittest case for the new alert entry
      (`charts/repo-guardian/tests/`) — remember `---` document-start and
      `equal:` path assertions (no `kind:` assertion type)
- [ ] Chart.yaml version bump + `helm-docs` regeneration if README
      template mentions alerts
- [ ] helm-unittest suite green; `make lint && make test` green

#### Success Criteria

- Rendered PrometheusRule contains valid PromQL for the new alert
- An operator can write the Loki ruler config from the ops doc alone
  (message text + keys documented and pinned by the Phase 3 Go test)

---

### Phase 5: Docs, examples, release notes

#### Tasks

- [ ] `docs/usage/policy-reference.md`: `annotation_properties` attribute
      (type, default, validation rules, reserved names), managed-set
      clear-on-removal semantics, schema-preflight behavior + optional
      org-level **Custom properties: read** App permission, new metric(s)
- [ ] `examples/guardian-full.hcl`: add the Jira map to the `catalog_info`
      reconcile block (reproduces pre-change homelab behavior)
- [ ] Release-notes entry covering the four behavioral edges: removed
      built-in Jira extraction; `.Catalog.Jira*` →
      `index .Catalog.Properties "..."`; clear-on-removal; optional new App
      permission
- [ ] `CLAUDE.md`: update custom_properties architecture notes (managed
      set, clears, preflight, `GetOrgPropertySchema` added to the
      mock-parity list, `CustomPropertyValue.Value *string`)
- [ ] `docs/usage/getting-started.md`: refresh the custom-properties demo
      if it names Jira properties
- [ ] Homelab smoke checklist (operator-side, checkbox stays open until
      run): upgrade with map configured ⇒ no behavior change; remove an
      annotation ⇒ property value clears; remove a schema definition ⇒
      warn + filter + metric
- [ ] `docz update impl design inv` to refresh indices; mkdocs strict
      build clean

#### Success Criteria

- Operator can configure the feature from `policy-reference.md` alone
- Example config reproduces pre-change behavior verbatim
- Release notes cover all four behavioral edges

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/policy/types.go` | Modify | `AnnotationProperties` on `ReconcilerConfig` |
| `internal/policy/loader.go` | Modify | schema entry + `AsValueMap` decode |
| `internal/policy/validate.go` | Modify | reserved/dup/empty/charset validation |
| `internal/policy/defaults.go` | Modify | confirm empty map in back-compat injection |
| `internal/catalog/catalog.go` | Modify | generic `Parse(content, map)`; `Extra`; Jira fields deleted |
| `internal/github/client.go` | Modify | `Value *string`; `GetOrgPropertySchema` |
| `internal/reconciler/custom_properties.go` | Modify | managed-set diff/payload/clears; schema cache; preflight |
| `internal/template/contexts.go` | Modify | `CatalogInfo.Properties` map |
| `internal/rules/templates/set-custom-properties.tmpl` | Modify | map-driven workflow with null clears |
| `internal/metrics/metrics.go` | Modify | missing-schema CounterVec + cleared-properties CounterVec |
| `internal/checker/engine_test.go` | Modify | mock stub + `Value` type fixes |
| `internal/scheduler/sweep_test.go` | Modify | mock stub |
| `internal/reconciler/custom_properties_test.go` | Modify | mock stub, stateful schema mock, new cases |
| `charts/repo-guardian/templates/prometheusrule.yaml` | Modify | new alert |
| `charts/repo-guardian/Chart.yaml` | Modify | `1.0.0-rc.5` |
| `docs/operations/` (scaling or new section) | Modify | Loki contract + LogQL sample |
| `docs/usage/policy-reference.md` | Modify | attribute + semantics docs |
| `examples/guardian-full.hcl` | Modify | explicit Jira map |
| `CLAUDE.md` | Modify | architecture notes |

## Testing Plan

- [x] Loader/validate matrix (Phase 0): decode shapes + every rejection
- [x] Catalog parse matrix (Phase 1): present/absent/empty/nil-map/non-Component
- [x] Managed-set diff + payload with sorted-order and clear assertions (Phase 2)
- [x] Unmanaged-property isolation: never diffed, never emitted (Phase 2)
- [x] Cleared-properties counter + `cleared_properties` log field (Phase 2)
- [x] Golden rendered workflow for 0/1/N properties including a clear (Phase 2)
- [x] Regression: Jira map configured ⇒ set-identical output to pre-change (Phase 2)
- [x] Stateful schema mock + call counter (one fetch per org per TTL) (Phase 3)
- [x] Fail-open on 403; filter path; counter via `testutil.ToFloat64` (Phase 3)
- [x] Literal warn-message text assertion (Loki contract) (Phase 3)
- [ ] helm-unittest for the new alert (Phase 4)
- [ ] Homelab smoke (operator-side, Phase 5)

## Dependencies

- DESIGN-0019 Approved (currently Draft — flip before starting Phase 0)
- go-github v68 already provides `Organizations.GetAllCustomProperties` and
  nullable `CustomPropertyValue.Value` — no dependency bumps
- No store/queue/scheduler/webhook/chart-values changes

## Rollback

Roll-forward only (DESIGN-0019). Older binaries reject policies carrying
`annotation_properties` at load; deployments relying on the old Jira
built-ins break until their policy declares the map. Fix forward — no
rollback path is designed or tested.

## Open Questions

All resolved 2026-07-20 — every question decided as (a); the resolutions are
folded into the phase tasks above.

1. PR / branch grouping? — **Decided: (a)**
   - (a) two PRs: PR-1 = Phases 0–3 + Phase 5 docs on one feature branch
     (label `minor` — the behavior change and the docs describing it ship
     atomically); PR-2 = Phase 4 chart-only (`dont-release`, after PR-1
     merges so the metric name is final) (recommended)
   - (b) one PR per phase (six PRs) — smaller reviews, but Phases 1–2 leave
     awkward intermediate states (nil-map call site) on main
   - (c) single PR for everything including the chart
   - other:
2. Observability for successful clears (a destructive-ish write)? —
   **Decided: (a)** (task added to Phase 2)
   - (a) add `repo_guardian_custom_property_cleared_total{org}` CounterVec
     incremented per cleared property, plus a `cleared_properties` field on
     the existing "set custom properties via API" log line — cheap, and
     makes the new destructive behavior auditable from day one (recommended)
   - (b) log field only, no metric — add the counter later if wanted
   - (c) nothing beyond the PATCH itself — clears are just normal syncs
   - other:
3. Case sensitivity for reserved-name and duplicate validation? —
   **Decided: (a)**
   - (a) case-insensitive — GitHub property names are unique
     case-insensitively, so `owner = "..."` mapping to property `owner`
     would collide with built-in `Owner` at PATCH time anyway; reject at
     load (recommended)
   - (b) exact-match only — simpler, but lets `owner`/`OWNER` slip past the
     reservation and fail confusingly at reconcile time
   - other:
4. `GetCustomPropertyValues` mapping of non-string values into `*string`? —
   **Decided: (a)**
   Today the code coerces any non-nil `interface{}` with
   `fmt.Sprintf("%v")` (`client.go:442-445`) — multi-select properties
   arrive as arrays.
   - (a) keep the `%v` coercion for non-nil values (arrays render as Go
     slice syntax — lossy but identical to today's behavior, and the diff
     only compares managed single-value properties); nil stays nil
     (recommended)
   - (b) map only string values; treat non-strings as nil — cleaner
     typing but silently changes diff behavior for any repo where a
     managed name collides with a multi-select property
   - other:

## References

- DESIGN-0019 — Configurable annotation-sourced custom properties (parent
  design; decisions table + phase definitions)
- INV-0008 — parent investigation
- `internal/policy/loader.go:573-617` — reconcile block decode pattern
- `internal/github/client.go:430-476` — current value methods + `%v` coercion
- `internal/reconciler/custom_properties.go` — reconciler, both modes
- `internal/metrics/metrics.go:321-324` — CounterVec naming pattern
- `charts/repo-guardian/templates/prometheusrule.yaml:28-115` — alert style
- Mock-fidelity rule + mockClient parity contract — CLAUDE.md testing notes
