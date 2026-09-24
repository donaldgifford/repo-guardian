# Generated monitoring tier

Everything under `dashboards/`, `alerts/` and `k8s/` in this directory is
**generated**. Edits are overwritten on the next regeneration and, before
that, rejected by CI.

```bash
make monitoring-generate   # rewrite this tier
make lint-monitoring       # fail if it is stale (what CI runs)
```

The source is the built-in policy — `repo-guardian monitoring generate
--config ''` — so this tier is what a default install should deploy. An
operator running a `guardian.hcl` of their own should generate their own
tier from it rather than importing these files:

```bash
repo-guardian monitoring generate --config guardian.hcl --out ./monitoring
```

That is the whole point of the generator. The artifacts here are scoped
to the mechanisms the *default* policy configures, so a deployment with
a `custom_properties` reconciler, strict scope, or setting rules is
missing the alerts for them; and a deployment with `dry_run` on gets
alerts that can never fire.

## The two formats

`dashboards/` and `alerts/` are `--format json`: plain dashboard JSON for
hand-import, and a `groups:`-rooted rules file for a Prometheus that
loads rules from disk.

`k8s/` is the same artifacts under `--format k8s`: grafana-operator
`GrafanaDashboard` CRs and a `PrometheusRule`. The dashboard JSON inside
`spec.json` is byte-identical to its `dashboards/` twin — one
`dashboard.Render` call feeds both — so the two directories can never
disagree about a panel.

**Read `k8s/` as an example, not as something to apply.** Three values in
it are generation-time flags, pinned in the Makefile so the tier is
reproducible, and all three are almost certainly wrong for your cluster:

| In the committed tier | Flag | Why it matters |
| --- | --- | --- |
| `spec.instanceSelector.matchLabels: {dashboards: grafana}` | `--instance-selector` | Must match labels on your `Grafana` CR, or the dashboard sits unreconciled with nothing in `kubectl get` to say so. |
| `metadata.namespace: monitoring` | `--namespace` | Wrong namespace under ArgoCD means the resources land somewhere you did not intend (the PR #67 post-mortem). |
| no labels on the `PrometheusRule` | `--label` | kube-prometheus-stack's `ruleSelector` matches a release label. Without it the rule is created, shows in `kubectl get`, and is never loaded. |

Two more are defaults rather than pins, and are just as cluster-specific:
`--prometheus-uid` / `--loki-uid` default to the literal UIDs
`prometheus` and `loki`, and `--loki-selector` defaults to
`app="repo-guardian"`. Panels pointed at a datasource UID that does not
exist render empty, which looks exactly like a fleet with no data. See
`docs/operations/monitoring-generation.md`.

Hand-written recipes — the ones an operator adapts rather than
regenerates — live in `../loki/`.
