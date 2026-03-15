---
id: IMPL-0006
title: "Reconciler Interface and Push Event Handler"
status: Draft
author: Donald Gifford
created: 2026-03-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0006: Reconciler Interface and Push Event Handler

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-15

## Objective

Implement the `Reconciler` interface, reconciler registry, and the
`custom_properties` reconciler (migrating existing logic from
`internal/checker/properties.go`). Add a `push` event handler that triggers
re-evaluation when watched files are modified on the default branch.

**Implements:** DESIGN-0007

## Scope

### In Scope

- `Reconciler` interface with `ReconcileParams`
- `Registry` with factory pattern for reconciler type registration
- `custom_properties` reconciler migrating logic from
  `internal/checker/properties.go`
- `push` event handler in `internal/webhook/handler.go`
- Watched file path extraction from `PolicyConfig`
- `TriggerPush` trigger type
- Backward compatibility with `CUSTOM_PROPERTIES_MODE` env var
- Integration with checker engine (reconcilers run after assertions pass)

### Out of Scope

- `label_sync`, `branch_protection`, `workflow_sync` reconcilers (IMPL-0007)
- Ignore lists (IMPL-0007)
- `rule "setting"` and `rule "branch_protection"` types (IMPL-0007)

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met.

---

### Phase 1: Reconciler Interface and Registry

Define the core interface and registry that all reconciler types implement.

#### Tasks

- [ ] Create `internal/reconciler/reconciler.go` with:
  - `Reconciler` interface (`Name() string`,
    `Reconcile(ctx, ReconcileParams) error`)
  - `ReconcileParams` struct (Client, Owner, Repo, DefaultBranch,
    Content, OpenPRs, DryRun, Logger)
- [ ] Create `internal/reconciler/registry.go` with:
  - `Registry` struct with `factories map[string]Factory`
  - `Factory` type: `func(config ReconcilerConfig) (Reconciler, error)`
  - `NewRegistry() *Registry`
  - `Register(name string, factory Factory)`
  - `Build(config ReconcilerConfig) (Reconciler, error)`
- [ ] Add `ReconcilerConfig` type to `internal/policy/types.go` with HCL
  struct tags (type, mode, watch, and type-specific fields)
- [ ] Write unit tests:
  - Registry registers and builds reconcilers correctly
  - Registry returns error for unknown reconciler type
  - `ReconcileParams` construction
  - Interface compliance (compile-time check)

#### Success Criteria

- `Reconciler` interface is defined and documented
- Registry can register and build reconcilers
- `go build ./...` succeeds
- `make lint` and `make test` pass

---

### Phase 2: Custom Properties Reconciler

Migrate the existing custom properties logic from
`internal/checker/properties.go` into a `custom_properties` reconciler.

#### Tasks

- [ ] Create `internal/reconciler/custom_properties.go` implementing
  `Reconciler`:
  - `NewCustomPropertiesReconciler(config ReconcilerConfig) (Reconciler, error)`
  - `Name() string` returns `"custom_properties"`
  - `Reconcile(ctx, params)` mirrors current `CheckCustomProperties` flow
- [ ] Extract from `internal/checker/properties.go`:
  - YAML parsing logic (catalog-info extraction)
  - Custom property diff logic
  - API mode: set properties directly, PR if file missing
  - GHA mode: create PR with workflow
- [ ] Use `CustomPropertiesConfig` fields for YAML path mappings
  (owner, component, jira_project, jira_label)
- [ ] Support configurable defaults (owner, component default values)
- [ ] Preserve all existing metrics:
  - `properties_checked_total`
  - `properties_set_total`
  - `properties_already_correct_total`
- [ ] Register `custom_properties` factory in `NewRegistry()`
- [ ] Write unit tests:
  - Reconcile with valid catalog-info content: extracts correct values,
    diffs against current, calls `SetCustomPropertyValues`
  - Reconcile with missing fields: uses configured defaults
  - Reconcile with unparseable content: uses defaults
  - Reconcile in dry-run mode: logs but doesn't call API
  - Reconcile in api mode: sets properties directly
  - Reconcile in github-action mode: creates PR with GHA workflow
  - Metrics are incremented correctly

#### Success Criteria

- `custom_properties` reconciler produces identical behavior to current
  `CheckCustomProperties`
- All existing properties-related tests pass or are migrated
- Metrics are preserved
- `make test` and `make lint` pass

---

### Phase 3: Checker Engine Integration

Wire reconcilers into the checker engine's `CheckRepo` flow so they run
after file assertions pass.

#### Tasks

- [ ] Add `Reconcilers []Reconciler` field to the engine's internal rule
  representation (built from `FileRuleConfig.Reconcilers` via Registry)
