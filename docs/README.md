# repo-guardian docs

Design and planning documentation for repo-guardian, managed with
[docz](https://github.com/donaldgifford/docz).

## Structured Docs

| Type | Directory | Description |
|------|-----------|-------------|
| RFC | [rfc/](rfc/) | High-level proposals |
| Design | [design/](design/) | Technical design documents |
| Implementation | [impl/](impl/) | Phased implementation plans |
| Investigation | [investigation/](investigation/) | Research and spike findings |
| ADR | [adr/](adr/) | Architecture decision records |

## Legacy Docs

These docs predate the docz migration and have been migrated to the
structured directories above. They are kept for reference.

| Document | Migrated To |
|----------|-------------|
| [RFC.md](RFC.md) | [rfc/0001](rfc/0001-repo-compliance-app-repo-guardian.md) |
| [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md) | [impl/0001](impl/0001-repo-guardian-implementation-plan.md) |
| [custom_properties.md](custom_properties.md) | [design/0001](design/0001-custom-properties-from-backstage.md) |
| [custom_properties_implementation.md](custom_properties_implementation.md) | [impl/0002](impl/0002-custom-properties-implementation-plan.md) |
| [api_backoff.md](api_backoff.md) | [design/0002](design/0002-github-api-rate-limit-handling.md) |
| [tailscale_research.md](tailscale_research.md) | [design/0003](design/0003-tailscale-integration-research.md) |

## Quick Links

- [Configuration reference](../README.md#configuration) -- all environment variables
- [Adding Rules](ADDING_RULES.md) -- file/setting/branch-protection rules, reconcilers, multi-org configuration
- [Security](../SECURITY.md) -- webhook protection details
- [Kubernetes deployment](../README.md#kubernetes-deployment) -- Kustomize manifests and setup
- [Local development](../README.md#quick-start-local-development) -- Docker Compose quick start
- [Observability](../README.md#observability) -- Prometheus metrics reference
- [Metrics catalog](../contrib/README.md) -- exposed metrics, example PromQL, Grafana dashboard, Prometheus alerts
- [Examples](../examples/README.md) -- HCL policy samples (minimal, renovate, full, multi-org)
