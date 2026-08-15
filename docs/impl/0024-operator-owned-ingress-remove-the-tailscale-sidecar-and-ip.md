---
id: IMPL-0024
title: "Operator-owned ingress: remove the Tailscale sidecar and IP-allowlist middleware"
status: Draft
author: Donald Gifford
created: 2026-08-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0024: Operator-owned ingress: remove the Tailscale sidecar and IP-allowlist middleware

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-08-15

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Pre-implementation audit (2026-08-15)](#pre-implementation-audit-2026-08-15)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Binary — repoint the counter, delete the middleware, keep the drift gates green](#phase-1-binary--repoint-the-counter-delete-the-middleware-keep-the-drift-gates-green)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Chart — delete the surfaces, guard the removed values, cut 1.0.0](#phase-2-chart--delete-the-surfaces-guard-the-removed-values-cut-100)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Docs — ingress matrix, trust-model rewrite, supersession sweep](#phase-3-docs--ingress-matrix-trust-model-rewrite-supersession-sweep)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Ship — atomic PR, release verification](#phase-4-ship--atomic-pr-release-verification)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Execute DESIGN-0023: delete the chart's Tailscale sidecar surface and
the binary's webhook IP-allowlist middleware, fail loudly on stale
config at chart render and binary startup, repoint
`webhook_rejected_total` at the HMAC path, and ship
`docs/operations/ingress.md` as the options matrix. One atomic PR
(DESIGN-0023 OQ4 = a); appVersion `minor`; chart **`1.0.0` stable**
(OQ3).

**Implements:** DESIGN-0023 (from INV-0016, Concluded)

## Scope

### In Scope

- Binary: `internal/webhook/allowlist.go` (+test) deletion, `main.go`
  wiring, three config knobs across env + HCL, metric repoint
  (`reason="signature"`), alert description rewrite, E4 matcher
  reduction, generated-monitoring regeneration.
- Chart: `tailscale.*` + `webhookIPAllowlist.*` removal, render
  guards (schema + `_helpers.tpl`), tests, examples, NOTES.txt,
  README regeneration, `version: 1.0.0` / `appVersion: "1.14.0"`.
- Docs: `docs/operations/ingress.md` (matrix + migration section +
  recipes), SECURITY.md rewrite, `policy-reference.md` attr removal,
  supersession sweep, CLAUDE.md updates.

### Out of Scope

- Validating any matrix row (checkboxes are checked by later
  operator work, not this PR).
- CIDR-refresh automation for edge allowlists.
- Any change to webhook liveness telemetry
  (`webhook_received_total`, WebhookSilence alert).
- Go interface changes / mock regeneration (none needed — verified:
  no `github.Client`/`Store`/`Queue` surface moves).

## Pre-implementation audit (2026-08-15)

Code audit against DESIGN-0023's assumptions, per the standing
pre-IMPL pattern. Confirmations and deltas the design didn't name:

- **Go files touching the three knobs** (design named 5; audit found
  10): `cmd/repo-guardian/main.go`, `internal/config/config.go` +
  `config_test.go`, `internal/monitoring/dashboard/e4.go`,
  `internal/policy/{types,loader,defaults}.go` **+ all three test
  files** (`types_test.go`, `loader_test.go`, `defaults_test.go`).
- **`templates/NOTES.txt`** has a `tailscale.enabled` block (webhook
  URL + `tailscale status` command) the design missed — delete and
  replace with an `ingress.md` pointer.
- **`values.schema.json` contains NEITHER block today** — the schema
  tolerates unknown keys, so D5's rejection entries are additions,
  not edits.
- **Both example values files carry `webhookIPAllowlist:` blocks**
  (`examples/values-with-policy.yaml:153`,
  `values-multi-org.yaml:100`) and `examples/examples_test.go`
  exercises them — they would fail the new render guard if not
  stripped in the same PR.
- **`docs/usage/policy-reference.md`** documents all three guardian
  attrs (lines ~69–71) and the env-mapping rows (~637–638).
- **E4** (`e4.go:33-39`): `logRejectedIP`, `logNoIP` fold out of
  `webhookRejectedRe`; `logInvalidPayload` and `logEnqueueFailed`
  (and `webhookIncidentsRe`) survive; the comment block above the
  consts describes the two-layer split and needs rewriting.
- **The chart's PrometheusRule does not carry
  `RepoGuardianWebhookRejectionsHigh`** — catalogue/generated tier
  only; no hand-mirror to keep in step.
- **docz `design` statuses have no `Superseded`** (Draft / In Review
  / Approved / Implemented / Abandoned) → Open Question 1.
- **GHCR has no published chart `1.0.0`** (verified via anonymous
  `helm show chart`); ECR check is a Phase 4 task.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all
its tasks are checked off and its success criteria are met. Tasks
are ordered so the tree stays green (`make lint && make test`) after
every task — in particular 1.2 bundles the deletion with the E4
matcher edit because `TestLogLines_AreStillEmittedByTheBinary` fails
the moment a matched log line stops being emitted.

---

### Phase 1: Binary — repoint the counter, delete the middleware, keep the drift gates green

Removes the middleware and its config surface while keeping every
IMPL-0023 drift gate green at each step. The counter gains its new
producer *before* losing its old one, so
`repo_guardian_webhook_rejected_total` never has zero producers on
any commit.

#### Tasks

- [x] 1.1 Repoint `webhook_rejected_total`: increment
  `metrics.WebhookRejectedTotal.WithLabelValues("signature")` on the
  401 branch of `webhook.Handler.ServeHTTP`
  (`internal/webhook/handler.go:86-92`); update the metric's help
  text in `internal/metrics/metrics.go:177-180` ("rejected by IP
  allowlist" → "rejected (signature validation)"); handler test
  asserting exactly-once increment via `testutil.ToFloat64` and that
  the 202 contract tests still pass.
- [x] 1.2 Delete `internal/webhook/allowlist.go` and
  `allowlist_test.go`; remove `wrapWebhookAllowlist` and its call
  site from `cmd/repo-guardian/main.go` (~665, ~188 — handler mounts
  directly, otelhttp stays OUTERMOST); in the same commit reduce E4:
  drop `logRejectedIP`/`logNoIP` from `e4.go`, reduce
  `webhookRejectedRe` to `logInvalidPayload`, rewrite the const
  comment block and the webhook panel description (~207, currently
  explains allowlist-vs-signature + `TRUST_PROXY_HEADERS` behind
  Tailscale — the panel itself survives, OQ3 = a); run
  `make monitoring-generate` and commit
  `contrib/generated/` in the same commit.
- [x] 1.3 Rewrite `RepoGuardianWebhookRejectionsHigh`'s
  `Description` (`internal/monitoring/alert/alert.go:154` — the
  current text promises allowlist 403s that no longer exist);
  regenerate; `make lint-monitoring lint-alerts-generated` green.
- [x] 1.4 Remove the env knobs from `internal/config/config.go`
  (`WebhookIPAllowlist`, `WebhookIPAllowlistFailOpen`,
  `TrustProxyHeaders` fields; `envOrDefaultBool` loads ~278-290);
  update `config_test.go`.
- [ ] 1.5 Remove the HCL attrs — the INV-0010 lockstep in reverse,
  all three spots per attribute: `guardianBodySchema` (~378),
  `setGuardianAttr` (~429), `mergeGuardianConfig` (~1167), plus
  `applyEnvOverrides` (~1191) and `defaults.go` (~111); update
  `types.go`, `types_test.go`, `loader_test.go`, `defaults_test.go`.
- [ ] 1.6 Add the loader regression test: a `guardian {}` block
  containing `webhook_ip_allowlist = true` fails `Load` with
  "Unsupported argument". **Verify non-vacuously**: re-add the
  attribute to `guardianBodySchema`, watch the test fail, remove it
  (back up first per standing practice).
- [ ] 1.7 (OQ2 = a) startup `slog.Warn` when any of the three
  removed env vars is still set, naming
  `docs/operations/ingress.md`; test with `t.Setenv` (no
  `t.Parallel`).

#### Success Criteria

- `grep -rn "WebhookIPAllowlist\|TrustProxyHeaders\|WEBHOOK_IP_ALLOWLIST\|TRUST_PROXY_HEADERS\|webhook_ip_allowlist\|trust_proxy_headers" internal/ cmd/ --include='*.go'`
  returns only the 1.7 warn strings and nothing else.
- `make ci` green; `TestLogLines_AreStillEmittedByTheBinary`,
  `make lint-monitoring`, `make lint-alerts-generated` all green.
- The loader regression test exists and has been proven non-vacuous.
- `webhook_rejected_total{reason="signature"}` increments on a
  bad-signature POST (handler test) and no other reason value is
  producible.

---

### Phase 2: Chart — delete the surfaces, guard the removed values, cut 1.0.0

The chart half of the atomic change. Guards land before version
bump so the negative tests exercise the final message text.

#### Tasks

- [ ] 2.1 `values.yaml`: delete the `webhookIPAllowlist:` block
  (~174-180) and the `tailscale:` block (~182-196).
- [ ] 2.2 `templates/deployment.yaml`: delete the
  `WEBHOOK_IP_ALLOWLIST` env, the `tailscale.enabled` env fork
  (~100-113), the tailscale sidecar container, the
  `tailscale-state` emptyDir + serve-config volumes/mounts.
- [ ] 2.3 Delete `templates/tailscale-configmap.yaml` and
  `templates/tailscale-rbac.yaml`; remove the `tailscale.enabled`
  block from `templates/NOTES.txt` (webhook-URL guidance now points
  at `docs/operations/ingress.md`).
- [ ] 2.4 `_helpers.tpl`: add a `repo-guardian.validateRemovedValues`
  guard (IMPL-0018 `validateBackendSecrets` pattern, included at the
  top of `deployment.yaml`): if `.Values.tailscale` or
  `.Values.webhookIPAllowlist` is present, `fail` with the removed
  block's name and the migration URL
  (`docs/operations/ingress.md#migrating-from-the-baked-sidecar`).
- [ ] 2.5 `values.schema.json`: add explicit rejection entries
  (`"not": {}` with `"description"` naming the migration doc) for
  `tailscale` and `webhookIPAllowlist` — first appearance of either
  key in the schema (audit note).
- [ ] 2.6 Tests: remove the 4 tailscale assertions from
  `tests/deployment_test.yaml`; add negative cases to
  `tests/values_guard_test.yaml` (`tailscale.enabled=true` and
  `webhookIPAllowlist.enabled=true` each fail render with the
  migration message) plus a positive default-values render case.
- [ ] 2.7 Strip the `webhookIPAllowlist:` blocks from
  `examples/values-with-policy.yaml` (~153) and
  `examples/values-multi-org.yaml` (~100); `examples_test.go` green.
- [ ] 2.8 `Chart.yaml`: `version: 1.0.0`, `appVersion: "1.14.0"`
  (next minor after 1.13.0 — re-verify at PR time per the IMPL-0017
  appVersion-vs-tag lesson); `make helm-docs` (README regenerates
  from `.gotmpl` — never edit the rendered README).

#### Success Criteria

- `helm template` with default values renders no tailscale
  artifacts, no allowlist env vars, and every resource carries
  `namespace: {{ .Release.Namespace }}` (PR #67 sweep:
  `helm template ... | grep -E '^kind:|^  namespace:'`).
- helm-unittest fully green including the new negative guard cases;
  the guard message names the migration URL (asserted, not assumed).
- `make lint-alerts-chart` green (chart PrometheusRule untouched but
  the gate re-verified).
- `grep -rin "tailscale\|webhookIPAllowlist" charts/repo-guardian/`
  matches only `CHANGELOG.md` (generated history) and the guard
  strings in `_helpers.tpl`/`values.schema.json`.

---

### Phase 3: Docs — ingress matrix, trust-model rewrite, supersession sweep

Everything the failure messages point at must exist before the PR
merges — the guards from Phases 1–2 reference `ingress.md`, so this
phase is load-bearing, not cleanup.

#### Tasks

- [ ] 3.1 Create `docs/operations/ingress.md`: "Migrating from the
  baked sidecar" section first (DESIGN-0023 OQ2 = a — operator
  steps, loud-failure explanations, env-var silent-ignore
  asymmetry, post-upgrade policy-hash re-enqueue note, webhook-gap
  recovery via stale sweep); the seven-row options matrix verbatim
  from DESIGN-0023 D6; per-option recipe sections including the
  complete kustomize strategic-merge patch for the cloudflared
  sidecar (pinned digest + renovate-style comment, tunnel-token
  Secret ref — OQ6 = a) and each row's source-IP setup,
  X-Forwarded-For note, CIDR-refresh ownership, and observability
  story; closing checkbox contract.
- [ ] 3.2 Rewrite SECURITY.md's webhook sections: HMAC is the
  app-layer boundary; source-IP enforcement is operator-owned per
  `ingress.md`; one history paragraph pointing at INV-0016 with the
  spoofability finding (so the middleware doesn't get reintroduced
  as a "cheap second layer").
- [ ] 3.3 `docs/usage/policy-reference.md`: remove the three
  guardian-attr rows (~69-71) and the two env-mapping rows
  (~637-638).
- [ ] 3.4 Sweep the remaining live docs: `docs/operations/
  ent-setup.md`, `docs/index.md`, `docs/README.md`,
  `contrib/README.md` — replace Tailscale/allowlist references with
  `ingress.md` pointers (historical design/impl docs stay
  untouched).
- [ ] 3.5 Supersession (OQ1 = a): add `Superseded` to the design
  statuses in `.docz.yaml`; set DESIGN-0004 and DESIGN-0003 to
  `Superseded` with a banner naming DESIGN-0023; INV-0001 gets a
  banner pointing at INV-0016 (status stays Concluded); `docz
  update design` / `docz update inv`.
- [ ] 3.6 CLAUDE.md: remove the "Webhook IP allowlist" and
  "Tailscale Funnel" key-design-pattern bullets, update the
  `webhook/` architecture line (drop "IP allowlist middleware"),
  and add a short entry recording this change's contracts (HMAC
  sole app layer; `reason="signature"`; removed knobs fail
  load/render).
