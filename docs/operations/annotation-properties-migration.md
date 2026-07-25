# Custom-property annotation migration (appVersion 1.9.1 → next minor)

[IMPL-0017](../impl/0017-configurable-annotation-sourced-custom-properties.md)
replaces the `custom_properties` reconciler's hardcoded Jira annotation
extraction with an operator-defined `annotation_properties` map
([DESIGN-0019](../design/0019-configurable-annotation-sourced-custom-properties.md)).
This is a **roll-forward-only** change: no deprecation window, no
compatibility shim. It closes
[INV-0008](../investigation/0008-annotation-sourced-custom-properties-should-be-configurable-not.md).

This guide walks through the four behavioral edges an upgrading operator
may hit.

## 1. Built-in Jira extraction is gone

Before this change, `catalog.Parse` always looked for
`jira/project-key` and `jira/label` annotations and populated
`JiraProject`/`JiraLabel` custom properties — with no way to configure,
rename, or disable that behavior. It now extracts **nothing** beyond the
two Backstage-contract-guaranteed fields (`Owner` ← `spec.owner`,
`Component` ← `metadata.name`) unless you opt in:

```hcl
reconcile "custom_properties" {
  mode = "api"
  annotation_properties = {
    "jira/project-key" = "JiraProject"
    "jira/label"        = "JiraLabel"
  }
}
```

If your policy relied on the old built-in behavior and doesn't declare
this map, `JiraProject`/`JiraLabel` simply stop being synced — no error,
no warning, because as far as repo-guardian is concerned they're no
longer in the managed set. Add the map above (or map whatever
annotations/properties you actually use) to restore the old behavior.
See
[`docs/usage/policy-reference.md` § `custom_properties`](../usage/policy-reference.md#custom_properties)
for the full attribute reference and validation rules.

## 2. Template context: `.Catalog.Jira*` is gone

If any custom PR/file template referenced the old fields directly, rewrite
them:

| Before | After |
|---|---|
| `{{ .Catalog.JiraProject }}` | `{{ index .Catalog.Properties "JiraProject" }}` |
| `{{ .Catalog.JiraLabel }}` | `{{ index .Catalog.Properties "JiraLabel" }}` |

`.Catalog.Properties` is a `map[string]string` keyed by the GitHub
property name (the map's *values* in `annotation_properties`, not its
keys). Every mapped property is present in the map — an absent source
annotation renders as an empty string (the same "will clear" signal the
embedded `set-custom-properties.tmpl` workflow uses), so
`{{ if index .Catalog.Properties "JiraProject" }}` is the idiomatic guard
if you want to skip rendering a line entirely rather than render it empty.
`.Catalog.Owner` and `.Catalog.Component` are unchanged.

## 3. Removed annotations now clear the property (full state sync)

Previously, once a Jira property was set on a repo it stayed set even if
the annotation was later removed from `catalog-info.yaml` — there was no
mechanism to un-set it. `annotation_properties` targets are now a **full
state sync**: add, update, and clear. If a mapped annotation disappears
(edited out, or the whole file removed), the corresponding GitHub property
is set to `null` on the next reconcile.

> **Note (IMPL-0021).** Only the *edited-out annotation* half of this
> worked when it first shipped. The *whole file removed* half was
> unreachable — the engine skipped reconcilers entirely when the file was
> missing, so a deleted `catalog-info.yaml` left every mapped property
> stale (INV-0011 A3). Both halves behave as documented from appVersion
> 1.10.1 on. When the file is gone, `Owner`/`Component` fall back to
> `Unclassified` rather than clearing.

If you were relying on the old "sticky" behavior (a property, once set,
persists even after the source annotation is removed), there is no opt-out
— this is the DESIGN-0019 resolution (OQ 3 → b) and is considered a
correctness fix, not a new footgun: a stale, no-longer-true property value
is worse than a cleared one. Re-add the annotation if you want the value
back.

## 4. Optional new App permission: org-level "Custom properties: read"

[Phase 3](../impl/0017-configurable-annotation-sourced-custom-properties.md)
adds a schema preflight: before writing, repo-guardian checks which custom
properties the org's schema actually defines and drops any mapped property
the schema doesn't have, so one undefined property doesn't block the rest
of the sync. This is optional and **fails open**:

- **Without the permission** (or on any schema-endpoint error): every
  managed property syncs exactly as it did before this release — the
  unfiltered payload is sent, once per org per 30-minute window a
  `slog.Warn` line notes the schema fetch failed (see
  [`docs/operations/scaling.md` § Custom-property schema preflight](scaling.md#custom-property-schema-preflight-impl-0017-phase-3)
  for the exact log text). No action required.
- **With the permission granted:** a mapped property with no matching
  org-schema definition is dropped from the PATCH (the rest still syncs),
  logged, and counted via
  `repo_guardian_custom_property_missing_schema_total{org, property}`.
  Define the property in the org's custom-property schema (GitHub
  settings, not repo-guardian — the App never creates schema itself) to
  make the warning go away.

Granting the permission is a **strict improvement** in visibility with no
behavior change for repos whose schema is already correct. There is no
reason to grant it *except* to get the preflight warning/metric; declining
it reproduces pre-Phase-3 behavior indefinitely.

## Validation steps

1. Diff your policy against the table above — do you reference
   `.Catalog.Jira*` anywhere, or rely on the old hardcoded Jira
   extraction without declaring `annotation_properties`?
2. Dry-run: set `DRY_RUN=true` (or `guardian { dry_run = true }`) and
   check the logs for `"custom properties need update"` — the
   `desired_properties` field shows exactly what would sync under the new
   map-driven behavior.
3. If you want the schema preflight, grant the App org-level **"Custom
   properties: read"** permission and confirm
   `repo_guardian_custom_property_missing_schema_total` stays at zero (or
   investigate the named property if it doesn't).

## Rollback

Roll-forward only — there is no compatibility shim to fall back to. If a
rollback is needed, pin the binary/chart to the previous appVersion; the
old hardcoded Jira behavior returns, but any properties cleared under the
new full-state-sync behavior are **not** automatically restored (GitHub
still shows them as unset until the old binary's next successful sync
re-sets them from catalog-info).

## See also

- [IMPL-0017](../impl/0017-configurable-annotation-sourced-custom-properties.md) — full implementation plan
- [DESIGN-0019](../design/0019-configurable-annotation-sourced-custom-properties.md) — design rationale
- [INV-0008](../investigation/0008-annotation-sourced-custom-properties-should-be-configurable-not.md) — investigation that motivated this change
- [`docs/usage/policy-reference.md`](../usage/policy-reference.md#custom_properties) — operator-facing attribute reference
- [`docs/operations/scaling.md`](scaling.md#custom-property-schema-preflight-impl-0017-phase-3) — schema-preflight metrics + Loki matching contract
