# Webhook ingress

repo-guardian owns no ingress. As of chart `1.0.0` / appVersion
`1.14.0` (IMPL-0024, DESIGN-0023) the chart ships no Tailscale
sidecar and the binary ships no IP-allowlist middleware — the app
listens on a plain HTTP port, validates every webhook delivery's
HMAC signature, and everything in front of that port belongs to you.
This document is the options matrix for that front layer, with a
recipe per option.

The app-layer security boundary is the **HMAC signature**
(`X-Hub-Signature-256`, validated against `GITHUB_WEBHOOK_SECRET`).
Source-IP enforcement, where you want it, happens at your edge:
an ALB security group, a Cloudflare WAF rule, an ngrok traffic
policy. The in-app allowlist that used to exist was spoofable
behind every documented proxy topology — see
[INV-0016](../investigation/0016-retire-the-baked-tailscale-sidecar-for-operator-managed-ingress.md)
Observation 5 — so an edge layer is not a downgrade from it; it is
the first time the source-IP layer has actually been fail-closed.

## Migrating from the baked sidecar

Upgrading to chart `1.0.0` from any `1.0.0-rc.*` release that used
`tailscale.*` or `webhookIPAllowlist.*`:

1. **Stand up your chosen ingress option first** (pick from the
   matrix below). The sidecar path dies with the upgrade — for a
   homelab on Funnel that means the dedicated Tailscale `Ingress`
   (or the cloudflared sidecar patch) must be receiving deliveries
   *before* you upgrade, then repoint the GitHub App's webhook URL.
2. **Delete `tailscale.*` and `webhookIPAllowlist.*` from your
   values.** Leaving either in place fails the render loudly:
   `values.schema.json` rejects the key naming this document, and
   the `validateRemovedValues` helper backs it up on
   schema-skipping render paths.
3. **Delete the three `guardian.hcl` attributes** if you set them:
   `webhook_ip_allowlist`, `webhook_ip_allowlist_fail_open`,
   `trust_proxy_headers`. The guardian block's strict decode fails
   policy load (startup) if they are still present.
4. **Delete any stale env patches** setting `WEBHOOK_IP_ALLOWLIST`,
   `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN`, or `TRUST_PROXY_HEADERS`. Note
   the asymmetry: removed *HCL attributes* fail loudly at load, but
   removed *env vars* are silently ignored (12-factor env plumbing
   has no strict mode) — the binary logs one startup `slog.Warn`
   naming the stale vars and this document, and that is all. Clean
   them up so future readers don't think they do something.
5. **Expect one full-fleet re-enqueue after the upgrade.** Removing
   the `GuardianConfig` fields changes the policy-version hash
   exactly once, so every `repo_state` row goes drifted and the
   next stale sweep re-checks the fleet. Harmless and expected —
   the same class of burst as any policy edit, not a bug.
6. **Webhook deliveries missed during the cutover are recovered by
   the stale sweep** — the system tolerates missed webhooks by
   design (INV-0016 Observation 9). For anything urgent, GitHub's
   *Recent Deliveries* view on the App settings page can redeliver
   individual events.

Rollback: re-deploy the previous chart+binary pair and restore the
removed values/attributes. No data migration in either direction.

## Options matrix

| Option | Environment fit | K8s routing API | Source-IP enforcement | Cost | Validated |
|---|---|---|---|---|---|
| AWS LBC + Gateway API (`HTTPRoute` → ALB) | EKS / production | Gateway API | SG + GitHub prefix list, fail-closed | AWS | ☐ |
| Tailscale operator Funnel | homelab / tailnet clusters | `Ingress` (class `tailscale`) | none — HMAC-only, stated plainly | free | ☐ |
| Cloudflare Tunnel, operator-injected cloudflared sidecar | homelab / any cluster | none (edge → pod) | CF WAF allowlist, fail-closed | free + owned domain | ☐ |
| Cloudflare Tunnel via community operator (cfgate / lexfrei) | homelab / any cluster | Gateway API | CF WAF allowlist, fail-closed | free + owned domain | ☐ |
| Cloudflare Tunnel via STRRL controller | homelab / any cluster | `Ingress` (class `cloudflare-tunnel`) | CF WAF allowlist, fail-closed | free + owned domain | ☐ |
| ngrok Kubernetes operator | homelab / any cluster | Gateway API + `Ingress` | `restrict-ips` Traffic Policy, fail-closed | ~$20/mo | ☐ |
| ngrok CLI / `cloudflared` quick tunnel against localhost | local dev (`make run-local` + docker-compose dev services) | n/a | none (ngrok policy is paid) | free | ☑ ngrok; ☐ quick tunnel |

There is no crowned winner. The homelab trade is roughly: Funnel is
free and zero-new-components but HMAC-only; ngrok gives Gateway API
+ a fail-closed edge allowlist for ~$20/mo; Cloudflare Tunnel gives
the same capability set free with a bring-your-own domain, trading
the official operator for community ones (or none, with the sidecar
patch). Production on EKS is the ALB row.

