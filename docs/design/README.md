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
## All DESIGNs

| ID | Title | Status | Date | Author | Link |
|----|-------|--------|------|--------|------|
| DESIGN-0001 | Custom Properties from Backstage | Approved | 2026-03-01 | Donald Gifford | [0001-custom-properties-from-backstage.md](0001-custom-properties-from-backstage.md) |
| DESIGN-0002 | GitHub API Rate Limit Handling | Approved | 2026-03-01 | Donald Gifford | [0002-github-api-rate-limit-handling.md](0002-github-api-rate-limit-handling.md) |
| DESIGN-0003 | Tailscale Integration Research | Draft | 2026-03-01 | Donald Gifford | [0003-tailscale-integration-research.md](0003-tailscale-integration-research.md) |
| DESIGN-0004 | GitHub Webhook IP Allowlist Middleware | Implemented | 2026-03-14 | Donald Gifford | [0004-github-webhook-ip-allowlist-middleware.md](0004-github-webhook-ip-allowlist-middleware.md) |
| DESIGN-0005 | Helm Chart for repo-guardian | Implemented | 2026-03-14 | Donald Gifford | [0005-helm-chart-for-repo-guardian.md](0005-helm-chart-for-repo-guardian.md) |
| DESIGN-0006 | HCL Policy Configuration and Rule Engine | Implemented | 2026-03-15 | Donald Gifford | [0006-hcl-policy-configuration-and-rule-engine.md](0006-hcl-policy-configuration-and-rule-engine.md) |
| DESIGN-0007 | Reconciler Interface and Push Event Handler | Implemented | 2026-03-15 | Donald Gifford | [0007-reconciler-interface-and-push-event-handler.md](0007-reconciler-interface-and-push-event-handler.md) |
| DESIGN-0008 | Additional Rule Types and Ignore Lists | Implemented | 2026-03-15 | Donald Gifford | [0008-additional-rule-types-and-ignore-lists.md](0008-additional-rule-types-and-ignore-lists.md) |
| DESIGN-0009 | Distributed Renovate via Per-Repo GitHub Actions | Approved | 2026-03-15 | Donald Gifford | [0009-distributed-renovate-via-per-repo-github-actions.md](0009-distributed-renovate-via-per-repo-github-actions.md) |
| DESIGN-0010 | Per-org rule scoping and observability | Implemented | 2026-04-25 | Donald Gifford | [0010-per-org-rule-scoping-and-observability.md](0010-per-org-rule-scoping-and-observability.md) |
<!-- END DOCZ AUTO-GENERATED -->
