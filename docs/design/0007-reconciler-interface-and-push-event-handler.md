---
id: DESIGN-0007
title: "Reconciler Interface and Push Event Handler"
status: Draft
author: Donald Gifford
created: 2026-03-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0007: Reconciler Interface and Push Event Handler

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-15

## Overview

Define a `Reconciler` interface that allows rules to attach post-existence
behaviors (read a file, extract data, sync derived state), migrate the
existing custom properties checker into the first reconciler implementation,
and add a `push` event handler that triggers re-evaluation when watched
files are modified on the default branch.

**Implements:** RFC-0002 Phases 3, 5

**Depends on:** DESIGN-0006 (HCL config and rule engine)

## Goals and Non-Goals

### Goals

- Define a generic `Reconciler` interface with a fixed schema per type
- Migrate the existing custom properties checker into a
  `custom_properties` reconciler with identical behavior
- Preserve backward compatibility: `CUSTOM_PROPERTIES_MODE` env var
  continues to work as a shorthand
- Add `push` event handling to the webhook handler
- Only trigger on pushes to the default branch that modify watched file
  paths
- Add `TriggerPush` trigger type for metrics and logging
- Close the feedback loop: catalog-info.yaml merge → immediate rescan

### Non-Goals

- `label_sync`, `branch_protection`, `workflow_sync` reconciler
  implementations (DESIGN-0008)
- Ignore lists (DESIGN-0008)
- Additional rule types beyond `file` (DESIGN-0008)
- Debouncing rapid pushes (checker is idempotent)

## Background

### Current Custom Properties Flow

The custom properties checker (`internal/checker/properties.go`) is a
separate code path from the file rule engine. It:

1. Reads `catalog-info.yaml` (or `.yml`) from the repo
2. Parses it with `catalog.Parse()` to extract 4 fields (Owner, Component,
   JiraProject, JiraLabel)
3. Reads current GitHub custom properties via API
4. Diffs desired vs current
5. In `api` mode: sets properties directly; if catalog-info is missing,
   creates a PR to add it
6. In `github-action` mode: creates a PR with a GHA workflow that sets
   properties on merge

This logic is correct but special-cased. After DESIGN-0006 refactors the
rule engine, `catalog-info.yaml` becomes a standard `rule "file"` with an
attached reconciler that handles steps 2-6.

### Missing Feedback Loop

Today the app doesn't subscribe to `push` events. When repo-guardian creates
a PR to add `catalog-info.yaml` (api mode) and the PR is merged, the
properties stay as `Unclassified` until the weekly scheduler runs. The push
event handler closes this gap.

## Detailed Design

### Reconciler Interface

```go
// internal/reconciler/reconciler.go

// Reconciler performs post-existence actions when a rule's file is found
// in a repository. Each reconciler type has a fixed schema for configuration.
type Reconciler interface {
    // Name returns the reconciler type identifier.
    Name() string

    // Reconcile is called when the associated rule's file exists.
    // content is the file's raw content (empty string if file was just
    // created and content isn't available yet).
    Reconcile(ctx context.Context, params ReconcileParams) error
}

// ReconcileParams contains everything a reconciler needs.
type ReconcileParams struct {
    Client        github.Client
    Owner         string
    Repo          string
    DefaultBranch string
    Content       string            // file content
    OpenPRs       []*github.PullRequest
    DryRun        bool
    Logger        *slog.Logger
}
```

### Reconciler Registry

```go
// internal/reconciler/registry.go

// Registry maps reconciler type names to factory functions.
type Registry struct {
    factories map[string]Factory
}

// Factory creates a Reconciler from its HCL config block.
type Factory func(config ReconcilerConfig) (Reconciler, error)

// NewRegistry creates a registry with all built-in reconciler types.
func NewRegistry() *Registry

// Build creates a Reconciler instance from the given config.
func (r *Registry) Build(config ReconcilerConfig) (Reconciler, error)
```

Built-in factories registered at startup:

| Type | Factory |
|------|---------|
| `custom_properties` | `NewCustomPropertiesReconciler` |
| `label_sync` | (DESIGN-0008) |
| `branch_protection` | (DESIGN-0008) |
| `workflow_sync` | (DESIGN-0008) |

### Custom Properties Reconciler

The `custom_properties` reconciler migrates the logic from
`internal/checker/properties.go` into the reconciler interface.

#### HCL Config Schema (Fixed)

```hcl
reconcile "custom_properties" {
  mode         = "api"                                    # "api" or "github-action"
  watch        = true                                     # trigger on push events

  owner        = "spec.owner"                             # YAML path to owner field
  component    = "metadata.name"                          # YAML path to component field
  jira_project = "metadata.annotations.jira/project-key"  # YAML path to jira project
  jira_label   = "metadata.annotations.jira/label"        # YAML path to jira label

  defaults {
    owner     = "Unclassified"
    component = "Unclassified"
  }
}
```

#### Go Type