Every static-allowlist row shares one caveat: **the GitHub `hooks`
CIDRs (`https://api.github.com/meta`) change occasionally, and the
copy in your SG prefix list / Cloudflare list / ngrok policy needs a
refresh owner** — a scheduled Lambda/Terraform run or a documented
manual check. Each recipe notes where that copy lives.

## AWS LBC + Gateway API (EKS)

GA since LBC v2.14.0 (Gateway API v1.3.0): `Gateway`/`HTTPRoute`
provision an ALB directly, with AWS-vended CRDs
(`LoadBalancerConfiguration`, `TargetGroupConfiguration`,
`ListenerRuleConfiguration`) carrying the ALB-specific knobs.

- **Recipe:** an `HTTPRoute` for the webhook path targeting the
  repo-guardian Service; a `LoadBalancerConfiguration` attaching a
  security group whose ingress rule references a customer-managed
  **prefix list** built from the `hooks` CIDRs in
  `api.github.com/meta`. Default-deny otherwise. TLS terminates at
  the ALB (static or hostname-discovered certificate).
- **Source-IP enforcement:** the SG rule — enforced at the true
  source address, before any HTTP exchange, fail-closed.
- **X-Forwarded-For:** the ALB appends the real client IP; ALB
  access logs are the client-IP evidence. Nothing in the app reads
  the header anymore.
- **CIDR refresh:** the prefix list is the static copy; give it an
  owner (Terraform/Lambda on a schedule, or a runbook check).
- **Observability:** ALB access logs + target-group 5xx/health
  metrics on the edge side; `http_server_request_duration_seconds`
  (webhook route) and `webhook_rejected_total{reason="signature"}`
  on the app side answer "is traffic arriving / being rejected".

## Tailscale operator Funnel (homelab)

The operator has **no native Gateway API support**
([tailscale/tailscale#10656](https://github.com/tailscale/tailscale/issues/10656));
Funnel — public exposure — rides only the `Ingress` class. If the
cluster otherwise routes through Gateway API, the webhook's public
path is one small, dedicated `Ingress` alongside it. Pointing that
`Ingress` at the webhook Service directly is simpler than chaining
through the cluster's Gateway (the chain adds a hop and changes
nothing about the posture).

- **Recipe:** an `Ingress` with `ingressClassName: tailscale` and
  the `tailscale.com/funnel: "true"` annotation, backend = the
  repo-guardian Service. The tailnet policy must grant the `funnel`
  nodeAttr to the operator's proxy tag (`tag:k8s` by default —
  `autogroup:member` does not cover tagged devices). HA via
  `tailscale.com/proxy-group`.
- **Source-IP enforcement: none.** The `funnel` nodeAttr governs
  which *nodes may enable* Funnel, not which public clients may
  connect; there is no public-side IP filtering. On this path the
  posture is **HMAC-only** — which is exactly what the old baked
  sidecar delivered too, since it forced the allowlist fail-open.
  Use the ALB, ngrok, or Cloudflare rows when a fail-closed
  source-IP layer is required.
- **X-Forwarded-For:** undocumented for the operator's proxy on the
  Funnel path — verify empirically during smoke (checkbox item).
  Affects log fidelity only; nothing in the app depends on it.
- **Observability:** no edge-side request log; the app-side OTEL
  series are the whole story on this path.

## Cloudflare Tunnel — operator-injected cloudflared sidecar

The sidecar topology without the baggage that got the baked sidecar
removed: **you** inject it via a kustomize patch on the rendered
Deployment (the chart stays ingress-agnostic), and remotely-managed
tunnels keep it stateless — one container, one tunnel-token Secret,
no volume, no ConfigMap, no RBAC. Ingress rules, DNS, and the WAF
allowlist all live in the Cloudflare Terraform provider as code.

- **Recipe:** create a remotely-managed tunnel + DNS route + WAF
  rule in Cloudflare (Terraform or dashboard), put the tunnel token
  in a Secret, and apply this strategic-merge patch on top of the
  rendered chart:

  ```yaml
  # kustomize strategic-merge patch: operator-owned cloudflared sidecar.
  # The tunnel is remotely managed — ingress rules, DNS, and the WAF
  # allowlist live in Cloudflare (Terraform provider), so the sidecar
  # is stateless: no volume, no ConfigMap, no RBAC.
  apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: repo-guardian
  spec:
    template:
      spec:
        containers:
          - name: cloudflared
            # renovate: datasource=docker depName=cloudflare/cloudflared
            image: docker.io/cloudflare/cloudflared:2026.8.0@sha256:0000000000000000000000000000000000000000000000000000000000000000
            args:
              - tunnel
              - --no-autoupdate
              - run
            env:
              - name: TUNNEL_TOKEN
                valueFrom:
                  secretKeyRef:
                    name: cloudflared-tunnel-token
                    key: token
            securityContext:
              runAsNonRoot: true
              readOnlyRootFilesystem: true
              allowPrivilegeEscalation: false
            resources:
              requests:
                cpu: 10m
                memory: 32Mi
              limits:
                memory: 64Mi
  ```

  Replace the digest with the current one for your pinned tag (the
  placeholder digest above will not pull); the renovate comment
  keeps it updated — the supply-chain hygiene the old unpinned
  Tailscale sidecar lacked. Point the tunnel's ingress rule at
  `http://localhost:8080` (the webhook port — edge → sidecar →
  `127.0.0.1`, no ClusterIP hop).
