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

- [Getting started](usage/getting-started.md) -- the two Apps, a policy root, chart values, the first evaluation
- [repo-guardian and v2 at a glance](operations/v2-overview.md) -- controls, evaluate and remediate, the two Apps
- [Policy reference](usage/policy-reference.md) and [control-type reference](usage/controls-reference.md)
- [Adding a control type](ADDING_RULES.md) -- extending repo-guardian itself
- [Chart values](../charts/repo-guardian/README.md) -- the Helm chart, the only supported deployment
- [Security](../SECURITY.md) -- reporting vulnerabilities, webhook protection
- [Generated dashboards and alerts](operations/monitoring-generation.md)
- [Examples](../examples/README.md) -- policy roots and values files
