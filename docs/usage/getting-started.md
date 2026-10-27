# Getting started with repo-guardian

A fresh install of repo-guardian 2.0.0, from nothing to a first
evaluation, then remediation on one organisation. Each step is short and
links to the page that has the full detail. For what repo-guardian
enforces and why, read
[repo-guardian and v2 at a glance](../operations/v2-overview.md). For the
complete install runbook, read
[Turning on repo-guardian v2](../operations/v2-onboarding.md).

> [!NOTE]
> A new install writes nothing to GitHub. It reads every repository,
> records each control's result and reports compliance. Pull requests
> start only when an assignment is in `mode = "remediate"` on a
> repository the Remediation App can reach.

<!--toc:start-->
- [1. What you need](#1-what-you-need)
- [2. Create the two GitHub Apps](#2-create-the-two-github-apps)
- [3. Write a minimal policy](#3-write-a-minimal-policy)
- [4. Write the values file and install](#4-write-the-values-file-and-install)
- [5. The first evaluation](#5-the-first-evaluation)
- [6. Reading the UI](#6-reading-the-ui)
- [7. Turning on remediation for one organisation](#7-turning-on-remediation-for-one-organisation)
- [8. Next steps](#8-next-steps)
<!--toc:end-->

## 1. What you need

- **A Kubernetes cluster** and a namespace for repo-guardian
  (`repo-guardian` below).
- **Helm**, able to pull OCI charts from `ghcr.io`.
- **A Temporal cluster you run.** Every evaluation is a Temporal
  workflow, so Temporal is required. repo-guardian does not ship it.
  You need server 1.31 or newer, a namespace (default `repo-guardian`),
  network access to the frontend on port 7233, and mTLS or OIDC
  credentials. See
  [A Temporal cluster](../operations/v2-onboarding.md#2-a-temporal-cluster).
- **Postgres.** Pick one with `store.postgres.mode`:
  - `baked` (the default): the chart runs a single Postgres pod. Good
    for a trial.
  - `cnpg`: the chart renders a CloudNativePG cluster. Needs the CNPG
    operator.
  - `external`: you provide an empty database and a DSN Secret, for
    example on RDS.

  See [Postgres](../operations/v2-onboarding.md#3-postgres).
- **A public HTTPS route** to the ingest Service, so GitHub can deliver
  webhooks. The chart ships no webhook Ingress; pick an option from
  [webhook ingress](../operations/ingress.md).

## 2. Create the two GitHub Apps

repo-guardian runs as two GitHub Apps. The private key is the security
boundary: the process that only reads holds a key that cannot write.

| | Evaluation App | Remediation App |
| --- | --- | --- |
| Access | read-only | writes |
| Key held by | the evaluator | the remediator |
| Install on | every organisation the policy manages | only organisations you remediate |
| Webhook URL | `https://<your host>/webhooks/github/eval` | `https://<your host>/webhooks/github/remediate` |
| Needed for | everything | remediation only |

Create the **Evaluation App** now. The Remediation App can wait until
[step 7](#7-turning-on-remediation-for-one-organisation).

For each App:

1. Register it with its webhook active, pointing at its URL above.
   Generate a strong webhook secret.
2. Grant its permissions and subscribe to its events:
   - **Evaluation App:** a fixed read-only set (Metadata, Contents, Pull
     requests, Administration, and organisation Custom properties, all
     read), subscribed to Push, Repository, Installation repositories
     and Pull request. The evaluator logs the set at startup as
     `Evaluation App permissions required`. The full table is in
     [Evaluation App permissions](../operations/v2-onboarding.md#evaluation-app-permissions).
   - **Remediation App:** derived from your policy. The base set is
     Metadata read, Contents write and Pull requests write. A control
     that writes through the API with `apply = "direct"` adds write on
     its resource, and a control that writes under
     `.github/workflows/` adds Workflows write. The remediator logs the
     set it needs at startup as `Remediation App permissions required`,
     with the list in the `permissions` field. Subscribe it to
     Installation repositories only. See
     [Remediation App permissions](../operations/v2-onboarding.md#remediation-app-permissions).
3. Generate a private key.
4. Install it. The Evaluation App goes into every organisation the
   policy lists.

Put each App's key and webhook secret in a Secret with the keys
`private-key` and `webhook-secret`:

```bash
kubectl -n repo-guardian create secret generic rg-eval-app \
  --from-file=private-key=./eval.pem \
  --from-literal=webhook-secret="$EVAL_WEBHOOK_SECRET"
```

Installing across many organisations is covered in
[the enterprise setup guide](../operations/ent-setup.md). Use the
permission tables in the onboarding runbook in place of its single-App
table.

## 3. Write a minimal policy

The policy is a directory:

```text
policy/
├── enterprise.hcl     # the organisations, the baseline controls, the default mode
├── catalogue/*.hcl    # one control definition per file
├── orgs/*.hcl         # optional per-organisation layers
└── templates/         # optional template overrides
```

Start small and in `evaluate`. This is
[`examples/policy/minimal`](https://github.com/donaldgifford/repo-guardian/tree/main/examples/policy/minimal)
with the mode set to `evaluate`:

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

```hcl
# catalogue/dependabot.hcl
control "dependabot" {
  version     = 1
  type        = "dependency_updates"
  title       = "Dependabot"
  description = "Dependabot version updates are configured"
  template    = "dependabot"
  tool        = "dependabot"

  rule "configured" {
    title     = "A Dependabot config exists"
    remediate = true
  }
}
```

- `enterprise.orgs` is the onboarding list. A repository in any other
  organisation is discovered but unmanaged, so nothing is evaluated on
  it.
- `enterprise.controls` is the baseline: every listed organisation gets
  these controls, as `slug@version`.
- `mode` is `evaluate` (record results, change nothing) or `remediate`.
  Unset means `evaluate`.
- `remediate = true` on a rule means remediation may fix it. It does
  nothing while the mode is `evaluate`.
- The `codeowners` and `dependabot` templates are built in. Put a file
  under `templates/` only to override one.

To leave some repositories out, add an org layer. Every exclusion
carries a reason, which the report, the API and the UI show:

```hcl
# orgs/myorg.hcl
org "myorg" {
  exclude_repos {
    match  = [".github", "archive-*"]
    reason = "org config and archives are managed elsewhere"
  }
}
```

Check the policy before you deploy it. `monitoring generate` runs the
same loader and validation as the server and fails on any error:

```bash
repo-guardian monitoring generate --config ./policy --out /tmp/check
```

The [policy reference](policy-reference.md) has every attribute and
block, and the [control-type reference](controls-reference.md) has every
built-in control type.

## 4. Write the values file and install

A minimal values file for a split install with the baked Postgres:

```yaml
# values.yaml
topology: split                      # one Deployment per role (the default)

temporal:
  address: temporal-frontend.temporal.svc:7233
  namespace: repo-guardian
  tls:
    existingSecret: repo-guardian-temporal-tls   # tls.crt, tls.key, ca.crt

github:
  eval:
    appId: "123456"
    existingSecret: rg-eval-app      # private-key, webhook-secret

store:
  postgres:
    mode: baked

evalInterval: "24h"                  # how often each repository is re-evaluated

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
      dependabot.hcl: |
        control "dependabot" {
          version     = 1
          type        = "dependency_updates"
          title       = "Dependabot"
          description = "Dependabot version updates are configured"
          template    = "dependabot"
          tool        = "dependabot"

          rule "configured" {
            title     = "A Dependabot config exists"
            remediate = true
          }
        }
```

Notes on the values:

- **`temporal`**: with OIDC instead of mTLS, set
  `temporal.auth.oidc.tokenUrl`, `clientId` and `existingSecret` in
  place of `temporal.tls.existingSecret`.
- **`github.eval`**: the evaluator renders only when `appId` is set.
  Without `existingSecret` the chart creates the Secret from
  `privateKey` and `webhookSecret`, which is fine for a trial.
- **`store.postgres.mode`**: with `baked` under GitOps, set
  `store.postgres.baked.existingSecret`, or the generated password
  changes on every `helm template`. With `external`, set
  `store.postgres.existingSecret` to a Secret holding `STORE_DSN`. For
  separate evaluator and remediator database roles, see
  [Controls database roles](../operations/v2-onboarding.md#controls-database-roles).
- **`policy.files`**: the policy directory as a nested map. A string is
  a file, a map is a directory. The chart mounts it at
  `/etc/repo-guardian/policy` and points `GUARDIAN_CONFIG` at it. To
  manage the ConfigMap yourself, set `policy.existingConfigMap`
  instead. [`examples/values-with-policy.yaml`](https://github.com/donaldgifford/repo-guardian/blob/main/examples/values-with-policy.yaml)
  is a larger example.

Install:

```bash
helm upgrade --install repo-guardian \
  oci://ghcr.io/donaldgifford/charts/repo-guardian \
  --version 2.0.0 -n repo-guardian -f values.yaml
```

The migrate Job runs first. It creates the schema and activates the
policy's version. Then the ingest and evaluator pods start. With Argo
CD, read
[Installing with Argo CD](../operations/v2-onboarding.md#installing-with-argo-cd)
first.

## 5. The first evaluation

Once the evaluator is up:

1. It creates the `controls-discovery` schedule and runs it. Discovery
   lists every installation of the Evaluation App and every repository
   in it.
2. Each managed repository gets one `evaluation/<repository id>`
   workflow. It evaluates the repository's controls, records the
   results, and sleeps until the next evaluation.
3. First evaluations are spread across `evalInterval` (24h by default),
   so a large fleet does not drain the GitHub rate limit at once. A
   repository that is created, or pushed to on a path a control reads,
   is evaluated straight away.

Check it:

- **Pods.** `kubectl -n repo-guardian get pods` shows ingest and the
  evaluator Ready, and the migrate Job completed.
- **Permissions.** The evaluator's startup log has
  `Evaluation App permissions required`. Compare it with what the
  installation granted.
- **Webhooks.** Push to a repository, then open the Evaluation App's
  **Advanced → Recent Deliveries**. 202 means started, 204 means
  nothing to do, 401 means the webhook secret does not match, and 503
  means Temporal is unreachable.
- **Temporal.** In the Temporal UI, a `controls-discovery` run has
  completed and there is one `evaluation/<id>` workflow per managed
  repository.
- **One repository, from your machine.** `repo-guardian evaluate` runs
  the policy against one repository as the Evaluation App, with no
  database and no Temporal:

  ```bash
  EVAL_GITHUB_APP_ID=123456 EVAL_GITHUB_PRIVATE_KEY_PATH=./eval.pem \
    repo-guardian evaluate --repo myorg/service-a --policy ./policy
  ```

  `--policy` defaults to `$GUARDIAN_CONFIG`, and `--format json` prints
  the full result. It exits 0 when compliant (or when nothing was
  measured), 1 when non-compliant, 2 on a usage error and 3 on an
  operational one.
- **Status.** `/status` on the API (`/api/v1/status`), or the UI's
  status page, shows overall health with no login and no names.

## 6. Reading the UI

The UI is optional. It needs the API (`api.enabled`), an OIDC identity
provider and a few Secrets; [the business UI](ui.md) covers enabling it
and [identity providers](../operations/identity-provider.md) covers the
provider setup.

Once it is up:

- **Fleet** (`/`) is the overview: compliance, the worst controls, and
  parked repositories.
- **Findings** lists every non-compliant result, oldest first.
- **Controls**, **Orgs** and **Repositories** drill into one control,
  one organisation or one repository, with each rule's result and
  evidence.
- **Policy** shows the active policy version and its rollout.

Compliance reads "no data" when nothing is measured, never 100%.
Remediation panels read "Remediation is not enabled on this deployment"
until you add the Remediation App.

## 7. Turning on remediation for one organisation

Do this for one organisation, watch it, then widen it.

1. **Create and install the Remediation App** on that organisation only,
   with its webhook at `/webhooks/github/remediate`
   ([step 2](#2-create-the-two-github-apps)). Store its key and webhook
   secret in a Secret, `rg-remediate-app` below.
2. **Add it to the values** and upgrade:

   ```yaml
   github:
     remediate:
       appId: "234567"
       existingSecret: rg-remediate-app   # private-key, webhook-secret
   ```

   The chart now renders the `remediator` Deployment and mounts the
   Remediation App's key into it alone. Compare its
   `Remediation App permissions required` log line with what the
   installation granted.
3. **Wait for access.** The remediator's `controls-discovery-remediate`
   schedule records which repositories the App can reach. Until then,
   that organisation's assignments stay in `evaluate`, with the reason
   `remediation_app_no_access`.
4. **Set the mode** in that organisation's layer, and deploy the policy:

   ```hcl
   # orgs/myorg.hcl
   org "myorg" {
     mode = "remediate"
   }
   ```

   The most specific mode wins, so you can go narrower: a `repos` block
   or a single `control "<slug@N>" { mode = "remediate" }` override. See
   [Resolution](policy-reference.md#resolution).

What the pull requests look like:

- **One pull request per control**, on the branch
  `repo-guardian/<control slug>`, updated in place as results change.
- **A cap per repository.** `remediation { max_open_prs }` (default 3)
  limits how many are open at once. The rest wait, held as `pr_cap`.
- **Humans win.** You can push to a repo-guardian pull request. The
  remediator merges the default branch into it and keeps your edits. It
  never force-pushes; a branch it cannot update cleanly is held as
  `conflict`.
- **It closes what it opened**, when the control passes on the default
  branch or is no longer assigned, with a comment.
- **It never deletes a branch.** Not after a merge, not after a close.
  Use the repository's `delete_branch_on_merge` setting for cleanup.

When nothing happens, the UI's Remediations page says why, as a hold.
[How it fixes things](../operations/v2-overview.md#how-it-fixes-things)
explains each hold and how it clears.

To pause, set `mode = "evaluate"` again and deploy the policy.
Evaluation continues, and no new pull requests open.

## 8. Next steps

- [Policy reference](policy-reference.md): every attribute and block in
  the policy directory, and how a repository's assignments resolve.
- [Control-type reference](controls-reference.md): every built-in
  control type and its parameters.
- [repo-guardian and v2 at a glance](../operations/v2-overview.md): the
  model behind controls, modes and remediation.
- [Identity providers](../operations/identity-provider.md): OIDC setup
  for the API and the UI.
- [Scaling](../operations/scaling.md): sizing, rate limits and the
  compliance metrics.