- **HA / rollouts:** per-replica sidecars all register as
  connectors to the *same* tunnel — cloudflared's native HA model.
  A single-replica rollout drops the connector briefly; deliveries
  missed in that window are recovered by the stale sweep.
- **Source-IP enforcement:** one free-plan WAF custom rule scoped
  to the webhook hostname — `ip.src in $github_hooks` against a
  reusable IP List, default-block otherwise. Fail-closed at
  Cloudflare's edge.
- **X-Forwarded-For / evidence:** Cloudflare sets the standard
  headers; the client-IP evidence lives in the Cloudflare dashboard
  (or Logpush).
- **CIDR refresh:** the IP List is the static copy;
  API-updatable, so a scheduled job can own it.
- **Trade:** the route is invisible to the cluster's Gateway API
  resources (edge → pod bypasses `HTTPRoute` entirely). For a
  single app-owned route that is arguably a feature.

## Cloudflare Tunnel — community operators

Cloudflare's own ingress controller is archived; the Kubernetes
integration is community-run:

- **cfgate** — Gateway API-native: `CloudflareTunnel` /
  `CloudflareDNS` / Access CRDs; point a `Gateway` at the tunnel,
  attach `HTTPRoute`s; manages cloudflared pods and DNS.
- **lexfrei/cloudflare-tunnel-gateway-controller** — full
  `HTTPRoute` semantics via an embedded L7 proxy in a forked
  cloudflared; assumes exclusive ownership of the tunnel config.
- **STRRL/cloudflare-tunnel-ingress-controller** — `Ingress`-class
  based (`cloudflare-tunnel`), the established choice.

Source-IP enforcement, evidence, and CIDR refresh are identical to
the sidecar row (same edge). The trade is community-operator
maturity against the sidecar row's zero-operator simplicity — weigh
it if you want the webhook route visible in `HTTPRoute`s.

## ngrok Kubernetes operator

The operator (successor to the ngrok ingress controller) implements
both `Ingress` and the Gateway API. Traffic Policy attaches to
Gateway API resources via the `NgrokTrafficPolicy` CRD and the
`extensionRef` filter; the `restrict-ips` action runs in the
`on_tcp_connect` phase — denied at the TCP layer, at ngrok's edge,
before any HTTP exchange.

- **Recipe:** operator install + `Gateway`/`HTTPRoute` for the
  webhook path + a `NgrokTrafficPolicy` with one `restrict-ips`
  rule carrying the GitHub `hooks` CIDRs (a single rule takes the
  whole list — the free tier's 5-rules-per-policy cap is not a
  factor).
- **Plan gating:** the operator itself is free on every plan.
  A *stable* webhook URL (reserved/custom domain) and — per the
  pricing grid — Traffic Policy are Pay-as-you-go (~$20/mo base;
  policy units are negligible at webhook volume). Free keeps
  working for testing exactly as always.
- **X-Forwarded-For / evidence:** ngrok sets the standard headers;
  the ngrok inspector is the client-IP evidence.
- **CIDR refresh:** the policy document is the static copy; give it
  an owner.
- **Observability:** ngrok's edge dashboard/inspector plus the
  app-side OTEL series.

## Local development: quick tunnels

For `make run-local` (binary + docker-compose Postgres/Valkey):

```bash
ngrok http 8080                  # the habitual path — validated ☑
cloudflared tunnel --url http://localhost:8080   # free, no account (trycloudflare.com) — ☐
```

Point a test App's webhook URL at the printed public URL. No
source-IP layer on either (ngrok's policy is paid; quick tunnels
have none) — HMAC-only, which is fine for a test App with a
throwaway secret.

## Checkbox contract

A **Validated** box in the matrix is checked only after the option
has carried a **real GitHub App webhook delivery end-to-end** —
GitHub → edge → pod → 202, visible in the App's *Recent
Deliveries* view and the app's own metrics — not after the
manifests merely applied cleanly. The ngrok local-dev row is the
only one checked today, from years of testing use. Funnel's
X-Forwarded-For behaviour is part of its validation pass
(empirical, undocumented upstream). When you validate a row, check
the box in this file and note anything surprising in the row's
section.

## Observability after the removal

There is **no in-app client-IP telemetry** (DESIGN-0023 OQ5): the
deleted allowlist was the only consumer of `X-Forwarded-For`, and
otelhttp metrics stay IP-free by design (label cardinality). Each
row above names where its edge's client-IP evidence lives. On the
app side:

- `http_server_request_duration_seconds` (webhook route) — is
  traffic arriving.
- `webhook_rejected_total{reason="signature"}` — deliveries failing
  HMAC validation: a burst means a wrong or rotated webhook secret,
  **not** an unwanted-source problem; that signal lives at your
  edge layer now.
- The E4 evidence dashboard answers "which repository", never
  "which caller".