```go
// internal/reconciler/custom_properties.go

type CustomPropertiesConfig struct {
    Mode        string  // "api" or "github-action"
    Watch       bool
    Owner       string  // YAML path
    Component   string  // YAML path
    JiraProject string  // YAML path
    JiraLabel   string  // YAML path
    Defaults    CustomPropertiesDefaults
}

type CustomPropertiesDefaults struct {
    Owner     string
    Component string
}
```

#### Reconcile Flow

The `Reconcile` method mirrors the current `CheckCustomProperties`:

1. Parse `content` as YAML
2. Extract values using the configured YAML paths (reuses the YAML path
   evaluator from DESIGN-0006)
3. Apply defaults for empty values
4. Read current custom properties via GitHub API
5. Diff desired vs current
6. If match, return (increment `PropertiesAlreadyCorrectTotal`)
7. If mismatch, branch on mode:
   - `api`: set properties directly, create catalog-info PR if file was
     missing
   - `github-action`: create PR with GHA workflow

#### Backward Compatibility

When no HCL config is present, the built-in defaults include a
`catalog_info` file rule with a `custom_properties` reconciler -- but only
if `CUSTOM_PROPERTIES_MODE` is set. This preserves the current behavior
where the feature is disabled by default.

```go
// In the built-in defaults builder:
if mode := os.Getenv("CUSTOM_PROPERTIES_MODE"); mode != "" {
    catalogRule.Reconcilers = []ReconcilerConfig{{
        Type:  "custom_properties",
        Mode:  mode,
        Watch: false,  // no push handler in legacy mode
        // ... field mappings match current hardcoded values
    }}
}
```

### Checker Engine Integration

The checker engine's `CheckRepo` flow is extended:

```
CheckRepo(ctx, client, owner, repo)
  |
  for each enabled rule:
  |   check file existence (exists/contains/exact)
  |   |-- missing → create PR
  |   `-- present → run assertions (if contains mode)
  |                  |-- fail → create PR to fix
  |                  `-- pass → run reconcilers
  |                              |
  |                              for each reconciler:
  |                                reconciler.Reconcile(ctx, params)
```

The key change: reconcilers run **after** assertions pass (or in `exists`
mode, after the file is confirmed present). The file content is read once
and passed to all reconcilers.

### Push Event Handler

#### Webhook Handler Changes

Add a new case to `ServeHTTP` in `internal/webhook/handler.go`:

```go
case *gh.PushEvent:
    h.handlePushEvent(e)
```

#### handlePushEvent

```go
func (h *Handler) handlePushEvent(e *gh.PushEvent) {
    repo := e.GetRepo()
    defaultBranch := repo.GetDefaultBranch()

    // Only process pushes to the default branch.
    expectedRef := "refs/heads/" + defaultBranch
    if e.GetRef() != expectedRef {
        h.logger.Debug("ignoring push to non-default branch",
            "ref", e.GetRef(),
            "default_branch", defaultBranch,
        )
        return
    }

    // Check if any watched file paths were modified.
    if !h.hasWatchedFileChanges(e) {
        h.logger.Debug("push has no watched file changes",
            "owner", repo.GetOwner().GetLogin(),
            "repo", repo.GetName(),
        )
        return
    }

    h.logger.Info("watched file changed on default branch",
        "owner", repo.GetOwner().GetLogin(),
        "repo", repo.GetName(),
        "ref", e.GetRef(),
    )

    h.enqueue(
        repo.GetOwner().GetLogin(),
        repo.GetName(),
        e.GetInstallation().GetID(),
        checker.TriggerPush,
    )
}
```

#### Watched File Path Matching

The handler needs to know which file paths are "watched" (have a reconciler
with `watch = true`). This is provided by the policy config at startup.

```go
// Handler receives the set of watched paths at construction time.
type Handler struct {
    webhookSecret []byte
    queue         *checker.Queue
    logger        *slog.Logger
    watchedPaths  map[string]bool  // set of file paths to watch
}

func (h *Handler) hasWatchedFileChanges(e *gh.PushEvent) bool {
    for _, commit := range e.Commits {
        for _, path := range commit.Added {
            if h.watchedPaths[path] {
                return true
            }
        }
        for _, path := range commit.Modified {
            if h.watchedPaths[path] {
                return true
            }
        }
    }
    return false
}
```

Watched paths are extracted from the policy config at startup:

```go
// For each file rule with a reconciler where watch = true,
// add all rule.Paths to the watched set.
func ExtractWatchedPaths(config *PolicyConfig) map[string]bool
```

#### Trigger Type

Add a new trigger constant:

```go
// internal/checker/queue.go
TriggerPush Trigger = "push"
```

This flows through to `repo_guardian_repos_checked_total{trigger="push"}`.

#### Volume Considerations

Every push to the default branch fires a webhook. The handler:

