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
  - [Observation 6: the Tailscale operator has no native Gateway API support, and Funnel is Ingress-class-only](#observation-6-the-tailscale-operator-has-no-native-gateway-api-support-and-funnel-is-ingress-class-only)
  - [Observation 7: AWS LBC Gateway API support is GA](#observation-7-aws-lbc-gateway-api-support-is-ga)
  - [Observation 8: the ngrok operator is Gateway API-native with edge IP restrictions](#observation-8-the-ngrok-operator-is-gateway-api-native-with-edge-ip-restrictions)
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
2. ~~Verify the **Tailscale operator** covers the sidecar's one real
   feature (public HTTPS ingress to the webhook port via Funnel) —
   specifically via the **Gateway API**, because that is what the
   homelab runs: confirm the operator's GatewayClass +
   `Gateway`/`HTTPRoute` support and its maturity, that it can front a
   plain HTTP backend with Funnel enabled, and what it does about
   client IPs in `X-Forwarded-For`. If the operator's Gateway API
   support is missing or immature, document the
   `Ingress`-class-`tailscale` + Funnel-annotation path as the
   interim, mirroring step 3's fallback shape.~~ Done against current
   docs 2026-08-15 — Observation 6. One remainder: the operator docs
   are silent on forwarded-header behavior; verify `X-Forwarded-For`
   empirically during the migration smoke.
3. ~~Verify **AWS Load Balancer Controller + Gateway API** maturity:
   the controller's Gateway API implementation (L7
   `Gateway`/`HTTPRoute` → ALB) went through beta during 2025 — pin
   the exact controller version and GA status at write-time, and
   whether `HTTPRoute` + target-group binding covers the webhook path
   with TLS termination at the ALB. If L7 Gateway support is not yet
   GA, document the stock `Ingress`-class-`alb` path as the
   interim.~~ Done 2026-08-15 — Observation 7: GA, no interim path
   needed.
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
| HCL attrs | `webhook_ip_allowlist`, `webhook_ip_allowlist_fail_open`, `trust_proxy_headers` on `guardian {}` | strict decode (INV-0010) means removal makes existing configs **fail load**. Decided 2026-08-15: straight removal with a migration note — the loud load failure is exactly what strict mode is for; no deprecation shim. Note the asymmetry: a stale raw env var on the Deployment is silently ignored, only the HCL and chart-values surfaces fail loudly |
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

### Observation 6: the Tailscale operator has no native Gateway API support, and Funnel is Ingress-class-only

Verified against current Tailscale docs on 2026-08-15:

- **No native Gateway API.** The operator exposes workloads via
  `Ingress` (`ingressClassName: tailscale`, L7) and `Service`
  (`loadBalancerClass: tailscale` or `tailscale.com/expose`, L3).
  Gateway API support is an open feature request
  ([tailscale/tailscale#10656](https://github.com/tailscale/tailscale/issues/10656),
  open since Dec 2023). Tailscale's official Gateway API story is a
  BYOD pattern — Envoy Gateway (or similar) implements
  `Gateway`/`HTTPRoute`, and the operator merely provisions the
  gateway's LoadBalancer Service onto the tailnet — and that pattern
  is **tailnet-only**: no public exposure, no Funnel.
- **Funnel — the sidecar's one real feature — rides only the
  `Ingress` class.** Public exposure requires `ingressClassName:
  tailscale` + the `tailscale.com/funnel: "true"` annotation, plus a
  tailnet-policy `nodeAttrs` grant of the `funnel` attribute to the
  operator's proxy tag (`tag:k8s` by default — `autogroup:member`
  does not cover tagged devices). HA is available via
  `tailscale.com/proxy-group`.
- **Consequence for the homelab:** the cluster's Gateway API setup
  carries everything *except* this route. The webhook's public path
  is one small, dedicated `Ingress` object alongside the Gateway API
  resources — or ngrok when just testing. This is an operator-side
  deployment fact to document in `docs/operations/ingress.md`, not a
  chart concern.
- **No source-IP restriction on Funnel.** The `funnel` nodeAttr
  governs which *nodes may enable* Funnel, not which public clients
  may connect; the docs offer no public-side IP filtering. So the
  Funnel path is HMAC-only — exactly today's effective posture, since
  the sidecar already forces the allowlist fail-open (Observation 3).
  No regression, but `ingress.md` must say it plainly: on Funnel, the
  edge-enforcement layer is "none"; use the ALB or ngrok path when a
  source-IP layer is required.
- **Forwarded headers are undocumented** for the operator's proxy on
  the Funnel path. Client-IP visibility must be verified empirically
  during the migration smoke; nothing in the app depends on it
  post-removal (the allowlist was the only `X-Forwarded-For`
  consumer), so this affects log/telemetry fidelity only.
- **Chaining Funnel through the cluster's Gateway is possible but
  buys little.** The Funnel `Ingress` can point its backend at the
  `cilium-gateway-*` Service the Gateway controller creates, so
  routing logic stays in `HTTPRoute`s — but the tailscale `Ingress`
  object still exists (Funnel requires it), the path gains a hop, and
  the source-IP posture is unchanged (still none). Known Cilium
  interop gotchas if chained on Talos: the L7 `Ingress` path avoids
  the socket-LB DNAT bypass problem that bites L4
  `loadBalancerClass` exposure, and `bpf.hostLegacyRouting=true` has
  been needed where Talos's `forwardKubeDNSToHost` breaks eBPF host
  routing. For a single webhook route, pointing the Funnel `Ingress`
  directly at the webhook Service is the simpler recommendation.

### Observation 7: AWS LBC Gateway API support is GA

Verified 2026-08-15. AWS announced general availability of Gateway
API support in the Load Balancer Controller (2026-03): L7
(`ALBGatewayAPI`) provisions ALBs from `Gateway`/`HTTPRoute` (and
`GRPCRoute`) with controller **v2.14.0+**, built against Gateway API
v1.3.0; L4 (`NLBGatewayAPI`) covers `TCPRoute`/`UDPRoute`/`TLSRoute`.
TLS termination at the ALB with static or hostname-discovered
certificates is supported, and AWS-vended CRDs
(`LoadBalancerConfiguration`, `TargetGroupConfiguration`,
`ListenerRuleConfiguration`) carry the ALB-specific knobs — including
the security-group attachment where the GitHub prefix-list rule
lives. So the EKS path is exactly the Question's shape with no
interim: `HTTPRoute` → ALB, SG rule referencing a customer-managed
prefix list built from `api.github.com/meta` `hooks`, fail-closed at
the true source address. (Step-6 detail: the meta `hooks` CIDRs
change occasionally; the prefix-list refresh needs an owner —
scheduled Lambda/Terraform or a documented manual check.)

### Observation 8: the ngrok operator is Gateway API-native with edge IP restrictions

Verified 2026-08-15, prompted by Observation 6's Funnel gaps. The
ngrok Kubernetes Operator (the successor to their ingress controller)
implements **both `Ingress` and the Gateway API**, translating them
into ngrok cloud/agent endpoints. Traffic Policy attaches to Gateway
API resources via the `NgrokTrafficPolicy` CRD and the `extensionRef`
filter, and the `restrict-ips` action runs in the `on_tcp_connect`
phase — the connection is denied at the TCP layer, before any HTTP
exchange, at ngrok's edge.

That combination is exactly what the Tailscale path cannot offer:
**public webhook ingress declared as Gateway API resources, with a
fail-closed source-IP allowlist (GitHub's `hooks` CIDRs) enforced at
the edge.** Which means ngrok is not just the testing path — it is a
candidate *first-class* homelab ingress for the webhook route, and
the DESIGN should weigh three homelab options rather than assuming
Funnel:

| Option | Gateway API? | Source-IP layer | Notes |
|---|---|---|---|
| Funnel `Ingress` → webhook Service | no (dedicated `Ingress`) | none (HMAC-only) | zero new components; posture equals today's |
| Funnel `Ingress` → Cilium Gateway chain | routing yes, exposure no | none (HMAC-only) | extra hop; interop gotchas (Obs. 6) |
| ngrok operator + `Gateway`/`HTTPRoute` + `restrict-ips` | yes | fail-closed at ngrok's edge | new operator + external dependency; plan gating below |

Same CIDR-freshness caveat as the ALB prefix list: the allowlist in
the `NgrokTrafficPolicy` is a static copy of `api.github.com/meta`
`hooks` and needs a refresh owner.

**Plan gating, verified against ngrok's own pages 2026-08-15** (the
two official sources disagree in places; both cited in References):

- **The Kubernetes Operator itself is free on every plan** — the
  free-plan-limits doc lists it under "Features included for free on
  all plans." Gateway API support is part of the operator, not a
  plan line item.
- **Traffic Policy is ambiguous on Free.** The pricing page's plan
  grid shows Traffic Policy only from Pay-as-you-go up ($20/mo base,
  metered at $0.10 per 100k Traffic Policy Units); the
  free-plan-limits doc, however, lists "Traffic policy rules (per
  policy): 5" as a *free-tier limit*, implying some Traffic Policy
  works on Free. Whether `restrict-ips` specifically runs on Free is
  unverified — one empirical check (attach the policy on a free
  account) settles it. Note the 5-rule cap is not a blocker either
  way: the whole GitHub allowlist is a single `restrict-ips` rule
  with multiple CIDRs.
- **Free-tier operational limits bite regardless:** 1 GB / 20k HTTP
  requests per month, no reserved or custom domains (only one
  auto-assigned dev domain), and an interstitial on browser HTML
  traffic (API/webhook POSTs are unaffected — consistent with our
  testing history). A GitHub App needs a *stable* webhook URL, and
  reserved/custom domains are paid.
- **Practical read:** for testing, Free keeps working exactly as we
  have always used it. As a *first-class* homelab ingress with the
  fail-closed allowlist and a stable domain, budget Pay-as-you-go
  (~$20/mo base; Traffic Policy Units are negligible at webhook
  volume). The DESIGN's homelab decision is therefore: is the
  fail-closed IP layer + Gateway API-native declaration worth
  ~$20/mo over the free-but-HMAC-only Funnel path?

## Conclusion

**Answer:** yes, with one asterisk on the homelab path. The
hypothesis is confirmed by Observations 1–7: the Tailscale surface is
chart-only, the middleware's blast radius is fully mapped and the
middleware is spoofable on every proxied path, the EKS path is fully
served by the now-GA LBC Gateway API implementation, and the
Tailscale operator covers the Funnel capability — but via its
`Ingress` class, not Gateway API (which the operator does not
natively implement). The homelab therefore keeps one dedicated
`Ingress`-class-`tailscale` + Funnel object for the webhook route
alongside its Gateway API resources, and the Funnel path's
edge-enforcement layer is honestly "none" (HMAC-only), which is
already today's effective posture. Observation 8 adds a third homelab
option that closes even that gap: the ngrok operator is Gateway
API-native and can enforce a fail-closed GitHub-CIDR allowlist at its
edge, making it a candidate first-class webhook ingress rather than
only the testing path. Remaining before the DESIGN: empirical
`X-Forwarded-For` verification on the Funnel path (telemetry fidelity
only), one empirical ngrok check (does `restrict-ips` run on the Free
plan — Observation 8's plan-gating note), the homelab option pick
from Observation 8's table, and CIDR-refresh ownership for whichever
static allowlists exist (ALB prefix list, ngrok policy).

## Recommendation

Pending the conclusion. Expected shape if the hypothesis holds: a
DESIGN covering (a) the chart removal — breaking, values-schema
rejection for `tailscale.*` and `webhookIPAllowlist.*` with a
migration pointer, the IMPL-0016 `deprecatedBackends` message
pattern; (b) the middleware removal — HMAC becomes the sole app-layer
defense, the three config knobs go (HCL strict-decode migration note
required), the orphaned metric/alert and E4 matcher are dispositioned
per Observation 5; (c) a new `docs/operations/ingress.md` with the
three documented paths — Tailscale operator (`Ingress`-class +
Funnel for the webhook route; the cluster's Gateway API carries
everything else, per Observation 6), AWS LBC + Gateway API
(`HTTPRoute` → ALB + SG prefix-list rule, per Observation 7), and
ngrok for testing — each path's edge-layer source-IP enforcement
recipe (including "none — HMAC-only" stated plainly for Funnel), and
the OTEL signals that replace the deleted allowlist telemetry; and
(d) supersession notes on INV-0001, DESIGN-0003, and DESIGN-0004,
plus the chart version bump.

## References

- [INV-0001](0001-tailscale-funnel-for-webhook-testing.md) — the Funnel's origin (webhook testing)
- [DESIGN-0003](../design/0003-tailscale-integration-research.md) — the sidecar's design
- [DESIGN-0004](../design/0004-github-webhook-ip-allowlist-middleware.md) — the allowlist middleware now slated for removal
- `SECURITY.md` §Reverse proxies — the two-layer webhook defense
- [IMPL-0016](../impl/0016-deprecate-memory-backend.md) — precedent: remove the baked thing, document the real thing
- Tailscale Kubernetes operator: <https://tailscale.com/kb/1236/kubernetes-operator>
- Tailscale operator cluster ingress (Ingress class): <https://tailscale.com/kb/1439/kubernetes-operator-cluster-ingress>
- Tailscale operator Funnel exposure: <https://tailscale.com/docs/kubernetes-operator/ingress/expose-workload-to-internet>
- Tailscale BYOD Gateway API pattern (tailnet-only, Envoy Gateway): <https://tailscale.com/docs/solutions/kubernetes-operator-byod-gateway-api>
- Operator Gateway API feature request: <https://github.com/tailscale/tailscale/issues/10656>
- AWS Load Balancer Controller: <https://kubernetes-sigs.github.io/aws-load-balancer-controller/>
- LBC Gateway API guide: <https://kubernetes-sigs.github.io/aws-load-balancer-controller/latest/guide/gateway/gateway/>
- LBC Gateway API GA announcement (2026-03): <https://aws.amazon.com/blogs/networking-and-content-delivery/aws-load-balancer-controller-adds-general-availability-support-for-kubernetes-gateway-api/>
- Gateway API: <https://gateway-api.sigs.k8s.io/>
- GitHub hook IP ranges (`hooks` key): <https://api.github.com/meta>
- ngrok IP restrictions (Traffic Policy): <https://ngrok.com/docs/traffic-policy/actions/restrict-ips/>
- ngrok Kubernetes operator Gateway API support: <https://ngrok.com/blog/introducing-support-for-kubernetes-gateway-api-in-ngrok-kubernetes-operator>
- ngrok Traffic Policy on Gateway API (`NgrokTrafficPolicy` + `extensionRef`): <https://ngrok.com/blog/policy-support-in-gateway-api>
- ngrok k8s IP-restriction guide: <https://ngrok.com/docs/k8s/guides/how-to/restrict-ips>
- ngrok pricing (plan grid): <https://ngrok.com/pricing>
- ngrok free-plan limits (operator free on all plans; 5 traffic-policy rules): <https://ngrok.com/docs/pricing-limits/free-plan-limits>
- Cilium Gateway API: <https://docs.cilium.io/en/latest/network/servicemesh/gateway-api/gateway-api/>
