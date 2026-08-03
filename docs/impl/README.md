# Implementation Plans

This directory contains implementation plans with concrete tasks and milestones.

## What are Implementation Plans?

Implementation plans describe **the concrete steps to build a feature or system**.
Each plan includes:

- **Objective**: What is being implemented and which RFC/design it implements
- **Scope**: What is in and out of scope
- **Implementation Steps**: Ordered tasks with checkboxes
- **File Changes**: Key files that will be created or modified
- **Testing Plan**: How the implementation will be validated

## Creating a New Implementation Plan

```bash
docz create impl "Your Implementation Title"
```

## Implementation Status

- **Draft**: Plan is being written
- **In Progress**: Implementation is underway
- **Completed**: Implementation is finished
- **Paused**: Work is temporarily stopped
- **Cancelled**: Plan was abandoned

<!-- BEGIN DOCZ AUTO-GENERATED -->
## All Implementation Plans

| ID | Title | Status | Date | Author | Link |
|----|-------|--------|------|--------|------|
| IMPL-0001 | Repo Guardian Implementation Plan | Completed | 2026-02-06 | Donald Gifford | [0001-repo-guardian-implementation-plan.md](0001-repo-guardian-implementation-plan.md) |
| IMPL-0002 | Custom Properties Implementation Plan | Completed | 2026-03-01 | Donald Gifford | [0002-custom-properties-implementation-plan.md](0002-custom-properties-implementation-plan.md) |
| IMPL-0003 | GitHub Webhook IP Allowlist Middleware | Completed | 2026-03-14 | Donald Gifford | [0003-github-webhook-ip-allowlist-middleware.md](0003-github-webhook-ip-allowlist-middleware.md) |
| IMPL-0004 | Helm Chart for repo-guardian | Completed | 2026-03-14 | Donald Gifford | [0004-helm-chart-for-repo-guardian.md](0004-helm-chart-for-repo-guardian.md) |
| IMPL-0005 | HCL Policy Configuration and Rule Engine | Completed | 2026-03-15 | Donald Gifford | [0005-hcl-policy-configuration-and-rule-engine.md](0005-hcl-policy-configuration-and-rule-engine.md) |
| IMPL-0006 | Reconciler Interface and Push Event Handler | Completed | 2026-03-15 | Donald Gifford | [0006-reconciler-interface-and-push-event-handler.md](0006-reconciler-interface-and-push-event-handler.md) |
| IMPL-0007 | Additional Rule Types and Ignore Lists | Completed | 2026-03-15 | Donald Gifford | [0007-additional-rule-types-and-ignore-lists.md](0007-additional-rule-types-and-ignore-lists.md) |
| IMPL-0008 | Distributed Renovate via Per-Repo GitHub Actions | Completed | 2026-03-15 | Donald Gifford | [0008-distributed-renovate-via-per-repo-github-actions.md](0008-distributed-renovate-via-per-repo-github-actions.md) |
| IMPL-0009 | Per-org rule scoping and observability | Completed | 2026-04-25 | Donald Gifford | [0009-per-org-rule-scoping-and-observability.md](0009-per-org-rule-scoping-and-observability.md) |
| IMPL-0010 | Publish Helm chart via OCI registry | Completed | 2026-05-02 | Donald Gifford | [0010-publish-helm-chart-via-oci-registry.md](0010-publish-helm-chart-via-oci-registry.md) |
| IMPL-0011 | Persistent reconcile state and multi-replica coordination | Implemented | 2026-05-03 | Donald Gifford | [0011-persistent-reconcile-state-and-multi-replica-coordination.md](0011-persistent-reconcile-state-and-multi-replica-coordination.md) |
| IMPL-0012 | Customizable PR templates and extensible template ConfigMap | Completed | 2026-05-03 | Donald Gifford | [0012-customizable-pr-templates-and-extensible-template-configmap.md](0012-customizable-pr-templates-and-extensible-template-configmap.md) |
| IMPL-0013 | Reconcile open PRs when file rules become satisfied | Implemented | 2026-05-28 | Donald Gifford | [0013-reconcile-open-prs-when-file-rules-become-satisfied.md](0013-reconcile-open-prs-when-file-rules-become-satisfied.md) |
| IMPL-0014 | Remove legacy engine path and deprecated overlays | Implemented | 2026-05-30 | Donald Gifford | [0014-remove-legacy-engine-path-and-deprecated-overlays.md](0014-remove-legacy-engine-path-and-deprecated-overlays.md) |
| IMPL-0015 | Stale-sweep cutover and repository discovery | In Progress | 2026-06-23 | Donald Gifford | [0015-stale-sweep-cutover-and-repository-discovery.md](0015-stale-sweep-cutover-and-repository-discovery.md) |
| IMPL-0016 | Deprecate memory backend | Completed | 2026-06-23 | Donald Gifford | [0016-deprecate-memory-backend.md](0016-deprecate-memory-backend.md) |
| IMPL-0017 | Configurable annotation-sourced custom properties | Completed | 2026-07-20 | Donald Gifford | [0017-configurable-annotation-sourced-custom-properties.md](0017-configurable-annotation-sourced-custom-properties.md) |
| IMPL-0018 | Fix silently ignored operator config | Completed | 2026-07-20 | Donald Gifford | [0018-fix-silently-ignored-operator-config.md](0018-fix-silently-ignored-operator-config.md) |
| IMPL-0019 | Absent check mode and conditional file rules | Completed | 2026-07-23 | Donald Gifford | [0019-absent-check-mode-and-conditional-file-rules.md](0019-absent-check-mode-and-conditional-file-rules.md) |
| IMPL-0020 | Pre-IMPL-0019 high-severity fixes (INV-0011 Group A High) | Completed | 2026-07-23 | Donald Gifford | [0020-pre-impl-0019-high-severity-fixes-inv-0011-group-a-high.md](0020-pre-impl-0019-high-severity-fixes-inv-0011-group-a-high.md) |
| IMPL-0021 | Post-IMPL-0019 hardening and structural cleanup (INV-0011 Group A Medium + Group B) | Completed | 2026-07-23 | Donald Gifford | [0021-post-impl-0019-hardening-and-structural-cleanup-inv-0011-group.md](0021-post-impl-0019-hardening-and-structural-cleanup-inv-0011-group.md) |
| IMPL-0022 | Delayed-requeue job contract and rate-limit consolidation | Completed | 2026-08-02 | Donald Gifford | [0022-delayed-requeue-job-contract-and-rate-limit-consolidation.md](0022-delayed-requeue-job-contract-and-rate-limit-consolidation.md) |
| IMPL-0023 | Compliance posture state, dashboard suite, and OTEL-first observability | Draft | 2026-08-02 | Donald Gifford | [0023-compliance-posture-state-dashboard-suite-and-otel-first.md](0023-compliance-posture-state-dashboard-suite-and-otel-first.md) |
<!-- END DOCZ AUTO-GENERATED -->
