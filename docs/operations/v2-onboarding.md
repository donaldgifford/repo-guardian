# Turning on repo-guardian v2

A runbook for a **new** v2 install: what has to exist first, the steps
to turn it on, how organisations and repositories are onboarded, and how
to change policy and follow a change through the fleet. Upgrading an
existing v1 install is a different path, in
[v1 → v2 migration](v2-migration.md). For what repo-guardian enforces
and why, read [repo-guardian and v2 at a glance](v2-overview.md) first.

> **This release candidate evaluates only.** It reads every repository,
> records each control's result and reports compliance. It writes
> nothing to GitHub: no pull requests, no setting or ruleset changes.
> Remediation arrives with IMPL-0030. You can register and install the
> Remediation App now, and the remediator role will start, but it does
> no work yet.

<!--toc:start-->
- [What you end up with](#what-you-end-up-with)
- [Prerequisites](#prerequisites)
  - [1. Kubernetes and Helm](#1-kubernetes-and-helm)
  - [2. A Temporal cluster](#2-a-temporal-cluster)
  - [3. Postgres](#3-postgres)
  - [4. The two GitHub Apps](#4-the-two-github-apps)
  - [5. A public route for webhooks](#5-a-public-route-for-webhooks)
  - [6. An OIDC identity provider (API and UI only)](#6-an-oidc-identity-provider-api-and-ui-only)
  - [7. Prometheus and Grafana (optional)](#7-prometheus-and-grafana-optional)
  - [8. A first policy](#8-a-first-policy)
- [Turning it on](#turning-it-on)
- [Installing with Argo CD](#installing-with-argo-cd)
- [Onboarding organisations and repositories](#onboarding-organisations-and-repositories)
- [Changing policy](#changing-policy)
- [Tracking a change through the fleet](#tracking-a-change-through-the-fleet)
- [Day-two operations](#day-two-operations)
- [Checklist](#checklist)
<!--toc:end-->

## What you end up with

```text
                ┌─/webhooks/github/eval──────┐
GitHub ─────────┤                            ├──▶ ingest ──▶ Temporal ──┬──▶ evaluator  ──▶ GitHub API (read)
                └─/webhooks/github/remediate─┘                         └──▶ remediator ──▶ GitHub API (write, IMPL-0030)
                                                                                │
                                                                                ▼
                                          UI ──▶ api (read-only) ──────────▶ Postgres
```

| Role | Holds | Does |
| --- | --- | --- |
| `ingest` | both webhook secrets and App ids, Temporal client | validates webhooks and starts workflows; holds no App key and no database access |
| `evaluator` | Evaluation App private key, Postgres, Temporal client | discovers repositories, resolves the policy, evaluates controls, records results |
| `remediator` | Remediation App private key, Postgres, Temporal client | starts and reports ready; opens pull requests and applies API changes from IMPL-0030 |
| `api` (optional) | read-only Postgres role, optionally a Temporal client | serves compliance over REST behind OIDC |
| `ui` (optional) | OIDC client secret, session keys | the web UI and public status page |

The rc's single `worker` role is gone. The evaluator and the remediator
each run one Temporal worker on their own queue and their own worker
deployment:

| Role | Task queue | Temporal worker deployment | App |
| --- | --- | --- | --- |
| `evaluator` | `repo-guardian-eval` | `repo-guardian-eval` | Evaluation App |
| `remediator` | `repo-guardian-remediate` | `repo-guardian-remediate` | Remediation App |

Everything is one Helm chart, `oci://ghcr.io/donaldgifford/charts/repo-guardian`,
with `topology: split` (one Deployment per role, the default) or `all`
(one Deployment running every role, for small installs). In `split`,
the evaluator renders only when `github.eval.appId` is set and the
remediator only when `github.remediate.appId` is set. In `all`, the pod
runs each half only when its App is set. The API always runs in `all`,
on `api.port` (8081); `api.enabled` only adds its Service and auth.

## Prerequisites

Work through these in order. Each ends with what to record for the
values file.

### 1. Kubernetes and Helm

- A cluster to run in, and a namespace (`repo-guardian` below).
- Helm, able to pull OCI charts from `ghcr.io`.
- A way to create Secrets: External Secrets, 1Password operator, or
  `kubectl`. Every credential below is referenced from a Secret, never
  written into values. Private keys reach the pods as files.

### 2. A Temporal cluster

v2 runs every evaluation as a Temporal workflow, so Temporal is
required, not optional. It is shared infrastructure and is not part of
this chart.

- **Server 1.31 or newer.** The workers refuse to start against an older
  frontend. `contrib/temporal/` is a tested reference install (upstream
  chart 1.7.0, server 1.32), with CNPG persistence and a choice of
  visibility store.
- **A namespace** for repo-guardian, default `repo-guardian`.
  `contrib/temporal/namespace-job.yaml` creates it with 7-day retention.
- **Reachability** from the repo-guardian namespace to the frontend
  (port 7233). The reference `networkpolicy.yaml` admits namespaces
  labelled `repo-guardian.io/temporal-client=true`.
- **Authentication**, one of:
  - **mTLS:** a client certificate from the frontend's CA, in a Secret
    with `tls.crt`, `tls.key` and `ca.crt`.
  - **OIDC (JWT authorizer, for example Keycloak):** an OAuth2
    client-credentials client whose token grants write on the namespace
    (for example `repo-guardian:write`). No cluster-scoped role is
    needed. The client secret goes in a Secret under `client-secret`.
    If the frontend's certificate is from a private CA, put that CA in
    a Secret under `ca.crt`.

Record: `temporal.address`, `temporal.namespace`, and either
`temporal.tls.existingSecret` or
`temporal.auth.oidc.{tokenUrl, clientId, existingSecret}` (plus
`temporal.tls.caSecret` if needed).

Other Temporal values, and the combinations the chart refuses to render:

| Value | Use |
| --- | --- |
| `temporal.tls.serverName` | name to verify the frontend's certificate against, when it differs from `address` |
| `temporal.tls.caSecret` | OIDC only: a Secret with just `ca.crt`. Cannot be combined with `tls.existingSecret`; for mTLS, put `ca.crt` in that Secret instead |
| `temporal.tls.disabled` | plaintext, for a cluster-internal frontend with no TLS. Contradicts the three values above, and with OIDC the bearer token is readable on the wire |
| `temporal.auth.oidc.scopes` | scopes to request, if your IdP needs them |
| `temporal.auth.oidc.audience` | `audience` parameter, for IdPs that take one (Keycloak uses a client-scope mapper instead) |

> KEDA's Temporal scaler cannot mint OIDC tokens, so the chart refuses
> `keda.trigger: temporal` together with `temporal.auth.oidc`. The
> default `prometheus` trigger works with OIDC.

#### Autoscaling with KEDA

KEDA scales the evaluator and the remediator separately, one
ScaledObject per role, each on its own queue's backlog. Each needs the
KEDA CRDs, `topology: split`, and that role's App (`github.eval.appId`
or `github.remediate.appId`). The rc's `worker.keda` block is gone; the
chart refuses to render while it is still set.

```yaml
keda:
  trigger: prometheus                    # default; or temporal
  prometheus:
    serverAddress: http://prometheus.monitoring:9090   # required with prometheus
    authenticationRef: ""                # an operator-owned TriggerAuthentication
evaluator:
  keda:
    enabled: true
    minReplicas: 1
    maxReplicas: 4
    targetQueueSize: "50"
    fallbackReplicas: null               # null holds evaluator.replicas
    query: ""                            # empty builds the default below
remediator:
  keda:
    enabled: true
    minReplicas: 0                       # promotion does not need a running remediator
```

- **`prometheus` (default)** reads the Temporal server's
  `approximate_backlog_count` from your Prometheus, so it needs
  `keda.prometheus.serverAddress`. The default query per role is
  `sum(max by (partition, task_type, task_priority, worker_build_id)
  (approximate_backlog_count{exported_namespace="repo_guardian",
  taskqueue="repo_guardian_eval"}))` (`repo_guardian_remediate` for the
  remediator). Temporal sanitises label values, so `-` becomes `_` in
  both the namespace and the queue. The Temporal namespace is read from
  `exported_namespace` because a ServiceMonitor scrape renames
  Temporal's `namespace` label (the pod's namespace takes it). If your
  Prometheus keeps the original label (`honorLabels: true`, or a plain
  scrape config), set `keda.prometheus.namespaceLabel: namespace`.
  Override it per role with `<role>.keda.query` when your server's
  metrics carry a prefix.
- **`temporal`** asks the frontend directly. With
  `temporal.tls.existingSecret` the chart renders a TriggerAuthentication
  from its `tls.crt`, `tls.key` and `ca.crt`. It is refused with OIDC.
- **`fallback`**: when the trigger fails three polls in a row, KEDA holds
  the role at `fallbackReplicas`, or at the role's `replicas` when that is
  null, instead of going silent.

KEDA replaces the role's `replicas`. Size `maxReplicas` to the GitHub
rate limit, not the backlog: evaluations are rate-limit bound, so extra
pods only wait.

One worker value follows from Temporal:

- **`worker.buildId`** is the controls workers' Temporal build ID
  (`TEMPORAL_BUILD_ID`). Leave it empty and it follows `image.tag`,
  then the chart's appVersion. Set it only for an image whose tag is not
  a version, and change it whenever the image changes: each worker
  promotes its build at startup and the newest semver build wins.

### 3. Postgres

Repositories, policy resolutions, control results, evaluation history
and policy versions live in Postgres. Choose a mode:

| `store.postgres.mode` | You provide | Good for |
| --- | --- | --- |
| `baked` (default) | nothing; the chart runs a single-pod Postgres | trying it out, homelab |
| `cnpg` | the CloudNativePG operator | production on Kubernetes |
| `external` | an **empty** database, a DSN Secret (`STORE_DSN`), and a read-only login in a second Secret (`api.roDsn.existingSecret`, `STORE_RO_DSN`) | RDS or another managed Postgres |

For GitOps with `baked`, set `store.postgres.baked.existingSecret`. The
generated password otherwise changes on every `helm template` render.

Schema migrations run automatically as a Helm hook Job
(`migrate.enabled`, on by default). The Job runs `repo-guardian migrate`,
which applies the controls schema chain and then **activates the
mounted policy's version**. Keep it on: the evaluator starts a policy
rollout only for the version the migrate Job activated. The chain is
for an empty database and refuses one that already holds the rc schema.
The Job waits up to `migrate.waitSeconds` (300) for Postgres to accept
connections before it migrates, so it can start before the database is
up.

#### Controls database roles

The controls line splits the database writer in two: the evaluator is
meant to connect as `rg_evaluator`, the remediator as `rg_remediator`,
and `topology: all` as `rg_all`, a member of both. Column-level grants
are the writer boundary, so a remediator that tries to save a whole
result row fails at the database. Migrations grant to these roles and
never create them; the owner that runs the migrations holds no
`CREATEROLE`.

`store.controls.enabled` (default off) provisions the roles, mounts each
role's DSN, and points the migrate Job at the owner. Turn it on only for
an empty database. Each controls worker connects with its own role's
DSN and logs which variable it used (`dsn_env`, `db_user` on the
"controls store" line); without `store.controls` it falls back to
`STORE_DSN`.

- **`baked`**: the chart creates the roles, plus a non-superuser owner,
  `rg_owner`, that the migrate Job connects as (the image's
  `POSTGRES_USER` is a superuser, which would bypass every grant). An
  init script covers a new volume; a hook Job creates missing roles and
  resets passwords on an existing one. Each role's password is in its
  own Secret, `<release>-repo-guardian-postgres-rg-<role>`.
- **`cnpg`**: the roles are CNPG managed roles with the same Secrets; the
  cluster's application owner runs the migrations.
- **`external`**: create the roles yourself, as an administrator, before
  the first migration, then put each role's DSN in a Secret
  (`store.controls.evaluator.existingSecret`,
  `store.controls.remediator.existingSecret`, and for `topology: all`
  `store.controls.all.existingSecret`). `STORE_DSN` stays the owner's.

```sql
-- The owner: owns the schema and runs `repo-guardian migrate`. Skip it
-- if your STORE_DSN user is already a non-superuser owner.
CREATE ROLE rg_owner LOGIN PASSWORD '...' NOSUPERUSER NOCREATEROLE;
GRANT ALL ON SCHEMA public TO rg_owner;

-- The application roles.
CREATE ROLE rg_evaluator  LOGIN PASSWORD '...' NOSUPERUSER;
CREATE ROLE rg_remediator LOGIN PASSWORD '...' NOSUPERUSER;
CREATE ROLE rg_all        LOGIN PASSWORD '...' NOSUPERUSER IN ROLE rg_evaluator, rg_remediator;
GRANT USAGE ON SCHEMA public TO rg_evaluator, rg_remediator;
```

Each DSN is mounted only into the pods of the role that uses it:
`STORE_DSN_EVALUATOR` into the evaluator, `STORE_DSN_REMEDIATOR` into
the remediator. A split evaluator or remediator then gets no `STORE_DSN`
at all; only the migrate Job keeps the owner's credentials.

### 4. The two GitHub Apps

v2 runs as two GitHub Apps. The private key is the security boundary: a
process holding a write-capable key can mint a write token whatever it
intends, so the evaluator holds a key that cannot write.

| | Evaluation App | Remediation App |
| --- | --- | --- |
| Install on | every organisation in `enterprise.orgs` | organisations with any `remediate` assignment (from IMPL-0030) |
| Key held by | evaluator (and `all`) | remediator (and `all`) |
| Webhook URL | `POST /webhooks/github/eval` | `POST /webhooks/github/remediate` |
| Rate budget | `installation/eval/<installation id>` | `installation/remediate/<installation id>` |

#### Evaluation App permissions

Read-only. This is the fixed set the evaluator logs at startup
(`Evaluation App permissions required`):

| Permission | Access | Needed for |
| --- | --- | --- |
| Metadata | Read | always (mandatory); rulesets and custom property values |
| Contents | Read | every file-based control |
| Pull requests | Read | pull requests and labels |
| Administration | Read | `security_and_analysis` and the vulnerability-alerts read (`repo_settings`) |
| Custom properties (organization) | Read | the organisation's property schema (`custom_properties`) |

Merge-policy settings are read through GraphQL, which returns them
under this set; no write permission is needed to read them.

#### Evaluation App events

Subscribe to **Push**, **Repository**, **Installation repositories** and
**Pull request**. Installation events are delivered to every App webhook without a
subscription.

| Event | Actions handled | What happens |
| --- | --- | --- |
| `installation` | `created`, `unsuspend` | records the installation and starts a discovery run for it |
| `installation` | `suspend` | marks the installation suspended |
| `installation` | `deleted` | parks every repository of the installation (`installation_removed`) |
| `installation_repositories` | `added` | discovers the repositories (from the payload when it names 100 or fewer on an all-repositories installation, otherwise by a discovery run) |
| `installation_repositories` | `removed` | parks each repository (`removed`) |
| `repository` | `created`, `renamed`, `transferred`, `archived`, `unarchived` | upserts the repository (renames and transfers match by GitHub id), parks an archived or forked one, resolves it and evaluates it if managed |
| `repository` | `deleted` | parks it (`removed`) |
| `push` | default branch only | re-evaluates a known, managed repository with the paths the push changed, if it touched a path a control owns or reads; an unknown repository is discovered; a parked one stays parked |
| `pull_request` | `synchronize`, `closed`, `reopened`, on a `repo-guardian/` head | observes the repository's remediation pull requests (`pr_event`): records a moved head, a merge or a user close, and evaluates nothing |

Ingest answers anything else with 204: other actions, tag pushes, pushes
to other branches, pushes touching no watched path, and pull requests
on any other branch.

#### Remediation App permissions

The remediator logs its set at startup (`Remediation App permissions
required`). The base set is always required:

| Permission | Access | Needed for |
| --- | --- | --- |
| Metadata | Read | always (mandatory) |
| Contents | Read & write | every file-based control |
| Pull requests | Read & write | every remediation pull request |

When remediation lands, grant write on each resource a control type
your policy remediates:

| Permission | Access | Control types |
| --- | --- | --- |
| Administration | Read & write | `repo_settings`, `branch_ruleset` |
| Custom properties | Read & write | `custom_properties` |
| Issues | Read & write | `labels` |
| Workflows | Read & write | any control that writes under `.github/workflows/` (GitHub requires it in addition to Contents) |

#### Remediation App events

Subscribe to **Installation repositories** only. The Remediation App's
events describe where it may write, not which repositories exist, so
they never start discovery.

| Event | Actions handled | What happens |
| --- | --- | --- |
| `installation` | `created`, `deleted`, `suspend`, `unsuspend` | re-resolves the installation's repositories, since the effective mode depends on it |
| `installation_repositories` | `added`, `removed` | re-resolves the named repositories |

The Remediation App's own access records are written by the remediator
from IMPL-0030. Until then every assignment's effective mode is
`evaluate`, with reason `remediation_app_no_access`, whatever the
policy asks for.

#### Registering and installing

For each App:

1. **Register it** with its webhook **active**, pointing at its URL from
   step 5. Generate a strong **webhook secret**.
2. **Grant the permissions and subscribe to the events** above.
3. **Generate a private key** and store it in your secrets backend.
4. **Install it.** The Evaluation App goes into every organisation the
   policy names, on all repositories or a selection. You can install it
   into more organisations at any time; see
   [Onboarding organisations](#onboarding-organisations-and-repositories).

[The enterprise setup guide](ent-setup.md) covers installing an App
across many organisations. Use the permission and event tables on this
page in place of its single-App table.

Record each App in its own block. The key and the webhook secret go in
a Secret with keys `private-key` and `webhook-secret`:

```yaml
github:
  eval:
    appId: "123456"                  # empty disables the block
    existingSecret: rg-eval-app      # keys: private-key, webhook-secret
  remediate:
    appId: "234567"                  # optional in this rc
    existingSecret: rg-remediate-app
```

Without `existingSecret` the chart creates `<release>-eval` /
`<release>-remediate` from `privateKey` and `webhookSecret`, for a quick
trial. The keys are mounted as files only into the role that acts as
that App; the binary reads `EVAL_GITHUB_PRIVATE_KEY_PATH` and
`REMEDIATE_GITHUB_PRIVATE_KEY_PATH`, never a key value. Ingest gets
both webhook secrets and both App ids, and no key. The single-App
`secrets.*` and `config.appId` values are removed and fail render (see
[controls chart values](v2-migration.md#controls-chart-values)).

### 5. A public route for webhooks

GitHub must reach the ingest Service. The chart deliberately ships no
webhook Ingress: pick an option from [webhook ingress](ingress.md) (AWS
load balancer, Tailscale Funnel, Cloudflare Tunnel, ngrok). The
webhook's HMAC signature is the only check the app itself makes. If you
want to restrict source IPs to GitHub's hook ranges, do it at your edge.

Each App has its own URL on the same Service, mounted when that App's
webhook secret is set:

| App | Webhook URL |
| --- | --- |
| Evaluation App | `POST /webhooks/github/eval` |
| Remediation App | `POST /webhooks/github/remediate` |

Each route checks only its own App's secret and refuses an installation
event naming the other App's id (401, counted as
`webhook_rejected_total{reason="app_mismatch"}`). The App a delivery
belongs to is taken from the route, never from a header.

The Service keeps the release's full name (`repo-guardian` for a
release of that name) and listens on port 80 (`service.httpPort`),
forwarding to the container's 8080.

### 6. An OIDC identity provider (API and UI only)

Skip this if you do not enable `api` or `ui`.

- **Issuer** URL (`api.auth.issuer`). The API and UI trust the same one.
- **API audience:** tokens must carry `aud` = `repo-guardian-api`
  (`api.auth.audience`), and a groups claim (`groups` by default).
- **UI client:** a confidential client (`repo-guardian-ui` by default)
  with redirect URI `https://<ui host>/auth/callback`. Put its secret
  and one or more random session keys (32+ bytes each, comma-separated,
  newest first so keys can be rotated) in a Secret with keys
  `oidc-client-secret` and `session-keys` (`ui.existingSecret`).
- **Who sees what:** `api.authz.groups` maps IdP groups to the
  organisations they may read (`["*"]` for all). A caller with no
  mapped organisation gets 403 on everything except `/me` and `/status`.
- **Machine clients:** if your IdP puts no groups on client-credentials
  tokens, map the client's `azp` to groups in `api.authz.clients`.
- **Status page:** `/status` is served without a token by default; set
  `api.status.public: false` to require one.
- **Re-evaluate button:** `POST /repositories/{id}/evaluate` needs a
  Temporal client on the api role. Set `api.temporal: true` to give it
  the `temporal.*` connection; without it the POST answers 501 and the
  UI hides the button. `all` always has one.
- **UI origin:** the UI's browser-facing URL defaults to
  `https://<ui.ingress.host>`. If you bring your own ingress, set
  `ui.publicUrl`.

### 7. Prometheus and Grafana (optional)

Every repo-guardian pod (ingest, evaluator, remediator, api, or the
single `all` pod) exposes `/metrics` on port 9090; the ui pod does not.
Dashboards and alerts are generated from your policy rather than shipped
by hand:

```bash
repo-guardian monitoring generate --config ./policy --format k8s \
  --namespace monitoring --instance-selector dashboards=grafana \
  --name repo-guardian --prometheus-selector 'namespace="repo-guardian"'
```

`--config` takes the policy directory. See
[generating dashboards and alerts](monitoring-generation.md). The chart
can also render a `PrometheusRule` of its own.

### 8. A first policy

`guardian.hcl` is gone. The policy is a **directory**:

```text
policy/
├── enterprise.hcl        # the organisations repo-guardian manages, the baseline, the default mode
├── catalogue/*.hcl       # one control definition per file
├── orgs/*.hcl            # optional per-organisation layers
└── templates/            # optional template overrides
```

Keep the first policy small and evaluate-only. `examples/policy/minimal`
is a reasonable start; the [policy reference](../usage/policy-reference.md),
the [controls reference](../usage/controls-reference.md) and
[`examples/policy/`](https://github.com/donaldgifford/repo-guardian/tree/main/examples/policy)
have more.

```hcl
# enterprise.hcl
enterprise {
  orgs     = ["myorg"]
  mode     = "evaluate"
  controls = ["codeowners@1", "dependabot@1"]
}
```

```hcl
# catalogue/codeowners.hcl
control "codeowners" {
  version     = 1
  type        = "codeowners"
  title       = "CODEOWNERS"
  description = "A CODEOWNERS file exists in one of GitHub's locations"
  template    = "codeowners"

  rule "exists" {
    title     = "A CODEOWNERS file exists"
    remediate = true
  }
}
```

**Scope** is `enterprise.orgs`. The rc's top-level `scope {}` and
per-rule scopes are gone: an organisation in the list gets the
enterprise baseline whether or not it has a file under `orgs/`, and a
repository in any other organisation is discovered but `unmanaged`, so
nothing is evaluated on it. Narrow it with an org file:

```hcl
# orgs/myorg.hcl
org "myorg" {
  exclude_repos {
    match  = [".github", "archive-*"]
    reason = "org config and archives are managed elsewhere"
  }

  repos "legacy" {
    match = ["legacy-*"]

    exclude "codeowners@1" {
      reason = "legacy repositories predate the ownership policy"
    }
  }
}
```

`exclude_repos` takes repositories out of the policy (`excluded`, with
the reason recorded); `exclude "<control>@<version>"` drops one control
from the organisation or from a `repos` block. Every exclusion carries a
reason, and the report and `/orgs/{org}` list them.

**`dryRun` is gone; mode replaces it.** Each assignment resolves to
`evaluate` (report only) or `remediate` (open pull requests or apply
API changes, per the control's `remediate` rules). The mode is set on
`enterprise`, an org, a `repos` block, or a single control, and the
most specific wins; with none set it is `evaluate`. A `remediate`
assignment on a repository the Remediation App cannot reach is
evaluated only. In this rc every assignment is effectively `evaluate`.

Validate the policy before deploying. `monitoring generate` runs the
same loader and validation the server does, and fails on any error:

```bash
repo-guardian monitoring generate --config ./policy --out /tmp/check
```

To see what it does to one repository, run it against GitHub as the
Evaluation App, with no database:

```bash
EVAL_GITHUB_APP_ID=123456 EVAL_GITHUB_PRIVATE_KEY_PATH=./eval.pem \
  repo-guardian evaluate --repo myorg/service-a --policy ./policy
```

It exits 0 when compliant (or when nothing was measured), 1 when
non-compliant, 2 on a usage error and 3 on an operational one; add
`--format json` for the full result.

Keep the policy in git. Every later change goes through review.

Ship it either as `policy.files`, the directory as a nested map (a
string is a file, a map is a directory), or as a ConfigMap you manage,
named in `policy.existingConfigMap`. `examples/values-with-policy.yaml`
carries `examples/policy/full` this way:

```yaml
policy:
  files:
    enterprise.hcl: |
      enterprise {
        orgs     = ["myorg"]
        mode     = "evaluate"
        controls = ["codeowners@1", "dependabot@1"]
      }
    catalogue:
      codeowners.hcl: |
        control "codeowners" { ... }
      dependabot.hcl: |
        control "dependabot" { ... }
    orgs:
      myorg.hcl: |
        org "myorg" { ... }
```

The chart renders one ConfigMap (keys joined with dots,
`catalogue.codeowners.hcl`) and mounts each file back at its path under
`/etc/repo-guardian/policy`, which `GUARDIAN_CONFIG` names. Ingest (for
the paths pushes are filtered on), the evaluator, the remediator and
the migrate Job all read it. An `existingConfigMap` is mounted whole,
so it can hold only top-level files unless you mount it with your own
`items` through `extraVolumes`. The chart refuses to render without one
of the two.

## Turning it on

1. **Create the Secrets** from prerequisites 2–6 in the release
   namespace.

2. **Write the values file:**

   ```yaml
   topology: split

   github:
     eval:
       appId: "123456"
       existingSecret: rg-eval-app      # private-key, webhook-secret

   temporal:
     address: temporal-frontend.temporal.svc:7233
     namespace: repo-guardian
     tls:
       existingSecret: repo-guardian-temporal-tls

   store:
     postgres:
       mode: cnpg

   evalInterval: "24h"                  # EVAL_INTERVAL

   policy:
     files: {}                          # the policy directory; or existingConfigMap

   # Optional: the API and the UI.
   api:
     enabled: true
     temporal: true                     # the re-evaluate button
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

   With Argo CD, see [Installing with Argo CD](#installing-with-argo-cd)
   first. The migrate Job creates the schema and activates the policy
   version. The evaluator then creates the `controls-discovery` and
   `controls-snapshot` schedules, starts
   `controls-rollout/<version>/<activated_at>` for the activated policy,
   and promotes its own build to Temporal's current version of the
   `repo-guardian-eval` deployment.

4. **Verify the service:**
   - `kubectl -n repo-guardian get pods`: ingest and evaluator (and
     remediator, api, ui) are Ready, and the migrate Job succeeded.
   - The evaluator's build is current. Temporal gives a versioned worker
     no work until it is, and a worker still not current two minutes
     after start fails its `deployment` readiness check:

     ```bash
     temporal worker deployment describe --deployment-name repo-guardian-eval
     ```

   - The evaluator's startup log has `Evaluation App permissions
     required`; compare it with the installation's permissions.
   - In the Temporal UI, a `controls-discovery` run has completed and
     there is one `evaluation/<repository id>` workflow per managed
     repository, plus one `installation/eval/<installation id>` budget
     workflow per installation.

5. **Verify webhooks.** Push to any repository, then check the
   Evaluation App's **Advanced → Recent Deliveries**:
   - **202:** accepted and started;
   - **204:** accepted, nothing to do (for example a tag push, or a push
     touching no path a control watches);
   - **401:** the webhook secret does not match, or the payload names
     the other App;
   - **503:** Temporal is unreachable. GitHub keeps the delivery, and you
     can redeliver it.

6. **Review the results.** First evaluations from discovery are spread
   across `evalInterval` (24h by default) so a large fleet does not hit
   the GitHub rate limit at once; a repository created or pushed to is
   evaluated at once. Look at them in the UI, at `GET /api/v1/summary`
   and `/api/v1/controls`, or in a report:

   ```bash
   repo-guardian report --dsn "$STORE_DSN" --out ./reports
   ```

   Add `exclude_repos` or `exclude` entries, with reasons, for
   legitimate exceptions, and fix any control that is wrong.

7. **Generate the dashboards and alerts** (prerequisite 7).

Turning on remediation (setting `mode = "remediate"` and installing the
Remediation App) does nothing in this rc; it takes effect with
IMPL-0030.

## Installing with Argo CD

Argo CD renders the chart with `helm template` and orders the result
itself. It does not tell an install from an upgrade, and it maps a
`helm.sh/hook` with no Argo annotation by phase: `pre-*` becomes PreSync
and `post-*` PostSync. Before chart 2.0.0-rc.6 the migrate Job's
`pre-upgrade` made it a PreSync hook. On a first install it ran before
the ServiceAccount, the store and its Secret existed, hit its deadline,
and the application never came up. The workaround was two commits:
`migrate.enabled: false`, then `true`.

From 2.0.0-rc.6 every hook Job also carries
`argocd.argoproj.io/hook: Sync`, so Argo ignores its `helm.sh/hook`, and
the chart sets sync waves:

| Wave | Resources |
| --- | --- |
| 0 | everything not listed below: ServiceAccount, RBAC, ConfigMaps, Secrets, the store (baked StatefulSet or CNPG Cluster and Pooler), Services |
| 1 | the controls-roles Job (baked, `store.controls.enabled`) |
| 2 | the migrate Job |
| 3 | the read-only role Job (baked, split with the API) |
| 4 | the role Deployments, their PodDisruptionBudgets and ScaledObjects |

Argo waits for each wave to be healthy, and for its hook Jobs to finish,
before the next. Upgrades therefore still migrate before new pods roll
out. Argo can report a CNPG Cluster healthy before Postgres accepts
connections, so the migrate Job and the psql Jobs wait for the database
themselves.

Two consequences:

- **The migrate Job runs on every sync**, not just on a chart change:
  Argo re-runs Sync hooks each time. Migrating an up-to-date schema is a
  no-op, and so is activating the policy version that is already current.
- **A finished Job is deleted when it succeeds.** Argo honours the
  chart's `helm.sh/hook-delete-policy: hook-succeeded` as well as its own
  `BeforeHookCreation`, so read migrate's logs while it runs, from your
  log store, or from the Job's events (`kubectl get events`) for about an
  hour. A failed Job is kept until the next sync replaces it.

Helm itself ignores the Argo annotations; `helm install` and
`helm upgrade` behave as before.

## Onboarding organisations and repositories

**A new organisation** is onboarded by listing it and installing the
Evaluation App into it:

1. Add the organisation to `enterprise.orgs` (and, if it needs its own
   layer, add `orgs/<org>.hcl`), and roll out that policy change first.
   An installed organisation that is not in the list is discovered but
   `unmanaged`: nothing is evaluated on it.
2. Install the Evaluation App into the organisation (all repositories,
   or a selection).
3. The `installation` webhook starts a discovery run for that
   installation at once (`controls-discovery/installation/<id>/<delivery>`);
   the hourly `controls-discovery` schedule (`discovery.interval`)
   catches anything missed. Each managed repository gets an
   `evaluation/<repository id>` workflow, and first evaluations are
   spread across `evalInterval`.
4. Confirm it with `GET /api/v1/orgs/<org>`, the per-org rows on the
   dashboards, or `repo-guardian report --orgs <org>`.

**A new repository** in an installed organisation is picked up by the
`repository.created` webhook and evaluated without waiting for
discovery.

**Archived and forked repositories** are parked, not evaluated, and
their results are cleared. **Removing** the App from a repository, or
deleting the repository, parks it the same way. A parked repository
keeps its history, is excluded from compliance numbers, and is
un-parked only by discovery; a push never un-parks it.

**Offboarding an organisation** means removing it from
`enterprise.orgs` and uninstalling the Evaluation App. The
`installation.deleted` webhook parks every repository of the
installation.

## Changing policy

Treat the policy directory like code.

1. **Propose the change as a PR** to the repository that holds the
   policy.
2. **Validate it in CI** with
   `repo-guardian monitoring generate --config ./policy --out /tmp/check`,
   which fails on any policy error, including a control definition
   whose template does not render. `repo-guardian evaluate` against a
   few repositories shows the effect before merge.
3. **Introduce enforcement in stages.** Add a new control with
   `mode = "evaluate"`, read its results, add exclusions, then move it
   to `remediate` for one `repos` block or organisation before widening
   it. (Remediation itself waits for IMPL-0030.)
4. **Grant new App permissions before the control ships.** A control
   type may need a permission an App lacks: Administration read on the
   Evaluation App for `repo_settings`, or, for remediation, Workflows
   write on the Remediation App for a control that writes a workflow
   file. Add it to the App, and have each organisation accept it, first.
5. **Merge and deploy.** The policy reaches ingest, the controls workers
   and the migrate Job as mounted files; the migrate Job activates the
   new version and the pods read it at startup. With `policy.files` in
   values, `helm upgrade` runs the migrate Job and rolls those pods
   automatically. With `policy.existingConfigMap`, the ConfigMap is
   yours: update it, run `helm upgrade` (or sync) so the migrate Job
   activates the new version, then restart the pods:
   `kubectl -n repo-guardian rollout restart deploy/repo-guardian-ingest deploy/repo-guardian-evaluator`.
   An evaluator whose loaded policy is not the activated one logs
   `loaded policy is not the activated one` and starts no rollout.

What a change costs:

- **A change that can alter an outcome** (a control definition, an
  assignment, a mode, an exclusion, a template) produces a new policy
  version. Activating it starts `controls-rollout/<version>/<activated_at>`,
  which re-resolves every active repository and signals each managed
  one to re-evaluate at a random point inside `policyRolloutWindow`
  (24h by default), with webhook-triggered evaluations still served
  first. Reverting to an earlier version is a new activation and rolls
  out again.
- **An operational change** (log level, concurrency, intervals) changes
  nothing about the policy version and triggers no re-evaluation.

## Tracking a change through the fleet

| Question | Where to look |
| --- | --- |
| Which policy version is live, and has its rollout finished? | `GET /api/v1/policies`: version, rollout state, each visible org's layer and the catalogue entries it references (never template bodies) |
| How is one control doing across the fleet? | `GET /api/v1/controls/<id>`, or the control's row in the UI and on the KPI dashboard |
| Which repositories fail it, and why? | `GET /api/v1/repositories?org=…`, then `GET /api/v1/repositories/<id>/controls`: each assignment with its bucket, mode, rule results and evidence |
| What happened to one repository, and when? | `GET /api/v1/repositories/<id>/evaluations` (the evaluation log) and `/events` (result changes, renames, transfers, parks) |
| Is compliance trending up? | `GET /api/v1/compliance/history`, from daily snapshots (`posture.snapshotInterval`) |
| Re-evaluate one repository now | `POST /api/v1/repositories/<id>/evaluate`, or the UI's button (needs `api.temporal`) |
| A report for someone without API access | `repo-guardian report`: one Markdown file per organisation, with no GitHub calls |
| Did the background jobs run? | the Temporal UI (`controls-discovery`, `controls-snapshot` and `controls-rollout/<version>/<activated_at>` workflows); `/status` for overall health |

The rc's findings endpoints are gone:

| Before | Now |
| --- | --- |
| `/rules`, `/rules/{kind}/{name}` | `/controls`, `/controls/{id}` |
| `/findings` | `/repositories/{id}/controls` |
| `/policy` | `/policies` |
| `/repositories/{id}/checks` | `/repositories/{id}/evaluations` |
| `/installations` | removed; `/status` counts installations per App |

Every compliance figure is a posture: counts per bucket, a percent over
compliant and non-compliant, and a coverage over what is assigned. When
nothing is measured the percent is "no data", never 100. See
[the API](../usage/api.md).

After a policy change, expect results to move over the rollout window
rather than all at once. A rollout is complete when `/policies` reports
it finished.

## Day-two operations

- **Upgrades.** Bump the chart version. New workers promote their build
  to their deployment's current version when it is newer by semver, so
  in-flight workflows move to the new code without manual steps. Each
  role promotes its own deployment, so a remediator scaled to zero
  never holds the evaluator back. **Rolling back** to an older 2.x needs
  one command per deployment, which the older pods print in their logs:
  `temporal worker deployment set-current-version --deployment-name repo-guardian-eval --build-id <tag>`
  (and `repo-guardian-remediate` for the remediator).
- **Throughput.** Evaluations are bound by GitHub's rate limit, not CPU.
  Each installation has its own budget per App, and webhook-triggered
  evaluations are served before scheduled ones. Raising
  `evaluator.concurrency` or replicas past what the budget allows only
  adds waiting.
- **Cadence.** Each repository is re-evaluated every `evalInterval`
  (`EVAL_INTERVAL`, 24h, jittered ±10%), and at once on a push that
  touches a path one of its controls owns or reads. `CHECK_INTERVAL` is
  removed; the binary warns and ignores it.
- **Retention.** This rc prunes nothing: evaluation records, results,
  their events and compliance history are all kept. `checksRetention`
  is accepted and has no effect yet.
- **Pausing.** There is no dry-run switch. Set `mode = "evaluate"` on
  the enterprise (or the org) and roll the policy out; evaluation
  continues and results stay current. In this rc nothing is written to
  GitHub in any mode.

## Checklist

Prerequisites:

- [ ] Kubernetes namespace, Helm, and a way to create Secrets
- [ ] Temporal ≥ 1.31 reachable on 7233, namespace created, mTLS or OIDC
      credentials with write on the namespace
- [ ] Postgres mode chosen on an empty database; DSN Secrets for
      `external`
- [ ] Evaluation App registered: webhook active on `/webhooks/github/eval`,
      Push, Repository, Installation repositories and Pull request subscribed,
      read-only permissions granted, private key stored
- [ ] (Optional in this rc) Remediation App registered on
      `/webhooks/github/remediate`, base permissions granted
- [ ] Public HTTPS route to the ingest Service
- [ ] (API/UI) OIDC issuer, API audience, UI client, authz groups
- [ ] A policy directory in git that validates, with every assignment
      in `evaluate`

Turn on:

- [ ] Secrets created, values written, chart installed
- [ ] Pods Ready, migrate Job succeeded, evaluator build current on
      `repo-guardian-eval`
- [ ] `controls-discovery` completed; one `evaluation/<id>` workflow per
      managed repository
- [ ] Webhook deliveries return 202 or 204
- [ ] Results reviewed; exclusions added with reasons
- [ ] Dashboards and alerts generated and applied

Each new organisation:

- [ ] Added to `enterprise.orgs` (and `orgs/<org>.hcl` if needed),
      rolled out
- [ ] Evaluation App installed, and any new permissions accepted
- [ ] Organisation visible in `/api/v1/orgs/<org>` with results
