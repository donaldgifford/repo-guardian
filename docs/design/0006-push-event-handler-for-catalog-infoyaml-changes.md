---
id: DESIGN-0006
title: "Push Event Handler for catalog-info.yaml Changes"
status: Draft
author: Donald Gifford
created: 2026-03-14
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0006: Push Event Handler for catalog-info.yaml Changes

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-03-14

## Overview

Add a `push` event handler to repo-guardian's webhook handler so that when
`catalog-info.yaml` (or `.yml`) is added or modified on a repo's default
branch, the repo is immediately re-enqueued for a custom properties check.
This closes the feedback loop where repo-guardian creates a PR to add
`catalog-info.yaml` (in `api` mode), but has no way to detect when that PR
is merged until the next scheduler reconciliation cycle (default 168h).

## Goals and Non-Goals

### Goals

- React to `catalog-info.yaml` changes on the default branch within seconds
  of the push, rather than waiting up to a week for the scheduler
- Filter push events efficiently -- only enqueue when the push includes
  changes to `catalog-info.yaml` or `catalog-info.yml`
- Work with both `api` and `github-action` custom properties modes
- Add observability via existing webhook metrics

### Non-Goals

- Handling push events for non-default branches (feature branches, PRs)
- Filtering by other file types (CODEOWNERS, dependabot, etc.) -- this is
  specifically for `catalog-info.yaml`
- Replacing the scheduler -- the scheduler remains the safety net for repos
  that were missed or for initial reconciliation
- Debouncing rapid pushes to the same repo -- the checker engine is already
  idempotent, so duplicate work is harmless

## Background

Today repo-guardian listens for three webhook events:

| Event | Action | Behavior |
|-------|--------|----------|
| `repository` | `created` | Enqueue new repo for check |
| `installation_repositories` | `added` | Enqueue added repos |
| `installation` | `created` | Enqueue all repos in new installation |

When `CUSTOM_PROPERTIES_MODE=api`, the checker reads `catalog-info.yaml`,
extracts property values (Owner, Component, JiraProject, JiraLabel), and sets
them via the GitHub API. If `catalog-info.yaml` is missing, it sets
`Unclassified` defaults and creates a PR to add the file.

The problem: once that PR is merged (or a developer manually adds/edits
`catalog-info.yaml`), there is no event-driven trigger to re-scan the repo.
The properties stay as `Unclassified` until the weekly scheduler runs.

### GitHub Push Event Payload

The `push` webhook event includes:

- `ref` -- the full ref that was pushed (e.g., `refs/heads/main`)
- `repository` -- standard repo object with owner, name, default_branch
- `installation.id` -- the app installation ID
- `commits[]` -- array of commit objects, each with:
  - `added[]` -- file paths added in this commit
  - `modified[]` -- file paths modified in this commit
  - `removed[]` -- file paths removed in this commit

This gives us everything we need to filter by file path without making any
API calls.

## Detailed Design

### Webhook Handler Changes

Add a new case to the `ServeHTTP` switch in `internal/webhook/handler.go`:

```go
case *gh.PushEvent:
    h.handlePushEvent(e)
```

The `handlePushEvent` method:

1. Check that the push is to the repo's default branch by comparing
   `e.GetRef()` against `refs/heads/<default_branch>`
2. Scan `commits[].added` and `commits[].modified` for `catalog-info.yaml`
   or `catalog-info.yml`
3. If found, enqueue the repo for a full check
4. If not found, log at debug level and return

```
push event received
  |
  ref == refs/heads/<default_branch>?
  |-- No  -> debug log, ignore
  `-- Yes -> commits contain catalog-info.yaml or .yml?
             |-- No  -> debug log, ignore
             `-- Yes -> enqueue repo for check
```

### File Path Matching