- [ ] Build reconcilers from `PolicyConfig` at engine construction time
  using the `Registry`
- [ ] Modify `CheckRepo` flow:
  - After file existence + assertions pass → read file content once →
    run each reconciler with `ReconcileParams`
  - `exists` mode: reconcilers run when file is present
  - `contains` mode: reconcilers run when assertions pass
  - `exact` mode: reconcilers run when file matches template
- [ ] Remove `checkCustomPropertiesIfEnabled` from `CheckRepo` (replaced
  by reconciler invocation)
- [ ] Remove `customPropertiesMode` field from `Engine` struct
- [ ] Update `NewEngine` signature (no more `customPropertiesMode` param)
- [ ] Write unit tests:
  - Reconciler runs when file present (exists mode)
  - Reconciler runs when assertions pass (contains mode)
  - Reconciler does not run when file missing
  - Reconciler does not run when assertions fail
  - Multiple reconcilers run in order
  - Reconciler error is logged but doesn't fail the check
  - DryRun flag propagated to ReconcileParams

#### Success Criteria

- Reconcilers run at the correct point in the CheckRepo flow
- Custom properties behavior is unchanged
- `checkCustomPropertiesIfEnabled` is removed
- `make test` and `make lint` pass

---

### Phase 4: Backward Compatibility Layer

Ensure `CUSTOM_PROPERTIES_MODE` env var continues to work when no HCL config
is present.

#### Tasks

- [ ] In `internal/policy/defaults.go`: when `CUSTOM_PROPERTIES_MODE` is
  set and no HCL config is loaded, add a `custom_properties` reconciler
  to the `catalog_info` file rule in built-in defaults
- [ ] Map env var mode value to reconciler config fields:
  - `mode = os.Getenv("CUSTOM_PROPERTIES_MODE")`
  - `watch = false` (no push handler in legacy mode)
  - Field mappings match current hardcoded values in `properties.go`
- [ ] Write backward compatibility test:
  - No HCL config + `CUSTOM_PROPERTIES_MODE=api` → engine behavior
    identical to current
  - No HCL config + `CUSTOM_PROPERTIES_MODE=""` → no reconciler attached
  - HCL config present → `CUSTOM_PROPERTIES_MODE` env var is ignored
    (HCL takes precedence)

#### Success Criteria

- `CUSTOM_PROPERTIES_MODE=api` without HCL produces identical behavior
- `CUSTOM_PROPERTIES_MODE=""` disables custom properties (same as today)
- HCL config takes precedence over env var
- `make test` passes

---

### Phase 5: Push Event Handler

Add `push` event handling to the webhook handler that triggers re-evaluation
when watched files are modified on the default branch.

#### Tasks

- [ ] Add `TriggerPush` constant to `internal/checker/queue.go`
- [ ] Add `watchedPaths map[string]bool` field to `webhook.Handler`
- [ ] Update `webhook.NewHandler` signature to accept `watchedPaths`
- [ ] Add `case *gh.PushEvent:` to `ServeHTTP` switch
- [ ] Implement `handlePushEvent`:
  - Check `e.GetRef()` matches `"refs/heads/" + defaultBranch`
  - Log tag pushes at debug level
  - Call `hasWatchedFileChanges`
  - If matched, enqueue with `TriggerPush`
- [ ] Implement `hasWatchedFileChanges`:
  - Check `Added` and `Modified` paths in all commits
  - Return true if any path is in `watchedPaths`
  - Do NOT check `Removed` paths
- [ ] Update `main.go` to pass `watchedPaths` to `NewHandler`
- [ ] Write unit tests:
  - Push to default branch with watched file in `added`: enqueues
  - Push to default branch with watched file in `modified`: enqueues
  - Push to default branch with unrelated files: does not enqueue
  - Push to non-default branch: does not enqueue
  - Push with watched file in `removed` only: does not enqueue
  - Push with no watched paths configured: does not enqueue
  - Multiple commits, watched file in later commit: enqueues
  - Tag push: logged at debug, not enqueued

#### Success Criteria

- Push events for watched files on default branch trigger rescans
- Non-matching pushes are silently ignored
- `repos_checked_total{trigger="push"}` metric works
- No new GitHub API calls from push handler (payload only)
- `make test` and `make lint` pass

---

### Phase 6: Watched Path Extraction

Build the utility that extracts watched file paths from the policy config.

#### Tasks

- [ ] Create `internal/policy/watch.go` with
  `ExtractWatchedPaths(config *PolicyConfig) map[string]bool`
- [ ] For each file rule with a reconciler where `watch = true`, add all
  rule paths to the watched set
- [ ] Call `ExtractWatchedPaths` in `main.go` and pass result to
  `webhook.NewHandler`
