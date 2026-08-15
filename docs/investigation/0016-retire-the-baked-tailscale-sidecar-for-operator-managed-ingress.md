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
  - [Observation 5: the allowlist middleware is bypassable behind every documented proxy](#observation-5-the-allowlist-middleware-is-bypassable-behind-every-documented-proxy)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Can every Tailscale-specific surface in the chart and code be deleted
outright — sidecar container, serve-config ConfigMap, RBAC, values
block, and the env fork it drives — with ingress instead *documented*
as operator-owned paths:

1. the **Tailscale Kubernetes operator** for the Talos homelab
   (dev/test),
2. **AWS Load Balancer Controller with the Gateway API** for EKS
   (production), and
3. **ngrok** as the documented webhook-testing path (we have used it
   repeatedly for exactly that),

without losing any capability the sidecar provides today?

And the second half, which follows from the first: once ingress is
explicitly outside the app's scope, should the **webhook IP-allowlist
middleware** (`internal/webhook/allowlist.go`) go with it? Source-IP
enforcement moves to the layer that actually sees the true source
address — an ALB security-group rule against a GitHub prefix list, a
Tailscale ACL, an ngrok IP policy — HMAC remains the app-layer
boundary, and the OTEL instrumentation added in IMPL-0023 is how we
observe whichever edge the operator chose.

## Hypothesis

Yes to both, and the removal is a security *improvement*, not a
trade. Four reasons, each grounded in the inventory below:

- The binary's webhook handling is ingress-agnostic: HMAC validation
  works identically behind any proxy. Nothing in Go knows Tailscale
  exists.
- The sidecar is pure chart machinery — a second container, its state
  volume, a serve-config ConfigMap, and RBAC — that the chart must
  version, patch, and reason about, duplicating exactly what the
  Tailscale operator exists to own.
- Enabling the sidecar today **hard-forces the IP allowlist to
  fail-open**, so the "two-layer defense" from SECURITY.md is really
  one layer on the Funnel path.
- The allowlist middleware itself is **spoofable behind every proxy we
  document** (Observation 5): with `TRUST_PROXY_HEADERS=true` it
  validates the leftmost — client-controlled — `X-Forwarded-For`
  entry. Edge-layer enforcement acts on the true L3/L4 source address
  and cannot be header-spoofed; moving the check there is strictly
  stronger, not a concession.

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
4. Map **edge-layer source-IP enforcement per path**, replacing the
   in-app allowlist: ALB security-group rule referencing a
   customer-managed prefix list built from `api.github.com/meta`
   `hooks` CIDRs (and how that list stays current); the equivalent for
   the Tailscale operator/Funnel path (ACL, or accept that Funnel is
   public and HMAC-only); ngrok's IP-restriction policy for the
   testing path. Verify each against current provider docs, not
   memory.
5. Inventory the **middleware removal blast radius** —
   ~~done, Observation 5~~ — and decide the disposition of each
   dependent: the orphaned `webhook_rejected_total` metric and its
   alert, the E4 log matcher, the three config knobs across env + HCL
   (strict decode makes attribute removal a breaking config change),
   and SECURITY.md's two-layer narrative.
6. Draft the removal + docs plan: which chart objects and binary
   surfaces go, what the values-schema rejection message says (the
   IMPL-0016 `deprecatedBackends` pattern), what
   `docs/operations/ingress.md` (or similar) must contain for each of
   the three paths — including which OTEL/edge signals replace the
   deleted allowlist metric for "someone is knocking" visibility —
   and which historical docs (INV-0001, DESIGN-0003, DESIGN-0004) get
   superseded pointers. Decide the chart version bump — removing
   `tailscale.*` and `webhookIPAllowlist.*` values is breaking for
   anyone setting them.

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

So no binary change is *required* by the sidecar removal itself. The
one binary change now in scope is deliberate and additive to the
question, not forced by it: deleting the allowlist middleware
(Observation 5), which takes `TRUST_PROXY_HEADERS` with it — the
allowlist constructor is that knob's only consumer.

### Observation 3: the sidecar path disables the IP-allowlist layer

`templates/deployment.yaml:103-113`: when `tailscale.enabled=true` the
chart ignores `webhookIPAllowlist.failOpen` and `trustProxyHeaders`
and hard-sets `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN=true` +
`TRUST_PROXY_HEADERS=true`. The two-layer webhook defense (IP
allowlist → HMAC) documented in SECURITY.md is therefore a single
layer (HMAC only, in practice) on exactly the deployment shape that
exposes the webhook to the public internet via Funnel. The chart's
own default path already concedes what Observation 5 then confirms
from the code: the in-app IP layer isn't doing the job, and the layer
that can — the edge — is outside the chart's scope.

### Observation 4: the sidecar is unversioned and unaudited

`tailscale.image` defaults to `ghcr.io/tailscale/tailscale:latest` —
unpinned, no renovate annotation, invisible to the image-scanning that
covers the main container, and mutable under every pod restart. The
operator-managed replacement moves that supply-chain surface to a
component with its own upgrade lifecycle, which is where it belongs.

### Observation 5: the allowlist middleware is bypassable behind every documented proxy

Verified in code on 2026-08-15. `allowlist.go.extractIP` takes the
**leftmost** `X-Forwarded-For` entry when `TRUST_PROXY_HEADERS=true`
(`strings.Cut(xff, ",")` → first element). The leftmost entry is the
one the *client* supplies; proxies **append** the true source address
to the end of an inbound chain rather than stripping it (ALB's
documented behavior, and the common default elsewhere). So behind any
of the three documented paths, a caller who sends
`X-Forwarded-For: 140.82.112.1` presents a GitHub hooks-range IP and
walks through the check. The middleware genuinely enforces only on
direct-exposure deployments — a shape no documented path uses. It is
not a weak second layer; on proxied paths it is not a layer at all.

Edge enforcement does not share this flaw: a security-group rule or
provider ACL matches the L3/L4 source address of the actual
connection, which no header can forge.

Removal blast radius, verified by sweep:

| Surface | Location | Disposition question |
|---|---|---|
| Middleware + meta fetcher | `internal/webhook/allowlist.go` (207 LOC) + `allowlist_test.go` (304 LOC) | delete; also removes the `api.github.com/meta` outbound dependency, its 24h refresh goroutine, and the fail-open/fail-closed semantics |
| Wiring | `cmd/repo-guardian/main.go.wrapWebhookAllowlist` | delete |
| Env knobs | `WEBHOOK_IP_ALLOWLIST`, `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN`, `TRUST_PROXY_HEADERS` (`internal/config`) | delete — the allowlist is `TRUST_PROXY_HEADERS`'s only consumer |
| HCL attrs | `webhook_ip_allowlist`, `webhook_ip_allowlist_fail_open`, `trust_proxy_headers` on `guardian {}` | strict decode (INV-0010) means removal makes existing configs **fail load** — needs a migration note, or a deprecation window |
| Metric | `webhook_rejected_total{reason}` | the allowlist is its **only producer** — the HMAC 401 path increments nothing — so removal orphans it and `RepoGuardianWebhookRejectionsHigh` becomes an alert that cannot fire (the INV-0012 finding-A shape). Its description already overpromises: "signature failures" never reach this counter today |
| E4 dashboard | `dashboard/e4.go` `logRejectedIP` matcher + webhook-panel description | must change in the same PR — `TestLogLines_AreStillEmittedByTheBinary` fails the build if a matched log line stops being emitted (the drift gate working as designed) |
| Chart | `webhookIPAllowlist.*` values + deployment env wiring + the tailscale env fork | delete; values-schema rejection for the removed keys |
| Docs | SECURITY.md two-layer narrative, DESIGN-0004 | rewrite / supersede |

The observability replacement is already in place from IMPL-0023:
otelhttp on the webhook route records status-code-labelled request
metrics, so a 401 spike ("someone is knocking with a bad signature")
is visible without any bespoke counter, and each edge has its own
native telemetry (ALB access logs, Tailscale, the ngrok dashboard).
Whether `webhook_rejected_total` should be deleted outright or
repointed at the HMAC path (reason=`signature`) is a step-6 decision.

## Conclusion

**Answer:** pending — steps 2–4 of the approach (operator capability
verification, LBC Gateway API maturity, per-path edge-enforcement
mapping) are outstanding. The local half of the hypothesis is
confirmed by Observations 1–5: the Tailscale surface is chart-only,
the middleware's blast radius is fully mapped, and three of the five
findings are affirmative arguments for removal rather than neutral
inventory — the sidecar disables the allowlist, the allowlist is
spoofable anyway, and its alert already cannot report what its
description claims.

## Recommendation

Pending the conclusion. Expected shape if the hypothesis holds: a
DESIGN covering (a) the chart removal — breaking, values-schema
rejection for `tailscale.*` and `webhookIPAllowlist.*` with a
migration pointer, the IMPL-0016 `deprecatedBackends` message
pattern; (b) the middleware removal — HMAC becomes the sole app-layer
defense, the three config knobs go (HCL strict-decode migration note
required), the orphaned metric/alert and E4 matcher are dispositioned
per Observation 5; (c) a new `docs/operations/ingress.md` with the
three documented paths (Tailscale operator, AWS LBC + Gateway API,
ngrok for testing), each path's edge-layer source-IP enforcement
recipe, and the OTEL signals that replace the deleted allowlist
telemetry; and (d) supersession notes on INV-0001, DESIGN-0003, and
DESIGN-0004, plus the chart version bump.

## References

- [INV-0001](0001-tailscale-funnel-for-webhook-testing.md) — the Funnel's origin (webhook testing)
- [DESIGN-0003](../design/0003-tailscale-integration-research.md) — the sidecar's design
- [DESIGN-0004](../design/0004-github-webhook-ip-allowlist-middleware.md) — the allowlist middleware now slated for removal
- `SECURITY.md` §Reverse proxies — the two-layer webhook defense
- [IMPL-0016](../impl/0016-deprecate-memory-backend.md) — precedent: remove the baked thing, document the real thing
- Tailscale Kubernetes operator: <https://tailscale.com/kb/1236/kubernetes-operator>
- Tailscale operator Ingress + Funnel: <https://tailscale.com/kb/1439/kubernetes-operator-cluster-ingress>
- AWS Load Balancer Controller: <https://kubernetes-sigs.github.io/aws-load-balancer-controller/>
- Gateway API: <https://gateway-api.sigs.k8s.io/>
- GitHub hook IP ranges (`hooks` key): <https://api.github.com/meta>
- ngrok IP restrictions (Traffic Policy): <https://ngrok.com/docs/traffic-policy/actions/restrict-ips/>