- [ ] 3.7 `docz update impl` + `docz wiki update`; `make
  lint-docs`-equivalent checks if present (yamllint/markdownlint
  via existing make targets).

#### Success Criteria

- Repo-wide `grep -ril tailscale` outside `docs/{design,impl,investigation,rfc,adr}/`
  and chart CHANGELOG matches only `ingress.md` and SECURITY.md
  (both intentional).
- Every URL emitted by a Phase 1–2 failure path resolves to a real
  heading in `ingress.md`.
- mkdocs nav includes `ingress.md`; docz indexes regenerated
  cleanly.

---

### Phase 4: Ship — atomic PR, release verification

The release-side checklist, including the two items that fail
*silently* if skipped (post-mortem discipline).

#### Tasks

- [ ] 4.1 Full local gate: `make ci`, `helm-unittest`, `make
  lint-monitoring lint-alerts-generated lint-alerts-chart`; confirm no
  mock regeneration needed (no interface diffs in `git diff
  --stat`).
- [ ] 4.2 **Verify no chart `1.0.0` exists in ECR** (GHCR already
  verified clean 2026-08-15): `helm show chart oci://<ECR>/
  repo-guardian-chart --version 1.0.0` (or `aws ecr
  describe-images`) — required because the publish workflow's `helm
  pull` idempotency precheck *silently skips* publishing over an
  existing version.
- [ ] 4.3 Re-verify `appVersion` against the tag the `minor` label
  will actually cut (IMPL-0017 lesson) — if a release landed since,
  adjust `appVersion` before merge.
- [ ] 4.4 Open the single atomic PR with the `minor` label; body
  carries the migration summary and the breaking-change callouts.
- [ ] 4.5 Post-merge: confirm the push-triggered `release.yml` run
  **creates jobs** (startup_failure is silent — post-mortem), then
  v1.14.0 tag exists, chart `1.0.0` published to GHCR **and** ECR,
  both signed with provenance attached.
- [ ] 4.6 Flip DESIGN-0023 → Implemented, IMPL-0024 → Completed;
  `docz update design` / `docz update impl`.

#### Success Criteria

- Release run created jobs and completed; v1.14.0 tag on the merge
  commit; chart `1.0.0` pullable from both registries; cosign
  verification passes.
- A `helm upgrade` with old values fails render with the migration
  message; a binary started against an old `guardian.hcl` exits
  with "Unsupported argument" — both confirmed once against real
  artifacts, not only unit tests.
- Docs statuses flipped; indexes regenerated.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/webhook/allowlist.go` / `allowlist_test.go` | Delete | middleware + meta fetcher + tests (511 LOC) |
| `internal/webhook/handler.go` (+ test) | Modify | 401 branch increments `webhook_rejected_total{reason="signature"}` |
| `internal/metrics/metrics.go` | Modify | counter help text |
| `cmd/repo-guardian/main.go` | Modify | drop `wrapWebhookAllowlist` + call site; add removed-env-var startup warn (OQ2 = a) |
| `internal/config/config.go` (+ test) | Modify | drop three env knobs |
| `internal/policy/{types,loader,defaults}.go` (+ 3 tests) | Modify | drop three HCL attrs (lockstep ×3 spots each) + regression test |
| `internal/monitoring/dashboard/e4.go` | Modify | matcher reduction + panel description |
| `internal/monitoring/alert/alert.go` | Modify | WebhookRejectionsHigh description |
| `contrib/generated/**` | Regenerate | `make monitoring-generate` |
| `charts/repo-guardian/values.yaml`, `values.schema.json`, `Chart.yaml` | Modify | blocks deleted; rejections added; `1.0.0` / `1.14.0` |
| `charts/repo-guardian/templates/{deployment.yaml,NOTES.txt,_helpers.tpl}` | Modify | env fork + sidecar + volumes out; guard in |
| `charts/repo-guardian/templates/tailscale-{configmap,rbac}.yaml` | Delete | 59 lines |
| `charts/repo-guardian/tests/{deployment_test,values_guard_test}.yaml` | Modify | assertions out; negative guards in |
| `examples/values-{with-policy,multi-org}.yaml` | Modify | strip `webhookIPAllowlist:` blocks |
| `docs/operations/ingress.md` | Create | migration section + options matrix + recipes |
| `SECURITY.md`, `docs/usage/policy-reference.md`, `docs/operations/ent-setup.md`, `docs/index.md`, `docs/README.md`, `contrib/README.md`, `CLAUDE.md` | Modify | trust-model rewrite + reference sweep |
| `docs/design/0003-*`, `0004-*`, `docs/investigation/0001-*` | Modify | supersession (per OQ1) |
| `.docz.yaml` | Modify | add `Superseded` to design statuses (OQ1 = a) |