- [ ] When no HCL config and no `watch = true` reconcilers, return empty
  map (push events silently ignored)
- [ ] Write unit tests:
  - Rule with `watch = true` reconciler: all rule paths in set
  - Rule with `watch = false` reconciler: no paths in set
  - Rule with no reconciler: no paths in set
  - Multiple rules with watch: union of all paths
  - Empty config: empty map

#### Success Criteria

- Watched paths correctly extracted from config
- Empty map when no watched reconcilers
- `make test` and `make lint` pass

---

### Phase 7: Cleanup and Integration Testing

Remove the old custom properties code path and verify end-to-end behavior.

#### Tasks

- [ ] Remove `internal/checker/properties.go` (logic migrated to reconciler)
- [ ] Remove `internal/checker/properties_test.go`
- [ ] Remove `customPropertiesMode` from `config.Config`
- [ ] Update any remaining references to the old properties code path
- [ ] Write integration test: HCL config with `custom_properties` reconciler
  → engine creation → mock GitHub client → expected API calls
- [ ] Write integration test: push event → handler → enqueue → engine →
  reconciler runs
- [ ] Run `make ci` (lint + test + build)
- [ ] Update `cmd/repo-guardian/main.go` imports

#### Success Criteria

- No references to old `properties.go` code path
- `make ci` passes
- Integration tests verify end-to-end reconciler flow
- Integration tests verify end-to-end push event flow

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/reconciler/reconciler.go` | Create | Interface and ReconcileParams |
| `internal/reconciler/registry.go` | Create | Factory registry |
| `internal/reconciler/custom_properties.go` | Create | Custom properties reconciler |
| `internal/reconciler/reconciler_test.go` | Create | Interface and registry tests |
| `internal/reconciler/custom_properties_test.go` | Create | Custom properties tests |
| `internal/policy/types.go` | Modify | Add ReconcilerConfig type |
| `internal/policy/defaults.go` | Modify | Add CUSTOM_PROPERTIES_MODE compat |
| `internal/policy/watch.go` | Create | Watched path extraction |
| `internal/policy/watch_test.go` | Create | Watched path tests |
| `internal/checker/engine.go` | Modify | Integrate reconcilers into CheckRepo |
| `internal/checker/engine_test.go` | Modify | Add reconciler integration tests |
| `internal/checker/properties.go` | Delete | Migrated to reconciler |
| `internal/checker/properties_test.go` | Delete | Migrated to reconciler |
| `internal/checker/queue.go` | Modify | Add TriggerPush constant |
| `internal/webhook/handler.go` | Modify | Add push event handler |
| `internal/webhook/handler_test.go` | Modify | Add push event tests |
| `internal/config/config.go` | Modify | Remove customPropertiesMode |
| `cmd/repo-guardian/main.go` | Modify | Wire reconcilers and watched paths |

## Testing Plan

- [ ] Unit tests for reconciler interface and registry
- [ ] Unit tests for custom properties reconciler (mirrors existing tests)
- [ ] Unit tests for push event handler (all scenarios)
- [ ] Unit tests for watched path extraction
- [ ] Integration test: config → engine → reconciler flow
- [ ] Integration test: push event → enqueue → check flow
- [ ] Backward compatibility test: CUSTOM_PROPERTIES_MODE without HCL

## Dependencies

- IMPL-0005 (HCL Policy Configuration) — must be completed first
- DESIGN-0007 — design document (completed)
- Current custom properties code: `internal/checker/properties.go`
- Current webhook handler: `internal/webhook/handler.go`

## Open Questions

1. **Reconciler error handling:** Should a reconciler error fail the entire
   `CheckRepo` call, or should it be logged and the check continue? Current
   `checkCustomPropertiesIfEnabled` logs and continues. The design doc
   follows this pattern, but should we make it configurable per reconciler?

2. **Push event handler testing:** The `PushEvent` type from `go-github`
   has complex nested structures. Should we use real JSON fixtures from
   GitHub's webhook docs, or construct Go structs directly in tests?

3. **Reconciler content reading:** The design says file content is read once
   and passed to all reconcilers. Should we use `client.GetFileContent` (new
   method) or reuse the existing `client.GetContents` which only checks
   existence? We'll need to add a content-reading method to the GitHub
   client interface.

## References

- [DESIGN-0007: Reconciler Interface and Push Event Handler](../design/0007-reconciler-interface-and-push-event-handler.md)
- [RFC-0002: HCL-driven Policy Engine](../rfc/0002-hcl-driven-policy-engine-for-repo-guardian.md)
- Current custom properties: `internal/checker/properties.go`
- Current webhook handler: `internal/webhook/handler.go`
- Current queue: `internal/checker/queue.go`
