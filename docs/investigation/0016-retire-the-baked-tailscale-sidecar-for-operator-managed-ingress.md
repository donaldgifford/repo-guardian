---
id: INV-0016
title: "Retire the baked Tailscale sidecar for operator-managed ingress"
status: Open
author: Donald Gifford
created: 2026-08-14
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0016: Retire the baked Tailscale sidecar for operator-managed ingress

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-08-14

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: the Tailscale surface is chart-only](#observation-1-the-tailscale-surface-is-chart-only)
  - [Observation 2: the binary is already ingress-agnostic](#observation-2-the-binary-is-already-ingress-agnostic)
  - [Observation 3: the sidecar path disables the IP-allowlist layer](#observation-3-the-sidecar-path-disables-the-ip-allowlist-layer)
  - [Observation 4: the sidecar is unversioned and unaudited](#observation-4-the-sidecar-is-unversioned-and-unaudited)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Can every Tailscale-specific surface in the chart and code be deleted
outright — sidecar container, serve-config ConfigMap, RBAC, values
block, and the env fork it drives — with ingress instead *documented*
as two operator-owned paths:

1. the **Tailscale Kubernetes operator** for the Talos homelab
   (dev/test), and
2. **AWS Load Balancer Controller with the Gateway API** for EKS
   (production),

without losing any capability the sidecar provides today and without
any binary change?

## Hypothesis

Yes, and the removal is a security *improvement*, not a trade. Three
reasons, each grounded in the inventory below:

- The binary is already ingress-agnostic: `TRUST_PROXY_HEADERS` and the
  webhook IP allowlist are generic L7-proxy support that an ALB needs
  just as much as a Funnel does. Nothing in Go knows Tailscale exists.
- The sidecar is pure chart machinery — a second container, its state
  volume, a serve-config ConfigMap, and RBAC — that the chart must
  version, patch, and reason about, duplicating exactly what the
  Tailscale operator exists to own.
- Enabling the sidecar today **hard-forces the IP allowlist to
  fail-open**, so the "two-layer defense" from SECURITY.md is really
  one layer on the Funnel path. Behind an ALB (or the operator's
  ingress proxy) the allowlist can run fail-closed as designed.

## Context

**Triggered by:** operator request, post-IMPL-0023 (v1.13.0 / chart
1.0.0-rc.12).

The sidecar dates from the project's earliest webhook-testing needs:
INV-0001 validated Tailscale Funnel as a way to receive GitHub webhooks
without public ingress, and DESIGN-0003 turned that into the baked
sidecar the chart still ships. Both predate the Helm chart being the
only supported deployment surface (IMPL-0014) and predate any
production EKS target. The deployment story is now split — Talos
homelab for dev/test, EKS for production — and neither is served well
by a hand-rolled sidecar: the homelab wants the Tailscale operator
(which owns auth-key lifecycle, state, and upgrades), and EKS wants
native AWS ingress (ALB via the Load Balancer Controller), where a
Funnel would be an anti-pattern.

The direction also matches the chart's own precedent: IMPL-0016
removed the baked memory backends in favor of the real thing; this
removes a baked network path in favor of the platform's real ingress.

## Approach

1. ~~Inventory every Tailscale reference in code and chart, with the
   blast radius of each.~~ Done — Findings below.
2. Verify the **Tailscale operator** covers the sidecar's one real
   feature (public HTTPS ingress to the webhook port via Funnel):
   confirm the operator's `Ingress`-class-`tailscale` +
   Funnel-annotation path (or `ProxyGroup`/`Connector` equivalent)
   serves a plain HTTP backend on the tailnet with `AllowFunnel`, and
   what it does about client IPs in `X-Forwarded-For`. Do this against
   current operator docs, not memory.
3. Verify **AWS Load Balancer Controller + Gateway API** maturity: the
   controller's Gateway API implementation (L7 `Gateway`/`HTTPRoute` →
   ALB) went through beta during 2025 — pin the exact controller
   version and GA status at write-time, and whether `HTTPRoute` +
   target-group binding covers the webhook path with TLS termination
   at the ALB. If L7 Gateway support is not yet GA, document the
   stock `Ingress`-class-`alb` path as the interim and note the
   Gateway API upgrade as a values-only change.
4. Map the **allowlist interaction per path**: what sets
   `X-Forwarded-For`, whether the hop chain is trustworthy, and what
   `webhookIPAllowlist.{failOpen,trustProxyHeaders}` should be for
   each. The goal state is fail-closed on both paths, with the
   fail-open forcing removed from the chart entirely.
5. Draft the removal + docs plan: which chart objects go, what the
   values-schema rejection message says (the IMPL-0016
   `deprecatedBackends` pattern), what `docs/operations/ingress.md`
   (or similar) must contain for each path, and which historical docs
   (INV-0001, DESIGN-0003) get superseded pointers. Decide the chart
   version bump — removing `tailscale.*` values is breaking for
   anyone with `tailscale.enabled=true`.

## Environment

| Component | Version / Value |
|-----------|----------------|
| Chart | `1.0.0-rc.12` |
| appVersion | `1.13.0` |
| helm | 4.2.2 |
| Dev/test target | Talos k8s (homelab, tailnet-connected) |
| Production target | EKS |
| Sidecar image today | `ghcr.io/tailscale/tailscale:latest` (unpinned) |

## Findings

### Observation 1: the Tailscale surface is chart-only

The complete inventory, verified by sweep on 2026-08-14:

| Surface | Location | What it is |
|---|---|---|
| Values block | `values.yaml` `tailscale:` (~15 lines) | `enabled`, `image`, `hostname`, `userspace`, `authKeySecret`, `rbac.create` |
| Sidecar container | `templates/deployment.yaml` (~25 lines) | `TS_AUTHKEY`/`TS_HOSTNAME`/`TS_STATE_DIR`/`TS_USERSPACE`/`TS_SERVE_CONFIG` env + mounts |
| Env fork | `templates/deployment.yaml` | `tailscale.enabled` overrides the `webhookIPAllowlist.*` values — see Observation 3 |
| Volumes | `templates/deployment.yaml` | `tailscale-state` emptyDir + serve-config ConfigMap mount |
| Serve config | `templates/tailscale-configmap.yaml` (30 lines) | Funnel on `:443` → proxy to `127.0.0.1:<port>`, `AllowFunnel: true` |
| RBAC | `templates/tailscale-rbac.yaml` (29 lines) | Role/RoleBinding for tailscale state Secrets |
| Chart tests | `tests/deployment_test.yaml` | sidecar render assertions |
| Docs | `SECURITY.md`, `docs/operations/ent-setup.md`, chart README, E4 panel description | prose references |

Notably absent: `values.schema.json` has **no** `tailscale` entry — the
block was never schema-validated, so a typo'd `tailscale.enable=true`
already silently deploys nothing. The auth-key Secret is
operator-supplied (`tailscale-auth`), so no Secret template is
involved.

### Observation 2: the binary is already ingress-agnostic

`grep -ri tailscale internal/ cmd/` matches **only comments** (the E4
dashboard's webhook-panel description and doc references). The two
runtime mechanisms the sidecar path leans on are both generic:

- `TRUST_PROXY_HEADERS` — reads `X-Forwarded-For` when the direct peer
  is a proxy. An ALB sets the same header; so does the Tailscale
  operator's ingress proxy. Nothing to remove, nothing to add.
- The webhook IP allowlist — validates the *effective* client IP
  against GitHub's published hook ranges. Works identically behind any
  proxy that forwards the real client IP.

So the investigation's "without any binary change" clause holds by
construction; the entire diff is chart + docs.

### Observation 3: the sidecar path disables the IP-allowlist layer

`templates/deployment.yaml:103-113`: when `tailscale.enabled=true` the
chart ignores `webhookIPAllowlist.failOpen` and `trustProxyHeaders`
and hard-sets `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN=true` +
`TRUST_PROXY_HEADERS=true`. The two-layer webhook defense (IP
allowlist → HMAC) documented in SECURITY.md is therefore a single
layer (HMAC only, in practice) on exactly the deployment shape that
exposes the webhook to the public internet via Funnel. Replacing the
Funnel with ALB (or operator ingress) whose forwarded client IP is
GitHub's real source address lets the allowlist run fail-closed — the
removal strengthens the posture rather than trading it away. Whether
the *Tailscale operator* path can also run fail-closed depends on step
2's `X-Forwarded-For` verification.

### Observation 4: the sidecar is unversioned and unaudited

`tailscale.image` defaults to `ghcr.io/tailscale/tailscale:latest` —
unpinned, no renovate annotation, invisible to the image-scanning that
covers the main container, and mutable under every pod restart. The
operator-managed replacement moves that supply-chain surface to a
component with its own upgrade lifecycle, which is where it belongs.

## Conclusion

**Answer:** pending — steps 2–4 of the approach (operator capability
verification, LBC Gateway API maturity, per-path allowlist mapping)
are outstanding. The local half of the hypothesis is confirmed by
Observations 1–4: the surface is chart-only, the binary needs no
change, and two of the four findings are affirmative arguments for
removal rather than neutral inventory.

## Recommendation

Pending the conclusion. Expected shape if the hypothesis holds: a
short DESIGN covering the chart removal (breaking — values-schema
rejection for `tailscale.*` with a migration pointer, the IMPL-0016
`deprecatedBackends` message pattern), a new
`docs/operations/ingress.md` with the two documented paths and their
`webhookIPAllowlist` settings, supersession notes on INV-0001 and
DESIGN-0003, and the chart version bump.

## References

- [INV-0001](0001-tailscale-funnel-for-webhook-testing.md) — the Funnel's origin (webhook testing)
- [DESIGN-0003](../design/0003-tailscale-integration-research.md) — the sidecar's design
- [DESIGN-0004](../design/0004-github-webhook-ip-allowlist-middleware.md) — the allowlist the sidecar path bypasses
- `SECURITY.md` §Reverse proxies — the two-layer webhook defense
- [IMPL-0016](../impl/0016-deprecate-memory-backend.md) — precedent: remove the baked thing, document the real thing
- Tailscale Kubernetes operator: <https://tailscale.com/kb/1236/kubernetes-operator>
- Tailscale operator Ingress + Funnel: <https://tailscale.com/kb/1439/kubernetes-operator-cluster-ingress>
- AWS Load Balancer Controller: <https://kubernetes-sigs.github.io/aws-load-balancer-controller/>
- Gateway API: <https://gateway-api.sigs.k8s.io/>
