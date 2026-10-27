# Generating dashboards and alerts

`repo-guardian monitoring generate` derives Grafana dashboards and
Prometheus alerting rules from the same controls policy root
(`enterprise.hcl`, `catalogue/`, `orgs/`) the server loads.

The point is that a panel cannot outlive the control it charts. A
hand-maintained dashboard drifts the moment a control is renamed or
withdrawn, and it drifts *silently* — an empty panel and a
compliant fleet look identical. So does an alert whose metric has no
producer: it never fires, and a never-firing alert looks exactly like a
healthy system. That is not hypothetical; it is
[INV-0012](../investigation/0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
finding A, which survived for months.

## Quick start

```bash
repo-guardian monitoring generate --config ./policy --out ./monitoring
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
  --config ./policy \
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
| `--config` | `$GUARDIAN_CONFIG` | Controls policy root to derive from, loaded with the built-in control types. Empty means the unconfigured static tier (no orgs, no controls, no mechanisms). A path that does not exist is an error, not a fallback. |
| `--out` | `./monitoring` | Output directory. Created if absent. |
| `--format` | `json` | `json` for plain files, `k8s` for custom resources. |
| `--org` | — | Repeatable. Adds an org to the per-org rows. See *Silent orgs* below. |
| `--prometheus-uid` | `prometheus` | UID of the Prometheus datasource the panels query. |
| `--loki-uid` | `loki` | UID of the Loki datasource the log panels query. |
| `--loki-selector` | `app="repo-guardian"` | Stream selector, without braces, matching repo-guardian's logs. See *The log dashboard's stream selector* below. |
| `--prometheus-selector` | — | Label matchers, without braces, added to every PromQL series selector in the dashboards and alerts. See *Several installs in one Prometheus* below. |
| `--namespace` | — | `k8s` only. Stamped on every generated object. |
| `--name` | `repo-guardian` | Prefixes every dashboard's uid, title and CR name (`<name>-kpi`, `<name> — KPI`), and names the `PrometheusRule`. At most 33 characters, so `<name>-detail` fits Grafana's 40-character uid limit. |
| `--folder` | `repo-guardian` | `k8s` only. The Grafana folder the dashboards are filed under (`spec.folder`). |
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
series; if it is off, the series never appears. Mechanisms come from the
controls the policy actually **assigns**: a catalogue entry nothing
assigns turns nothing on. A `custom_properties` control with
`remediation { apply = "workflow" }` is the GitHub Actions mechanism;
`repo_settings` and `branch_ruleset` are the setting and ruleset
mechanisms (plus remediation when a rule remediates); the file-shaped
types are the file-rules mechanism.

On the controls line no alert is mechanism-gated yet: every alert reads
a series the evaluator, ingest, the store or the Temporal SDK always
produces. Gated alerts return with remediation (IMPL-0030), and the
generator still reports every skipped alert and why.

### The four dashboards

Each answers one question, and they are deliberately not combinable: a
business gauge next to a service counter cannot be read, because when
the picture looks wrong there is no way to tell which half is lying.

| Dashboard | Answers | Reads |
| --- | --- | --- |
| `repo-guardian-kpi` | Is the fleet compliant, and which control is failing? | Prometheus |
| `repo-guardian-detail` | Which organisation? | Prometheus |
| `repo-guardian-system` | Is the service itself healthy? | Prometheus |
| `repo-guardian-logs` | Which repository, and why? | Loki |

The last one is not garnish. repo-guardian's metrics carry no
repository label on purpose — a 20,000-repo fleet times a dozen controls
is a cardinality bomb — so "which repository" is a question only the
logs can answer.

### The log dashboard's stream selector

The log dashboard's panels start from a Loki stream selector, and that
selector is the one input on any of these dashboards that this project
cannot verify for you: stream labels are minted by your log shipper.
The `app="repo-guardian"` default is a promtail/alloy convention, not
something the binary controls.

```bash
repo-guardian monitoring generate --config ./policy \
  --loki-selector 'job="platform/repo-guardian"'
```

Check yours before believing an empty dashboard:

```bash
logcli query '{app="repo-guardian"}' --limit 5
```

A blank log dashboard is far more likely to be a selector mismatch than
a quiet fleet. The first panel's description prints the selector that
was compiled into it.

The panels match on specific log lines — the catalog-parse failure, the
parking line, the attempt-cap drop, the sweep summary. Those strings
are a contract, not an implementation detail: a matcher that stops
matching returns no rows, and no rows renders exactly like "this never
happens". `TestLogLines_AreStillEmittedByTheBinary` walks `internal/`
and fails the build if any of them stops being emitted.

### Several installs in one Prometheus

Out of the box the queries are unscoped: `repo_guardian_controls_status`
means every series of that name in the datasource. That is right for a
Prometheus holding one repo-guardian and nothing else, and wrong as soon
as it holds two (prod and dev, v1 and v2), or anything else exporting
the OpenTelemetry HTTP semconv series (`http_server_request_duration_seconds`
is also what Alloy, among others, exports). Panels then sum the installs
together, and the alerts evaluate across all of them and fire alongside
each install's own.

Give each install its own selector and its own name:

```bash
repo-guardian monitoring generate --config ./policy --format k8s \
  --namespace monitoring --instance-selector dashboards=grafana \
  --name repo-guardian-dev \
  --prometheus-selector 'namespace="repo-guardian-dev"'
```

The selector is added to every series selector on every panel, to the
org variable, and to every alert expression, merged into any matchers
already there. Use whichever label your scrape config gives the pods:
`namespace`, `job`, `service`. `--name` keeps the two installs' dashboards
from overwriting each other in Grafana, which keys dashboards by uid.

### Silent orgs

The per-org rows are best when they come from the config, because a
configured row that renders empty is a *signal* — that org has stopped
reporting. Rows discovered from the series instead just disappear, which
is the failure this design exists to avoid.

The generator declares a row for every org the policy root names:
`enterprise { orgs = [...] }`, every `orgs/<org>.hcl` file, and every
`--org`. It warns when it has none — an empty `--config`, which is how
the shipped tier is generated — and then the rows are discovered from
`controls_coverage`. A name containing a glob character (`acme-*`) is
not an org, so it never becomes a declared row; name orgs literally.

## Generation is also config validation

`monitoring generate` loads the policy through exactly the code path the
server uses, so **a config that fails to load fails generation**. Adding
it to CI is a free syntax-and-semantics check on the policy root — the
same check the server runs at startup.

```yaml
- name: Validate the controls policy
  run: repo-guardian monitoring generate --config ./policy --out "$(mktemp -d)"
```

This catches HCL parse errors, unknown attributes, unknown control types
and parameters, unresolvable `replace`/`exclude` references and template
compile errors — everything `policy.Load` validates — before a deploy
does.

One caveat, and it is the reason `--config` is strict about missing
paths: without the guard, `--config ./polcy` could emit the
unconfigured artifacts with exit 0, and the validation you thought you
were running would have skipped the policy entirely. The generator
refuses a `--config` path that does not exist.

An empty `--config` remains legal and means the unconfigured static tier
— that is how the shipped tier is generated.

## The shipped static tier

`contrib/generated/` holds the unconfigured artifacts: no orgs (rows
are discovered from series), no controls, every unconditional alert. They
are regenerated and diff-checked in CI:

```bash
make monitoring-generate   # rewrite the tier
make lint-monitoring       # fail if it is stale (what CI runs)
```

Hand-editing a generated file turns CI red. If you need a change, change
the generator.

Import them directly only if you do not need declared org rows.
Otherwise generate your own tier from your own policy root — a tier
generated from a different policy is missing the alerts for the
mechanisms you enabled, which is exactly the silence this tool exists to
remove.

## Troubleshooting

**The generator warns that no orgs are declared.** Expected with an
empty `--config`. See *Silent orgs*.

**`--format k8s` fails with "needs an instance selector".** Pass
`--instance-selector key=value` matching a label on your `Grafana` CR.

**A generated dashboard renders empty.** Check the datasource UID first
(`--prometheus-uid`), then whether the panel's mechanism is configured
at all. Panels show `no data` rather than `0` for an absent series
deliberately: a zero is a measurement, an absent series is the absence
of one, and rendering the second as the first is how a dashboard reports
a healthy fleet while the exporter is dead.

**Every panel on the log dashboard is empty.** Almost always the stream
selector — see *The log dashboard's stream selector*. Confirm with
`logcli query '{...}' --limit 5` before concluding the fleet is quiet.
If only the repository-fault panels are empty, that is the good case:
those match on failure lines, so a healthy fleet produces none.

**CI says the committed tier is stale.** Run `make monitoring-generate`
and commit the result.
