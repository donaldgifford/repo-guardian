---
id: INV-0006
title: "Per-org GitHub App credentials"
status: Deferred
author: Donald Gifford
created: 2026-05-26
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0006: Per-org GitHub App credentials

**Status:** Deferred
**Author:** Donald Gifford
**Date:** 2026-05-26

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Findings](#findings)
  - [Observation 1 — current single-app model](#observation-1--current-single-app-model)
  - [Observation 2 — rate limits are already per-installation](#observation-2--rate-limits-are-already-per-installation)
  - [Observation 3 — the four real motivations](#observation-3--the-four-real-motivations)
  - [Observation 4 — implementation cost sketch](#observation-4--implementation-cost-sketch)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [Triggers that would re-open this](#triggers-that-would-re-open-this)
- [References](#references)
<!--toc:end-->

## Question

Today repo-guardian authenticates as **one GitHub App** installed across
N orgs. Could we (and should we) configure it to use a **separate
GitHub App per org**, with each org owning its own app definition,
private key, and webhook secret?

## Hypothesis

Yes technically — but the motivation most people reach for first (rate
limit isolation) is already handled by the current model because GitHub
App rate limits are per-installation, not per-app. The strongest real
driver for multi-app is **multi-enterprise deployments**, which isn't
in scope for the current homelab + small-multi-org use case.

For now: deferred. Named triggers below would re-open it.

## Context

**Triggered by:** Operator question during INV-0005 review on PR #80.
Not a current production need; the homelab runs one app across all
deployed orgs. The question is documented now to preserve the
analysis for whenever the trigger conditions appear.

## Approach

1. Read `internal/github/client.go` and `internal/config/config.go`
   to confirm how the current single-app model loads credentials and
   mints installation tokens.
2. Verify that rate-limit gating
   (`internal/checker/sweep.go:RateLimitRemaining`) is already
   per-installation, not per-app.
3. Enumerate the realistic motivations for multi-app config and
   weight each against the current deployment surface.
4. Sketch the minimum implementation cost: config schema, transport
   pool, webhook secret discrimination, Helm chart shape.

## Findings

### Observation 1 — current single-app model

Configuration loads a single set of GitHub App credentials from env:

- `GITHUB_APP_ID` — one app ID
- `GITHUB_PRIVATE_KEY` — one PEM private key
- `WEBHOOK_SECRET` — one HMAC secret for the app's webhook

The binary constructs one `ghinstallation.AppsTransport` at startup
and uses `CreateInstallationClient(ctx, installation_id)` to mint
short-lived installation-scoped tokens on demand. Each org that
installs the app gets its own `installation_id` and its own token
scope, but they all derive from the same shared app definition and
the same private key.

Permission grants on the app are fixed at the app's definition
level. Org admins accept-or-reject those permissions at install
time, but they can't selectively grant a subset.

### Observation 2 — rate limits are already per-installation

`internal/checker/sweep.go:39` —
`RateLimitRemaining(ctx context.Context, installationID int64) (remaining, limit int, err error)`
keys explicitly on the installation ID. The `RATE_LIMIT_RESERVE` gate
(`sweep.go:175-189`) blocks per-installation when the budget runs low.

GitHub assigns rate-limit buckets per installation:

- Free / Pro / Team installations: 5,000/hr floor per installation,
  scales with users / repos via documented formula.
- Enterprise Cloud installations: 15,000/hr per installation.

If "repo-guardian" is installed at org A (`installation_id=1`), org
B (`installation_id=2`), and org C (`installation_id=3`), each gets
its own bucket. They do NOT share. So the most common reason
operators imagine wanting per-org apps — rate limit isolation — is
already a property of the current design.

### Observation 3 — the four real motivations

| Motivation | Real benefit? | Notes |
|---|---|---|
| Rate limit isolation | **No (already handled)** | Per-installation buckets. Multi-app doesn't help. |
| Different permission grants per org | Yes, if orgs have heterogeneous security postures | App permissions are baked into the app definition. One org grants Contents:Write, another grants Contents:Read — only possible with per-org apps. |
| Per-org audit trail / branding | Yes, for governance-heavy environments | `repo-guardian-acme` vs `repo-guardian-globex` show up as different actors in audit logs and PR commit attribution. |
| Federated ownership / key rotation | Yes, if central team doesn't want to gate every org's credential lifecycle | Each org rotates its own private key independently. |
| Multi-enterprise deployments | **Yes — strongest driver** | You **cannot** install one GitHub App across two separate enterprises. Apps live within an enterprise/account boundary. Multi-tenant SaaS or M&A scenarios force this. |

### Observation 4 — implementation cost sketch

| Concern | Today | Multi-app shape |
|---|---|---|
| Config | `GITHUB_APP_ID` + `GITHUB_PRIVATE_KEY` + `WEBHOOK_SECRET` | `apps: { acme: {app_id, private_key, webhook_secret}, globex: {...} }` map. New HCL block or new env-var convention. |
| Client construction | One `ghinstallation.NewAppsTransport` at startup | Pool of transports indexed by org. New `internal/github.Pool` interface that the engine + reconcilers consume in place of the singleton client. |
| Job dispatch | `CreateInstallationClient(installation_id)` on the single app | Resolve `org → app credentials` first, then mint installation token via the right app. Requires knowing which app owns a given installation_id — probably a reverse-index built at startup by calling `ListInstallations` on each configured app. |
| Webhook validation | One `WEBHOOK_SECRET` validates `X-Hub-Signature-256` | N secrets. Route on `X-GitHub-Hook-Installation-Target-ID` (the app ID) to pick the correct secret **before** HMAC validation. Get this wrong and there's a signature-forgery vector. |
| Webhook endpoint | One URL | Same single URL works — but discrimination happens at the handler layer using request headers, not at the network layer. Operators can still expose `/webhook` only. |
| Helm chart | One secret manifest (`secrets.privateKey`, `secrets.webhookSecret`) | A `secrets.apps[]` list or N existing-secret refs. Chart `_helpers.tpl` already has the per-backend dispatch pattern from IMPL-0011; same pattern applies here. |

The trickiest piece is **webhook secret routing**. GitHub sends
`X-GitHub-Hook-Installation-Target-ID` (the app ID) and
`X-GitHub-Hook-Installation-Target-Type=integration` headers on App
webhooks. The handler must read these headers, look up the
corresponding secret, **then** validate the HMAC signature. The
existing handler does HMAC validation as the first gate; multi-app
inverts that order, which deserves a security review.

Migration path is additive: keep `GITHUB_APP_ID` / `GITHUB_PRIVATE_KEY`
/ `WEBHOOK_SECRET` as a "default app" fallback, treat the `apps:`
map as overrides per org. Operators not using the feature see no
behavior change.

## Conclusion

**Answer:** Technically feasible, not currently warranted.

The single-app model already provides per-org isolation for the one
property most people assume requires multi-app: rate limits. The
remaining motivations (per-org permissions, branding, federated
ownership, multi-enterprise) are all real but conditional on
deployment shapes repo-guardian doesn't have today.

Implementation cost is non-trivial — particularly the webhook-secret
routing inversion, which has a security dimension that needs careful
review.

## Recommendation

**Defer.** Do not build. Re-open this investigation only when a
trigger below appears in real operator demand.

If/when that happens, the follow-up artifact is a **DESIGN doc**
covering:

1. Config schema (HCL map vs env-var convention).
2. `internal/github.Pool` interface and how the engine threads it.
3. Webhook-secret routing algorithm using
   `X-GitHub-Hook-Installation-Target-ID`, including the security
   review of inverting the HMAC-validation ordering.
4. Helm chart shape (likely mirroring the IMPL-0011 backend-mode
   helper pattern).
5. Backwards compatibility — additive `apps:` map with the existing
   env vars as a default-app fallback.
6. Migration recipe for operators going from single-app to multi-app.

## Triggers that would re-open this

Concrete scenarios that would change the calculation:

- A real org joins the deployment with a meaningfully different
  permission posture (e.g., wants Contents:Read only).
- Audit / compliance feedback names "single shared bot identity
  across orgs" as a finding.
- The central platform team is overloaded with key rotation
  requests from individual orgs.
- repo-guardian is asked to span **two separate GitHub
  enterprises** (the strongest trigger — this is a hard
  requirement, not a nice-to-have).
- A future multi-tenant SaaS framing for repo-guardian.

Absent any of those, the analysis above suggests staying with the
single-app model.

## References

- [INV-0005](0005-stale-prs-when-file-rules-become-satisfied-on-main.md)
  — investigation that surfaced this question during operator
  review.
- [INV-0002](0002-multi-org-and-forgejo-support-for-repo-guardian.md)
  — earlier multi-org thinking; predates the current single-app
  ghinstallation model. Worth re-reading if this investigation
  re-opens.
- [DESIGN-0002](../design/0002-github-api-rate-limit-handling.md)
  — rate-limit handling design; reaffirms per-installation gating.
- `internal/config/config.go` —
  `GITHUB_APP_ID` / `GITHUB_PRIVATE_KEY` / `WEBHOOK_SECRET` env
  bindings; the natural extension point for an `apps:` map.
- `internal/github/client.go` — `ghinstallation.AppsTransport`
  construction and `CreateInstallationClient`; the natural seam
  for a `Pool` abstraction.
- `internal/webhook/handler.go` — HMAC validation gate; the spot
  that would need the routing inversion in a multi-app world.
