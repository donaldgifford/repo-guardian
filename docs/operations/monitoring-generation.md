# Generating dashboards and alerts

`repo-guardian monitoring generate` derives Grafana dashboards and
Prometheus alerting rules from the same `guardian.hcl` the server loads.

The point is that a panel cannot outlive the rule it charts. A
hand-maintained dashboard drifts the moment a rule is renamed or a
reconciler is removed, and it drifts *silently* — an empty panel and a
compliant fleet look identical. So does an alert whose metric has no
producer: it never fires, and a never-firing alert looks exactly like a
healthy system. That is not hypothetical; it is
[INV-0012](../investigation/0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
finding A, which survived for months.

## Quick start

```bash
repo-guardian monitoring generate --config guardian.hcl --out ./monitoring
```

Writes plain files:

```
monitoring/
  dashboards/<slug>.json     # importable Grafana dashboards
  alerts/rules.yaml          # a Prometheus rule_files entry
```

For Kubernetes:

```bash
repo-guardian monitoring generate \
  --config guardian.hcl \
  --format k8s \
  --namespace monitoring \
  --instance-selector dashboards=grafana \
  --out ./monitoring
```

Writes `monitoring.coreos.com/v1` `PrometheusRule` and
`grafana.integreatly.org/v1beta1` `GrafanaDashboard` manifests,
ready for `kubectl apply` or an ArgoCD source.

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--config` | `$GUARDIAN_CONFIG` | Policy to derive from. Empty means the built-in defaults. A path that does not exist is an error, not a fallback. |
| `--out` | `./monitoring` | Output directory. Created if absent. |
| `--format` | `json` | `json` for plain files, `k8s` for custom resources. |
| `--org` | — | Repeatable. Adds an org to the per-org rows. See *Silent orgs* below. |
| `--prometheus-uid` | `prometheus` | UID of the Prometheus datasource the panels query. |
| `--loki-uid` | `loki` | UID of the Loki datasource the log panels query. |
| `--namespace` | — | `k8s` only. Stamped on every generated object. |
| `--name` | `repo-guardian` | `k8s` only. Base name for generated objects. |
| `--label` | — | `k8s` only, repeatable `key=value`. Added to every object's metadata. |
| `--instance-selector` | — | `k8s` only, repeatable `key=value`. The `matchLabels` naming the Grafana instance. **Required when there are dashboards to emit.** |
| `--allow-cross-namespace-import` | `false` | `k8s` only. Set when Grafana runs in a different namespace than the CRs. |
| `--resync-period` | — | `k8s` only, e.g. `10m`. How often the operator re-applies a dashboard. Unset leaves the operator's default. |

### Why `--instance-selector` is mandatory

Omitting it does not make `kubectl apply` fail. The operator simply has
no Grafana to file the dashboard into, so the CR sits unreconciled
forever with nothing in `kubectl get` to say so. The generator refuses
to emit something inert instead.

### Datasource UIDs are concrete, never inputs

Generated dashboards name their datasource by UID. They never carry a
`${DS_PROMETHEUS}` input placeholder, and never declare `__inputs`.

A dashboard with an input prompts the importer to choose a datasource,
which makes the generated tier un-provisionable: grafana-operator
applies a CR and nobody is there to answer the prompt. The panels land
with no datasource and render empty — indistinguishable from a fleet
with no data. If your cluster names its datasources differently, pass
`--prometheus-uid` / `--loki-uid`.

## What gets emitted, and what does not

Artifacts are scoped to the **mechanisms your policy configures**. A
mechanism is a configured feature that is the sole producer of a metric
series; if it is off, the series never appears.

Concretely, with no `custom_properties` reconciler you get no
`RepoGuardianPropertySchemaMissing` alert, because nothing increments
`repo_guardian_custom_property_missing_schema_total`. With
`auto_close_pr = false` you get no `RepoGuardianPRDrift`, because an
open PR whose rules are all satisfied is then the *designed* behaviour
and the alert would fire permanently. With `dry_run = true` you get none
of the PR-shaped alerts, because dry run suppresses
`repo_guardian_prs_created_total` and every one of them would be empty
by construction.

A few things are deliberately unconditional.
`RepoGuardianRepoAccessDenied` ships regardless of configuration,
because an installation can lose read access to a repository at any
time. Note its `{reason="access_denied"}` selector is load-bearing:
`repo_guardian_repos_parked_total` also counts routine archived and fork
parks, so the same expression without the selector would page on every
normal onboarding sweep.

### Silent orgs

The per-org rows are best when they come from the config, because a
configured row that renders empty is a *signal* — that org has stopped
reporting. Rows discovered from the series instead just disappear, which
is the failure this design exists to avoid.

The generator can only declare rows when the policy has a top-level
`scope { orgs = [...] }` block with literal names. It warns when it
cannot:

- **No top-level scope block** (legacy mode): the policy carries no org
  list anywhere, so there is nothing to enumerate. Pass `--org` for the
  orgs that must always render, or add a scope block.
- **Glob patterns in scope** (`orgs = ["acme-*"]`): a pattern is not an
  org, so those rows are discovered rather than declared. Name them
  literally, or pass `--org`.

## Generation is also config validation

`monitoring generate` loads the policy through exactly the code path the
server uses, so **a config that fails to load fails generation**. Adding
it to CI is a free syntax-and-semantics check on `guardian.hcl` — the
same trick `--strict-templates` plays at startup.

```yaml
- name: Validate the guardian policy
  run: repo-guardian monitoring generate --config guardian.hcl --out "$(mktemp -d)"
```

This catches HCL parse errors, unknown `guardian {}` attributes, invalid
`annotation_properties` maps, strict-scope violations (every rule must
declare its own `scope {}` once a top-level one exists), and PR-template
compile errors — before a deploy does.

One caveat, and it is the reason `--config` is strict about missing
files: `policy.Load` treats an absent file as "use the built-in
defaults" and returns no error, which is right for the server and wrong
here. Without the guard, `--config guardain.hcl` would emit the *default*
artifacts with exit 0, and the validation you thought you were running
would have skipped the file entirely. The generator refuses a `--config`
path that does not exist.

An empty `--config` remains legal and still means built-in defaults —
that is how the shipped static tier is generated.

## The shipped static tier

`contrib/generated/` holds the artifacts for the built-in policy. They
are regenerated and diff-checked in CI:

```bash
make monitoring-generate   # rewrite the tier
make lint-monitoring       # fail if it is stale (what CI runs)
```

Hand-editing a generated file turns CI red. If you need a change, change
the generator.

Import them directly only if you run close to the default policy.
Otherwise generate your own tier from your own `guardian.hcl` — a tier
generated from a different policy is missing the alerts for the
mechanisms you enabled, which is exactly the silence this tool exists to
remove.

## Troubleshooting

**The generator warns about undeclarable orgs on every run.** Expected
for the built-in policy, which has no scope block. See *Silent orgs*.

**`--format k8s` fails with "needs an instance selector".** Pass
`--instance-selector key=value` matching a label on your `Grafana` CR.

**A generated dashboard renders empty.** Check the datasource UID first
(`--prometheus-uid`), then whether the panel's mechanism is configured
at all. Panels show `no data` rather than `0` for an absent series
deliberately: a zero is a measurement, an absent series is the absence
of one, and rendering the second as the first is how a dashboard reports
a healthy fleet while the exporter is dead.

**CI says the committed tier is stale.** Run `make monitoring-generate`
and commit the result.
