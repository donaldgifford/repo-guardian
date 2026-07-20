---
id: INV-0008
title: "Annotation-sourced custom properties should be configurable, not hardcoded"
status: Resolved
author: Donald Gifford
created: 2026-07-19
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0008: Annotation-sourced custom properties should be configurable, not hardcoded

**Status:** Resolved
**Author:** Donald Gifford
**Date:** 2026-07-19

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Findings](#findings)
  - [Observation 1 — the mapping is entirely compile-time](#observation-1--the-mapping-is-entirely-compile-time)
  - [Observation 2 — annotations are not part of the Component contract](#observation-2--annotations-are-not-part-of-the-component-contract)
  - [Observation 3 — the values PATCH is all-or-nothing](#observation-3--the-values-patch-is-all-or-nothing)
  - [Observation 4 — repo-guardian cannot create property definitions](#observation-4--repo-guardian-cannot-create-property-definitions)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Question

Should the `custom_properties` reconciler hardcode extraction of Backstage
*annotations* (`jira/project-key`, `jira/label`) into GitHub custom property
values, or should annotation-sourced properties be operator-configurable —
with only fields guaranteed by the Component kind (`metadata.name`,
`spec.owner`) built in?

## Hypothesis

The Jira annotation mapping is a homelab-specific convention that leaked into
the generic reconciler. Annotations are free-form, deployment-specific
metadata; hardcoding two particular annotation keys (and their GitHub property
names) makes the reconciler fail in any org that doesn't define matching
schema, and makes it useless for orgs that use *different* annotations.

## Context

Surfaced during the live rollout of `examples/guardian-full.hcl` against an
enterprise-managed org (2026-07). The GitHub App permission error was fixed,
but schema setup exposed the deeper issue: the operator must define
`JiraProject` / `JiraLabel` property schemas org-wide even if only some repos
carry the annotations — and there is no way to opt out of, rename, or extend
the annotation mapping.

**Triggered by:** custom-properties rollout debugging session (post-IMPL-0015);
related: DESIGN-0007 (reconciler interface), IMPL-0006.

## Approach

1. Trace the catalog-info → property mapping in `internal/catalog/catalog.go`.
2. Trace the property payload construction in
   `internal/reconciler/custom_properties.go`.
3. Check the HCL surface of the `reconcile "custom_properties"` block for any
   mapping configuration.
4. Confirm GitHub API semantics for undefined property names in the values
   PATCH, and whether repo-guardian can create definitions.

## Findings

### Observation 1 — the mapping is entirely compile-time

`internal/catalog/catalog.go` (`Parse`, lines 60–65) hardcodes four
extractions:

| GitHub property | Source in `catalog-info.yaml` | Behavior |
|---|---|---|
| `Owner` | `spec.owner` | Always written; defaults to `"Unclassified"` when empty |
| `Component` | `metadata.name` | Always written; defaults to `"Unclassified"` when empty |
| `JiraProject` | `metadata.annotations["jira/project-key"]` | Written only when present and non-empty |
| `JiraLabel` | `metadata.annotations["jira/label"]` | Written only when present and non-empty |

The GitHub-side property names are compile-time constants in
`desiredToPropertyValues` (`internal/reconciler/custom_properties.go:404-424`).
The `reconcile "custom_properties" {}` HCL block configures only *how* the
reconciler runs (`mode`, `watch`) — there is no knob for *which* properties
are extracted or what they are named.

### Observation 2 — annotations are not part of the Component contract

`metadata.name` and `spec.owner` are required by the Backstage entity schema
for `kind: Component` — every valid catalog-info file has them, so built-in
extraction (with `Unclassified` fallback) is safe. `metadata.annotations` is a
free-form string map; `jira/project-key` and `jira/label` are conventions of
one deployment, not the Backstage spec. Hardcoding them means:

- Orgs using different annotation keys (or none) cannot map their metadata.
- Orgs whose repos *do* carry these annotations are forced to define
  `JiraProject`/`JiraLabel` schemas org-wide, or repos break (Observation 3).

### Observation 3 — the values PATCH is all-or-nothing

`PATCH /repos/{owner}/{repo}/properties/values` returns 422 for the **entire
payload** if any property name is not defined in the org (or enterprise)
schema. A repo whose catalog-info carries `jira/project-key` but whose org
lacks a `JiraProject` definition fails to sync `Owner` and `Component` too.
Fleet symptom: annotation-free repos sync fine, annotated repos error every
sweep — same policy, confusing partial failure.

### Observation 4 — repo-guardian cannot create property definitions

`internal/github/client.go` exposes only `GetCustomPropertyValues` (:431) and
`SetCustomPropertyValues` (:457). Schema definitions must be created
out-of-band (org level, or enterprise level for enterprise-managed orgs), with
`values_editable_by=org_and_repo_actors` so the App's repo-scoped write is
accepted. There is no preflight check: a missing definition surfaces only as a
per-repo 422 at reconcile time.

## Conclusion

**Answer:** Yes — the hypothesis is confirmed. Only `metadata.name` and
`spec.owner` are safe to extract by default; the Jira annotation mapping is a
deployment-specific convention hardcoded into a generic reconciler and should
move to configuration.

## Recommendation

Design a small follow-up (DESIGN doc) covering:

1. **Keep built-in:** `Owner ← spec.owner`, `Component ← metadata.name` with
   the existing `Unclassified` fallbacks. These are contract-guaranteed.
2. **Remove hardcoded Jira extraction** and replace it with an operator-defined
   annotation→property map on the `reconcile "custom_properties"` block, e.g.:

   ```hcl
   reconcile "custom_properties" {
     mode  = "api"
     watch = true

     # annotation key → GitHub custom property name
     annotation_properties = {
       "jira/project-key" = "JiraProject"
       "jira/label"       = "JiraLabel"
     }
   }
   ```

   Empty/absent map → only `Owner` + `Component` are written.
3. **Migration note:** this is a behavior change for any deployment relying on
   the built-in Jira mapping (the homelab). The example config carries the map
   explicitly, so migration is copy-paste; call it out in the chart/app
   changelog.
4. **Schema preflight — warn AND filter (decided, see Q3):** preflight the org
   schema (`GET /orgs/{org}/properties/schema`, cached per org per sweep — not
   per repo, to keep the rate-limit cost at one call per org) and:
   - emit a structured `slog.Warn` naming each mapped property missing from
     the schema, with stable keys (`org`, `missing_properties`) so a Loki
     rule can match and alert on it;
   - **filter** the missing properties out of the payload and PATCH the rest,
     so `Owner`/`Component` (and any schema-defined mapped properties) still
     converge instead of the whole PATCH 422ing;
   - increment `custom_property_missing_schema_total{org, property}` — both
     labels are bounded (orgs by installation count, property names by the
     operator's config map), so cardinality is safe and matches the existing
     per-org label convention. A starter alert on the counter joins the
     Loki log-based alert as complementary signals.

## Open Questions

All resolved 2026-07-19.

1. Config shape for the mapping? — **Decided: (a)**
   - (a) `annotation_properties = { "key" = "PropName" }` map attribute on the
     existing reconcile block — smallest surface, matches HCL idioms already
     in use (recommended)
   - (b) repeated `property "PropName" { annotation = "key" }` sub-blocks —
     more extensible (per-property defaults, value transforms) but heavier
   - (c) top-level `properties {}` block shared across reconcilers
   - other:
2. Should built-in `Owner`/`Component` names be renameable too? — **Decided: (a)**
   - (a) no — keep them fixed; they're the product's contract and every doc/
     dashboard refers to them (recommended)
   - (b) yes — full map including the built-ins, defaults preserved
   - other:
3. Preflight schema check? — **Decided: (a) + (b) combined.** Warn-log the
   mapped properties missing from the org schema (structured, stable keys so
   Loki can match and alert), AND filter them out of the payload so the
   schema-defined properties still PATCH successfully. Track occurrences with
   `custom_property_missing_schema_total{org, property}` (bounded cardinality:
   property names come from the operator's config map). Alerting comes from
   both the Prometheus counter and a Loki rule on the warn line.
   - (a) warn-only log line when a mapped property is missing from the org
     schema, keep attempting the PATCH
   - (b) skip unmapped-in-schema properties from the payload so the rest of
     the PATCH succeeds
   - (c) none — document the 422 failure mode and move on
   - other:
4. Should repo-guardian ever create schema definitions itself (needs org-admin
   custom-properties permission)? — **Decided: (a)**
   - (a) no — schema is an org-governance concern, keep the App least-privilege
     (recommended)
   - (b) yes, behind an opt-in flag for single-tenant installs
   - other:

## References

- `internal/catalog/catalog.go` — hardcoded extraction (`Parse`, lines 60–65)
- `internal/reconciler/custom_properties.go` — `desiredToPropertyValues`
  (lines 404–424), drift check (lines 388–400)
- `internal/github/client.go` — `GetCustomPropertyValues` (:431),
  `SetCustomPropertyValues` (:457)
- DESIGN-0007 — Reconciler Interface and Push Event Handler
- GitHub REST: [custom properties for repositories](https://docs.github.com/en/rest/repos/custom-properties),
  [org property schema](https://docs.github.com/en/rest/orgs/custom-properties)
