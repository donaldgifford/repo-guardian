# Turning on repo-guardian v2

A runbook for a **new** v2 install: what has to exist first, the steps
to turn it on, how organisations and repositories are onboarded, and how
to change rules and follow a change through the fleet. Upgrading an
existing v1 install is a different path, in
[v1 → v2 migration](v2-migration.md). For what repo-guardian enforces
and why, read [repo-guardian and v2 at a glance](v2-overview.md) first.

<!--toc:start-->
- [What you end up with](#what-you-end-up-with)
- [Prerequisites](#prerequisites)
  - [1. Kubernetes and Helm](#1-kubernetes-and-helm)
  - [2. A Temporal cluster](#2-a-temporal-cluster)
  - [3. Postgres](#3-postgres)
  - [4. The GitHub App](#4-the-github-app)
  - [5. A public route for webhooks](#5-a-public-route-for-webhooks)
  - [6. An OIDC identity provider (API and UI only)](#6-an-oidc-identity-provider-api-and-ui-only)
  - [7. Prometheus and Grafana (optional)](#7-prometheus-and-grafana-optional)
  - [8. A first policy](#8-a-first-policy)
- [Turning it on](#turning-it-on)
- [Onboarding organisations and repositories](#onboarding-organisations-and-repositories)
- [Changing rules](#changing-rules)
- [Tracking a change through the fleet](#tracking-a-change-through-the-fleet)
- [Day-two operations](#day-two-operations)
- [Checklist](#checklist)
<!--toc:end-->

## What you end up with

```text
GitHub ──webhook──▶ ingest ──▶ Temporal ──▶ worker ──▶ GitHub API
                                              │
                                              ▼
                  UI ──▶ api (read-only) ──▶ Postgres
```

| Role | Holds | Does |
| --- | --- | --- |
| `ingest` | webhook secret, Temporal client | validates webhooks and starts workflows; holds no App key and no database access |
| `worker` | App private key, Postgres, Temporal client | runs checks, opens PRs, writes findings |
| `api` (optional) | read-only Postgres role | serves findings over REST behind OIDC |
| `ui` (optional) | OIDC client secret, session keys | the web UI and public status page |

Everything is one Helm chart, `oci://ghcr.io/donaldgifford/charts/repo-guardian`,
with `topology: split` (one Deployment per role, the default) or `all`
(one Deployment running ingest and worker together, for small installs).

## Prerequisites

Work through these in order. Each ends with what to record for the
values file.

### 1. Kubernetes and Helm

- A cluster to run in, and a namespace (`repo-guardian` below).
- Helm, able to pull OCI charts from `ghcr.io`.
- A way to create Secrets: External Secrets, 1Password operator, or
  `kubectl`. Every credential below is referenced from a Secret, never
  written into values.

### 2. A Temporal cluster

v2 runs every check as a Temporal workflow, so Temporal is required, not
optional. It is shared infrastructure and is not part of this chart.

- **Server 1.31 or newer.** The worker refuses to start against an older
  frontend. `contrib/temporal/` is a tested reference install (upstream
  chart 1.7.0, server 1.32), with CNPG persistence and a choice of
  visibility store.
- **A namespace** for repo-guardian, default `repo-guardian`.
  `contrib/temporal/namespace-job.yaml` creates it with 7-day retention.
- **Reachability** from the repo-guardian namespace to the frontend
  (port 7233). The reference `networkpolicy.yaml` admits namespaces
  labelled `repo-guardian.io/temporal-client=true`.
- **Authentication**, one or both (both is the recommended posture: see
  [Temporal client auth](temporal-client-auth.md)):
  - **mTLS:** a client certificate from the frontend's CA, in a Secret
    with `tls.crt`, `tls.key` and `ca.crt`, or issued and renewed by
    cert-manager (`temporal.tls.certManager`). Renewals are picked up
    without a restart.
  - **OIDC (JWT authorizer, for example Keycloak):** an OAuth2
    client-credentials client whose token grants write on the namespace
    (for example `repo-guardian:write`). No cluster-scoped role is
    needed. The client secret goes in a Secret under `client-secret`.
    If the frontend's certificate is from a private CA, put that CA in
    a Secret under `ca.crt`.

Record: `temporal.address`, `temporal.namespace`, and
`temporal.tls.existingSecret` or `temporal.tls.certManager.issuerRef.name`,
and/or `temporal.auth.oidc.{tokenUrl, clientId, existingSecret}` (plus
`temporal.tls.caSecret` for OIDC alone against a private CA).

> **Autoscaling the worker.** `worker.keda.enabled` defaults to KEDA's
> Prometheus trigger on the Temporal server's `approximate_backlog_count`.
> KEDA then never dials Temporal, so it works under any client auth.
> Record `worker.keda.prometheus.serverAddress` (required). Size
> `worker.keda.maxReplicas` to the GitHub budget, not the backlog:
> checks are rate-limit bound, so extra pods only wait on more timers
> (INV-0018 Observation 7). KEDA's own Temporal trigger
> (`trigger: temporal`) is for mTLS-only clusters, because it cannot
> present an OIDC token.

### 3. Postgres

Findings, repositories, history and policy versions live in Postgres.
Choose a mode:

| `store.postgres.mode` | You provide | Good for |
| --- | --- | --- |
| `baked` (default) | nothing; the chart runs a single-pod Postgres | trying it out, homelab |
| `cnpg` | the CloudNativePG operator | production on Kubernetes |
| `external` | a database, a DSN Secret (`STORE_DSN`), and, with the API enabled, a read-only login in a second Secret (`STORE_RO_DSN`) | RDS or another managed Postgres |

For GitOps with `baked`, set `store.postgres.baked.existingSecret`. The
generated password otherwise changes on every `helm template` render.

Schema migrations run automatically as a Helm hook Job
(`migrate.enabled`, on by default).

### 4. The GitHub App

Follow [the enterprise setup guide](ent-setup.md) for the full procedure.
In short:

1. **Register an App** with its webhook **active**, pointing at the URL
   you will expose in step 5. Generate a strong **webhook secret**.
2. **Subscribe to events:** Push, Repository, Installation repositories.
3. **Grant permissions.** The core set is always required. Grant the
   others only for features your policy uses:

   | Permission | Access | Needed for |
   | --- | --- | --- |
   | Contents | Read & write | every file rule |
   | Pull requests | Read & write | every file rule |
   | Metadata | Read | always (mandatory) |
   | Administration | Read & write | `rule "setting"` and `rule "branch_protection"` remediation |
   | Issues | Read & write | `label_sync` |
   | Workflows | Read & write | any rule that writes `.github/workflows/*` |
   | Custom properties (repository) | Read & write | `custom_properties` in `api` mode |
   | Custom properties (organisation) | Read | the custom-property schema check (optional) |

4. **Generate a private key** and store it in your secrets backend.
5. **Install the App** into each organisation, on all repositories or a
   selection. You can install into more organisations at any time; see
   [Onboarding organisations](#onboarding-organisations-and-repositories).

Record: `config.appId`, and a Secret holding the webhook secret and the
private key (`secrets.existingSecret`, or `secrets.webhookSecret` and
`secrets.privateKey` for a quick trial).

### 5. A public route for webhooks

GitHub must reach the ingest Service at `POST /webhooks/github`. The
chart deliberately ships no webhook Ingress: pick an option from
[webhook ingress](ingress.md) (AWS load balancer, Tailscale Funnel,
Cloudflare Tunnel, ngrok). The webhook's HMAC signature is the only
check the app itself makes. If you want to restrict source IPs to
GitHub's hook ranges, do it at your edge.

The Service keeps the release's full name (`repo-guardian` for a
release of that name), port 8080.

### 6. An OIDC identity provider (API and UI only)

Skip this if you do not enable `api` or `ui`.

- **Issuer** URL (`api.auth.issuer`). The API and UI trust the same one.
- **API audience:** tokens must carry `aud` = `repo-guardian-api`
  (`api.auth.audience`), and a groups claim (`groups` by default).
- **UI client:** a confidential client (`repo-guardian-ui` by default)
  with redirect URI `https://<ui host>/auth/callback`. Put its secret
  and one or more random session keys (32+ bytes each, comma-separated,
  newest first so keys can be rotated) in a Secret with keys `oidc-client-secret` and `session-keys`
  (`ui.existingSecret`).
- **Who sees what:** `api.authz.groups` maps IdP groups to the
  organisations they may read (`["*"]` for all). A caller with no
  mapped organisation gets 403 on everything except `/me` and `/status`.

### 7. Prometheus and Grafana (optional)

Every pod exposes `/metrics` on port 9090. Dashboards and alerts are
generated from your policy rather than shipped by hand:

```bash
repo-guardian monitoring generate --config guardian.hcl --format k8s \
  --namespace monitoring --instance-selector dashboards=grafana \
  --name repo-guardian --prometheus-selector 'namespace="repo-guardian"'
```

See [generating dashboards and alerts](monitoring-generation.md). The
chart can also render a `PrometheusRule` of its own.

### 8. A first policy

Keep the first policy small and non-destructive. The built-in defaults
(`codeowners`, `dependabot`) are a reasonable start; the
[policy reference](../usage/policy-reference.md) and
[`examples/`](https://github.com/donaldgifford/repo-guardian/tree/main/examples)
have more.

```hcl
guardian {
  dry_run = true # report only; nothing is written to GitHub
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"
}

rule "setting" "vulnerability_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = false # report only, even after dry run is off
}
```

Validate it before deploying. `monitoring generate` runs the same
loader and validation the server does, and fails on any error:

```bash
repo-guardian monitoring generate --config guardian.hcl --out /tmp/check
```

Keep the policy in git. Every later change goes through review.

## Turning it on

1. **Create the Secrets** from prerequisites 2–6 in the release
   namespace.

2. **Write the values file:**

   ```yaml
   topology: split

   config:
     appId: "123456"
     dryRun: true # remove once the findings look right

   secrets:
     create: false
     existingSecret: repo-guardian-github # webhook secret + private key

   temporal:
     address: temporal-frontend.temporal.svc:7233
     namespace: repo-guardian
     tls:
       existingSecret: repo-guardian-temporal-tls

   store:
     postgres:
       mode: cnpg

   policy:
     existingConfigMap: repo-guardian-policy # or policy.config: |- ...

   # Optional: the API and the UI.
   api:
     enabled: true
     auth:
       issuer: https://idp.example.com/realms/eng
     authz:
       groups:
         platform: ["*"]
   ui:
     enabled: true
     existingSecret: repo-guardian-ui
     ingress:
       enabled: true
       host: repo-guardian.example.com
   ```

3. **Install:**

   ```bash
   helm upgrade --install repo-guardian \
     oci://ghcr.io/donaldgifford/charts/repo-guardian \
     --version <chart version> -n repo-guardian -f values.yaml
   ```

   The migrate Job creates the schema. The worker then creates the
   `discovery` and `snapshot` schedules, records the policy version, and
   promotes its own build to Temporal's current worker version.

4. **Verify the service:**
   - `kubectl -n repo-guardian get pods`: ingest, worker (and api, ui)
     are Ready, and the migrate Job succeeded.
   - The worker's build is current. Temporal gives a versioned worker no
     work until it is, and a worker still not current two minutes after
     start fails its `deployment` readiness check:

     ```bash
     temporal worker deployment describe --deployment-name repo-guardian
     ```

   - In the Temporal UI, a `discovery` run has completed and there is one
     `repo/<id>` workflow per repository the App can see.

5. **Verify webhooks.** Push to any repository, then check the App's
   **Advanced → Recent Deliveries**:
   - **202:** accepted and started;
   - **204:** accepted, nothing to do (for example a tag push);
   - **401:** the webhook secret does not match;
   - **503:** Temporal is unreachable. GitHub keeps the delivery, and you
     can redeliver it.

6. **Review the dry-run findings.** Initial checks are spread across
   `checkInterval` (24h by default) so a large fleet does not hit the
   GitHub rate limit at once. Look at them in the UI, at
   `GET /api/v1/summary` and `/api/v1/rules`, or in a report:

   ```bash
   repo-guardian report --dsn "$STORE_DSN" --out ./reports
   ```

   Add `ignore` entries for legitimate exceptions and fix any rule that
   is wrong, before anything is written to GitHub.

7. **Leave dry run.** Remove `config.dryRun` (and `dry_run` from the
   policy) and upgrade. File rules start opening PRs; setting and
   branch-protection rules write only where `remediate = true`.

8. **Generate the dashboards and alerts** (prerequisite 7).

## Onboarding organisations and repositories

**A new organisation** is onboarded by installing the App into it:

1. If the policy has a top-level `scope { orgs = [...] }`, add the
   organisation there and to the `scope` of each rule that should apply,
   and roll out that policy change first. With strict scope, an
   organisation that no rule names is discovered but nothing is enforced
   on it.
2. Install the App into the organisation (all repositories, or a
   selection).
3. The `installation` webhook starts a discovery run for that
   installation at once; the hourly `discovery` schedule catches anything
   missed. Each repository gets a `repo/<id>` workflow, and first checks
   are spread across `checkInterval`.
4. Confirm it with `GET /api/v1/orgs/<org>`, the per-org rows on the
   dashboards, or `repo-guardian report --orgs <org>`.

**A new repository** in an installed organisation is picked up by the
`repository.created` webhook and checked without waiting for discovery.

**Archived and forked repositories** are parked, not checked, while they
stay archived or forked. **Removing** the App from a repository, or
deleting the repository, parks it. Parked repositories keep their
history and are excluded from compliance numbers.

**Offboarding an organisation** means uninstalling the App from it.
The installation is marked removed, and each repository is parked at
the latest when its next check finds it can no longer read it.

## Changing rules

Treat `guardian.hcl` like code.

1. **Propose the change as a PR** to the repository that holds the
   policy.
2. **Validate it in CI** with
   `repo-guardian monitoring generate --config guardian.hcl --out /tmp/check`,
   which fails on any policy error. If you use custom PR templates, set
   `templating.strict: true` in the chart so a template referencing a
   missing field fails startup instead of rendering blank.
3. **Introduce enforcement in stages.** A new setting or branch-protection
   rule starts with `remediate = false`. A new file rule has no per-rule
   switch, so either `scope` it to a test organisation first or run a
   short global dry run. Read the findings, add `ignore` entries, then
   widen the scope or turn remediation on.
4. **Grant new App permissions before the rule ships.** Some rules need
   a permission the App lacks (for example Administration for the first
   setting rule, or Workflows for a rule that writes a workflow file).
   Add it to the App, and have each organisation accept it, first.
5. **Merge and deploy.** The policy reaches the ingest and worker pods
   as a mounted file and is read at startup. With `policy.config` in
   values, a change rolls those pods automatically. With
   `policy.existingConfigMap`, the ConfigMap is yours, so restart them
   after updating it:
   `kubectl -n repo-guardian rollout restart deploy/repo-guardian-ingest deploy/repo-guardian-worker`.

What a change costs:

- **A change that can alter an outcome** (a rule, a scope, an ignore, a
  template) produces a new policy version. Every repository is re-checked
  once, spread across `policyRolloutWindow` (24h by default), with
  webhook-triggered checks still served first.
- **An operational change** (log level, concurrency, intervals) changes
  nothing about the policy version and triggers no re-check.

## Tracking a change through the fleet

| Question | Where to look |
| --- | --- |
| Which policy version is live, and has its rollout finished? | `GET /api/v1/policy`: version, rollout state and a summary of every rule (never template bodies) |
| How is one rule doing across the fleet? | `GET /api/v1/rules/<kind>/<name>`, or the rule's row in the UI and on the KPI dashboard |
| Which repositories fail it, and why? | `GET /api/v1/findings?…`: each finding has a status, a reason (`file_missing`, `assertion_failed`, `setting_mismatch`…) and a remediation (`pr_open`, `applied`, `dry_run`…) |
| What happened to one repository, and when? | `GET /api/v1/repositories/<id>/events`: every status change, rename, transfer and park |
| Is compliance trending up? | `GET /api/v1/compliance/history`, from daily snapshots (`posture.snapshotInterval`) |
| A report for someone without API access | `repo-guardian report`: one Markdown file per organisation, with no GitHub calls |
| Did the background jobs run? | the Temporal UI (`discovery`, `snapshot`, `policy-rollout/<version>` workflows); `/status` for overall health |

After a policy change, expect findings to move over the rollout window
rather than all at once. A rollout is complete when `/policy` reports it
finished; stragglers are re-signalled once at the end of the window.

## Day-two operations

- **Upgrades.** Bump the chart version. New workers promote their build
  to Temporal's current version when it is newer by semver, so in-flight
  workflows move to the new code without manual steps. **Rolling back**
  to an older 2.x needs one command, which the older pods print in their
  logs:
  `temporal worker deployment set-current-version --deployment-name repo-guardian --build-id <tag>`.
- **Throughput.** Checks are bound by GitHub's rate limit, not CPU. Each
  installation has its own budget, and webhook-triggered checks are
  served before scheduled ones. Raising `worker.concurrency` or replicas
  past what the budget allows only adds waiting.
- **Retention.** Check records are pruned after `checksRetention` (90
  days). Findings, their events and compliance history are kept.
- **Pausing writes.** Set `config.dryRun: true` and upgrade. Checks
  continue and findings stay current; nothing is written to GitHub.

## Checklist

Prerequisites:

- [ ] Kubernetes namespace, Helm, and a way to create Secrets
- [ ] Temporal ≥ 1.31 reachable on 7233, namespace created, mTLS or OIDC
      credentials with write on the namespace
- [ ] Postgres mode chosen; DSN Secrets for `external`
- [ ] GitHub App registered: webhook active, events subscribed,
      permissions granted, private key stored
- [ ] Public HTTPS route to `POST /webhooks/github`
- [ ] (API/UI) OIDC issuer, API audience, UI client, authz groups
- [ ] A first policy in git that validates and starts in dry run

Turn on:

- [ ] Secrets created, values written, chart installed
- [ ] Pods Ready, migrate Job succeeded, worker build current
- [ ] Discovery completed; one `repo/<id>` workflow per repository
- [ ] Webhook deliveries return 202 or 204
- [ ] Dry-run findings reviewed; exceptions added to `ignore`
- [ ] Dry run off; first PRs reviewed
- [ ] Dashboards and alerts generated and applied

Each new organisation:

- [ ] Added to `scope` (strict mode only)
- [ ] App installed, and any new permissions accepted
- [ ] Organisation visible in `/api/v1/orgs/<org>` with findings