## Testing Plan

- [ ] Handler test: bad signature → 401 + exactly-once
  `reason="signature"` increment (`testutil.ToFloat64`); 202
  contract untouched.
- [ ] Loader regression: stale attr → "Unsupported argument",
  proven non-vacuous by temporary schema re-add.
- [ ] helm-unittest: negative guard cases assert the migration
  message text; positive default render; sidecar assertions
  removed.
- [ ] Drift gates (existing): `TestLogLines_AreStillEmittedByTheBinary`,
  `make lint-monitoring`, `make lint-alerts-generated`,
  `make lint-alerts-chart`.
- [ ] Render sweep: `helm template | grep -E '^kind:|^  namespace:'`
  (PR #67 pattern) + absence grep for tailscale/allowlist strings.
- [ ] `examples_test.go` green after example strips.
- [ ] End-to-end negative checks against real artifacts in Phase 4
  (old values → render failure; old HCL → startup failure).

## Dependencies

- PR #180 (INV-0016) and the DESIGN-0023 PR merge first — this
  IMPL's failure messages and supersession banners reference both.
- No Go module, tool, or schema-migration dependencies. No mock
  regeneration (no interface changes).
- Phase 4.2 needs ECR read access (AWS credentials) — operator-side
  step if CI credentials aren't at hand.

## Open Questions

**All three decided 2026-08-15** (operator review): 1a, 2a, 3a.
Decisions are folded into the tasks above (1.2, 1.7, 3.5, File
Changes); the original options are preserved below for the record.

**1. How do DESIGN-0003 and DESIGN-0004 record supersession?**
*Decided: (a).*
The docz `design` status list has no `Superseded` (audit finding);
RFC and ADR types both have it.

- (a) **Add `Superseded` to the design statuses in `.docz.yaml`**
  and set both docs to it, with a banner naming DESIGN-0023. One
  config line; the design lifecycle gains the state it evidently
  needs (this is the second time a design has been superseded in
  practice), and the README index shows the truth at a glance.
- (b) Banner-only: leave both docs at their current status, add a
  prominent "Superseded by DESIGN-0023" banner under the H1. No
  config change, but the index table keeps listing a dead design as
  Approved/Implemented.
- (c) Set them to `Abandoned` — exists today, but semantically wrong
  (the designs shipped and served; they weren't abandoned).
- other: ____

**2. Should the binary warn when the removed env vars are still
set?**
*Decided: (a).*
The HCL and chart surfaces fail loudly, but a stale
`TRUST_PROXY_HEADERS=true` env patch is silently ignored
(DESIGN-0023 documented the asymmetry; this question is whether to
soften it).

- (a) **Yes — one `slog.Warn` at startup** listing whichever of the
  three removed vars are set, pointing at
  `docs/operations/ingress.md`. ~10 lines plus a test; turns the
  one silent migration edge into a logged one, and E4's evidence
  tier picks it up for free. Remove the warn in a future major as
  a tombstone.
- (b) No — the design documented the asymmetry, the migration note
  covers it, and startup warnings for env vars the binary no longer
  knows about set a precedent of keeping ghost knowledge around.
- other: ____

**3. Does the E4 webhook "Rejections by message" panel survive?**
*Decided: (a).*
Its matcher reduces to the single `invalid webhook payload` line.

- (a) **Keep the panel** with the reduced matcher and a rewritten
  description ("signature rejections only; source-IP rejections now
  happen at the operator's edge — see ingress.md"). A wrong-secret
  incident after rotation is exactly when an operator looks at E4,
  and the panel now graphs precisely that.
- (b) Fold it away: delete the panel, keep the line only in the
  broader incidents panel (`webhookIncidentsRe`). One less panel,
  but the highest-signal failure mode loses its dedicated view.
- other: ____

## References

- [DESIGN-0023](../design/0023-operator-owned-ingress-remove-the-tailscale-sidecar-and-ip.md) — the design this implements (all six design OQs decided 2026-08-15)
- [INV-0016](../investigation/0016-retire-the-baked-tailscale-sidecar-for-operator-managed-ingress.md) — Observations 1–9, options matrix, straight-removal decision
- [IMPL-0016](0016-deprecate-memory-backend.md) — remove-the-baked-thing precedent
- [IMPL-0003](0003-github-webhook-ip-allowlist-middleware.md) — the middleware's original implementation (historical)
- CLAUDE.md — INV-0010 guardian strict-decode lockstep; IMPL-0023 drift gates; PR #67 namespace sweep; IMPL-0017 appVersion-vs-tag lesson; release post-mortem (startup_failure is silent)
