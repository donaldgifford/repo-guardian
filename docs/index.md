# repo-guardian

repo-guardian evaluates every repository in your GitHub organisations
against a compliance policy you keep as code, records who is compliant
with which control and why not, and, where the policy says so, opens a
pull request or makes the API change that fixes it.

> [!IMPORTANT]
> These pages describe repo-guardian 2.x, a fresh install. The
> runbooks under **Historical (1.x)** describe the 1.x line and say so
> at the top; moving from 1.x is covered in
> [Migrating from v1 to v2](operations/v2-migration.md).

## Start here

- [repo-guardian and v2 at a glance](operations/v2-overview.md): what
  it enforces, controls and rules, evaluate and remediate, the two
  GitHub Apps.
- [Getting started](usage/getting-started.md): the two Apps, a minimal
  policy root, chart values, the first evaluation, turning on
  remediation for one org.
- [Turning on repo-guardian v2](operations/v2-onboarding.md): the
  production checklist.

## Reference

- [Policy reference](usage/policy-reference.md): the policy directory
  (`enterprise.hcl`, `catalogue/`, `orgs/`, `templates/`).
- [Control-type reference](usage/controls-reference.md): every built-in
  control type, generated from the code.
- [The business UI](usage/ui.md) and [the read-only API](usage/api.md).
- [Identity providers](operations/identity-provider.md): Keycloak,
  Okta, Entra ID and Auth0 recipes for the API and UI.
- [Verifying releases](operations/verifying-releases.md): cosign, SLSA
  provenance and SBOMs for every image and chart.

## How it works

- **The policy is a directory**, resolved per repository into
  assignments: which controls apply, in which mode.
- **The unit is a control** (`codeowners@1`): a type, a version and a
  set of rules. Results, compliance and history are per control and per
  rule.
- **Two GitHub Apps.** The Evaluation App reads; the Remediation App
  writes, only where you install it.
- **Temporal and Postgres.** Each repository has a durable evaluation
  workflow; results and history live in Postgres behind per-role
  database users.
- **Remediation** is one pull request per control, or a direct API
  change, only for assignments in `mode = "remediate"`. It never
  force-pushes and never deletes a branch.

## Document types

- [RFCs](rfc/README.md)
- [ADRs](adr/README.md)
- [Design](design/README.md)
- [Implementation Plans](impl/README.md)
- [Plans](plan/README.md)
- [Investigations](investigation/README.md)