- Does zero API calls -- only inspects the webhook payload
- File path check is O(commits * files_per_commit) against a small set
- Only pushes touching watched paths result in enqueued work
- The work queue drops jobs when full (existing behavior)

#### GitHub App Configuration

The GitHub App must subscribe to the `push` event:

- **Events:** Add `push` to the subscribed events list
- **Permissions:** No new permissions required (`metadata: read` is
  sufficient, already granted)

This is a one-time manual change in GitHub App settings.

### Metrics

No new metrics are needed. Existing metrics cover the new functionality:

| Metric | How It's Used |
|--------|--------------|
| `webhook_received_total{event_type="push"}` | Push events received |
| `repos_checked_total{trigger="push"}` | Repos rescanned due to push |
| `properties_checked_total` | Custom properties evaluations (unchanged) |
| `properties_set_total` | Properties set via API (unchanged) |
| `properties_already_correct_total` | Properties already correct (unchanged) |

## API / Interface Changes

### New Trigger Type

`TriggerPush` is added to the `Trigger` type. This appears in logs and the
`trigger` label on `repos_checked_total`.

### Handler Constructor Change

`webhook.NewHandler` gains a `watchedPaths` parameter:

```go
func NewHandler(
    webhookSecret string,
    queue *checker.Queue,
    logger *slog.Logger,
    watchedPaths map[string]bool,
) *Handler
```

When no HCL config is present (or no reconcilers have `watch = true`), an
empty map is passed and push events are silently ignored.

## Data Model

No data model changes. The `RepoJob` struct is reused as-is.

## Testing Strategy

### Unit Tests -- Reconciler

- **Interface compliance:** `CustomPropertiesReconciler` implements
  `Reconciler`
- **Reconcile with valid catalog-info:** Extracts correct values, diffs
  against current, calls `SetCustomPropertyValues`
- **Reconcile with missing fields:** Uses defaults for empty owner/component
- **Reconcile with unparseable content:** Uses defaults
- **Reconcile in dry-run mode:** Logs but doesn't call API
- **Reconcile in api mode:** Sets properties directly, creates
  catalog-info PR if missing
- **Reconcile in github-action mode:** Creates PR with GHA workflow
- **Backward compatibility:** `CUSTOM_PROPERTIES_MODE=api` without HCL
  produces identical behavior to current code

### Unit Tests -- Push Event Handler

- **Push to default branch with watched file in `added`:** Enqueues
- **Push to default branch with watched file in `modified`:** Enqueues
- **Push to default branch with unrelated files:** Does not enqueue
- **Push to non-default branch:** Does not enqueue
- **Push with watched file in `removed` only:** Does not enqueue
- **Push with no watched paths configured:** Does not enqueue
- **Multiple commits, watched file in later commit:** Enqueues

### Unit Tests -- Watched Path Extraction

- **Rule with `watch = true` reconciler:** All rule paths in watched set
- **Rule with `watch = false` reconciler:** No paths in watched set
- **Rule with no reconciler:** No paths in watched set
- **Multiple rules with watch:** Union of all paths

## Migration / Rollout Plan

1. Ship the reconciler interface and custom properties migration -- no
   behavioral change for existing deployments
2. Verify `CUSTOM_PROPERTIES_MODE` env var still works identically
3. Deploy updated binary
4. Add `push` to GitHub App's subscribed events
5. Monitor `webhook_received_total{event_type="push"}` for event flow
6. Monitor `repos_checked_total{trigger="push"}` for triggered rescans
7. Verify properties update within seconds of merging a catalog-info PR
8. If push event volume is too high, remove the `push` subscription as a
   kill switch (no code change needed)

## Resolved Questions

1. **Reconciler receives check mode result:** No. Keep it simple --
   reconcilers receive file content only. They only run when assertions
   pass (or in `exists` mode, when the file is present). If we need
   conditional behavior later, we can add it then.

2. **Full `CheckRepo` vs reconcile-only for push jobs:** Full `CheckRepo`.
   Simple, idempotent, and the extra file existence checks are lightweight.
   No new job type needed.

3. **Push-triggered `RepoJob` carries changed file paths:** No. Since we
   run full `CheckRepo`, there's no need to pass changed paths. The
   standard `RepoJob` struct is reused as-is.

4. **Tag push handling:** Log tag pushes at debug level for visibility.
   The `refs/heads/<default_branch>` filter already ignores them, but an
   explicit debug log helps with troubleshooting.

## References

- [RFC-0002: HCL-driven Policy Engine](../rfc/0002-hcl-driven-policy-engine-for-repo-guardian.md)
- [DESIGN-0006: HCL Policy Configuration and Rule Engine](0006-hcl-policy-configuration-and-rule-engine.md)
- [DESIGN-0008: Additional Rule Types and Ignore Lists](0008-additional-rule-types-and-ignore-lists.md)
- [GitHub Push Event Payload](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push)
- Current custom properties checker: `internal/checker/properties.go`
- Current webhook handler: `internal/webhook/handler.go`
