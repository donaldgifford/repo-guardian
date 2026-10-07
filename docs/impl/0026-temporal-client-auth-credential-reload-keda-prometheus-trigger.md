---
id: IMPL-0026
title: "Temporal client auth: credential reload and OpenBao-issued certificates"
status: Draft
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0026: Temporal client auth: credential reload and OpenBao-issued certificates

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Pre-implementation audit (2026-10-01)](#pre-implementation-audit-2026-10-01)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Client credential reload (internal/temporal)](#phase-1-client-credential-reload-internaltemporal)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Chart](#phase-2-chart)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Generated monitoring](#phase-3-generated-monitoring)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Reference Temporal cluster (contrib/temporal/)](#phase-4-reference-temporal-cluster-contribtemporal)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Documentation](#phase-5-documentation)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Verify, release rc.5 and roll out](#phase-6-verify-release-rc5-and-roll-out)
    - [Tasks](#tasks-5)
    - [Verification results](#verification-results)
    - [Success Criteria](#success-criteria-5)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
  - [OQ1: How does the reload goroutine stop?](#oq1-how-does-the-reload-goroutine-stop)
  - [OQ2: Where do the two metrics register?](#oq2-where-do-the-two-metrics-register)
  - [OQ3: How does the client-certificate flag reach the monitoring model?](#oq3-how-does-the-client-certificate-flag-reach-the-monitoring-model)
  - [OQ4: What if the live checks are not done before rc.5 is tagged?](#oq4-what-if-the-live-checks-are-not-done-before-rc5-is-tagged)
  - [OQ5: One operations page, or sections in existing docs?](#oq5-one-operations-page-or-sections-in-existing-docs)
  - [OQ6: One rc, or two?](#oq6-one-rc-or-two)
  - [OQ7: Should the expiry gauge carry a label for which certificate it is?](#oq7-should-the-expiry-gauge-carry-a-label-for-which-certificate-it-is)
- [References](#references)
<!--toc:end-->

> **Scope change (2026-10-07):** the KEDA Prometheus trigger moved to
> IMPL-0028 (controls foundations), which builds one ScaledObject per role
> and task queue. Building a single-queue Prometheus trigger here first
> would be rebuilt there. This plan keeps credential reload, the OpenBao
> certificates and their monitoring, and runs after IMPL-0028. Tasks
> marked "Moved" are tracked in IMPL-0028 Phase 5.

## Objective

Implement [DESIGN-0028](../design/0028-temporal-client-auth-oidc-over-mtls-with-openbao-issued.md)
for the next v2 rc (`v2.0.0-rc.5`, chart `2.0.0-rc.5`):

- repo-guardian's Temporal clients re-read their client certificate, CA
  bundle and OIDC client secret when the files change, with no restart;
- KEDA scales the worker from the Temporal server's
  `approximate_backlog_count` through its Prometheus trigger;
- the chart can render a cert-manager `Certificate` for an
  operator-owned OpenBao-backed Issuer;
- `contrib/temporal/` runs the JWT authorizer on top of its mTLS;
- certificate expiry is visible as a metric, a chart alert, and a
  generated alert and panel.

**Implements:** DESIGN-0028 (all ten open questions decided).

## Scope

### In Scope

- `internal/temporal`: credential reload (`reload.go`), `Dial` wiring,
  `TEMPORAL_TLS_RELOAD_INTERVAL`, OIDC secret re-read, two metrics.
- `internal/monitoring`: `MechanismTemporalClientCert`, one alert spec,
  one E3 stat, the `--temporal-client-cert` flag.
- Chart: `temporal.tls.reloadInterval`, `temporal.tls.certManager.*`,
  `worker.keda.{trigger, fallbackReplicas, prometheus.*}`, the
  `Certificate` and `TriggerAuthentication` templates, the
  ScaledObject rewrite, guards, schema, the expiry alert, tests.
- `contrib/temporal/`: authorization, audience, TLS refresh, internal
  frontend, namespace Job moved to the internal frontend.
- Docs: a Temporal client auth operations page with the OpenBao and
  cert-manager recipe, onboarding and migration updates, chart README,
  CLAUDE.md, INV-0020 and DESIGN-0028 status.

### Out of Scope

- Running OpenBao, cert-manager, KEDA or Prometheus (operator-owned).
- How people reach Temporal (the Temporal UI, human sign-in).
- The status page's backlog in split topology (INV-0020 Observation 10).
- Extracting a shared module (INV-0020 § Future work; after the rc is
  proven).
- Temporal Cloud specifics.

## Pre-implementation audit (2026-10-01)

Verified against the code on `v2` (rc.4) before writing the phases:

| # | Assumption in DESIGN-0028 | Finding | Effect on this plan |
| --- | --- | --- | --- |
| A1 | The client certificate is read once | `tlsConfig` calls `tls.LoadX509KeyPair` and fills `Certificates`; `RootCAs` from one `os.ReadFile` | Phase 1 replaces both |
| A2 | The OIDC secret is read once | `OIDCConfig.tokenSource` reads it once; its comment says rotation needs a restart | Phase 1 task 1.6 |
| A3 | Certificate + OIDC together are allowed | `Config.validate` and `validateTemporalAuth` both allow them | No change needed |
| A4 | `Dial` owns the client's lifetime | `Dial` returns a bare `client.Client`; callers are `cmd/repo-guardian/roles.go` (`dialTemporal`) and `cmd/rg-burst` | A goroutine needs a stop hook (OQ1) |
| A5 | Metrics register through `internal/metrics` | Every repo-guardian metric is `promauto` in `internal/metrics/metrics.go` | Phase 1 follows it (OQ2) |
| A6 | Mechanisms can be set from outside the policy | `Mechanisms.add` is unexported; `Derive(cfg, opts Options)` builds them from the policy | Add a `Derive` option (OQ3) |
| A7 | The Temporal alerts are mirrored by hand in the chart | `prometheusrule.yaml` group `repo-guardian.temporal`, comment says "keep the two in step" | Same treatment for the new alert |
| A8 | The dev server can run mTLS | `temporal server start-dev` (CLI v1.9.1) has no TLS flags | Integration test via an in-process TLS-terminating proxy (task 1.9) |
| A9 | The ScaledObject only renders in split | `worker-scaledobject.yaml` gates on `topology == split` | Unchanged |
| A10 | The KEDA + OIDC guard is unconditional | `validateTemporalAuth` fails on `oidc.tokenUrl` + `worker.keda.enabled` | Phase 2 makes it conditional on `trigger: temporal` |
| A11 | The upstream Temporal chart supports authorization, audience, TLS refresh and the internal frontend | chart 1.7.0: `server.config.authorization`, `server.config.tls` merged into `global.tls`, `server.internal-frontend.enabled`; server `config.Authorization.Audience` exists | Phase 4 is values only |

## Implementation Phases

Each phase ends with `make lint` and `make test` green (plus the phase's
own gates), and a commit per numbered task. The live checks DESIGN-0028
depends on are in Phase 6, before the tag: the rc is held until they
pass (OQ4), and this IMPL cannot close until they are done.

### Phase 1: Client credential reload (`internal/temporal`)

#### Tasks

- [ ] 1.1 `ConfigFromEnv`: add `TLSReloadInterval time.Duration` from
  `TEMPORAL_TLS_RELOAD_INTERVAL` (Go duration, default `30s`). `validate`
  rejects values outside 5s–10m with a message naming the bounds. Table
  tests for default, valid, out of range, unparsable.
- [ ] 1.2 `reload.go`: `credentialFiles` with an
  `atomic.Pointer[tlsMaterial]` snapshot, `reload()` (stat with symlinks
  followed; skip when every modification time is unchanged; read, parse,
  validate pair match and `NotAfter > now`; swap), and `run(ctx)`
  ticking at `TLSReloadInterval`. The first `reload` is strict; later
  failures keep the last good snapshot, `slog.Warn` with the file and
  error, and count an error.
- [ ] 1.3 `getClientCertificate` serves the snapshot's certificate.
  `verifyConnection` verifies the peer chain against the snapshot's
  pool: `x509.VerifyOptions{DNSName: serverName, Roots, Intermediates
  from PeerCertificates[1:], KeyUsages: [ExtKeyUsageServerAuth],
  CurrentTime: now}`. `InsecureSkipVerify` is set only when a CA path is
  configured, with `//nolint:gosec // G402: verification moved to
  VerifyConnection so the CA can reload (DESIGN-0028)`. Without a CA
  path (OIDC on system roots), standard verification stays.
- [ ] 1.4 Rewrite `tlsConfig` to build from `credentialFiles` (no more
  `Certificates`/`RootCAs` fields). Keep `TestTLSConfig`'s cases and
  update its assertions to the callbacks.
- [ ] 1.5 Lifecycle (OQ1: a `Close` wrapper): `Dial` returns a type
  embedding `client.Client` whose `Close` stops the poller, then closes
  the client. `run` starts on `context.WithoutCancel(ctx)` plus its own
  cancel, so a startup timeout on the dial context never stops
  reloading. Both
  callers (`dialTemporal`, `rg-burst`) keep their code unchanged.
- [ ] 1.6 OIDC: replace the read-once secret with a `TokenSource` that
  reads `ClientSecretPath` on each fetch and builds the
  client-credentials request; keep the `ReuseTokenSourceWithExpiry`
  wrapper. Update the `tokenSource` comment. A read failure counts
  `credential="oidc_secret", outcome="error"`.
- [ ] 1.7 Metrics (OQ2: `internal/metrics`, `promauto`): `repo_guardian_temporal_client_cert_expiry_timestamp_seconds`
  (gauge, set on every successful load) and
  `repo_guardian_temporal_credential_reloads_total{credential, outcome}`
  (`client_cert|ca|oidc_secret` × `changed|error`; unchanged polls
  count nothing). Pre-initialise the six label pairs so `increase()`
  sees the first change.
- [ ] 1.8 Unit tests (`reload_test.go`), with a helper that mimics
  kubelet's `..data` symlink swap and a test CA:
  - rotation is picked up on the next tick;
  - mismatched pair, empty file, missing file and expired certificate
    each keep the last good snapshot and count an error;
  - a bad startup fails `Dial`;
  - handshake: an in-process `tls.Listen` requiring client certs sees
    the new serial after rotation and reconnect;
  - CA rollover: handshake fails until the client CA file holds the new
    CA, then succeeds with no restart;
  - verification: wrong hostname, unknown CA, expired server cert and a
    cert without server-auth EKU are all rejected;
  - OIDC: an `httptest` token endpoint accepting only the current
    secret; rotate the file, expire the token, the next fetch uses the
    new secret;
  - interval parsing (in 1.1).
- [ ] 1.9 Integration test (`-tags integration`): an in-process TLS
  proxy (requires client certs, ALPN `h2`) in front of
  `temporaltest.Start`. `Dial` with mTLS through it, rotate the client
  certificate, drop connections at the proxy, and assert the next call
  succeeds with the new serial and no new `Dial`.
- [ ] 1.10 Non-vacuous check: temporarily restore static
  `Certificates`/`RootCAs` and a read-once secret; confirm the rotation,
  CA-rollover and OIDC tests fail; restore. Record the result in the
  task.
- [ ] 1.11 Go doc comments on every new type and function;
  `internal/temporal` package doc mentions reload.

#### Success Criteria

- Every test in 1.8 and 1.9 passes under `-race`, and 1.10 shows they
  fail without the change.
- `make lint` clean, with exactly one justified `gosec` exclusion.
- `Dial`'s signature and both callers are unchanged.
- The parity suite and replay tests are untouched (no workflow or
  engine change).

### Phase 2: Chart

#### Tasks

- [ ] 2.1 Values: `temporal.tls.reloadInterval` (→
  `TEMPORAL_TLS_RELOAD_INTERVAL` on roles that dial Temporal),
  `temporal.tls.certManager.{enabled, issuerRef.{name,kind,group},
  commonName, duration, renewBefore, secretName}`,
  `worker.keda.{trigger, fallbackReplicas}`,
  `worker.keda.prometheus.{serverAddress, query, authenticationRef}`.
  Defaults per DESIGN-0028 § Chart values; `trigger: prometheus`.
- [ ] 2.2 `_helpers.tpl`: `repo-guardian.temporalTLSSecret` returns the
  effective Secret (`certManager.secretName`, else `existingSecret`,
  else `<fullname>-temporal-tls` when `certManager.enabled`). The TLS
  env, volume and checksum logic use it, so `certManager.enabled` alone
  mounts the certificate.
- [ ] 2.3 `templates/temporal-certificate.yaml`: the `Certificate`
  (usages `client auth`, `digital signature`; ECDSA P-256;
  `rotationPolicy: Always`), namespace stamped, rendered when
  `certManager.enabled`.
- [x] 2.4 **Moved 2026-10-07 to IMPL-0028 Phase 5:** the KEDA half of this plan is folded into the controls foundations, which build one ScaledObject per role and queue. `worker-scaledobject.yaml`: `prometheus` trigger with
  `serverAddress`, `query` (default built from `temporal.namespace` and
  `temporal.taskQueue`, as designed; task 6.3 confirms or corrects it
before the tag), `threshold` from
  `targetQueueSize`, optional `authenticationRef`; `temporal` trigger as
  today plus `authenticationRef` when a client certificate is
  configured; `fallback` (`failureThreshold: 3`, `replicas` =
  `fallbackReplicas` or `worker.replicas`) for both. A template comment
  explains why KEDA 2.21's composite running-workflows metric is never
  set.
- [x] 2.5 **Moved 2026-10-07 to IMPL-0028 Phase 5:** the KEDA half of this plan is folded into the controls foundations, which build one ScaledObject per role and queue. `templates/worker-triggerauthentication.yaml`: `cert`, `key`,
  `ca` from the effective TLS Secret, rendered for `trigger: temporal`
  with a client certificate. `tlsServerName` in the trigger metadata
  from `temporal.tls.serverName`.
- [ ] 2.6 **Split 2026-10-07:** the `trigger` and OIDC-with-KEDA guards moved to IMPL-0028 Phase 5; this task keeps the `certManager.enabled` without `issuerRef.name` guard. Guards in `validateTemporalAuth`: `trigger: prometheus`
  without `serverAddress` fails; the OIDC guard fails only for
  `trigger: temporal`, naming `trigger: prometheus` as the fix;
  `certManager.enabled` without `issuerRef.name` fails; an unknown
  `trigger` fails. Messages name the value to change.
- [ ] 2.7 `values.schema.json`: the new keys, `trigger` enum,
  `reloadInterval` pattern.
- [ ] 2.8 `prometheusrule.yaml`: `RepoGuardianTemporalClientCertExpiring`
  in the `repo-guardian.temporal` group, rendered only with a client
  certificate configured, overridable like its siblings
  (`prometheusRule.alerts.TemporalClientCertExpiring`).
- [ ] 2.9 **Split 2026-10-07:** `keda_test.yaml` moved to IMPL-0028 Phase 5; this task keeps the temporal-auth, certificate and prometheusrule tests. helm-unittest: new `keda_test.yaml` (both triggers, default
  and custom query, `authenticationRef`, `fallback`, composite metric
  absent, TriggerAuthentication present only for temporal + client
  cert); `temporal_auth_test.yaml` (relaxed and new guards,
  `reloadInterval` env, certManager mounting); `certificate_test.yaml`
  (rendering, secret-name precedence, namespace); `prometheusrule_test.yaml`
  (alert gating). `make lint-alerts-chart` passes.
- [ ] 2.10 `README.md.gotmpl` notes for the new blocks; `make
  helm-docs`.

#### Success Criteria

- `make helm-test` and `make lint-alerts-chart` green.
- A render with `temporal.auth.oidc` + `worker.keda.enabled` (default
  trigger) succeeds; with `trigger: temporal` it fails with the new
  message.
- `helm template ... | grep -E '^kind:|^  namespace:'` pairs every new
  kind with the release namespace.

### Phase 3: Generated monitoring

#### Tasks

- [ ] 3.1 `MechanismTemporalClientCert` in `mechanism.go`, with the
  membership comment naming its two series.
- [ ] 3.2 Plumbing (OQ3): `monitoring.Options.TemporalClientCert bool`;
  `Derive` adds the mechanism when set.
- [ ] 3.3 `repo-guardian monitoring generate --temporal-client-cert`
  sets the option.
- [ ] 3.4 Alert spec `RepoGuardianTemporalClientCertExpiring` in
  `temporalSpecs`, `Requires: MechanismTemporalClientCert`, same
  expression, threshold and `for` as the chart rule.
- [ ] 3.5 E3 stat "Temporal client certificate: time to expiry"
  (`min(...expiry_timestamp_seconds) - time()`, seconds unit), gated on
  the mechanism.
- [ ] 3.6 Tests: alert and panel absent without the flag, present with
  it; a test comparing the catalogue expression with the chart rule's
  rendered expression; existing promtool check covers the new spec.
- [ ] 3.7 `make monitoring-generate` produces no diff (the committed
  tier is generated without the flag); `make lint-monitoring` clean.

#### Success Criteria

- `contrib/generated/` unchanged; `make lint-monitoring` clean.
- With `--temporal-client-cert`, the alert and the stat appear and pass
  promtool.

### Phase 4: Reference Temporal cluster (`contrib/temporal/`)

#### Tasks

- [ ] 4.1 `values-base.yaml`: `server.config.authorization`
  (`jwtKeyProvider.keySourceURIs` placeholder, `refreshInterval: 1m`,
  `permissionsClaimName: permissions`, `audience` placeholder,
  `authorizer: default`, `claimMapper: default`),
  `server.config.tls.refreshInterval: 1m`,
  `server.internal-frontend.enabled: true`. Comments link DESIGN-0028.
- [ ] 4.2 `namespace-job.yaml`: target the internal frontend with the
  internode certificate (OQ7 of DESIGN-0028); keep retention and
  idempotency; update the header comment.
- [ ] 4.3 `networkpolicy.yaml`: allow the namespace Job to reach the
  internal frontend; repo-guardian still reaches only the external
  frontend.
- [ ] 4.4 `README.md`: prerequisites (Keycloak or Okta JWKS, the
  audience per environment, the client CA in `clientCaFiles`), rewrite
  § Why these choices (mTLS + NetworkPolicy bound reachability; the JWT
  authorizer bounds rights), and an "upgrading an mTLS-only install"
  note.
- [ ] 4.5 `make lint-temporal-contrib` asserts the rendered config
  contains `authorization` with `authorizer: default`, the TLS
  `refreshInterval`, and an internal-frontend Service.

#### Success Criteria

- `make lint-temporal-contrib` green across every visibility mode,
  including the new assertions.

### Phase 5: Documentation

#### Tasks

- [ ] 5.1 New page (OQ5) `docs/operations/temporal-client-auth.md`: the
  posture and why (the INV-0020 trade-off table), environment isolation
  (client per environment + audience; separate tenants as the stronger
  option), the OpenBao commands, Issuer, token-request Role, chart
  values, the rollout order and rollback from DESIGN-0028, and
  troubleshooting (the two metrics, the alert, `cmctl status`).
- [ ] 5.2 `docs/operations/v2-onboarding.md`: link the page from
  prerequisites; replace the KEDA guidance with the Prometheus trigger
  and the sizing note.
- [x] 5.3 **Moved 2026-10-07 to IMPL-0028 Phase 5:** the KEDA half of this plan is folded into the controls foundations, which build one ScaledObject per role and queue. `docs/operations/v2-migration.md`: the KEDA values change
  (default trigger is now `prometheus`, needs `serverAddress`).
- [ ] 5.4 `mkdocs.yml` nav; `make` docs build clean of new warnings.
- [ ] 5.5 CLAUDE.md: one v2-branch entry (reload contract: strict
  first load, forgiving later loads, `InsecureSkipVerify` only with a
  CA path and why; the KEDA trigger default; the hand-mirrored alert).
- [ ] 5.6 Status: INV-0020 → Concluded, DESIGN-0028 → Approved (if not
  already), this doc → In Progress / Completed as phases land; `docz
  update`.

#### Success Criteria

- markdownlint and the mkdocs build pass with no new warnings.
- A reader can go from nothing to a renewing OpenBao-issued certificate
  using only `temporal-client-auth.md`.

### Phase 6: Verify, release rc.5 and roll out

The rc is held until 6.1–6.4 pass. Every *deferred: human required*
task must be checked off before this IMPL is marked Completed.

#### Tasks

- [x] 6.1 **Moved 2026-10-07 to IMPL-0028 Phase 5:** the KEDA half of this plan is folded into the controls foundations, which build one ScaledObject per role and queue. In the homelab Prometheus, list the series and labels of
  `approximate_backlog_count{namespace="repo-guardian"}` (and any
  prefixed variant). Record whether per-`priority` series coexist with
  an aggregate, and whether `worker_build_id` splits a partition. —
  *deferred: human required*
- [x] 6.2 **Moved 2026-10-07 to IMPL-0028 Phase 5:** the KEDA half of this plan is folded into the controls foundations, which build one ScaledObject per role and queue. With the queue non-empty, compare the default query's value
  with the backlog `DescribeTaskQueue` reports (the status page in
  `all` topology, or `temporal task-queue describe`). Record both
  numbers under § Verification results. — *deferred: human required*
- [x] 6.3 **Moved 2026-10-07 to IMPL-0028 Phase 5:** the KEDA half of this plan is folded into the controls foundations, which build one ScaledObject per role and queue. If 6.1/6.2 show double counting or a dropped split, change the
  default query in `worker-scaledobject.yaml` (group by the splitting
  label before summing), update its helm-unittest, and record why.
- [ ] 6.4 Confirm the installed cert-manager supports Vault Kubernetes
  auth with `serviceAccountRef` (record the token audience format it
  sends), and that the installed OpenBao signs through
  `pki_*/sign/<role>` for a throwaway Certificate. Put the minimum
  versions in `temporal-client-auth.md`. — *deferred: human required*
- [ ] 6.5 `Chart.yaml` `2.0.0-rc.5` / appVersion `2.0.0-rc.5`; helm
  unittest pins updated; CHANGELOG via git-cliff.
- [ ] 6.6 PR to `v2` with `dont-release` (Rule 6); after merge, tag
  `v2.0.0-rc.5` and dispatch `ghcr.yml`; verify assets, signatures,
  provenance, and that `latest` did not move.
- [ ] 6.7 Homelab, in DESIGN-0028's order: OpenBao mount, roles, policy
  and Kubernetes auth; Issuer; `certManager.enabled` (pods present a
  certificate the server does not yet require). — *deferred: human
  required*
- [ ] 6.8 Frontend `requireClientAuth` with the OpenBao CA in
  `clientCaFiles`, `tls.refreshInterval`, `authorization.audience`. —
  *deferred: human required*
- [x] 6.9 **Moved 2026-10-07 to IMPL-0028 Phase 5:** the KEDA half of this plan is folded into the controls foundations, which build one ScaledObject per role and queue. `worker.keda.enabled` with the Prometheus trigger; generate a
  backlog (a policy change, or `rg-burst`) and watch it scale out and
  back. — *deferred: human required*
- [ ] 6.10 `cmctl renew` the client certificate and restart the
  frontend; confirm `credential_reloads_total{outcome="changed"}`
  rises, no pod restarts, workers keep polling, the alert stays quiet.
  — *deferred: human required*

#### Verification results

<!-- Filled in by 6.1, 6.2 and 6.4. -->

#### Success Criteria

- The default KEDA query matches `DescribeTaskQueue`'s backlog on live
  series (6.1–6.3), and the minimum cert-manager and OpenBao versions
  are documented (6.4), before the tag.
- rc.5 published like rc.4 (assets, cosign, provenance, `latest`
  unchanged).
- Homelab: renewal and a frontend restart cause no worker restarts and
  no failed checks; KEDA scales on backlog under OIDC.
- Every deferred task is checked off.

## File Changes

| File | Change |
| --- | --- |
| `internal/temporal/temporal.go` | `TLSReloadInterval`; `tlsConfig` from `credentialFiles`; `Dial` lifecycle |
| `internal/temporal/reload.go` (new) | `credentialFiles`, `tlsMaterial`, callbacks |
| `internal/temporal/reload_test.go` (new) | unit tests in 1.8 |
| `internal/temporal/mtls_integration_test.go` (new) | proxy-based integration test in 1.9 |
| `internal/temporal/oidc.go`, `oidc_test.go` | secret re-read |
| `internal/temporal/temporal_test.go` | interval parsing; `TestTLSConfig` |
| `internal/metrics/metrics.go` | two metrics |
| `internal/monitoring/mechanism.go`, `derive.go`, `model.go` | mechanism and option |
| `internal/monitoring/alert/alert.go`, tests | alert spec |
| `internal/monitoring/dashboard/e3.go`, tests | expiry stat |
| `cmd/repo-guardian/monitoring.go` | `--temporal-client-cert` |
| `charts/repo-guardian/values.yaml`, `values.schema.json`, `README.md.gotmpl`, `README.md` | values |
| `charts/repo-guardian/templates/_helpers.tpl` | effective TLS Secret, env, guards |
| `charts/repo-guardian/templates/deployment.yaml` | reload interval env, Secret helper |
| `charts/repo-guardian/templates/worker-scaledobject.yaml` | triggers, fallback |
| `charts/repo-guardian/templates/worker-triggerauthentication.yaml` (new) | mTLS for the temporal trigger |
| `charts/repo-guardian/templates/temporal-certificate.yaml` (new) | cert-manager Certificate |
| `charts/repo-guardian/templates/prometheusrule.yaml` | expiry alert |
| `charts/repo-guardian/tests/*` | `keda_test.yaml`, `certificate_test.yaml` (new); auth and rule suites |
| `contrib/temporal/values-base.yaml`, `namespace-job.yaml`, `networkpolicy.yaml`, `README.md` | layered posture |
| `Makefile` | `lint-temporal-contrib` assertions |
| `docs/operations/temporal-client-auth.md` (new), `v2-onboarding.md`, `v2-migration.md`, `mkdocs.yml` | docs |
| `CLAUDE.md` | v2-branch entry |
| `charts/repo-guardian/Chart.yaml`, `CHANGELOG.md` | rc.5 |

## Testing Plan

| Layer | What | Where |
| --- | --- | --- |
| Unit | reload, verification, OIDC secret, interval | `internal/temporal/*_test.go` (1.1, 1.8) |
| Integration | mTLS through a proxy to the dev server, rotation without re-dial | `mtls_integration_test.go` (1.9) |
| Non-vacuous | static credentials make the rotation tests fail | 1.10 |
| Chart | triggers, guards, Certificate, TriggerAuthentication, alert gating | helm-unittest (2.9) |
| Alerts | promtool on chart and catalogue | `make lint-alerts-chart`, monitoring tests |
| Drift | generated tier unchanged | `make lint-monitoring` (3.7) |
| Reference cluster | authorizer, refresh, internal frontend render | `make lint-temporal-contrib` (4.5) |
| Live | query vs `DescribeTaskQueue`, renewal, frontend restart, KEDA scale-out | homelab (6.1–6.4, 6.7–6.10) |

## Dependencies

- DESIGN-0028 decisions (all ten decided).
- The Phase 6 live checks gate the tag (OQ4): the default KEDA query
  (6.1–6.3) and the minimum cert-manager/OpenBao versions (6.4).
- Operator-owned: OpenBao with the Kubernetes auth method, cert-manager
  with the Vault issuer, KEDA 2.21+, a Prometheus scraping the Temporal
  server, Keycloak or Okta.
- Phases 1 and 2 are independent and can proceed in parallel; Phase 3
  needs the metric names from Phase 1; Phase 5 needs 1–4; Phase 6 needs
  all.

## Open Questions

### OQ1: How does the reload goroutine stop?

- (a) ✅ decided: **`Dial` returns a thin wrapper that embeds `client.Client` and
  overrides `Close`** to stop the poller, then close the client. The
  goroutine runs on a context detached from the dial context, so a
  startup timeout never stops reloading. Callers stay unchanged.
- (b) `DialOptions` gains a long-lived `Context` the caller cancels.
  Explicit, but both callers change, and forgetting it leaks or stops
  reload early.
- (c) No stop: the goroutine lives for the process. Simplest, but tests
  that dial repeatedly leak goroutines.
- other:

Why, and what is typical. Go has two common shapes for a background
goroutine owned by a component:

| | `Close`/`Stop` on the owning object | `Run(ctx)` / a lifetime context |
| --- | --- | --- |
| Examples | `grpc.ClientConn.Close`, `http.Server.Shutdown`, `time.Ticker.Stop`, the Temporal SDK's own `client.Close` | controller-runtime `certwatcher.Start(ctx)`, Kubernetes `dynamiccertificates` `Run(ctx)` |
| Lifetime | tied to the thing it serves: reload can neither outlive the client nor stop before it | tied to whatever context the caller passes; a short one stops reload while the client keeps working, and certificates go stale silently |
| Callers | unchanged | every caller must thread a process-lifetime context through |
| Go guidance | fits "contexts are for request scope, not object lifetime" (go.dev/blog/context-and-structs) | normal for components run under a manager or errgroup |
| Cost | a wrapper type embedding `client.Client` | one more `DialOptions` field |

The reloader is part of the client, not a separate component under a
manager, so it follows the client's own `Close`. If the shared module
later grows a standalone reloader for other consumers, that type can
expose `Run(ctx)` too; the client wrapper would call it.

### OQ2: Where do the two metrics register?

- (a) ✅ decided: **`internal/metrics` with `promauto`**, like every other
  `repo_guardian_*` metric, so names and the generated catalogue line
  up.
- (b) Through the OpenTelemetry `MeterProvider` already passed to
  `Dial`. Fits the OTel-first direction, but the names then follow the
  OTel bridge's conventions and `rg-burst` (no provider) records
  nothing.
- other:

On the OTel/Prometheus split: DESIGN-0022 assigns OTel the
off-the-shelf instrumentation at the hops (`otelhttp`, `redisotel`,
`otelpgx`, the Temporal SDK's handler), which is where RED comes from.
Domain and state metrics no library knows about stay hand-written in
`internal/metrics`. Certificate expiry and reload outcomes are state, not
request traffic, and no instrumentation library emits them, so they
belong with the hand-written set. `rg-burst` also dials with no meter
provider, so OTel would record nothing there.

When this moves to the shared module, switch it to the OTel meter API
taken through `DialOptions.MeterProvider`: a library should not register
into a global Prometheus registry. Not now.

### OQ3: How does the client-certificate flag reach the monitoring model?

- (a) ✅ decided: **A `monitoring.Options.TemporalClientCert` field** that `Derive`
  turns into the mechanism. Keeps `Mechanisms.add` unexported and
  every mechanism decided in one place.
- (b) Export `Mechanisms.Add` and set it in `cmd`. Less code, but any
  caller can then add any mechanism, which the package's membership
  rule is designed to prevent.
- other:

There is no second use today. The `Options` field is still a flag at the
CLI; the difference is internal. Each future deployment fact (KEDA on,
OIDC on) becomes one more `Options` field, decided inside `Derive`
beside the policy-derived mechanisms. Exporting `add` would only pay
off for a caller outside the package, and none exists.

### OQ4: What if the live checks are not done before rc.5 is tagged?

- (a) **Ship the designed default query, documented as "verify against
  your series", and keep `worker.keda.prometheus.query` overridable.**
  KEDA is off by default, so nothing scales on an unverified query
  unless an operator enables it.
- (b) Make `worker.keda.prometheus.query` required (no default) until
  verified. Safe, but every operator writes PromQL first.
- (c) ✅ decided: hold the rc until the live checks pass. They are
  tasks 6.1–6.4, deferred to a human, ahead of the tag in the last
  phase, so the IMPL cannot close until they are done.
- other:

### OQ5: One operations page, or sections in existing docs?

- (a) ✅ decided: **A new `docs/operations/temporal-client-auth.md`**, linked from
  onboarding. The recipe is long, and it is the piece most likely to
  move into a shared module later.
- (b) A section in `v2-onboarding.md`. One fewer page, a much longer
  runbook.
- other:

Decided: the page is linked from `v2-onboarding.md` (task 5.2).

### OQ6: One rc, or two?

- (a) ✅ decided: **One rc (rc.5) with Phases 1–5.** The pieces are small, and the
  rollout order in Phase 6 already lets each be switched on separately
  by values.
- (b) rc.5 with Phases 1–2 (reload and KEDA), rc.6 with Phases 3–4
  (monitoring and the reference cluster). Smaller PRs, two release
  rounds.
- other:

### OQ7: Should the expiry gauge carry a label for which certificate it is?

- (a) ✅ decided: **No labels.** One client certificate per process; Prometheus
  target labels already identify the pod.
- (b) A `subject` label (the certificate's common name). Useful if one
  process ever presents more than one certificate; adds a label nothing
  needs today.
- other:

Why no labels:

- **One series per pod already.** Each process presents one client
  certificate, and Prometheus adds `pod`/`namespace`/`job` at scrape
  time.
- **The useful labels churn.** A `serial` label would start a new series
  on every renewal (daily, with 24h certificates), and the old one
  would linger as stale data until it ages out.
- **The static ones say nothing new.** The common name is fixed by
  chart values (`repo-guardian`), so a `subject` label repeats config.
- **The alert does not need it.** It uses `min(...)`, which picks the
  worst pod regardless of labels; adding a label later would not break
  it.

## References

- [DESIGN-0028](../design/0028-temporal-client-auth-oidc-over-mtls-with-openbao-issued.md)
- [INV-0020](../investigation/0020-keda-worker-autoscaling-cannot-authenticate-to-temporal.md)
- [IMPL-0025](0025-v2-findings-model-temporal-control-plane-read-only-api-and-ui.md):
  the v2 phases this rc builds on.
- `internal/temporal/`, `internal/monitoring/`,
  `charts/repo-guardian/templates/worker-scaledobject.yaml`,
  `contrib/temporal/`.