Check both `added` and `modified` arrays (not `removed` -- removing
`catalog-info.yaml` doesn't need a rescan since properties are already set).
Match against:

- `catalog-info.yaml`
- `catalog-info.yml`

These are always at the repo root, so exact string matching is sufficient --
no glob or path prefix logic needed.

### Trigger Type

Add a new trigger constant to distinguish push-triggered jobs in metrics and
logs:

```go
TriggerPush Trigger = "push"
```

This flows through to the existing `repo_guardian_repos_checked_total` counter
with label `trigger="push"`.

### Interaction with Existing CheckRepo Flow

The enqueued job runs through the same `CheckRepo` path as webhook and
scheduler jobs. This means:

- File rules (CODEOWNERS, dependabot, etc.) are also checked -- this is fine,
  it's a lightweight no-op if files exist
- Custom properties are re-evaluated with the now-present `catalog-info.yaml`
- The check is fully idempotent

No changes to the checker engine are needed.

### Volume Considerations

Every push to the default branch of every installed repo fires this webhook.
For an active org, this could be significant volume. However:

- The handler does zero API calls -- it only inspects the webhook payload
- The file path check is O(commits * files_per_commit), typically trivial
- Only pushes touching `catalog-info.yaml` result in enqueued work
- The work queue has a bounded buffer and drops jobs when full (existing
  behavior)

### GitHub App Configuration

The GitHub App must subscribe to the `push` event. This is a one-time manual
change in the GitHub App settings:

- **Events:** Add `push` to the subscribed events list
- **Permissions:** No new permissions required -- `push` events only need
  `metadata: read` (already granted)

## API / Interface Changes

### Configuration

No new environment variables or configuration changes. The push handler is
always active -- it only does useful work when `CUSTOM_PROPERTIES_MODE` is
set, but the filtering is cheap enough that there's no reason to gate it
behind a separate toggle.

### Helm Chart

No changes to the Helm chart values. The push event subscription is
configured on the GitHub App side, not in the deployment.

## Data Model

No data model changes. The existing `RepoJob` struct and work queue are
reused as-is.

## Testing Strategy

- **Unit tests for `handlePushEvent`:**
  - Push to default branch with `catalog-info.yaml` in `added` -- enqueues
  - Push to default branch with `catalog-info.yml` in `modified` -- enqueues
  - Push to default branch with unrelated files only -- does not enqueue
  - Push to non-default branch -- does not enqueue
  - Push with `catalog-info.yaml` in `removed` only -- does not enqueue
- **Unit tests for file path matching helper:**
  - Exact match for both `.yaml` and `.yml` extensions
  - No false positives on paths like `subdir/catalog-info.yaml`
- **Existing tests** should continue to pass unmodified

## Migration / Rollout Plan

1. Merge the code change -- no behavioral impact until the GitHub App is
   configured to send push events
2. Deploy the updated binary
3. Add `push` to the GitHub App's subscribed events in GitHub App settings
4. Monitor `repo_guardian_webhook_received_total{event_type="push"}` to
   confirm events are arriving
5. Monitor `repo_guardian_repos_checked_total{trigger="push"}` to confirm
   catalog-info changes trigger rescans
6. Verify properties update within seconds of merging a `catalog-info.yaml`
   PR (vs waiting for the scheduler)

The rollout is low-risk because:

- The handler is additive -- it doesn't change existing event handling
- Filtering is done entirely on the payload, no new API calls
- The checker engine is already idempotent
- If the push event volume is too high, the GitHub App event subscription can
  be removed without any code changes

## Open Questions

1. **Should we also trigger on `catalog-info.yaml` removal?** When the file
   is deleted, should we re-scan to set properties back to `Unclassified`?
   Currently the design ignores removals since the file being gone results in
   the same `Unclassified` defaults on next scheduler run. But if we want
   faster feedback, we could include `removed` in the check.

2. **Should push events trigger a full `CheckRepo` or only
   `CheckCustomProperties`?** Currently we enqueue a standard `RepoJob` that
   runs the full check (file rules + custom properties). This is simple and
   idempotent, but does unnecessary work for repos that already have all
   config files. An alternative is a `PropertiesOnlyJob` that skips file
   rules. The tradeoff is simplicity vs efficiency.

3. **Should we also handle `catalog-info.yaml` changes in subdirectories?**
   Some monorepos might place catalog-info files in subdirectories (e.g.,
   `services/my-service/catalog-info.yaml`). The current design only matches
   root-level files. Do we need to support nested paths?

4. **Should the push handler be gated behind `CUSTOM_PROPERTIES_MODE`?** The
   current design always handles push events but only does useful work when
   custom properties mode is enabled. An alternative is to skip push event
   registration entirely when the mode is disabled. The tradeoff is
   simplicity (always handle, filter is cheap) vs purity (don't process
   events we can't act on).

## References

- [DESIGN-0001: Custom Properties from Backstage](0001-custom-properties-from-backstage.md)
- [IMPL-0002: Custom Properties Implementation Plan](../impl/0002-custom-properties-implementation-plan.md)
- [GitHub Push Event Documentation](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push)
- Current webhook handler: `internal/webhook/handler.go`
- Custom properties checker: `internal/checker/properties.go`
