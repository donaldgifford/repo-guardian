---
id: INV-0020
title: "KEDA worker autoscaling cannot authenticate to Temporal"
status: Open
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0020: KEDA worker autoscaling cannot authenticate to Temporal

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: as shipped, KEDA only works against an unauthenticated plaintext frontend](#observation-1-as-shipped-keda-only-works-against-an-unauthenticated-plaintext-frontend)
  - [Observation 2: the mTLS case fails silently](#observation-2-the-mtls-case-fails-silently)
  - [Observation 3: KEDA's Temporal scaler supports mTLS and a static API key, nothing else](#observation-3-kedas-temporal-scaler-supports-mtls-and-a-static-api-key-nothing-else)
  - [Observation 4: a rotating token would self-heal, because KEDA rebuilds a failing scaler](#observation-4-a-rotating-token-would-self-heal-because-keda-rebuilds-a-failing-scaler)
  - [Observation 5: worker versioning does not hide the backlog](#observation-5-worker-versioning-does-not-hide-the-backlog)
  - [Observation 6: KEDA 2.21 removed settings we do not use, and added one we must not](#observation-6-keda-221-removed-settings-we-do-not-use-and-added-one-we-must-not)
  - [Observation 7: autoscaling buys burst latency, not throughput](#observation-7-autoscaling-buys-burst-latency-not-throughput)
  - [Observation 8: the Temporal server already exports the backlog to Prometheus](#observation-8-the-temporal-server-already-exports-the-backlog-to-prometheus)
  - [Observation 9: Temporal OSS has exactly one built-in authorizer, and it reads JWTs](#observation-9-temporal-oss-has-exactly-one-built-in-authorizer-and-it-reads-jwts)
  - [Observation 10: the UI does not see the backlog in split topology, with or without KEDA](#observation-10-the-ui-does-not-see-the-backlog-in-split-topology-with-or-without-keda)
  - [Observation 11: mTLS and OIDC already work together, and certificates are ours to issue](#observation-11-mtls-and-oidc-already-work-together-and-certificates-are-ours-to-issue)
  - [Observation 12: the client certificate is read once, so rotation needs a restart](#observation-12-the-client-certificate-is-read-once-so-rotation-needs-a-restart)
- [Options](#options)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
  - [Open questions](#open-questions)
- [Future work: a shared Temporal auth package](#future-work-a-shared-temporal-auth-package)
- [References](#references)
<!--toc:end-->

## Question

Can the chart's optional KEDA autoscaling (`worker.keda.enabled`) scale
the v2 worker against a Temporal frontend that requires authentication,
either mTLS or OIDC bearer tokens? If not, what is the smallest change
that makes it work for both?

## Hypothesis

KEDA works with mTLS and fails with OIDC: the chart already refuses
KEDA together with `temporal.auth.oidc`, because KEDA's Temporal
trigger cannot mint OIDC tokens. A secondary worry was that our use of
Worker Deployment versioning might make KEDA's backlog reading always
zero, since the ScaledObject sets no version.

The first half turned out to be wrong (mTLS does not work either). The
worry about versioning turned out to be unfounded.

## Context

**Triggered by:** the v2.0.0-rc.4 homelab rollout. The homelab Temporal
uses a Keycloak JWT authorizer (`temporal.auth.oidc`, IMPL-0025 rc.2),
so `worker.keda.enabled` fails to render there. Reviewing why led to
the chart template and to KEDA's scaler source.

KEDA was made optional in DESIGN-0026 (OQ14) and rendered by
`charts/repo-guardian/templates/worker-scaledobject.yaml` in IMPL-0025
Phase 17. INV-0018 Observation 7 set the expectation for what
autoscaling is worth here.

## Approach

1. Read the chart's ScaledObject template and its render-time guards.
2. Read KEDA's Temporal scaler source at the v2.21.0 release
   (`pkg/scalers/temporal_scaler.go`): its auth options, how it dials
   Temporal, and which API it reads the backlog from.
3. Read KEDA's scaler cache (`pkg/scaling/cache/scalers_cache.go`) to
   see what happens when a scaler's credentials go stale.
4. Measure, on a real Temporal dev server, whether the unversioned
   `DescribeTaskQueue` call KEDA makes by default reports the backlog of
   a versioned queue.
5. Read the Temporal server's metric definitions and its authorization
   package, to find a credential-free backlog source and to establish
   what mTLS-only clients can do on a JWT-authorized cluster.
6. Trace how the status page gets its backlog in each topology.

## Environment

| Component | Version / Value |
| --------- | --------------- |
| repo-guardian chart | 2.0.0-rc.4 |
| KEDA | v2.21.0 (2026-09-23); scaler source at `main`, last changed 2026-09-09 |
| Temporal dev server | Temporal CLI v1.9.1 (the `temporaltest` pin) |
| Temporal Go SDK | as pinned in `go.mod` |
| Worker versioning | Worker Deployments, deployment `repo-guardian`, build ID = image tag, AutoUpgrade |

## Findings

### Observation 1: as shipped, KEDA only works against an unauthenticated plaintext frontend

The whole trigger the chart renders:

```yaml
triggers:
  - type: temporal
    metadata:
      endpoint: {{ .Values.temporal.address | quote }}
      namespace: {{ .Values.temporal.namespace | quote }}
      taskQueue: {{ .Values.temporal.taskQueue | quote }}
      queueTypes: workflow,activity
      targetQueueSize: {{ .Values.worker.keda.targetQueueSize | quote }}
```

There is no `authenticationRef`, no `TriggerAuthentication`, and no
`tlsServerName`. The scaler only turns on TLS when it is given a client
certificate or an API key (`getTemporalClient`, `buildTemporalTLSConfig`),
so with this template it dials the frontend in plaintext with no
credentials.

| Temporal auth (`temporal.*`) | KEDA as shipped |
| --- | --- |
| None, plaintext (`tls.disabled: true`) | works |
| Server TLS only, no client auth | fails: KEDA does not speak TLS |
| mTLS (`tls.existingSecret`) | renders, never connects |
| OIDC (`auth.oidc`) | refused at render by `validateTemporalAuth` |

The reference cluster in `contrib/temporal/` requires mTLS, so KEDA has
never worked against it.

### Observation 2: the mTLS case fails silently

The chart renders, `helm install` succeeds, and nothing complains. Each
KEDA poll fails, the HPA KEDA manages cannot read its metric, and the
worker stays at whatever replica count it has. No `fallback` is
configured on the ScaledObject.

It is not an outage: the workers keep running and checks happen. It just
never scales, and the only signal is error conditions on the
ScaledObject and lines in the KEDA operator's log, neither of which our
alerts watch.

### Observation 3: KEDA's Temporal scaler supports mTLS and a static API key, nothing else

From the scaler's metadata and auth parameters:

| Auth parameter | Source | Notes |
| --- | --- | --- |
| `cert`, `key`, `ca`, `keyPassword` | TriggerAuthentication | standard mTLS |
| `apiKey` / `apiKeyFromEnv` | TriggerAuthentication | passed to `sdk.NewAPIKeyStaticCredentials`: a fixed bearer token |
| `enableTLS` | trigger metadata | TLS with an API key (added in KEDA 2.21) |
| `tlsServerName`, `unsafeSsl` | trigger metadata | server verification |

The mTLS path is enough for the mTLS case: the worker's Secret
(`temporal.tls.existingSecret`, keys `tls.crt`, `tls.key`, `ca.crt`)
maps onto it one to one. The chart just never wires it up.

OIDC is a different problem. A Keycloak access token *is* a bearer
token, so `apiKey` would accept one, but access tokens expire after
minutes. A static key does not fit them.

Nor does mTLS help on a cluster that authorizes JWTs, such as the
homelab's. A client presenting only a certificate carries no token, gets
no roles, and is denied (Observation 9). KEDA's Temporal trigger
therefore works on a JWT-authorized cluster only if the server is also
changed to authorize certificate identities.

### Observation 4: a rotating token would self-heal, because KEDA rebuilds a failing scaler

`ScalersCache.GetMetricsAndActivityForScaler`: when a scaler's
`GetMetricsAndActivity` returns an error, KEDA calls `refreshScaler`,
which rebuilds the scaler from its factory and so re-resolves the
TriggerAuthentication. It then retries once:

```go
metric, activity, err := sb.Scaler.GetMetricsAndActivity(requestCtx, metricName)
if err == nil {
    return metric, activity, time.Since(startTime), nil
}

ns, err := c.refreshScaler(ctx, index)
```

So if something keeps a Secret's token fresh, an expired token costs one
failed poll and then picks up the new one. That makes a refresher
(Option C below) workable, not just theoretical.

### Observation 5: worker versioning does not hide the backlog

The ScaledObject sets neither of KEDA's Worker Deployment fields
(`workerDeploymentName`, `workerDeploymentBuildId`), so KEDA uses its
unversioned path: `DescribeTaskQueue` with `ReportStats: true` and no
version selector. Our status page reads the backlog the same way
(`internal/temporal/backlog.go`). KEDA's own source suggests that path
reads only the unversioned queue: its running-workflow query in that
mode filters on `TemporalWorkerDeploymentVersion is null`. If so, the
backlog for our versioned workers would always read near zero.

Measured on the dev server with a throwaway integration test:

1. start a versioned worker at build `1.0.0`, promote it with
   `PromoteBuild`, then stop it;
2. start 7 workflows on the task queue, with no poller;
3. every 4s, read the backlog both ways.

```text
round 0: unversioned DescribeTaskQueue backlog=7 age=4.0s  | DescribeWorkerDeploymentVersion backlog=7
round 1: unversioned DescribeTaskQueue backlog=7 age=4.0s  | DescribeWorkerDeploymentVersion backlog=7
round 2: unversioned DescribeTaskQueue backlog=7 age=12.0s | DescribeWorkerDeploymentVersion backlog=7
round 3: unversioned DescribeTaskQueue backlog=7 age=12.0s | DescribeWorkerDeploymentVersion backlog=7
```

The unversioned call reports the current version's backlog. Neither
KEDA's default mode nor our status page needs a version selector,
as long as there is one current version. Two caveats:

- We never ramp between versions (each build becomes current
  outright), so a split backlog across two versions does not arise.
- This was measured on the CLI dev server, not on the homelab's 1.32
  cluster. The dev server runs the same matching service, but a
  one-off check on the homelab would close the gap.

### Observation 6: KEDA 2.21 removed settings we do not use, and added one we must not

- KEDA 2.21 removed `buildId`, `selectAllActive` and `selectUnversioned`,
  because Temporal is dropping the rules-based versioning APIs. The
  chart uses none of them, so it is compatible.
- KEDA 2.20 added Worker Deployment fields (`workerDeploymentName`,
  `workerDeploymentBuildId`). We do not need them (Observation 5).
  Setting them would also tie the ScaledObject to a single build ID,
  which changes on every release.
- KEDA 2.21 added a composite metric: backlog plus the count of running
  workflows. **This must never be enabled here.** Every repository is a
  permanently running `RepoWorkflow`, so the running count is roughly
  the fleet size, and the scaler would always demand `maxReplicas`.

### Observation 7: autoscaling buys burst latency, not throughput

INV-0018 Observation 7 still holds under v2, and more strongly. Checks
are bound by each installation's GitHub budget, which the
`InstallationWorkflow` enforces. A check denied a lease waits on a
workflow timer, and a timer is not backlog, so a fleet throttled on
budget correctly reads as idle. More pods past the budget only add
waiting.

What scaling does buy is shorter bursts: a policy rollout, onboarding a
large org, or a push storm, where activity tasks are ready but every
slot is busy. `maxReplicas` should be sized from the budget
(roughly: budget-limited checks per hour divided by checks per pod per
hour), not from the backlog. The chart's values comment already says
this; the operator docs do not.

### Observation 8: the Temporal server already exports the backlog to Prometheus

KEDA's Temporal trigger is not the only way to read the backlog, and not
the conventional one. Before the trigger existed (KEDA 2.17), Temporal
workers were scaled on Prometheus metrics, and that remains the fallback
wherever the trigger's auth does not fit.

The server publishes the number we need. In
`common/metrics/metric_defs.go`, the matching service defines
`approximate_backlog_count` and `approximate_backlog_age_seconds` as
gauges. `service/matching/task_queue_partition_manager.go` records them
per partition and per priority, with these labels from
`common/metrics/tags.go`:

| Label | Example |
| --- | --- |
| `namespace` | `repo-guardian` |
| `taskqueue` | `repo-guardian` |
| `task_type` | `Workflow`, `Activity` |
| `partition` | partition ID, or `__sticky__` |
| `worker_version` / `worker_build_id` | the Worker Deployment version |

They are on by default. The dynamic-config settings
`metrics.breakdownByTaskQueue` and `metrics.breakdownByPartition` both
default to `true`, and turning either off removes these gauges. They are
fed by the same task-queue stats `DescribeTaskQueue` returns, so the
Observation 5 measurement applies to them too.

That makes this a trigger with no Temporal credentials and no
repo-guardian code:

```yaml
triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus.monitoring:9090
      query: |
        sum(max by (partition, task_type) (
          approximate_backlog_count{namespace="repo-guardian", taskqueue="repo-guardian"}
        ))
      threshold: "50"
```

The inner `max by (partition, task_type)` keeps a partition from being
counted twice while it moves between matching hosts. The metric may
carry a prefix depending on how the server's Prometheus reporter is
configured, so the chart should take the query as a value, not hard-code
it.

The only requirement is that the Temporal server's metrics reach a
Prometheus KEDA can query. That holds when you run Temporal yourself and
scrape it, as the homelab does. It does not hold for Temporal Cloud, or
for a shared cluster another team runs without exposing its metrics.
Only then is a worker-exported gauge (Option B2) needed.

### Observation 9: Temporal OSS has exactly one built-in authorizer, and it reads JWTs

This finding reframes the auth question. In the server source
(`common/authorization/`):

- `GetClaimMapperFromConfig` accepts `claimMapper: ""` (no-op) or
  `"default"` (the JWT claim mapper). `GetAuthorizerFromConfig` accepts
  `authorizer: ""` (no-op: allow everything) or `"default"`. Any other
  mapper or authorizer means compiling a custom server binary.
- The default JWT claim mapper reads only `AuthToken`. It never looks at
  `TLSSubject`, although `AuthInfo` carries it.
- The default authorizer denies every call from a caller without claims,
  except health checks:

  ```go
  if IsHealthCheckAPI(target.APIName) {
      return resultAllow, nil
  }
  if claims == nil {
      return resultDeny, nil
  }
  ```

  A caller with a token but no matching role gets zero roles, and is
  denied as well.

So on self-hosted Temporal, mTLS and JWTs are not alternative ways of
doing the same job. They are layers:

| Layer | Mechanism | Answers |
| --- | --- | --- |
| Transport | TLS, or mTLS | is the connection encrypted, and does the client hold a cert from a trusted CA? |
| Authorization | JWT claim mapper and default authorizer | what may this caller do, in which namespace? |

"mTLS for workers, OIDC only for people logging in" therefore has a
specific meaning on OSS Temporal: **frontend authorization is turned
off** (no-op authorizer), and holding a client certificate from the CA
grants full access to every namespace, including cluster-level
operations. The Temporal UI's OIDC login then gates humans at the UI
only. The UI's own connection to the frontend carries full power.

What switching the homelab workers from OIDC to mTLS would trade:

| | Workers on OIDC (today) | Workers on mTLS, no-op authorizer |
| --- | --- | --- |
| Least privilege | the token grants `repo-guardian:write` on one namespace | any certificate is cluster admin |
| Dev and prod in one cluster | a dev token cannot touch the prod namespace | a dev certificate can |
| Revocation | disable the client in the IdP; tokens die within their lifetime | rotate the CA, or wait for expiry; Temporal checks no CRL |
| Audit | IdP logs every token issued; the frontend sees a principal | the frontend sees a certificate subject only |
| Runtime dependency | the IdP must be up to renew tokens (one minute early); an IdP outage stops workers within one token lifetime | none once the certificate is issued |
| Rotation | automatic (token refresh) | automatic with cert-manager |
| Tooling | KEDA's Temporal trigger cannot authenticate; the `temporal` CLI needs a token | KEDA's trigger and the CLI work with the certificate |
| Per-user authorization in the Temporal UI | possible: the UI can forward each user's token | impossible: the authorizer is off |

Keeping fine-grained authorization *and* using certificates means a
custom claim mapper that turns a certificate subject into claims. That
in turn means building and running your own Temporal server binary.

Temporal Cloud is different: client certificates and API keys there are
scoped per namespace natively. That is why ecosystem tools assume
"mTLS or an API key". It is not a sign that JWT client auth is
unsupported on OSS. The Go SDK supports it directly
(`client.NewAPIKeyDynamicCredentials`, which repo-guardian uses), and
the JWT authorizer is the only fine-grained authorization OSS Temporal
ships. The friction is in tools built for Cloud and mTLS, KEDA among
them, not in the server or the SDK.

**With Okta instead of Keycloak.** The Temporal side is unchanged: it
needs a JWKS URL and a permissions claim. The IdP side is harder,
subject to your Okta SKU:

- A client-credentials token with a custom `permissions` claim
  typically needs an Okta custom authorization server, which is part of
  API Access Management, a paid add-on.
- Okta's org authorization server issues tokens only for Okta's own
  APIs.
- repo-guardian's API has the same need: a custom audience
  (`repo-guardian-api`) and a groups claim in access tokens.

An organisation on Okta without API Access Management would find mTLS
with a no-op authorizer the practical choice for Temporal. It would
still need a custom authorization server for the repo-guardian API.

### Observation 10: the UI does not see the backlog in split topology, with or without KEDA

The status page has a `backlog` component (`internal/api/status.go`),
fed by `temporal.DescribeBacklog`. `cmd/repo-guardian/api.go` wires that
probe only when the process holds a Temporal client:

```go
if tc != nil {
    statusCfg.Backlog = func(ctx context.Context) (api.Backlog, error) {
        age, pollers, err := temporal.DescribeBacklog(ctx, tc, tcfg)
        ...
```

Only the `all` role has one. The `api` role in `split` topology, the
default, deliberately holds no Temporal credentials (DESIGN-0027:
least privilege, read-only Postgres only). So in split, the backlog
component is **omitted from the status page today**.

The choice of KEDA trigger is independent of this. KEDA reads
Prometheus, the UI reads the API, and neither path feeds or blocks the
other. Choosing the Prometheus trigger makes the UI no worse off. It
also does not fix the gap. Ways to fix it:

| | How | Fits the role model? |
| --- | --- | --- |
| (a) | The worker writes the backlog (count, age, pollers) to a small Postgres row on each poll; the API reads it like every other status input | yes: the API keeps reading Postgres only |
| (b) | Give the `api` role read-only Temporal credentials | weakens the api role's least privilege |
| (c) | The API queries Prometheus | adds a runtime dependency the API does not have today |

(a) matches the design: the worker already holds the credentials, and
the API stays read-only Postgres. It also replaces Option B2's
worker-exported gauge, if that is ever needed, with one poll that feeds
both.

### Observation 11: mTLS and OIDC already work together, and certificates are ours to issue

The layered posture from Observation 9 needs no new code paths:

- `internal/temporal/temporal.go.tlsConfig` loads the client
  certificate when `TEMPORAL_TLS_CERT_PATH` is set, and `Dial` attaches
  the OIDC token source whenever `TEMPORAL_OIDC_*` is set. Neither
  excludes the other.
- `repo-guardian.validateTemporalAuth` in `_helpers.tpl` allows
  `temporal.tls.existingSecret` together with `temporal.auth.oidc`. It
  only refuses `caSecret` alongside `existingSecret`, and KEDA alongside
  OIDC.

With both set, the frontend checks the certificate at the TLS handshake
(transport: is this a workload we issued a certificate to?) and the
token on each call (authorization: what may it do, in which namespace?).
KEDA, on the Prometheus trigger, needs neither.

The identity provider does not supply the certificate. Keycloak can
check client certificates for its own logins, and can bind a token to a
certificate (RFC 8705), but Temporal never checks that binding. Okta's
certificate features cover employee devices, not workloads. A
certificate authority for Temporal clients is ours to run:

| | Issuer | Fits |
| --- | --- | --- |
| cert-manager with a CA Issuer | cert-manager, with the CA key in a k8s Secret | quickest to stand up |
| **cert-manager backed by OpenBao** | OpenBao's PKI engine, through cert-manager's Vault issuer (OpenBao keeps Vault's API) | CA key outside k8s, audited issuing, per-role limits on the names a certificate may carry |
| OpenBao directly | OpenBao agent or CSI driver, with pods authenticating by service account | sites already running the OpenBao agent everywhere |

The middle row is the chosen posture:

1. A dedicated PKI mount in OpenBao, used only for Temporal clients. At
   the transport layer every certificate it signs is admitted, so
   sharing the CA with anything else widens who gets through.
2. An OpenBao role per workload, limiting the common names and lifetime
   it may request.
3. cert-manager's Vault `Issuer` against that role, and a `Certificate`
   writing the Secret named in `temporal.tls.existingSecret`.
   trust-manager can distribute the CA certificate.
4. The Temporal frontend requires client certificates and trusts only
   that CA. Its TLS settings can reload certificate files on an
   interval; confirm the setting name in the pinned Temporal chart.
5. `temporal.auth.oidc` stays as it is.

OpenBao can later also hold the Keycloak client secret, so a worker's
two credentials come from one place.

### Observation 12: the client certificate is read once, so rotation needs a restart

`tlsConfig` calls `tls.LoadX509KeyPair` once and stores the result in
`tls.Config.Certificates`. The connection is built at `Dial`, at
startup. When cert-manager renews the certificate, kubelet updates the
mounted file, but the process keeps presenting the old certificate until
the pod restarts. Once that certificate expires, the frontend refuses
the handshake and the worker stops.

Short-lived certificates make this certain rather than possible:
OpenBao-issued certificates typically live hours to days. The usual
external workaround, a restart-on-Secret-change controller such as
Reloader, is another component for every operator to install, and it
restarts workers mid-check.

The fix belongs in the client. Set `tls.Config.GetClientCertificate` to
a loader that re-reads the key pair when the files change (checking
modification time, and keeping the last good pair if a read fails
mid-rotation). Go calls it on every handshake, so a reconnect picks up
the new certificate with no restart. The CA bundle has the same shape
and needs the same treatment through `VerifyPeerCertificate` or a
reloaded `RootCAs`, because a CA rollover is the other half of rotation.

## Options

| | Approach | Works with | Cost | Verdict |
| --- | --- | --- | --- | --- |
| **A** | **Wire mTLS into the ScaledObject.** Render a `TriggerAuthentication` from `temporal.tls.existingSecret` (`cert`, `key`, `ca`) and set `tlsServerName` from `temporal.tls.serverName`. | mTLS | chart only, small | Do it: it is a bug fix |
| **B1** | **KEDA's `prometheus` trigger on the server's `approximate_backlog_count`** (Observation 8). | any auth | chart only: a `worker.keda.trigger` choice, a Prometheus address and a query value; needs the server's metrics in Prometheus | **Recommended** |
| **B2** | **The worker exports the backlog itself.** A goroutine calls `DescribeTaskQueue` with the worker's authenticated client and publishes it, as a gauge or as the Postgres row of Observation 10 (a). | any auth | one goroutine; needs Prometheus for KEDA | Only where the server's metrics are out of reach (Temporal Cloud, someone else's cluster) |
| **C** | **Token-refresher CronJob.** Mint a token every few minutes into the Secret the TriggerAuthentication reads as `apiKey`, with `enableTLS: true`. Self-heals per Observation 4. | OIDC | a new pod holding the client secret plus RBAC to write a Secret; a second Keycloak client (ideally read-only on the namespace) | Workable, but more moving parts and more credential surface than B1 |
| **D** | **Long-lived token.** A Keycloak client with a token lifetime of months, stored as `apiKey`. | OIDC | a long-lived Temporal credential in a Secret | No |
| **E** | **Do nothing.** Keep KEDA off, set replicas by hand. | any | none | Fine at current fleet size; the bug in Observation 2 stays |
| **F** | **Move the cluster's workers to mTLS** to suit KEDA's trigger. | mTLS | on a JWT-authorized cluster, either turn off frontend authorization or build a custom claim mapper (Observation 9) | No: it trades least privilege for one tool's convenience, and B1 makes it unnecessary |

The server's backlog gauge also serves dashboards and alerts better
than the SDK's schedule-to-start latency, which only moves once tasks
are picked up. Neither needs repo-guardian code.

## Conclusion

**Answer:** No, not as shipped. KEDA works today only against a
plaintext, unauthenticated Temporal frontend:

- **mTLS:** the chart renders, but KEDA cannot connect, and it fails
  silently (Observations 1 and 2).
- **OIDC:** refused at render.
- **Versioning:** the worry was unfounded; the backlog is visible
  without a version selector (Observation 5).

The mTLS gap is a chart bug with a chart-only fix (Option A). OIDC needs
the backlog to reach KEDA without Temporal credentials, and the Temporal
server already publishes it to Prometheus (Option B1, Observation 8). A
rotating token (Option C) would also work, but it costs more than it is
worth.

The broader worry, that we chose an unsupported auth pattern, does not
hold up (Observation 9). On OSS Temporal, JWT authorization is the only
built-in fine-grained authorization, and the SDK supports JWT clients
directly. mTLS is the transport layer, not a substitute for it. Moving
the workers to mTLS alone means turning frontend authorization off.
Ecosystem tools assume mTLS or API keys because Temporal Cloud scopes
those per namespace; that is where KEDA's gap comes from. The homelab
should keep OIDC worker auth. If anything, it could add client
certificates on top of it, for the full layered posture.

The UI is unaffected by the trigger choice. In split topology it lacks
the backlog today for an unrelated reason (Observation 10).

## Recommendation

For an rc.5:

1. **Option A:** render a `TriggerAuthentication` for mTLS. Add a
   helm-unittest case per Temporal auth mode, asserting the ScaledObject
   references it under mTLS and needs nothing under plaintext.
2. **Option B1:** a `worker.keda.trigger` value (`temporal` |
   `prometheus`) with `worker.keda.prometheus.{serverAddress, query,
   threshold}`, defaulting the query to the Observation 8 expression.
   Relax the OIDC + KEDA guard to refuse only `trigger: temporal`. No Go
   code.
3. **Guard the composite metric:** never render it, and say why in the
   template.
4. **Docs:** add KEDA sizing guidance (`maxReplicas` from the budget) to
   the chart values and `docs/operations/v2-onboarding.md` § Day-two.
5. **Close the measurement gap:** run the Observation 5 check once
   against the homelab 1.32 cluster. In the same pass, confirm the
   server's `approximate_backlog_count` name and labels in the homelab
   Prometheus.
6. **Keep OIDC worker auth, and add mTLS beneath it** (Observation 11).
   Document the posture (cert-manager backed by OpenBao) and the
   Observation 9 trade-off in `docs/operations/v2-onboarding.md`, with
   the OpenBao role, Issuer and Certificate as a worked example.
7. **Reload the client certificate and CA on rotation**
   (Observation 12). Go change plus a test that rotates the files under
   a live client and asserts the next handshake presents the new
   certificate. No Reloader requirement.
8. **Separately, and later:** fix the status page's backlog in split
   topology with Observation 10 (a).

Until then: leave `worker.keda.enabled: false` and set
`worker.replicas` by hand.

### Open questions

- **OQ1: Which auth paths should KEDA support?**
  - (a) ✅ recommended: A + B1. mTLS through KEDA's own scaler, everything
    else through the server's backlog metric. Covers every auth mode
    with no new credentials and no Go code.
  - (b) B1 only. Simplest. mTLS users also go through Prometheus, and
    the Observation 2 bug is fixed by removing the `temporal` trigger.
  - (c) A + C. OIDC through a token refresher; no Prometheus dependency,
    but a new credential-holding workload.
  - other:

- **OQ2: Should the Prometheus trigger replace the Temporal trigger
  entirely?**
  - (a) ✅ recommended: no. Offer both, defaulting to `temporal`. The
    native scaler has no scrape lag and no Prometheus dependency where
    mTLS works.
  - (b) Yes. One code path, but Prometheus becomes mandatory for
    autoscaling.
  - other:

- **OQ3: How should the UI get the backlog in split topology?**
  - (a) ✅ recommended: the worker writes it to Postgres and the API reads
    it (Observation 10 (a)). Keeps the api role read-only Postgres.
  - (b) Read-only Temporal credentials for the api role. Least code,
    weaker least privilege.
  - (c) Leave it: the backlog shows only in `all` topology and in
    Grafana.
  - other:

- **OQ4: Should the homelab move Temporal worker auth from OIDC to
  mTLS?**
  - (a) ✅ decided: neither alone; both. Keep OIDC for authorization
    (namespace scope, IdP revocation and audit) and add client
    certificates from cert-manager backed by OpenBao for the transport
    layer (Observation 11). B1 keeps KEDA out of both.
  - (b) Yes, with a no-op authorizer. Simpler tooling and no IdP
    runtime dependency, but any certificate is cluster admin, and dev
    and prod are not isolated.
  - (c) Yes, with a custom claim mapper. Keeps authorization, but means
    building and running a custom Temporal server.
  - other:

## Future work: a shared Temporal auth package

The pattern settled here, OIDC for authorization, mTLS from cert-manager
backed by OpenBao for transport, hot certificate reload, and KEDA scaling
from server metrics, is likely to be the default for every Temporal
workload, not only repo-guardian. Much of the GitHub App plumbing
(installation-scoped clients, the rate-limit transport, webhook HMAC
validation) is similarly reusable.

Once the rc ships and the posture is proven in the homelab, extract the
reusable parts into a shared module, such as `donaldgifford/x`:

- Temporal client dialing: `ConfigFromEnv`, mTLS with hot reload, the
  OIDC token source, the server version check.
- The GitHub App client stack and webhook validation.
- Chart snippets or docs for the OpenBao issuer and the KEDA Prometheus
  trigger.

This could be the last step of the v2 beta before 2.0.0. It is out of
scope for this investigation; it gets its own design doc when started.

## References

- [DESIGN-0026](../design/0026-v2-temporal-control-plane-and-role-split.md):
  § Scaling, OQ14 (KEDA optional).
- [INV-0018](0018-repo-guardian-v2-role-split-business-state-and-a-platform-ui.md):
  Observation 7, autoscaling buys burst latency, not throughput.
- [IMPL-0025](../impl/0025-v2-findings-model-temporal-control-plane-read-only-api-and-ui.md):
  Phase 17 (chart roles, ScaledObject); rc.2 (Temporal OIDC auth).
- `charts/repo-guardian/templates/worker-scaledobject.yaml`;
  `validateTemporalAuth` in `charts/repo-guardian/templates/_helpers.tpl`.
- `internal/temporal/backlog.go`, the status page's backlog probe.
- KEDA Temporal scaler:
  [docs](https://keda.sh/docs/2.18/scalers/temporal/) ·
  [`pkg/scalers/temporal_scaler.go`](https://github.com/kedacore/keda/blob/main/pkg/scalers/temporal_scaler.go) ·
  [`pkg/scaling/cache/scalers_cache.go`](https://github.com/kedacore/keda/blob/main/pkg/scaling/cache/scalers_cache.go).
- Temporal server: `common/metrics/metric_defs.go`
  (`approximate_backlog_count`), `common/dynamicconfig/constants.go`
  (`metrics.breakdownByTaskQueue`), `common/authorization/`
  (`default_authorizer.go`, `default_jwt_claim_mapper.go`,
  `claim_mapper.go`, `authorizer.go`).
- `cmd/repo-guardian/api.go`, where the backlog probe is wired only with
  a Temporal client.
- KEDA changes: #7672 (Worker Deployment versions), #7460 (composite
  metric), #7854 (`enableTLS`), #7985 (removed `buildId`,
  `selectAllActive`, `selectUnversioned`).
