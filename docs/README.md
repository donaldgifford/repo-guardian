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

## Quick Links

- [Configuration reference](../README.md#configuration) -- all environment variables
- [Adding Rules](ADDING_RULES.md) -- file/setting/branch-protection rules, reconcilers, multi-org configuration
- [Security](../SECURITY.md) -- webhook protection details
- [Kubernetes deployment](../README.md#kubernetes-deployment) -- Kustomize manifests and setup
- [Local development](../README.md#quick-start-local-development) -- Docker Compose quick start
- [Observability](../README.md#observability) -- Prometheus metrics reference
- [Metrics catalog](../contrib/README.md) -- exposed metrics, example PromQL, Grafana dashboard, Prometheus alerts
- [Examples](../examples/README.md) -- HCL policy samples (minimal, renovate, full, multi-org)
