---
id: DESIGN-0023
title: "Operator-owned ingress: remove the Tailscale sidecar and IP-allowlist middleware"
status: Draft
author: Donald Gifford
created: 2026-08-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0023: Operator-owned ingress: remove the Tailscale sidecar and IP-allowlist middleware

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-08-15

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [D1: Binary — delete the allowlist middleware](#d1-binary--delete-the-allowlist-middleware)
  - [D2: Binary — config-surface removal](#d2-binary--config-surface-removal)
  - [D3: Observability disposition](#d3-observability-disposition)
  - [D4: Chart — delete the Tailscale and allowlist surfaces](#d4-chart--delete-the-tailscale-and-allowlist-surfaces)
  - [D5: Chart — reject removed values at render time](#d5-chart--reject-removed-values-at-render-time)
  - [D6: Documentation — the ingress options matrix](#d6-documentation--the-ingress-options-matrix)
  - [D7: Historical-doc supersession](#d7-historical-doc-supersession)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Remove every Tailscale-specific surface from the Helm chart and the
webhook IP-allowlist middleware from the binary, per INV-0016's
concluded findings: the middleware is spoofable behind every proxy we
document, the sidecar path already forces it fail-open, and source-IP
enforcement belongs at the edge layer that sees the true source
address. Ingress becomes operator-owned and is documented as an
**options matrix** (`docs/operations/ingress.md`) — no crowned winner,
per-option validation checkboxes. HMAC remains the sole app-layer
defense; OTEL (IMPL-0023) observes whichever edge the operator chose.

## Goals and Non-Goals

### Goals

- Delete the chart's `tailscale.*` surface: values block, sidecar
  container, volumes, serve-config ConfigMap, RBAC, env fork, tests.
- Delete the chart's `webhookIPAllowlist.*` values and env wiring.
- Delete `internal/webhook/allowlist.go` (+ test), its `main.go`
  wiring, and the three config knobs across env + HCL
  (`WEBHOOK_IP_ALLOWLIST`, `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN`,
  `TRUST_PROXY_HEADERS`).
- Fail loudly for operators still carrying the removed config:
  HCL strict decode at binary startup, values rejection at chart
  render, both pointing at a migration note.
- Ship `docs/operations/ingress.md` as the seven-row options matrix
  from INV-0016 with per-option recipes and validation checkboxes.
- Rewrite SECURITY.md's webhook trust model; supersede INV-0001,
  DESIGN-0003, DESIGN-0004.
- Keep the E4 evidence tier and alert catalogue truthful — no matcher
  that matches nothing, no alert that cannot fire.

### Non-Goals

- **Picking a homelab ingress winner.** The matrix documents; the
  deployment chooses (INV-0016 Recommendation, decided 2026-08-15).
- **Building any replacement IP filtering in the app.** The entire
  point is that the app stops pretending to do this.
- **Automating CIDR refresh** for the edge allowlists (ALB prefix
  list, Cloudflare list, ngrok policy). The matrix names the refresh
  ownership question per option; automation is per-deployment
  tooling (e.g. Terraform), not repo-guardian scope.
- **Validating matrix rows.** Checkboxes get checked when an option
  is exercised against a real GitHub App delivery, not in this
  change.
- **Removing `repo_guardian_webhook_received_total` or the
  WebhookSilence alert** — webhook liveness is unrelated to ingress
  choice.

## Background

INV-0016 (Concluded 2026-08-15) established:

- The Tailscale surface is **chart-only**; the binary's webhook
  handling is ingress-agnostic (Observation 1–2).
- `tailscale.enabled=true` hard-forces
  `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN=true` + `TRUST_PROXY_HEADERS=true`
  (`deployment.yaml:103-113`), so the SECURITY.md "two-layer defense"
  is one layer on the public-facing path (Observation 3).
- The middleware validates the **leftmost** (client-controlled)
  `X-Forwarded-For` entry (`allowlist.go:154`), making it spoofable
  behind every documented proxy; proxies append the true source to
  the end of the chain (Observation 5).
- `TRUST_PROXY_HEADERS` has exactly one consumer (the allowlist
  constructor); `repo_guardian_webhook_rejected_total` has exactly
  one producer (the allowlist — the HMAC 401 path increments
  nothing) (Observation 5).
- Gateway API reality per path: AWS LBC GA (v2.14.0+); Tailscale
  operator none (Funnel is `Ingress`-class-only); ngrok operator
  native; Cloudflare Tunnel via community operators or an
  operator-injected stateless cloudflared sidecar (Observations
  6–9).

Precedents this design reuses:

- **IMPL-0016** — remove-the-baked-thing pattern: values-schema
  rejection of removed identifiers at render time, binary startup
  error pointing at a migration doc.
- **IMPL-0018** — `_helpers.tpl` render-time `fail` guards
  (`validateBackendSecrets`) for values that are set but meaningless.
- **INV-0010** — `guardian {}` strict decode: removed HCL attributes
  fail load with "Unsupported argument" by construction; the straight
  removal (no deprecation shim) was decided in INV-0016.
- **IMPL-0023** — the generated monitoring tier and its drift gates
  (`make lint-monitoring`, `TestLogLines_AreStillEmittedByTheBinary`),
  which force D3's E4 changes to ship in the same PR as D1.

## Detailed Design

### D1: Binary — delete the allowlist middleware

Deletions, verified against the current tree:

| Surface | Location | Notes |
|---|---|---|
| Middleware + meta fetcher | `internal/webhook/allowlist.go` (207 LOC) | takes the `api.github.com/meta` outbound dependency, the 24h refresh goroutine, and fail-open/fail-closed semantics with it |
| Tests | `internal/webhook/allowlist_test.go` (304 LOC) | |
| Wiring | `cmd/repo-guardian/main.go.wrapWebhookAllowlist` (~line 665) and its call site (~line 188) | the webhook handler mounts directly; no replacement wrapper |

The webhook route keeps its existing otelhttp instrumentation
(OUTERMOST per the IMPL-0022/0023 transport-ordering contract) and
the HMAC validation in `webhook.Handler.ServeHTTP`
(`gh.ValidatePayload` → 401). HMAC is now the only app-layer gate,
which SECURITY.md states plainly (D7).

### D2: Binary — config-surface removal

Three knobs, each with two surfaces (env + HCL):

- `internal/config/config.go`: drop `WebhookIPAllowlist`,
  `WebhookIPAllowlistFailOpen`, `TrustProxyHeaders` fields and their
  `envOrDefaultBool` loads (~lines 63–70, 278–290).
- `internal/policy/types.go`: drop the three `GuardianConfig` fields
  (`webhook_ip_allowlist`, `webhook_ip_allowlist_fail_open`,
  `trust_proxy_headers`).
- `internal/policy/loader.go`: the INV-0010 lockstep rule applies in
  reverse — remove all three spots per attribute or the loader
  breaks: the `guardianBodySchema` entries (~line 378), the
  `setGuardianAttr` cases (~line 429), and the `mergeGuardianConfig`
  carries (~line 1167). Plus the `applyEnvOverrides` lines (~line
  1191) and `internal/policy/defaults.go` (~line 111).

Post-removal behavior (all decided in INV-0016, all intended):

- A `guardian.hcl` still carrying any of the three attributes **fails
  load** with HCL's "Unsupported argument" diagnostic. No deprecation
  shim — the loud failure is what strict mode is for.
- A stale raw env var on a Deployment (e.g. a kustomize patch still
  setting `TRUST_PROXY_HEADERS=true`) is **silently ignored** — env
  removal has no rejection surface. The migration note carries this
  asymmetry.

### D3: Observability disposition

Every consumer of the deleted telemetry, and what happens to it:

- **`metrics.WebhookRejectedTotal`** — sole producer is the deleted
  middleware. Disposition per Open Question 1 (recommendation:
  repoint at the HMAC 401 path with `reason="signature"`, making the
  counter's one remaining reason true and cheap).
- **`RepoGuardianWebhookRejectionsHigh`**
  (`internal/monitoring/alert/alert.go:154`) — exists only in the
  catalogue/generated tier; the chart's PrometheusRule does **not**
  carry it (verified 2026-08-15), so there is no hand-mirrored copy
  to keep in step. Under OQ1(a) the alert survives with its
  `Description` rewritten (the current text promises allowlist 403s
  that will no longer exist and signature 401s that today never
  reach the counter). Under OQ1(b/c) the alert is deleted or
  re-expressed over otelhttp series
  (`http_server_request_duration_seconds_count` by
  `http_response_status_code`, the E3 vocabulary).
- **E4 dashboard** (`internal/monitoring/dashboard/e4.go:33-37`) —
  `logRejectedIP` ("rejected request from non-GitHub IP") and
  `logNoIP` stop being emitted; `webhookRejectedRe` reduces to
  `logInvalidPayload` ("invalid webhook payload"). The webhook
  panel's description (line ~207, which currently explains
  allowlist-vs-signature and `TRUST_PROXY_HEADERS` behind Tailscale)
  is rewritten. `TestLogLines_AreStillEmittedByTheBinary` **fails
  the build** if the matcher edit doesn't ship in the same PR as D1
  — the drift gate working as designed, and the reason D1+D3 are one
  atomic change.
- **`make lint-monitoring`** — `contrib/generated/` (rules.yaml +
  repo-guardian-logs.json) regenerates in the same PR or CI goes
  red.
- **Replacement signal** for "someone is knocking": otelhttp
  status-code metrics on the webhook route (401 rate), E4's
  invalid-payload log line, and each edge's native telemetry (ALB
  access logs, Cloudflare/Tailscale/ngrok dashboards). Documented in
  `ingress.md` (D6).

### D4: Chart — delete the Tailscale and allowlist surfaces

| Surface | Location | Action |
|---|---|---|
| `webhookIPAllowlist:` block | `values.yaml:174-180` | delete |
| `tailscale:` block | `values.yaml:182-196` | delete |
| Env fork + allowlist env | `templates/deployment.yaml:100-113` (`WEBHOOK_IP_ALLOWLIST`, the `tailscale.enabled` fork over `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN`/`TRUST_PROXY_HEADERS`) | delete |
| Sidecar container + volumes | `templates/deployment.yaml` (tailscale container, `tailscale-state` emptyDir, serve-config mount) | delete |
| Serve config | `templates/tailscale-configmap.yaml` | delete file |
| RBAC | `templates/tailscale-rbac.yaml` | delete file |
| Chart tests | `tests/deployment_test.yaml` sidecar assertions | delete; add negative render tests (D5) |
| README | `README.md.gotmpl` → `make helm-docs` | regenerated rows disappear; any prose mentioning Tailscale/allowlist updated in the `.gotmpl`, never the rendered README |

### D5: Chart — reject removed values at render time

Both mechanisms from precedent, layered:

1. **`values.schema.json`** — the IMPL-0016 pattern. Add `tailscale`
   and `webhookIPAllowlist` as explicitly rejected properties (JSON
   Schema `"not": {}` with a `"description"` naming the migration
   doc), so machine validation catches them even outside `helm
   template`. Note INV-0016 Observation 1: `tailscale` never had a
   schema entry at all, so this is the block's first and last
   appearance in the schema.
2. **`_helpers.tpl` `fail` guard** — the IMPL-0018
   `validateBackendSecrets` pattern, included at the top of
   `deployment.yaml`: if `.Values.tailscale` or
   `.Values.webhookIPAllowlist` is present, fail render with a
   message naming the removed block and the migration doc URL.
   Schema violations produce JSON-Schema error walls; the helper
   produces one legible sentence. They fire at different layers
   (`helm lint`/CI vs render), which is why both exist.

Chart version: per Open Question 3 (recommendation: stay in the rc
series).

### D6: Documentation — the ingress options matrix

New `docs/operations/ingress.md`, inheriting INV-0016's matrix
verbatim as its spine:

| Option | Environment fit | K8s routing API | Source-IP enforcement | Cost | Validated |
|---|---|---|---|---|---|
| AWS LBC + Gateway API (`HTTPRoute` → ALB) | EKS / production | Gateway API | SG + GitHub prefix list, fail-closed | AWS | ☐ |
| Tailscale operator Funnel | homelab / tailnet clusters | `Ingress` (class `tailscale`) | none — HMAC-only, stated plainly | free | ☐ |
| Cloudflare Tunnel, operator-injected cloudflared sidecar | homelab / any cluster | none (edge → pod) | CF WAF allowlist, fail-closed | free + owned domain | ☐ |
| Cloudflare Tunnel via community operator (cfgate / lexfrei) | homelab / any cluster | Gateway API | CF WAF allowlist, fail-closed | free + owned domain | ☐ |
| Cloudflare Tunnel via STRRL controller | homelab / any cluster | `Ingress` (class `cloudflare-tunnel`) | CF WAF allowlist, fail-closed | free + owned domain | ☐ |
| ngrok Kubernetes operator | homelab / any cluster | Gateway API + `Ingress` | `restrict-ips` Traffic Policy, fail-closed | ~$20/mo | ☐ |
| ngrok CLI / `cloudflared` quick tunnel against localhost | local dev (`make run-local` + docker-compose dev services) | n/a | none (ngrok policy is paid) | free | ☑ ngrok; ☐ quick tunnel |

Per-option sections carry: the recipe (manifests/annotations/policy
snippets), what sets `X-Forwarded-For` (telemetry-only post-removal),
the source-IP enforcement setup where one exists, the CIDR-refresh
ownership note for static allowlists, and the observability story
(which edge-native signal plus which repo-guardian OTEL series answer
"is traffic arriving / being rejected"). A closing section documents
the checkbox contract: a box is checked only after the option carries
a real GitHub App delivery end-to-end.

### D7: Historical-doc supersession

- **SECURITY.md** — the "two-layer defense" and Tailscale/Funnel
  sections are rewritten: HMAC is the app-layer boundary; source-IP
  enforcement is the operator's edge layer per `ingress.md`; the
  removed middleware's history gets one paragraph pointing at
  INV-0016 (including the spoofability finding, so nobody
  reintroduces it as a "cheap second layer").
- **INV-0001, DESIGN-0003** — superseded banners pointing at
  INV-0016 + this design (docz status untouched; these are
  historical records).
- **DESIGN-0004** — status → Superseded (it designed the middleware
  being deleted).
- **`docs/operations/ent-setup.md`** and the chart's
  `homelab-smoke.md` — Tailscale-sidecar references replaced with a
  pointer to `ingress.md`.

## API / Interface Changes

- **Removed env vars:** `WEBHOOK_IP_ALLOWLIST`,
  `WEBHOOK_IP_ALLOWLIST_FAIL_OPEN`, `TRUST_PROXY_HEADERS` (ignored
  if still set — the asymmetry is documented).
- **Removed HCL attributes** on `guardian {}`:
  `webhook_ip_allowlist`, `webhook_ip_allowlist_fail_open`,
  `trust_proxy_headers` (fail load if still present).
- **Removed chart values:** `tailscale.*`, `webhookIPAllowlist.*`
  (fail render if still present).
- **Metric:** `repo_guardian_webhook_rejected_total{reason}` — per
  OQ1; under (a) the `ip_not_allowed`/`allowlist_unavailable` reasons
  disappear and `signature` appears.
- **No Go interface changes.** `github.Client`, `Store`, `Queue`,
  the reconcilers, and the webhook `Handler` signature are untouched;
  no mock regeneration needed.

## Data Model

None. No store schema, queue payload, or policy-version-relevant
changes — `policy.Version` hashes the policy struct, and removing
`GuardianConfig` fields changes the hash exactly once at upgrade,
which re-enqueues the fleet on the next sweep: harmless and expected
(same class as any policy edit; note it in the migration doc so the
post-upgrade sweep burst isn't mistaken for a bug).

## Testing Strategy

- **Loader regression:** a test asserting that a `guardian {}` block
  containing `webhook_ip_allowlist = true` fails `Load` with
  "Unsupported argument" — pinning the intended breakage so a future
  schema addition can't silently resurrect the attribute. Verify
  non-vacuously (add the attribute back to the schema, watch the
  test fail, remove it).
- **Chart negative tests:** helm-unittest cases asserting render
  failure for `tailscale.enabled=true` and
  `webhookIPAllowlist.enabled=true` with the migration message
  (extend `tests/values_guard_test.yaml`, the IMPL-0018 suite), plus
  removal of the sidecar assertions from `deployment_test.yaml`.
- **Monitoring drift gates (existing, load-bearing):**
  `TestLogLines_AreStillEmittedByTheBinary` forces the E4 matcher
  edit; `make lint-monitoring` forces `contrib/generated/`
  regeneration; `make lint-alerts-contrib` re-parses the generated
  rules. No new machinery needed — the design leans on gates
  IMPL-0023 already built.
- **OQ1(a) case:** if the counter is repointed, a handler test
  asserting the 401 path increments
  `webhook_rejected_total{reason="signature"}` exactly once
  (`testutil.ToFloat64`), and the webhook 202 contract tests keep
  passing unchanged.
- **Render sweep:** `helm template` output diffed for exactly the
  expected deletions (no sidecar container, no allowlist env, no
  tailscale volumes/ConfigMap/RBAC) — the PR #67 namespace-stamping
  check pattern.

## Migration / Rollout Plan

1. **One release, both halves.** Binary and chart ship together
   (per OQ4 recommendation): appVersion bump `minor`, chart bump per
   OQ3. Shipping the chart removal against an old binary would leave
   the allowlist enabled-by-default with no values to disable it;
   shipping the binary first would strand chart values that render
   env vars the binary ignores. Atomic is simpler than either skew.
2. **Operator steps, in the migration note** (placement per OQ2):
   remove `tailscale.*` and `webhookIPAllowlist.*` from values;
   remove the three `guardian.hcl` attributes; delete any stale env
   patches (silently ignored, but confusing to future readers);
   stand up the chosen `ingress.md` option **before** upgrading (the
   old sidecar path dies with the upgrade — for the homelab that
   means the Funnel `Ingress` or cloudflared sidecar must be
   receiving deliveries first).
3. **Failure modes are loud and immediate:** stale values → render
   failure with the migration message; stale HCL → startup failure
   with "Unsupported argument". Both precede any traffic impact.
4. **Post-upgrade expectations, documented:** one full-fleet
   re-enqueue from the policy-hash change (Data Model note); webhook
   delivery gap during cutover recovered by the stale sweep
   (INV-0016 Observation 9 — the system tolerates missed webhooks by
   design, but GitHub's recent-deliveries view is the place to
   redeliver anything urgent).
5. **Rollback:** re-deploy the previous chart+binary pair and
   restore the removed values/attrs. No data migration in either
   direction.

## Open Questions

Format: (a) is the recommendation; pick a letter or write in
**other**.

**1. Disposition of `webhook_rejected_total` and its alert.**
The deleted middleware is the counter's only producer today; the
HMAC 401 path increments nothing (INV-0016 Observation 5).

- (a) **Repoint the counter at the HMAC path** — increment
  `webhook_rejected_total{reason="signature"}` on the 401 branch of
  `webhook.Handler.ServeHTTP`. One line of producer; the
  purpose-built low-cardinality counter and the
  `RepoGuardianWebhookRejectionsHigh` alert survive with a truthful
  description (which today promises signature visibility that
  doesn't exist). E4 keeps a bespoke rejection series to graph next
  to the invalid-payload log line.
- (b) Delete the counter and alert; re-express rejection monitoring
  over otelhttp (`http_server_request_duration_seconds_count` by
  `http_response_status_code`, the E3 vocabulary). One less bespoke
  metric, but the alert loses the `reason` label and inherits
  otel-semconv naming risk, and 401-rate-on-one-route is a clumsier
  alert expression than a purpose-built counter.
- (c) Delete the counter and alert outright; rejected webhooks are
  visible only in E4's invalid-payload log line and edge telemetry.
  Smallest surface, but a silent-HMAC-failure regression (wrong
  secret after a rotation) would page nobody.
- other: ____

**2. Where the migration note lives.**

- (a) **A section in `docs/operations/ingress.md` itself** —
  "Migrating from the baked sidecar" at the top of the doc every
  affected operator must read anyway; the chart guard message and
  startup-error pointer both name one URL. One doc owns ingress
  past, present, and future.
- (b) Extend `docs/operations/migrations.md` (the IMPL-0016 memory-
  backend precedent lives there), keeping all breaking-change notes
  in one file at the cost of splitting ingress reading across two
  docs.
- (c) A standalone `docs/operations/ingress-migration.md`,
  mirroring `template-migration.md`/`annotation-properties-migration.md`
  precedent — cleanest separation, one more file to eventually
  tombstone.
- other: ____

**3. Chart version for the breaking change.**

- (a) **Stay in the rc series (`1.0.0-rc.13`)** — the chart has
  never shipped a stable 1.0.0; rc semantics permit breaking
  changes, and every prior breaking rc (IMPL-0016's backend
  removal, IMPL-0018) did exactly this. The migration note and
  render guard carry the signal.
- (b) Jump to `2.0.0-rc.1` to signal loudly in the version number
  itself — honest semver-major optics, but it majors a chart whose
  1.0.0 never existed and breaks the rc narrative for the eventual
  stable cut.
- (c) Cut `1.0.0` stable first, then `2.0.0-rc.1` with this change —
  maximal semver correctness, two releases of ceremony for an
  audience of one deployment fleet.
- other: ____

**4. Sequencing of the removal.**

- (a) **One atomic PR** — binary + chart + docs + generated
  monitoring together. The drift gates effectively force D1+D3
  atomicity already; splitting chart from binary creates the skew
  states described in Migration step 1, and the whole diff is
  mostly deletions (~500 LOC Go, ~100 lines chart, plus docs).
- (b) Two PRs: binary first (middleware + knobs + monitoring), chart
  + docs second — smaller reviews, but the intermediate state ships
  a binary that ignores env vars the current chart still renders,
  and both PRs need the same migration doc.
- (c) Three phases IMPL-style (binary / chart / docs) — matches the
  IMPL-0014 cleanup shape, but this change is a fraction of that
  size and the docs are load-bearing for the other two phases'
  failure messages.
- other: ____

**5. Does anything replace the client-IP telemetry?**
Post-removal, nothing in the app reads `X-Forwarded-For`; the
`RemoteAddr` behind every documented edge is a proxy/sidecar
address. INV-0016 flagged Funnel's forwarded-header behavior as
unverified.

- (a) **Nothing in-app; document per-edge.** otelhttp does not
  record client IPs in metrics (cardinality), request logs at the
  edge (ALB access logs, CF, ngrok inspector) are strictly better
  evidence, and the one consumer that ever cared is being deleted
  for cause. `ingress.md` notes where each edge's client-IP evidence
  lives, and the Funnel XFF question stays an empirical checkbox
  item on that row.
- (b) Log the leftmost XFF value (clearly labeled untrusted) on the
  webhook handler's Warn paths only — cheap forensic breadcrumb on
  rejected requests, at the cost of resurrecting a header read this
  design just finished calling spoofable, in the exact place a
  future reader might mistake it for enforcement.
- other: ____

**6. Does the sidecar image reference survive anywhere as a worked
example?**
INV-0016 Observation 9 makes the operator-injected cloudflared
sidecar a first-class matrix row, and the homelab consumes the chart
via kustomize+helm in ArgoCD.

- (a) **Yes — ship a complete kustomize strategic-merge patch example
  in `ingress.md`** for the cloudflared sidecar row (container,
  token Secret reference, pinned+renovate-annotated image), so the
  first operator to validate that row starts from a copy-pasteable
  patch that already has the supply-chain hygiene the old sidecar
  lacked. The chart itself stays ingress-free.
- (b) No example manifests in `ingress.md` — links to vendor docs
  only. Less to maintain, but every operator re-derives the same
  patch and the hygiene lessons (pinned image, no `:latest`) don't
  carry.
- other: ____

## References

- [INV-0016](../investigation/0016-retire-the-baked-tailscale-sidecar-for-operator-managed-ingress.md) — the concluded investigation this design executes (Observations 1–9, options matrix, straight-removal decision)
- [DESIGN-0004](0004-github-webhook-ip-allowlist-middleware.md) — the middleware being removed (to be marked Superseded)
- [DESIGN-0003](0003-tailscale-integration-research.md) — the sidecar being removed (supersession pointer)
- [INV-0001](../investigation/0001-tailscale-funnel-for-webhook-testing.md) — the Funnel origin (supersession pointer)
- [IMPL-0016](../impl/0016-deprecate-memory-backend.md) — the remove-the-baked-thing precedent (schema rejection + migration pointer)
- IMPL-0018 — `_helpers.tpl` `fail`-guard precedent (`validateBackendSecrets`)
- `SECURITY.md` — the two-layer webhook narrative being rewritten
- `internal/monitoring/dashboard/e4.go` / `internal/monitoring/alert/alert.go` — the E4 matchers and `RepoGuardianWebhookRejectionsHigh` catalogue entry (D3)
- Provider references: see INV-0016's References section (Tailscale operator/Funnel, AWS LBC Gateway API GA, ngrok operator + Traffic Policy, Cloudflare Tunnel + WAF, cfgate/lexfrei/STRRL controllers)
