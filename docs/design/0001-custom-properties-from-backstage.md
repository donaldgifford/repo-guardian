---
id: DESIGN-0001
title: "Custom Properties from Backstage"
status: Approved
author: Donald Gifford
created: 2026-03-01
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0001: Custom Properties from Backstage

**Status:** Approved
**Author:** Donald Gifford
**Date:** 2026-03-01

## Summary

Ensure every repository in the GitHub organization has four custom properties
set (`Owner`, `Component`, `JiraProject`, `JiraLabel`) by reading values from
each repo's Backstage `catalog-info.yaml` file. This enables Wiz security
scanning to tag repositories with ownership and project metadata, ensuring
repos are assigned to the correct Wiz projects.

## Problem Statement

Wiz imports GitHub repository custom properties as tags. Without these
properties, repositories show up in Wiz untagged -- they cannot be attributed
to an owning team or mapped to a Wiz project. Setting four specific custom
properties on every repo closes this gap.

## Design

### Operational Modes

The feature supports two operational modes, configured via
`CUSTOM_PROPERTIES_MODE`:

| Mode | Default | App Permission | How Properties Are Set |
|---|---|---|---|
| `github-action` | Yes | `custom_properties: read` | PR with a GHA workflow that sets properties on merge |
| `api` | No | `custom_properties: read & write` | Direct API write; PR to add catalog-info.yaml if missing |

### Property Mapping

| Custom Property | Source in catalog-info.yaml | Wiz Usage |
|---|---|---|
| `Owner` | `spec.owner` | Identifies the owning team/individual |
| `Component` | `metadata.name` | Identifies the service/component name |
| `JiraProject` | `metadata.annotations["jira/project-key"]` | Maps to Jira project for issue routing |
| `JiraLabel` | `metadata.annotations["jira/label"]` | Additional Jira label for filtering |

### Source of Truth and Defaults

The `catalog-info.yaml` file (Backstage Component entity) is the preferred
source of truth. When catalog-info.yaml is missing or cannot be parsed:

- `Owner` = `Unclassified`
- `Component` = `Unclassified`
- `JiraProject` = `` (empty -- left unset)
- `JiraLabel` = `` (empty -- left unset)

This guarantees every repo gets at minimum an `Owner` and `Component` value.
Repos without a catalog-info.yaml are explicitly tagged as `Unclassified` so
they surface in Wiz as needing attention rather than silently going untagged.

### Mode: `github-action` (default / recommended)

The app has read-only access to custom properties. When properties need
updating, it creates a PR containing a GitHub Actions workflow that sets them
on merge.

```
catalog-info.yaml exists?
|-- Yes -> Parse, extract Owner/Component/Jira values
`-- No  -> Use defaults (Unclassified/Unclassified)
              |
Read current custom properties via API (read-only)
              |
Diff desired vs current
|-- Match    -> No action
`-- Mismatch -> Create PR with .github/workflows/set-custom-properties.yml
                 |
               Human reviews PR -> Merges -> GHA workflow runs -> Properties set
```

**Why this is the default:**

1. **Least-privilege** -- the app never needs `custom_properties: write`.
2. **Human review** -- property values go through the PR review process.
3. **Auditability** -- the PR and workflow run create a clear audit trail.
4. **Standard token** -- the GHA workflow uses the built-in `GITHUB_TOKEN`.

### Mode: `api`

The app has read/write access to custom properties. When catalog-info.yaml
exists, it sets properties directly via the API. When catalog-info.yaml is
missing, it sets `Unclassified` defaults via the API AND creates a PR asking
the repo owners to add a catalog-info.yaml file.

### Branch Naming

| Mode | Branch |
|---|---|
| `github-action` | `repo-guardian/set-custom-properties` |
| `api` (catalog-info PR) | `repo-guardian/add-catalog-info` |

### GitHub App Permissions

#### `github-action` mode (default)

| Permission | Access | Reason |
|---|---|---|
| **Contents** | Read & Write | Existing -- read files, create branches, commit workflow |
| **Pull Requests** | Read & Write | Existing -- check/create PRs |
| **Metadata** | Read | Existing -- required for all apps |
| **Custom properties** | Read | **New** -- read current values to diff |

#### `api` mode

| Permission | Access | Reason |
|---|---|---|
| **Contents** | Read & Write | Existing + commit catalog-info.yaml template |
| **Pull Requests** | Read & Write | Existing + create catalog-info PRs |
| **Metadata** | Read | Existing |
| **Custom properties** | Read & Write | **New** -- read current values + set new values directly |

### Edge Cases

- **Missing catalog-info.yaml**: Use `Unclassified` defaults in both modes.
- **Unparseable YAML**: Same as missing.
- **Missing Jira annotations**: Leave JiraProject/JiraLabel unset.
- **Org hasn't defined property schema**: API returns 422; logged as non-fatal.
- **Properties already match**: No action, increment
  `PropertiesAlreadyCorrectTotal`.
- **GHA workflow after merge**: Remains as dormant file; teams can delete it.
- **Idempotency**: Fully idempotent. Diffs current vs desired on every run.

### Metrics

| Metric | Type | Description |
|---|---|---|
| `repo_guardian_properties_checked_total` | Counter | Repos where properties were evaluated |
| `repo_guardian_properties_prs_created_total` | Counter | PRs created for custom properties |
| `repo_guardian_properties_set_total` | Counter | Repos where properties were set via API |
| `repo_guardian_properties_already_correct_total` | Counter | Repos where properties already matched |

## Rollout Plan

### For `github-action` mode (recommended starting point)

1. Merge code with `CUSTOM_PROPERTIES_MODE` unset (no behavioral change).
2. Define custom property schema in GitHub org settings.
3. Verify `GITHUB_TOKEN` permissions for the GHA workflow.
4. Update GitHub App permissions (add `custom_properties: read`).
5. Deploy with `CUSTOM_PROPERTIES_MODE=github-action` and `DRY_RUN=true`.
6. Disable dry-run. Monitor `PropertiesPRsCreatedTotal`.
7. Review and merge initial PRs. Verify workflows run.
8. Verify in Wiz.

## References

- [IMPL-0002: Custom Properties Implementation Plan](../impl/0002-custom-properties-implementation-plan.md)
- [RFC-0001: Repo Compliance App](../rfc/0001-repo-compliance-app-repo-guardian.md)
