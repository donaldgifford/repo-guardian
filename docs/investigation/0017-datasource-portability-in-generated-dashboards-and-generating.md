---
id: INV-0017
title: "Datasource portability in generated dashboards and generating the Loki evidence tier"
status: Open
author: Donald Gifford
created: 2026-09-18
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0017: Datasource portability in generated dashboards and generating the Loki evidence tier

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: the missing __inputs block is a decision, recorded in three places](#observation-1-the-missing-inputs-block-is-a-decision-recorded-in-three-places)
  - [Observation 2: the override for that decision exists and was not passed](#observation-2-the-override-for-that-decision-exists-and-was-not-passed)
  - [Observation 3: the hand-edits break the drift gate three ways, only one of which is the datasource](#observation-3-the-hand-edits-break-the-drift-gate-three-ways-only-one-of-which-is-the-datasource)
  - [Observation 4: the Makefile edit repoints the committed static tier at a private policy](#observation-4-the-makefile-edit-repoints-the-committed-static-tier-at-a-private-policy)
  - [Observation 5: the k8s and JSON tiers already carry byte-identical dashboard JSON](#observation-5-the-k8s-and-json-tiers-already-carry-byte-identical-dashboard-json)
  - [Observation 6: the k8s tier is missing more than the Loki rules](#observation-6-the-k8s-tier-is-missing-more-than-the-loki-rules)
  - [Observation 7: the hand-maintained Loki rules duplicate constants the dashboard package already guards](#observation-7-the-hand-maintained-loki-rules-duplicate-constants-the-dashboard-package-already-guards)
  - [Observation 8: the default stream selector matches — and is too broad in two ways](#observation-8-the-default-stream-selector-matches--and-is-too-broad-in-two-ways)
  - [Observation 9: a Loki rules CR has no single obvious target](#observation-9-a-loki-rules-cr-has-no-single-obvious-target)
  - [Observation 10: nothing in the generated tier is multi-instance safe](#observation-10-nothing-in-the-generated-tier-is-multi-instance-safe)
  - [Observation 11 (DEFERRED): SLOs are a different layer from anything generated today, and the two proposals are not alternatives](#observation-11-deferred-slos-are-a-different-layer-from-anything-generated-today-and-the-two-proposals-are-not-alternatives)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
  - [Open questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Question

Three questions, in dependency order:

1. **Why do the generated dashboards carry no `__inputs` / `__requires`
   block?** Is that a gap to close, or a decision to re-read? The
   dashboards emerging from `make monitoring-generate` had to be
   hand-edited after every run to add one, which is the shape of a
   generator that is wrong about its own output.

2. **Can the `--format k8s` tier be made to match the JSON tier?** If
   the answer to (1) is "add the inputs", the `GrafanaDashboard` CR has
   to carry them too — and that means `spec.datasources`, which
   `emit/k8s.go` deliberately omits today.

3. **Can `contrib/loki/rules.yaml` be generated rather than
   hand-maintained,** and emitted by `monitoring generate` alongside the
   dashboards and the `PrometheusRule` — in both `json` and `k8s`
   formats?

Two more were added once the first three were understood, because both
change what the generator has to emit:

4. **What happens when one cluster runs ten repo-guardians** — different
   environments, different GitHub orgs, different policies? Do the
   generated dashboards, alerts and log queries keep working, or do they
   collide?

5. **Should `monitoring generate` emit SLO/SLI artifacts** — via
   [SloK](https://github.com/slok-operator/slok)'s CRDs, or the
   [Kubernetes component SLI](https://kubernetes.io/docs/reference/instrumentation/slis/)
   convention — given that it is already in the business of generating
   monitoring from the policy?

   **DEFERRED (2026-09-22).** Investigated far enough to establish that
   it is blocked on a prerequisite and is not part of this thread; see
   Observation 11 and OQ10. Nothing below depends on it.

## Hypothesis

That (1) is a decision rather than a gap, and that the hand-editing is a
symptom of a *different* problem — the homelab Grafana naming its
datasources something other than the literal UIDs `prometheus` and
`loki` that the generator defaults to. If so, (2) dissolves: nothing
needs to change in the emitter, the generation command needs three flags
it was not given.

That (3) is a real gap, and a larger one than it looks. The Loki rules
restate by hand the same log-line matchers that `internal/monitoring/dashboard/e4.go`
holds as constants under a source-walking test guard — so the dashboard
half is protected against a reword and the rules half is not.

## Context

IMPL-0023 / DESIGN-0022 made dashboards and alerts generated artifacts,
drift-gated by `make lint-monitoring`. `contrib/loki/rules.yaml` was
deliberately left out of that: its header says so, and the reason given
is that what is worth recording depends on fleet size and Prometheus
cardinality tolerance.

What triggered this is a real deployment. `home.hcl` — the policy
actually running in the homelab, copied into the repo root on this
branch and currently untracked — was used to generate both tiers, and
three things went wrong in a row: the JSON dashboards needed hand-edits
to import at all, the k8s run produced no Loki rules, and the Makefile
was edited to point the committed static tier at `home.hcl` to make the
first two reproducible.

**Triggered by:** operating IMPL-0023's output against a live Grafana /
Loki stack; branch `chore/investigate-dashboards`.

## Approach

1. Read the three places the datasource decision could be recorded —
   `internal/monitoring/dashboard/dashboard.go`,
   `internal/monitoring/emit/k8s.go`, `docs/operations/monitoring-generation.md`.
2. Diff the working tree against `HEAD` to characterise exactly what the
   hand-edits changed, and check each change against what
   `dashboard.Render` can emit.
3. Re-run the reported `--format k8s` invocation into a scratch
   directory and inventory the output.
4. Read `emit.Generate` / `dashboardArtifacts` / `alertArtifact` to
   establish whether the two formats can diverge at all.
5. Compare `contrib/loki/rules.yaml`'s expressions against the E4 matcher
   constants and the guard in `logline_test.go`.
6. Check the reported pod labels against the generator's default stream
   selector.

## Environment

| Component | Version / Value |
| --------- | --------------- |
| Branch | `chore/investigate-dashboards` (from `ccb1e29`) |
| Policy under test | `home.hcl` (repo root, untracked) |
| Derived mechanisms | `auto_close_pr`, `custom_properties`, `file_rules`, `ignore_lists`, `orphan_cleanup`, `repo_parking`, `setting_remediation`, `setting_rules`, `strict_scope` |
| Command reproduced | `monitoring generate --config ./home.hcl --format k8s --out ./k8s --allow-cross-namespace-import --instance-selector dashboards=grafana` |
| Chart / appVersion on the pod | `repo-guardian-1.0.0` / `1.14.0` |
| Pod labels | `app.kubernetes.io/{name,instance,version}`, `helm.sh/chart`, `pod-template-hash`, `topology.kubernetes.io/{region,zone}` — **no `app` label** |
| Loki stream labels | `app`, `container`, `detected_level`, `filename`, `namespace`, `node`, `pod`, `service_name`, `stream` |
| Streams in `{app="repo-guardian"}` | four — `{namespace, container}` ∈ `{repo-guardian, repo-guardian-dev}` × `{repo-guardian, valkey}` (confirmed by query, 2026-09-23) |
| Loki deployment | `grafana/loki` Helm chart — no `loki.grafana.com` CRDs |
| Grafana deployment | grafana-operator v5 with unified alerting (`GrafanaAlertRuleGroup`, contact points, notification policies) |
| Grafana floor | 13+ (DESIGN-0022) |

## Findings

### Observation 1: the missing `__inputs` block is a decision, recorded in three places

Not an omission. `internal/monitoring/dashboard/dashboard.go:47-58`:

> Concrete UIDs, never the `${DS_PROMETHEUS}` input placeholder. A
> dashboard carrying an input prompts on every import, which makes the
> generated tier un-provisionable: grafana-operator applies a CR, and
> nobody is there to answer a prompt.

`internal/monitoring/emit/k8s.go:29-34` says the same thing from the CR
side, explaining why `spec.datasources` is absent:

> It exists to remap `${DS_X}` import placeholders onto concrete
> datasources, and the panel library bakes concrete UIDs precisely so no
> placeholder is ever emitted.

And `docs/operations/monitoring-generation.md:71-79` states it as
operator-facing contract, with the consequence spelled out: a CR with an
unanswered input binds to no datasource and renders empty, which is
indistinguishable from a fleet with no data — the exact failure mode the
whole `noData: "no data"` convention exists to prevent.

The three statements agree, so this is not drift between code and docs.
Re-deciding it is a design change, not a bug fix.

### Observation 2: the override for that decision exists and was not passed

`parseGenerateFlags` (`cmd/repo-guardian/monitoring.go:151-156`) already
carries the escape hatch the decision depends on:

| Flag | Default |
| --- | --- |
| `--prometheus-uid` | `prometheus` |
| `--loki-uid` | `loki` |
| `--loki-selector` | `app="repo-guardian"` |

The reproduced command passes none of them. So every panel in
`./k8s/dashboards/` and in `contrib/generated/dashboards/` points at a
datasource whose UID is the literal string `prometheus` (or `loki`).

Grafana mints a random UID (`PBFA97CFB590B2093`-shaped) for any
datasource created through the UI or through provisioning that does not
pin `uid:` explicitly. If the homelab's Prometheus datasource has such a
UID, every generated panel resolves to nothing — and hand-adding
`__inputs` is precisely the workaround for that, because it converts a
hardcoded-wrong UID into an import-time prompt the human can answer.

**This is the load-bearing unknown in the whole investigation, and it is
one command to settle.** Until the real UIDs are read off the cluster,
"the dashboards need `__inputs`" and "the dashboards were generated with
the wrong `--prometheus-uid`" are indistinguishable from the symptom.

**Confirmed 2026-09-23.** None of the homelab's `GrafanaDatasource` CRs
pins a UID:

```text
NAME         UID
prometheus   <none>
loki         <none>
tempo        <none>
```

So Grafana minted random UIDs for all three, and every generated panel
pointing at the literal `prometheus` / `loki` resolved to nothing. The
hand-added `__inputs` was compensating for exactly this. Two fixes, and
the second is better:

1. Read the minted UIDs back and pass them as `--prometheus-uid` /
   `--loki-uid`. Works, but the values are opaque, per-cluster, and
   change if a datasource is ever recreated.
2. **Pin `spec.datasource.uid: prometheus` and `uid: loki` on the CRs.**
   Then the generator's defaults are simply correct on this cluster, the
   committed `contrib/generated/` tier works unmodified, and the UID
   survives datasource recreation. Caveat: changing the UID of an
   existing datasource orphans anything already referencing the old
   random one — any hand-imported dashboard currently working through
   its `__inputs` prompt will need re-importing.

### Observation 3: the hand-edits break the drift gate three ways, only one of which is the datasource

`make lint-monitoring` regenerates into `build/` and `diff -r`s against
`contrib/generated/`. The working-tree diff introduces three independent
mismatches:

1. **`__inputs` / `__requires` blocks** — `dashboard.Render` has no code
   path that emits either.
2. **`"uid": "${DS_PROMETHEUS}"`** in place of the concrete UID.
3. **A JSON formatter ran over the files.** `"tags"` collapsed from a
   three-line array to `"tags": ["repo-guardian", "generated"]`.
   `Render` is `json.MarshalIndent(d, "", "  ")`, which always expands
   arrays — so this diff survives even if (1) and (2) are adopted into
   the generator.

A fourth thing is wrong on its own terms: the hand-written `__requires`
declares Grafana `11.0.0`, while DESIGN-0022 sets the floor at 13+. An
importer honouring that block would accept the dashboard onto a Grafana
two majors below what the panels were authored against.

### Observation 4: the Makefile edit repoints the committed static tier at a private policy

```diff
-MONITORING_GENERATE = ... monitoring generate --config '' --format json --out
+MONITORING_GENERATE = ... monitoring generate --config 'home.hcl' --format json --out
```

The comment two lines above it is the argument against:

> `--config ''` rather than the flag's default, which is `$GUARDIAN_CONFIG`.
> Without it the static tier would be generated from whatever policy the
> developer happens to have exported, and the drift gate would then fail
> for everyone else.

The committed alert diff is the evidence. It gains 46 lines, and every
one of them is mechanism-gated on something `home.hcl` turns on and the
built-in defaults do not:

| Added alert | Gated on | Enabled by |
| --- | --- | --- |
| `RepoGuardianSettingRemediationChurn` | `setting_remediation` | `rule "setting" "delete_branch_on_merge" { remediate = true }` |
| `RepoGuardianRuleNeverApplies` | `strict_scope` | the top-level `scope { orgs = [...] }` block |
| the whole `repo-guardian.custom-property-sync` group | `custom_properties` | `reconcile "custom_properties"` on `catalog_info` |

This is the gating working exactly as designed — and it is also why the
committed tier must not be generated from one operator's policy. In CI
the effect is at least loud rather than silent: `home.hcl` is untracked,
and `requireConfigExists` refuses an explicitly-given path that is not
there, so `make lint-monitoring` fails with
`--config home.hcl: no such file or directory` rather than quietly
producing a different tier.

**Revert this line regardless of what else this investigation
concludes.** Generating a personal tier is a legitimate thing to want;
it just needs its own target or a direct `go run`, not the variable the
drift gate reads.

### Observation 5: the k8s and JSON tiers already carry byte-identical dashboard JSON

`emit.dashboardArtifacts` calls `dashboard.Render(d.Builder)` **once**
per dashboard and then branches on format — writing those bytes as
`<slug>.json`, or handing the same `body` to `renderGrafanaDashboard`,
which embeds it verbatim as `spec.json`
(`internal/monitoring/emit/emit.go:152-172`).

So "get the k8s outputs to match" is already true of what the generator
produces. What diverged is the *hand-edited* `contrib/generated/` tier
against its own generator. Any change adopted into `Render` lands in
both formats automatically and needs no k8s-side work — with one
exception: `__inputs` in a `GrafanaDashboard` CR is inert without a
`spec.datasources` remap, so adopting the placeholder for real means
un-deciding the `k8s.go` comment too, not just adding a block to the
JSON.

### Observation 6: the k8s tier is missing more than the Loki rules

The reproduced run emitted five files — four dashboard CRs and one
`PrometheusRule` — and nothing else. Two further gaps show up in the
output itself, both from flags that exist and were not passed:

```yaml
---
apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaDashboard
metadata:
  name: repo-guardian-kpi     # <- no namespace
spec:
  allowCrossNamespaceImport: true
  folder: repo-guardian
```

- **No `namespace:` on any generated object** (`--namespace` not
  passed). CLAUDE.md records the PR #67 post-mortem for exactly this:
  under kustomize+helm via ArgoCD, a rendered manifest with no namespace
  lands wherever the applying tool defaults to. `emit.Options.Namespace`
  even carries that reasoning in its doc comment.
- **No labels on the `PrometheusRule`** (`--label` not passed).
  kube-prometheus-stack's default `ruleSelector` matches on a release
  label; an unlabelled `PrometheusRule` is created successfully, shows up
  in `kubectl get`, and is never loaded by Prometheus. That is the same
  silent-inert failure as an unanswered datasource input.

### Observation 7: the hand-maintained Loki rules duplicate constants the dashboard package already guards

`contrib/loki/rules.yaml` and `internal/monitoring/dashboard/e4.go`
match the same log lines — `catalog-info parse failed`, `parking
repository until discovery sees it again`, `job exceeded attempt cap`,
`store write-back failed`, `rule-state write-back failed` — and the
stream selector `{app="repo-guardian"}`.

E4 holds those as constants, and `TestLogLines_AreStillEmittedByTheBinary`
walks `internal/` to insist each one is still emitted by the binary.
Its doc comment explains why:

> a matcher that no longer matches returns no rows, and no rows renders
> exactly like "this never happens". An operator would read a blank
> "repositories with an unparseable catalog-info" panel as good news.

The copies in the YAML are guarded by nothing. Reword a log line today
and the E4 test fails loudly while the Loki recording rule silently
starts recording zero — and a recording rule that records zero is worse
than a blank panel, because alerts and dashboards downstream read it as
a healthy fleet.

There is a second, independent argument in the file's own header: every
expression must match whatever was passed to `--loki-selector`, or "the
dashboards and these rules will disagree about which logs are ours."
That is a generation-time value the hand-written file cannot see. The
same `Datasources.Stream()` that E4 uses would make the two agree by
construction.

The original reason for leaving it hand-written — fleet size and
cardinality tolerance — is real but narrower than it looks. It argues
against *shipping opinionated thresholds*, not against generating the
expressions. The `for:` durations and whether to alert in Loki or in
Prometheus are the tunable parts; the matchers and the selector are not.

### Observation 8: the default stream selector matches — and is too broad in two ways

The pod carries no `app` label, only `app.kubernetes.io/name` and
friends, so the expectation was that `DefaultLogStream`
(`app="repo-guardian"`) would match nothing. **It matches.** The shipper
synthesises `app` as a stream label, and `{app="repo-guardian"}`
returned 65 lines over 15 minutes in the homelab.

The nine stream labels available are `app`, `container`,
`detected_level`, `filename`, `namespace`, `node`, `pod`,
`service_name`, `stream`. So no chart change and no `--loki-selector`
override is needed to get *rows*.

The problem is what else comes with them. Confirmed by direct query
rather than inferred from a label view:

```logql
sum by (namespace, container) (count_over_time({app="repo-guardian"}[24h]))
```

**Four series. The default selector matches the full 2×2 cross product
of two namespaces and two containers**, and only one of the four is the
thing E4 is about:

| `namespace` | `container` | What it is |
| --- | --- | --- |
| `repo-guardian` | `repo-guardian` | **the only wanted stream** (~2.95K lines) |
| `repo-guardian-dev` | `repo-guardian` | a second environment's app logs |
| `repo-guardian` | `valkey` | the chart's baked Valkey StatefulSet |
| `repo-guardian-dev` | `valkey` | the dev environment's Valkey |

Three of the four are noise, and they are noisy in two different ways
that fail in opposite directions:

- **The dev namespace makes E4 report faults that are not happening in
  production.** A `catalog-info parse failed` line from dev is
  byte-identical in shape to one from prod, so every line-filter panel
  and every per-repository breakdown silently merges the two.
- **The two Valkey streams break `| json`.** E4's "Log lines by level"
  panel is
  `sum by (level) (count_over_time({app="repo-guardian"} | json [...]))`,
  and non-JSON lines land in a `__error__` bucket rather than under a
  level — skewing the one panel whose job is to say whether anything
  else on the dashboard can be believed. The `detected_level` breakdown
  corroborates it: `unknown` alongside `debug`, and `unknown` is what a
  non-JSON line looks like.

Volume is not the mitigation it appears to be. The prod app stream
dominates the graph and the other three sit near zero in a quiet
window, but the line-filter panels match on *content*, not on rate: one
dev parse failure renders exactly like a production one no matter how
small the surrounding stream is.

So the selector to generate with is narrower than the default, and
takes the four streams down to one:

```bash
--loki-selector 'namespace="repo-guardian", container="repo-guardian"'
```

`Datasources.Stream()` is string concatenation inside braces
(`dashboard.go:110-112`), so a multi-matcher selector needs no code
change. Nothing validates the string, though — a malformed selector
produces dashboards that fail at query time rather than at generation
time, which is what OQ6 resolves.

Note the shape of the right answer here: the disambiguators are
`namespace` and `container`, and `namespace` is the same label
Observation 10 needs to tell ten repo-guardians apart. The log tier and
the metric tier want the same instance identity, which is an argument
for one `--instance`-shaped flag deriving both rather than two
unrelated knobs.

**None of this is a chart bug, and adding an `app` label to the chart
would not have helped.** Stream labels are minted by the shipper's
relabel config, not copied from the pod; the chart is already correct to
use the standard `app.kubernetes.io/*` labels. The lever is
`--loki-selector`, exactly as designed.

What *is* worth doing chart-side is smaller and purely informational:
the chart knows its own release namespace and name, so NOTES.txt can
print the `--loki-selector` value that is right for that install —
including the `container=` matcher, which an operator has no reason to
think of until Valkey's logs are already in their dashboard.

### Observation 9: a Loki rules CR has no single obvious target

For `--format json` the output shape is settled: a
`groups:`-rooted YAML file, exactly what `contrib/loki/rules.yaml` is
today, which `alert.RenderGroups` can almost certainly already produce.

For `--format k8s` there is no equivalent of `PrometheusRule`'s
monopoly. Four targets exist depending on how Loki and Grafana were
deployed, and they are not interchangeable.

**The distinction that matters first: the `grafana/loki` Helm chart is
not the Loki Operator, and only the operator ships rule CRDs.** The
chart installs Loki itself; its ruler consumes rule *files*, from
storage or from a mounted ConfigMap. So a cluster running the chart —
even "with the CRDs" — very likely has no Loki rule CRD at all.

| # | Target | CRD / kind | Ships with |
| --- | --- | --- | --- |
| 1 | Loki Operator | `alertingrules.loki.grafana.com`, `recordingrules.loki.grafana.com` (`loki.grafana.com/v1`) | the Loki **Operator** only — not the Helm chart |
| 2 | Loki chart ruler | plain `ConfigMap`, discovered by a sidecar label (`loki_rule` in the common configuration) or mounted via the chart's ruler directories values | the `grafana/loki` Helm chart |
| 3 | Ruler storage directly | a file at `/etc/loki/rules/<tenant>/repo-guardian.yaml` | any Loki; what `contrib/loki/rules.yaml`'s header documents today |
| 4 | Grafana unified alerting | `grafanaalertrulegroups.grafana.integreatly.org` (`GrafanaAlertRuleGroup`) | grafana-operator v5 |

Option 4 deserves more weight than it first appears. It needs no Loki
ruler and no remote-write path: Grafana evaluates the LogQL itself
against the Loki datasource, on a schedule, and routes through Grafana's
own alerting. For a cluster that already runs grafana-operator — which
is the cluster that prompted this investigation — it is the only option
whose prerequisite is already installed. Its cost is that the rules
become Grafana-managed rather than Prometheus series, so they do not
compose with the `PrometheusRule` alerts; the "alert in one place"
advice in `contrib/loki/rules.yaml` applies here too.

**Settled against the homelab cluster.** `kubectl get crd` returns
thirteen `grafana.integreatly.org` CRDs and **zero** under
`loki.grafana.com`:

```text
grafanaalertrulegroups.grafana.integreatly.org
grafanacontactpoints.grafana.integreatly.org
grafanadashboards.grafana.integreatly.org
grafanadatasources.grafana.integreatly.org
grafanafolders.grafana.integreatly.org
...
```

So **option 1 is out** — confirming that the `grafana/loki` Helm chart
ships no rule CRDs — and **option 4 is available**, on a grafana-operator
new enough to carry the full unified-alerting surface (contact points,
notification policies, mute timings, library panels, manifests).

That leaves a real choice between 2 and 4, and the honest answer is that
it may be **both, split by rule kind**:

- The two **alerting** rules in `contrib/loki/rules.yaml` map cleanly
  onto `GrafanaAlertRuleGroup`. Grafana evaluates the LogQL against the
  Loki datasource on a schedule and routes through the contact points
  and notification policies this cluster already has as CRs.
- The five **recording** rules are the uncertain half. Grafana-managed
  recording rules need a Prometheus write target configured in Grafana,
  and whether this operator version exposes `record` on the CRD needs
  checking against the installed schema
  (`kubectl explain grafanaalertrulegroup.spec.rules`) rather than
  assuming. If it does not, recording rules fall back to option 2 — a
  `ConfigMap` for the Loki chart's ruler — and the tier emits two
  different kinds for the two halves.

There is also a design question hiding in option 4 that option 2 does
not have: a `GrafanaAlertRuleGroup` is Grafana-managed, so its alerts do
**not** become Prometheus series and do not compose with the generated
`PrometheusRule`. The "alert in one place, not both" advice in
`contrib/loki/rules.yaml` becomes a fork in the road rather than a
caveat.

Picking is an operator-facing decision, not an implementation detail,
and it is the only genuinely open part of question (3).

### Observation 10: nothing in the generated tier is multi-instance safe

The homelab already runs two repo-guardians — `repo-guardian` and
`repo-guardian-dev` (Observation 8) — so this is not hypothetical, and
ten would break in the same ways only louder. Every identifier the
generator emits is a hardcoded constant:

| Identifier | Where | Parameterizable today |
| --- | --- | --- |
| Dashboard UID (`repo-guardian-kpi`, `-detail`, `-system`, `-logs`) | `e1.go:61`, `e2.go:21`, `e3.go:32`, `e4.go:68` | **no** |
| Dashboard slug → JSON filename **and** `GrafanaDashboard` `metadata.name` | `Dashboard.Slug`, e.g. `e1.go:112` | **no** |
| Grafana folder (`repo-guardian`) | `suite.go:53` | **no** |
| Dashboard title (`repo-guardian — KPI`) | same `New(...)` calls | **no** |
| `PrometheusRule` `metadata.name` | `emit.Options.Name` | yes, `--name` |

The asymmetry on that last row is the tell: alerts were given a `--name`
and dashboards never were.

Four distinct failures follow, in rough order of how quickly they bite:

1. **The Grafana dashboard UID is global to a Grafana, not to a
   namespace.** Two `GrafanaDashboard` CRs in different namespaces both
   declaring `uid: repo-guardian-kpi`, both filed into the same Grafana,
   fight over one dashboard. With `resyncPeriod` set they will keep
   fighting, flapping the dashboard between two policies' panel sets on
   every resync. Nothing errors.
2. **Same-namespace installs collide on `metadata.name`** before they
   even reach Grafana.
3. **Alert expressions have no instance dimension.**
   `sum(increase(repo_guardian_repos_checked_total[2h])) == 0` is
   `RepoGuardianNoRepoChecks`, and a `sum` with no `by` aggregates across
   every scraped instance — so one dead repo-guardian among ten is
   perfectly masked by the nine that are fine. This is the alert whose
   entire job is to notice that nothing is being checked.
4. **`max by (org)` merges instances, not just replicas.** The
   posture-query contract (CLAUDE.md, IMPL-0023) says to use
   `max by (org)` so a demoted replica serving stale values cannot
   double-count through `sum`. That reasoning holds *within one
   deployment*. Across two deployments that both watch the same GitHub
   org — a staging and a production repo-guardian pointed at the same
   place, which is exactly what a dev instance is for — `max by (org)`
   silently reports whichever instance has the higher number, as though
   it were one fleet.

Failure 4 is the nastiest, because it is wrong in the direction that
looks healthy and it is produced by following the documented rule.

Note what the app does *not* provide: no metric carries an
instance-identifying label. `org` and `installation_id` exist;
namespace/pod/job come from the Prometheus scrape config (a
ServiceMonitor's target labels), not from the binary. So the
disambiguator must come from the scrape, and the generator has to be
told what it is called.

**The hard constraint on any fix is that mechanism gating is
per-policy.** Ten instances with ten `guardian.hcl` files produce ten
*different* panel sets and ten different alert sets — that is the entire
point of `alert.Generate`'s kept set. So "one shared dashboard with an
`instance` template variable" only works for a homogeneous fleet, and
cannot be the general answer. The general answer is per-instance
generation with every identifier prefixed, plus an injected instance
matcher on every query. The variable approach remains a nice
optimisation for the homogeneous case.

### Observation 11 (DEFERRED): SLOs are a different layer from anything generated today, and the two proposals are not alternatives

**Deferred 2026-09-22.** Recorded so the next person does not re-derive
it, not as work queued behind the rest. The one durable conclusion is
the last paragraph: the prerequisite is a freshness gauge, not a CRD
emitter.

Both references are real and both are usable, but they answer different
questions and only one is a candidate for this generator.

**[SloK](https://github.com/slok-operator/slok)** is a Kubernetes-native
SLO operator: `ServiceLevelObjective`, `SLOComposition` and
`SLOCorrelation` CRDs, from which it generates Prometheus recording
rules for SLI error rate, error budget and burn rate, plus multi-window
burn-rate alerts as `PrometheusRule` resources. An SLI is either a
PromQL pair the author supplies or one of its built-in templates
(`http-availability`, `http-latency`, `kubernetes-apiserver`). This is
directly analogous to what `monitoring generate` already does — derive
monitoring artifacts from a declarative source — and a
`ServiceLevelObjective` is a plausible fifth artifact type alongside the
dashboards and the `PrometheusRule`.

**[Kubernetes component SLIs](https://kubernetes.io/docs/reference/instrumentation/slis/)**
is something else entirely: a convention for *Kubernetes component
binaries* to expose `kubernetes_healthcheck{name,type}` and
`kubernetes_healthchecks_total{name,type,status}` on `/metrics/slis`,
stable since 1.32. It is an instrumentation contract for the binary, not
a generation target — and the metric names are namespaced to Kubernetes'
own components, so adopting them verbatim in an application that is not
one would be actively misleading. The *pattern* (a healthcheck gauge
plus a counter, scraped at high frequency) is portable; the names are
not.

If repo-guardian wants SLOs, the honest question is what the SLI should
be, and the interesting answer is not availability. Two are already
measurable:

- **Webhook availability and latency** — `otelhttp` already instruments
  the webhook route, so `http_server_request_duration_seconds` with its
  `http_response_status_code` label is an off-the-shelf
  availability/latency SLI pair. Real, and close to meaningless: nobody
  runs repo-guardian for its HTTP endpoint.
- **Freshness — the SLI that matches the product.** What repo-guardian
  promises is that every tracked repository gets checked and converges.
  "Fraction of tracked repositories checked within the freshness window"
  is the SLO an operator would actually defend, and it is *not currently
  a series*: `repo_state.last_checked_at` lives in Postgres and only
  `StaleRepos` reads it. The posture exporter is already the
  leader-elected reader of that table publishing gauges, so a
  `repos_stale{org}` companion to `repos_tracked{org}` would be a small
  addition and the thing that makes an SLO possible at all.

That ordering matters: **an SLO layer over today's metrics would measure
the wrong thing.** The prerequisite is a freshness gauge, not a CRD
emitter.

This is also where the scope line falls. Questions 1–4 are about what
`monitoring generate` emits for artifacts that already exist. Question 5
starts with a new metric and a new instrumentation decision, and should
graduate to its own DESIGN rather than ride along here.

## Conclusion

**Answer:** question (1) is **refuted as a gap** — the absent `__inputs`
block is a deliberate, triply-recorded decision, and the hand-editing is
almost certainly compensating for a datasource-UID mismatch that
`--prometheus-uid` / `--loki-uid` already solve (pending Observation 2's
one-command check).

Question (2) is **already satisfied** — both formats embed the identical
bytes from a single `dashboard.Render` call. What diverged is the
hand-edited `contrib/generated/` tier from its own generator, plus two
k8s-only gaps (`--namespace`, `--label`) that are unpassed flags rather
than missing features.

Question (3) is **confirmed as a real gap, and the most valuable one
here.** The Loki rules restate matchers and a stream selector that the
generator already owns, with none of the guard rails the dashboard half
has. The reason they were left hand-written argues against shipping
opinionated thresholds, not against generating expressions. The target
shape is narrowed but not closed: no `loki.grafana.com` CRDs exist, so
it is `GrafanaAlertRuleGroup` and/or a ruler `ConfigMap` (OQ3).

Question (4) is **confirmed, and is the largest gap this investigation
found.** Every dashboard UID, slug, folder and title is a hardcoded
constant, so two instances filed into one Grafana fight over the same
dashboard forever; and because no metric carries an instance label,
`RepoGuardianNoRepoChecks` cannot see one dead instance among ten, while
`max by (org)` silently merges two instances that watch the same org.
The homelab already runs two, so this is live rather than anticipated.

Question (5) is **deferred.** SloK is a genuine fit for a fifth artifact
type, but an SLO over today's metrics would measure webhook availability
— which is not what anyone runs repo-guardian for. The SLI that matches
the product is freshness, and it is not a series yet. Revisit if and
when `repos_stale{org}` exists; nothing else in this document waits on
it.

A finding nobody was looking for came out of the same evidence and
outranks every question above in immediate impact: **the default Loki
stream selector matches four streams and only one of them is wanted.**
Two namespaces × two containers — a dev environment and the chart's own
baked Valkey, both confirmed by query. See Observation 8. That is a
live misreading of production evidence today, independent of anything
generated, and its fix is one flag.

## Recommendation

**Done on this branch:**

1. ✅ Reverted the `MONITORING_GENERATE` line in the `Makefile` to
   `--config ''`, and regenerated `contrib/generated/` from the built-in
   defaults, discarding the hand-edits. `make lint-monitoring` is green.
2. ✅ `home.hcl` and the local `k8s/` output are `.gitignore`d. The
   policy stays at the repo root as a local functional test — a real
   deployed config exercises mechanisms the built-in defaults never turn
   on — while the committed tier stays defaults-only.
3. ✅ `make monitoring-generate` now emits a `--format k8s` reference
   tier into `contrib/generated/k8s/` alongside the JSON one, and
   `make lint-monitoring` drift-gates both. Verified non-vacuous by
   removing one generated CR and confirming the gate fails
   (`Only in build/monitoring/k8s/dashboards: repo-guardian-logs.yaml`).

   The k8s tier pins `--instance-selector dashboards=grafana` (required —
   emit refuses to render without it) and `--namespace monitoring`, both
   as *examples*. No `--label` is pinned: guessing the release label
   kube-prometheus-stack's `ruleSelector` wants would ship a
   `PrometheusRule` that looks applied and is never loaded, which is a
   worse failure than an obviously-absent label.
   `contrib/generated/README.md` tabulates the three flags and the three
   defaults that an operator must override.

**Still to do, to close questions (1) and (2):**

1. Read the real datasource UIDs off the cluster — the
   `grafanadatasources` CRD is installed, so:
   `kubectl get grafanadatasource -A -o custom-columns=NAME:.metadata.name,UID:.spec.datasource.uid`.
   This is the last unknown behind questions (1) and (2).
2. Re-run the homelab generation with the real values and the narrowed
   selector from Observation 8:

   ```bash
   repo-guardian monitoring generate --config ./home.hcl --format k8s --out ./k8s \
     --prometheus-uid '<uid>' --loki-uid '<uid>' \
     --loki-selector 'namespace="repo-guardian", container="repo-guardian"' \
     --namespace '<ns>' --label 'release=<kps-release>' \
     --instance-selector dashboards=grafana --allow-cross-namespace-import
   ```

   Confirm the dashboards import with no hand-editing, and that E4 shows
   no Valkey lines and no `repo-guardian-dev` traffic — the selector
   should take `{app="repo-guardian"}`'s four streams down to one.

If step 2 lands clean, the whole of questions (1) and (2) closes as
"pass the flags", and the residue is two docs fixes: the `--format k8s`
example in `docs/operations/monitoring-generation.md` should carry
`--namespace` and `--label`, because an un-namespaced CR and an
unlabelled `PrometheusRule` both fail silently; and the `--loki-selector`
section should lead with the `container=` matcher, because the chart's
own baked Valkey lands in the default selection and nobody would guess
that from the outside.

**Then, as a follow-up DESIGN, in this order.** Multi-instance comes
first because it changes the shape of every identifier the other work
would emit — doing it second means regenerating the Loki tier twice.

1. **Multi-instance identity (question 4, OQ8/OQ9).** An `--instance`
   flag suffixing every dashboard UID, slug, folder, title and object
   name, plus an injected instance matcher on every PromQL and LogQL
   expression. Two things make this more than plumbing: `max by (org)`
   currently merges instances that watch the same org, so the injected
   matcher is a correctness fix rather than a cosmetic one; and the
   disambiguating label comes from the scrape rather than the binary,
   so the generator has to be told its name.
2. **The Loki tier (question 3, OQ4/OQ5/OQ7).** Generate from the E4
   matcher constants and `Datasources.Stream()`, emitted by the same
   `monitoring generate` run, gated on the same mechanisms the alerts
   already use (`MechanismCustomProperties` for the catalog-parse rule,
   `MechanismRepoParking` for the parking rule), and drift-gated the
   same way. That reuses `alert.RenderGroups`, puts the rules under
   `TestLogLines_AreStillEmittedByTheBinary` by construction, and makes
   the "dashboards and rules must agree about the selector" requirement
   structural rather than a comment.
3. **`--loki-selector` validation (OQ6).** Small, independent, and worth
   doing alongside whichever of the above lands first.

**Deferred (question 5, OQ10):** a `repos_stale{org}` gauge on the
posture exporter, then an SLO emitter over it. Not part of this thread —
it starts with a new metric rather than a new artifact, and nothing in
items 1–3 above depends on it.

### Open questions

- **OQ1 — Does the homelab Grafana's Prometheus datasource UID equal the
  literal `prometheus`?** — **RESOLVED (a).** The flags stay the
  mechanism; no `__inputs` support is added. The existing
  `--prometheus-uid` / `--loki-uid` were simply not being passed.
  ~~(b)=pin `uid: prometheus` on the datasource; (c)=emit `__inputs`
  behind an opt-in flag~~

- **OQ2 — If `__inputs` is ever adopted, does the `GrafanaDashboard` CR
  grow `spec.datasources`?** — **MOOT**, dropped with OQ1. The
  concrete-UID decision in `dashboard.go` and `emit/k8s.go` stands
  unchanged.

- **OQ3 — Which Kubernetes shape does the Loki rules tier emit?** —
  **RESOLVED: `PrometheusRule` by default, `GrafanaAlertRuleGroup`
  behind a flag.** Keeps every alert in one evaluator and one place by
  default, which is the advice `contrib/loki/rules.yaml` already gives,
  and does not make the generated tier depend on grafana-operator being
  the alerting path. Note the consequence for the recording half: a
  Prometheus-evaluated alert over a Loki-derived series needs the Loki
  ruler to remote-write the recording rules first, so the recording
  rules still need a home (OQ7). ~~(b)=ConfigMap only;
  (c)=GrafanaAlertRuleGroup only~~

- **OQ4 — Do the generated Loki rules include the alerting group, or
  only the recording rules?** The current file ships both and tells the
  operator to pick one place to alert. (a)=recording rules only, and
  alert in Prometheus over the recorded series so every alert lives in
  one place — which is also what OQ3's resolution implies; (b)=both,
  mirroring the current file; other:

- **OQ5 — Does `contrib/loki/rules.yaml` move under
  `contrib/generated/`?** (a)=yes, and it becomes drift-gated like the
  rest; (b)=no — keep the hand-written example with its long
  explanatory header and generate alongside it; other:

- **OQ6 — Does `--loki-selector` get validated at generation time?** —
  **RESOLVED (a).** A cheap syntactic check — balanced quotes,
  `key op "value"` shape, reject enclosing braces — catching the
  realistic typos without taking a LogQL parser dependency the dashboard
  package was deliberately kept clear of. Observation 8 makes this more
  than cosmetic: the selector operators now need is multi-matcher, and
  its failure lands at query time on a dashboard rather than at
  generation time in a terminal. ~~(b)=Loki's own parser; (c)=nothing~~

- **OQ7 — Where do the Loki *recording* rules live, given OQ3?**
  `GrafanaAlertRuleGroup` was the only CRD available, and OQ3 chose not
  to make it the default. (a)=a ruler `ConfigMap`, discovered by a
  configurable sidecar label, since that is what the `grafana/loki` chart
  consumes; (b)=emit them as a plain rules file only, and let the
  operator mount it however their Loki is deployed; (c)=skip recording
  rules entirely and have Prometheus alert on nothing Loki-derived,
  losing the per-repository alerting this whole tier exists for; other:

- **OQ8 — How does the generator disambiguate ten instances
  (Observation 10)?** (a)=an `--instance <name>` flag that suffixes every
  dashboard UID, slug, folder, title and object name, AND injects a
  matcher into every PromQL and LogQL expression — per-instance
  generation, which is the only shape that works when the ten policies
  differ; (b)=a Grafana template variable over an instance label, one
  shared dashboard set — simpler, but only correct for a homogeneous
  fleet; (c)=both, with (b) as an opt-in for fleets that opt into a
  single policy; other:

- **OQ9 — Which label disambiguates instances, and who sets it?** No
  app metric carries one; it comes from the scrape. (a)=`namespace`,
  which a ServiceMonitor supplies for free and which matched the homelab
  exactly; (b)=`job`; (c)=a generator flag naming the label AND its
  value, so the operator can point at whatever their scrape config
  produces; (d)=teach the binary to carry its own identity as a const
  label so the series is self-describing rather than scrape-dependent;
  other:

- **OQ10 — Does question 5 (SLOs) become its own DESIGN, and does the
  freshness gauge come first?** — **DEFERRED 2026-09-22.** Not being
  decided now. When it is revisited, Observation 11 is the starting
  point and (a) is where it left off: add `repos_stale{org}` to the
  posture exporter first, then design the SloK `ServiceLevelObjective`
  emitter over it, because an SLO over today's metrics would measure
  webhook availability rather than the product. ~~(b)=ship the emitter
  now over the existing HTTP SLIs; (c)=no SLO layer at all~~

## References

- DESIGN-0022 — compliance posture, dashboard suite, Finding G (no repo
  label on metrics) and Finding I (the tier taxonomy)
- IMPL-0023 — Phase 5 (`monitoring generate`), Phase 6 (panel library)
- INV-0012 — the inert `BudgetTracker` and untrustworthy alert pack; the
  origin of mechanism gating
- INV-0013 — state-vs-event metrics and the dashboard suite
- `docs/operations/monitoring-generation.md` — operator-facing generation
  contract, including the concrete-UID rule
- `docs/operations/scaling.md` §Compliance posture
- `internal/monitoring/dashboard/dashboard.go` — `Datasources`,
  `DefaultLogStream`, `Render`
- `internal/monitoring/emit/k8s.go` — `renderGrafanaDashboard`, and why
  `spec.datasources` is absent
- `internal/monitoring/dashboard/logline_test.go` — the log-line guard
- `internal/monitoring/dashboard/suite.go` — `Dashboard.Slug`,
  `GrafanaFolder`, `ValidateSuite`; the hardcoded identifiers of
  Observation 10
- [SloK](https://github.com/slok-operator/slok) — Kubernetes-native SLO
  operator; `ServiceLevelObjective` / `SLOComposition` / `SLOCorrelation`
- [Kubernetes component SLIs](https://kubernetes.io/docs/reference/instrumentation/slis/)
  — `kubernetes_healthcheck` / `kubernetes_healthchecks_total` on
  `/metrics/slis`; an instrumentation convention for Kubernetes
  component binaries, not a generation target
- `contrib/loki/rules.yaml` — the hand-maintained tier under
  investigation
</content>
</invoke>
