# Design Documents

This directory contains detailed design documents for feature implementation.

## What are Design Documents?

Design documents describe **how a feature or system will be built**. Each design
document includes:

- **Overview**: What is being designed and why
- **Goals and Non-Goals**: Scope boundaries
- **Detailed Design**: Architecture, APIs, data models
- **Testing Strategy**: How the design will be validated
- **Migration Plan**: How to roll out the changes

## Creating a New Design Document

```bash
docz create design "Your Design Title"
```

## Design Status

- **Draft**: Initial draft, still being written
- **In Review**: Ready for review and feedback
- **Approved**: Approved and ready for implementation
- **Implemented**: Design has been fully implemented
- **Abandoned**: Design was not pursued

<!-- BEGIN DOCZ AUTO-GENERATED -->
## All Design

| ID | Title | Status | Date | Author | Link |
|----|-------|--------|------|--------|------|
| DESIGN-0001 | Custom Properties from Backstage | Approved | 2026-03-01 | Donald Gifford | [0001-custom-properties-from-backstage.md](0001-custom-properties-from-backstage.md) |
| DESIGN-0002 | GitHub API Rate Limit Handling | Implemented | 2026-03-01 | Donald Gifford | [0002-github-api-rate-limit-handling.md](0002-github-api-rate-limit-handling.md) |
| DESIGN-0003 | Tailscale Integration Research | Draft | 2026-03-01 | Donald Gifford | [0003-tailscale-integration-research.md](0003-tailscale-integration-research.md) |
| DESIGN-0004 | GitHub Webhook IP Allowlist Middleware | Implemented | 2026-03-14 | Donald Gifford | [0004-github-webhook-ip-allowlist-middleware.md](0004-github-webhook-ip-allowlist-middleware.md) |
| DESIGN-0005 | Helm Chart for repo-guardian | Implemented | 2026-03-14 | Donald Gifford | [0005-helm-chart-for-repo-guardian.md](0005-helm-chart-for-repo-guardian.md) |
| DESIGN-0006 | HCL Policy Configuration and Rule Engine | Implemented | 2026-03-15 | Donald Gifford | [0006-hcl-policy-configuration-and-rule-engine.md](0006-hcl-policy-configuration-and-rule-engine.md) |
| DESIGN-0007 | Reconciler Interface and Push Event Handler | Implemented | 2026-03-15 | Donald Gifford | [0007-reconciler-interface-and-push-event-handler.md](0007-reconciler-interface-and-push-event-handler.md) |
| DESIGN-0008 | Additional Rule Types and Ignore Lists | Implemented | 2026-03-15 | Donald Gifford | [0008-additional-rule-types-and-ignore-lists.md](0008-additional-rule-types-and-ignore-lists.md) |
| DESIGN-0009 | Distributed Renovate via Per-Repo GitHub Actions | Approved | 2026-03-15 | Donald Gifford | [0009-distributed-renovate-via-per-repo-github-actions.md](0009-distributed-renovate-via-per-repo-github-actions.md) |
| DESIGN-0010 | Per-org rule scoping and observability | Implemented | 2026-04-25 | Donald Gifford | [0010-per-org-rule-scoping-and-observability.md](0010-per-org-rule-scoping-and-observability.md) |
| DESIGN-0011 | Publish Helm chart via OCI registry | Implemented | 2026-05-02 | Donald Gifford | [0011-publish-helm-chart-via-oci-registry.md](0011-publish-helm-chart-via-oci-registry.md) |
| DESIGN-0012 | Persistent reconcile state and multi-replica coordination | Implemented | 2026-05-02 | Donald Gifford | [0012-persistent-reconcile-state-and-multi-replica-coordination.md](0012-persistent-reconcile-state-and-multi-replica-coordination.md) |
| DESIGN-0013 | Customizable PR templates and extensible template ConfigMap | Implemented | 2026-05-03 | Donald Gifford | [0013-customizable-pr-templates-and-extensible-template-configmap.md](0013-customizable-pr-templates-and-extensible-template-configmap.md) |
| DESIGN-0014 | Remove legacy engine path and deprecated overlays | Implemented | 2026-05-28 | Donald Gifford | [0014-remove-legacy-engine-path-and-deprecated-overlays.md](0014-remove-legacy-engine-path-and-deprecated-overlays.md) |
| DESIGN-0015 | Per-installation Valkey queue partitioning | Draft | 2026-06-22 | Donald Gifford | [0015-per-installation-valkey-queue-partitioning.md](0015-per-installation-valkey-queue-partitioning.md) |
| DESIGN-0016 | Separate Postgres read and write endpoints | Draft | 2026-06-22 | Donald Gifford | [0016-separate-postgres-read-and-write-endpoints.md](0016-separate-postgres-read-and-write-endpoints.md) |
| DESIGN-0017 | Stale-sweep cutover and repository discovery | Approved | 2026-06-22 | Donald Gifford | [0017-stale-sweep-cutover-and-repository-discovery.md](0017-stale-sweep-cutover-and-repository-discovery.md) |
| DESIGN-0018 | Deprecate memory backend | Approved | 2026-06-22 | Donald Gifford | [0018-deprecate-memory-backend.md](0018-deprecate-memory-backend.md) |
| DESIGN-0019 | Configurable annotation-sourced custom properties | Implemented | 2026-07-19 | Donald Gifford | [0019-configurable-annotation-sourced-custom-properties.md](0019-configurable-annotation-sourced-custom-properties.md) |
| DESIGN-0020 | Absent check mode and conditional file rules | Implemented | 2026-07-23 | Donald Gifford | [0020-absent-check-mode-and-conditional-file-rules.md](0020-absent-check-mode-and-conditional-file-rules.md) |
| DESIGN-0021 | Delayed-requeue job contract and rate-limit consolidation | Implemented | 2026-07-26 | Donald Gifford | [0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md](0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md) |
| DESIGN-0022 | Compliance posture state, dashboard suite, and OTEL-first observability | Implemented | 2026-08-02 | Donald Gifford | [0022-compliance-posture-state-dashboard-suite-and-otel-first.md](0022-compliance-posture-state-dashboard-suite-and-otel-first.md) |
| DESIGN-0023 | Operator-owned ingress: remove the Tailscale sidecar and IP-allowlist middleware | Draft | 2026-08-15 | Donald Gifford | [0023-operator-owned-ingress-remove-the-tailscale-sidecar-and-ip.md](0023-operator-owned-ingress-remove-the-tailscale-sidecar-and-ip.md) |
<!-- END DOCZ AUTO-GENERATED -->
