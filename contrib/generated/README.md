# Generated monitoring tier

Everything under `dashboards/` and `alerts/` in this directory is
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

For Kubernetes, add `--format k8s` to emit a `PrometheusRule` and
grafana-operator `GrafanaDashboard` CRs instead of plain files. See
`docs/operations/` for the flags.

Hand-written recipes — the ones an operator adapts rather than
regenerates — live in `../prometheus/` and `../grafana/`.
