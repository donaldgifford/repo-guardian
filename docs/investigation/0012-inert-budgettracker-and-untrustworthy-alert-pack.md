---
id: INV-0012
title: "Inert BudgetTracker and untrustworthy alert pack"
status: Open
author: Donald Gifford
created: 2026-07-25
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0012: Inert BudgetTracker and untrustworthy alert pack

**Status:** Open
**Author:** Donald Gifford
**Date:** 2026-07-25

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [A. The BudgetTracker never gets a snapshot in production](#a-the-budgettracker-never-gets-a-snapshot-in-production)
  - [B. Both consumers defer the refresh to each other](#b-both-consumers-defer-the-refresh-to-each-other)
  - [C. Chart alerts get no promtool check — but promtool would not have caught A7](#c-chart-alerts-get-no-promtool-check--but-promtool-would-not-have-caught-a7)
  - [D. helm template | yq | promtool works today](#d-helm-template--yq--promtool-works-today)
  - [E. Six alerts pair a window shorter than their for](#e-six-alerts-pair-a-window-shorter-than-their-for)
  - [F. The chart and contrib alert packs are near-duplicates with no drift gate](#f-the-chart-and-contrib-alert-packs-are-near-duplicates-with-no-drift-gate)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Question

Two questions, joined because the first is a worked example of the second:

1. Is the IMPL-0015 Phase 1 BudgetTracker doing anything in production, and
   if not, what should happen to it?
2. What would it take for the alert pack to be **trustworthy** — meaning a
   green CI run implies every shipped alert is syntactically valid, can
   fire under realistic conditions, and is fed by a metric something
   actually writes?

## Hypothesis

1. The tracker is inert: `RefreshFromAPI` has no production caller, so
   every `SpendableForEnqueue` returns `ErrNoSnapshot`, both gates fall
   open, and the four budget gauges are never published.
2. Adding promtool coverage for the chart's `PrometheusRule` would close
   the alert gap.

Hypothesis 1 is confirmed below. **Hypothesis 2 is refuted** — promtool
passes the exact defect INV-0011 A7 fixed. That refutation is the most
useful thing in this document; it means "add promtool to CI" is not the
remediation, only a floor.

## Context

Both items were found while implementing
[IMPL-0021](../impl/0021-post-impl-0019-hardening-and-structural-cleanup-inv-0011-group.md)
and deliberately left unfixed there — each is a behavioral change needing
its own decision, and IMPL-0021 was already a 17-commit `patch`.

INV-0011 finding A7 was "`RepoGuardianPropertySchemaMissing` pairs
`rate(...[15m]) > 0` with `for: 30m`, so it can never fire." IMPL-0021
Phase 4 fixed that one alert. The question this investigation exists to
answer is whether A7 was a one-off or the visible instance of a class, and
what CI would have to do to catch the class.

**Triggered by:** IMPL-0021 (PR #169), INV-0011 A7, IMPL-0015 Phase 1

## Approach

1. Grep every caller of `budget.Tracker.RefreshFromAPI` across production
   and test code; trace what each budget metric's write path depends on.
2. Read both budget gates (`StaleSweeper`, `Discoverer`) and record what
   each assumes about who refreshes.
3. Check the CI gate for `make lint-alerts` against the files that
   actually contain alerts.
4. Feed promtool a rule with the exact A7 shape and see whether it
   complains.
5. Try to render and validate the chart's `PrometheusRule` end to end, to
   establish whether a CI job is even mechanically possible.
6. Script a window-vs-`for` comparison across both alert files to size the
   class.

## Environment

| Component | Version / Value |
|-----------|----------------|
| repo-guardian | `main` @ `f4390bf` (post IMPL-0021) |
| chart / appVersion | 1.0.0-rc.9 / 1.10.1 |
| promtool | mise-managed (`prometheus` latest) |
| helm / yq | 4.2.2 / 4.48.1 |
| `RECONCILE_FRESHNESS` | 24h (default) |

## Findings

### A. The BudgetTracker never gets a snapshot in production

`RefreshFromAPI` is the only method that writes a snapshot, and every one
of its 13 call sites is a test:

```console
$ grep -rn "RefreshFromAPI" --include='*.go' . | grep -v _test.go
internal/budget/budget.go:106:// RefreshFromAPI fetches the current rate-limit budget for
internal/budget/budget.go:110:func (t *Tracker) RefreshFromAPI(...) error {
```

`main.go` constructs the tracker (`bringUp`, ~line 164) and threads it into
`StaleSweeperOptions.Budget` and `DiscovererOptions.Budget`, but nothing
ever calls `RefreshFromAPI` on it. Consequences, all verified by reading
the write paths:

| Symptom | Mechanism |
|---|---|
| Both budget gates always fall open | `SpendableForEnqueue` hits `!ok` on the empty `snapshots` map → `ErrNoSnapshot` → both callers `return true` |
| `api_budget_{remaining,spendable,reserve_fraction,utilisation}` never published | all four are written only by `publishGauges` (called from `RefreshFromAPI`) or by `SpendableForEnqueue`'s success path, which is unreachable |
| `api_budget_refresh_total` never increments | incremented inside `RefreshFromAPI` |
| `enqueue_gated_by_budget_total` never increments | both increments sit behind a `spendable <= 0` branch that requires a snapshot |
| `RepoGuardianBudgetGated` can never fire | its input metric is the counter above |
| `Decrement` is a silent no-op | early-returns on `!ok` before touching the gauge |

So the entire IMPL-0015 Phase 1 budget feature — 9 metrics, 1 alert, 4 env
vars, 2 chart values, and the values.schema.json ranges validating them —
is dead weight at runtime. Nothing is *broken* by this (falling open is the
documented fail-safe, and the live `RateLimitRemaining` reserve gate in
`StaleSweeper` is a separate, working defence), but operators are tuning
`DISCOVERY_RESERVE_FRACTION` and `DISCOVERY_ESTIMATED_COST_PER_REPO`
against a component that never reads them, and an empty budget dashboard
reads as "healthy" rather than "not wired".

### B. Both consumers defer the refresh to each other

The two gates each document their fall-open path by pointing at the other
one as the thing that populates the cache:

```go
// internal/checker/sweep.go
return true // fall open — caller drives refresh elsewhere
```

```go
// internal/scheduler/discoverer.go
if errors.Is(err, budget.ErrNoSnapshot) {
    // No snapshot — fall open; the StaleSweeper's leader-driven
    // refresh path will populate it.
    return true
}
```

There is no StaleSweeper leader-driven refresh path. This is the mechanism
by which the gap survived review: each side's comment is locally plausible
and cites a neighbour, and no test covers "who calls RefreshFromAPI",
because every test calls it directly in setup. Worth noting for its own
sake — the same shape (two components each deferring to the other, tests
that stub the seam) will hide the next one too.

### C. Chart alerts get no promtool check — but promtool would not have caught A7

The CI job and the make target are both scoped to `contrib/`:

```yaml
# .github/workflows/ci.yml
lint-alerts:
  if: needs.changes.outputs.alerts == 'true' || needs.changes.outputs.workflows == 'true'
#   filter → alerts: ['contrib/prometheus/**']
```

```make
lint-alerts:
	@promtool check rules contrib/prometheus/alerts.yaml
```

The chart's 9 alerts in `charts/repo-guardian/templates/prometheusrule.yaml`
are never checked — that part of the original framing holds. helm-unittest
asserts rendered strings, not PromQL validity.

**But promtool would not have caught A7.** Fed a rule with the exact
defective shape:

```console
$ cat /tmp/badrule.yaml
groups:
  - name: probe
    rules:
      - alert: WindowShorterThanFor
        expr: increase(some_counter_total[15m]) > 0
        for: 30m
$ promtool check rules /tmp/badrule.yaml
Checking /tmp/badrule.yaml
  SUCCESS: 1 rules found
```

`promtool check rules` is a **syntax** checker: PromQL parses, durations
are well-formed, labels are valid. Fireability is semantic and out of its
scope. So the honest decomposition is three distinct defect classes, only
the first of which promtool addresses:

| Class | Example | Caught by |
|---|---|---|
| Syntax — malformed PromQL, bad duration, typo'd function | — | `promtool check rules` |
| Semantic fireability — window vs `for`, window vs the metric's sampling cadence | A7 | `promtool test rules` with synthetic series, or a custom lint |
| Dead input — the metric has no production writer | `RepoGuardianBudgetGated` (finding A) | neither; needs a metric-writer audit |

A7 was class 2. `RepoGuardianBudgetGated` is class 3. Adding promtool to
CI closes class 1 only — worth doing, but it must not be mistaken for
closing the gap that motivated this investigation.

### D. `helm template | yq | promtool` works today

Mechanically possible with tools already in `mise.toml`, verified end to
end:

```console
$ helm template rg charts/repo-guardian \
    --set config.appId=1 --set secrets.webhookSecret=x --set secrets.privateKey=y \
    --set store.dsn=postgres://x --set queue.valkey.dsn=redis://x \
    --set prometheusRule.enabled=true \
  | yq 'select(.kind == "PrometheusRule") | .spec' > /tmp/rr.yaml
$ promtool check rules /tmp/rr.yaml
Checking /tmp/rr.yaml
  SUCCESS: 9 rules found
```

Two notes for whoever implements it: `prometheusRule.enabled` defaults to
`false`, so the job must set it explicitly or it silently checks nothing
(the same "0 rules found — SUCCESS" trap this probe hit on the first
attempt); and the `.spec` extraction is required because promtool wants a
bare rules file, not the CRD wrapper.

### E. Six alerts pair a window shorter than their `for`

Scripted comparison across both files:

```text
contrib/prometheus/alerts.yaml: 4
    RepoGuardianWebhookRejectionsHigh   window [15m], for 30m
    RepoGuardianRuleNeverApplies        window [1h],  for 6h
    RepoGuardianStoreQueryErrors        window [5m],  for 10m
    RepoGuardianBudgetGated             window [15m], for 30m
charts/.../prometheusrule.yaml: 2
    RepoGuardianStoreQueryErrors        window [5m],  for 10m
    RepoGuardianBudgetGated             window [15m], for 30m
```

**This is a smell, not six bugs, and the distinction matters.** For a
metric that increments continuously while the problem persists,
`rate(...[5m]) > 0 for 10m` fires correctly — the expression stays true
across the pending period. A7 was fatal because its metric samples roughly
*once per 24h* (one observation per repo per reconcile, and
`RECONCILE_FRESHNESS` defaults to 24h), so the 15m window was empty almost
always and the condition could not stay true for 30m.

Each of the six needs judging against its own metric's cadence, not a
blanket rewrite. `RepoGuardianRuleNeverApplies` (1h window, 6h `for`) and
`RepoGuardianStoreQueryErrors` (5m/10m) look like the highest-risk of the
remainder; `RepoGuardianBudgetGated` is moot until finding A is resolved.

### F. The chart and contrib alert packs are near-duplicates with no drift gate

8 of the chart's 9 alerts also exist in `contrib/prometheus/alerts.yaml`
(`RepoGuardianPropertySchemaMissing` is chart-only). The chart version is
the contrib expression with the threshold and `for` templated:

```yaml
# contrib
expr: max(repo_guardian_queue_depth{queue="jobs"}) > 1000
for: 10m
# chart
expr: max(repo_guardian_queue_depth{queue="jobs"}) > {{ default 1000 (get $alert "threshold") }}
for: {{ default "10m" (get $alert "for") | quote }}
```

Nothing enforces the correspondence. IMPL-0021's A7 fix landed in the
chart only, which was correct there (chart-only alert) but means the next
fix to a shared alert can silently apply to one copy. Whether these should
be one source with the other generated is a design question, not a defect
— recorded here so the eventual alert work considers it.

## Conclusion

**Answer to Q1: confirmed — the BudgetTracker is inert.** No production
code path writes a snapshot, so all nine budget metrics stay unpublished,
both gates permanently fall open, and `RepoGuardianBudgetGated` cannot
fire. The failure is fail-safe (nothing over-enqueues; the independent
live rate-limit reserve gate still works), so this is wasted capability
and misleading observability rather than an outage.

**Answer to Q2: the original framing was wrong.** Chart alerts genuinely
have no promtool coverage, but promtool does not catch the class of defect
that motivated the question — it passes A7's exact shape. Trustworthiness
needs three separate mechanisms, one per class in finding C, and only the
cheapest is a promtool job.

## Recommendation

Split into two efforts; they share only the `RepoGuardianBudgetGated`
alert and can proceed independently.

**1. Decide the BudgetTracker's fate (needs a decision, not just code).**
Three options, in ascending cost:

- *Delete it.* The live `RateLimitRemaining` reserve gate in `StaleSweeper`
  already provides rate-limit protection. Removes 9 metrics, 1 alert, 4 env
  vars, 2 chart values, and their schema ranges. Cheapest, and honest about
  what ships.
- *Wire it.* Add a leader-scoped refresh — the natural home is the
  `stale-sweep` handler refreshing per installation before its enqueue
  loop, which is what both consumer comments already assume exists.
  Preserves the design intent of IMPL-0015 Phase 1; needs care that a
  refresh per sweep does not itself become the rate-limit cost it is
  meant to protect.
- *Leave it, documented.* Cheapest in effort, worst in honesty — an
  operator tuning `DISCOVERY_RESERVE_FRACTION` deserves to know it is read
  by nothing.

Recommend **wire it**, on the grounds that the reserve fraction and
cost-per-repo knobs are already published in the chart's values schema and
documented in `scaling.md`; deleting them is a user-facing removal, and
the wiring is a single refresh call in a handler that already iterates
installations. If that proves awkward, delete rather than leave.

**2. Make the alert pack trustworthy, cheapest class first.**

- Extend the `alerts` paths-filter to `charts/repo-guardian/templates/prometheusrule.yaml`
  and add the render-extract-check pipeline from finding D to
  `make lint-alerts`. Closes class 1. Low cost, do it regardless of what
  else is decided.
- Audit the six alerts in finding E against each metric's real emission
  cadence. Fix the ones that cannot fire; leave the ones that can, with a
  comment recording why.
- Consider `promtool test rules` unit tests with synthetic series for the
  handful of alerts whose fireability actually matters. This is the only
  mechanism that closes class 2 mechanically, and it is real work — scope
  it deliberately rather than as a side quest.
- Class 3 (dead input metrics) has no tooling answer proposed here. A
  cheap approximation: assert every metric named in an alert expression
  has at least one non-test writer in the Go source. Whether that is worth
  building is an open question.

## Open Questions

1. **BudgetTracker disposition** — (a) wire it via a leader-scoped refresh
   in the `stale-sweep` handler; (b) delete the feature and its
   user-facing knobs; (c) leave it and document that it is inert.
   other:
2. **Alert-pack source of truth** — (a) leave the chart and `contrib/`
   packs as independent copies and add a CI drift check; (b) generate
   `contrib/` from the chart template; (c) leave as-is, accept drift.
   other:
3. **Class-3 detection** — is a "every alerted metric has a production
   writer" check worth building, or is it enough to have found this one by
   hand? other:
4. **Scope** — should the alert work be its own DESIGN, or is it small
   enough to go straight to an IMPL once the open questions above are
   answered? other:

## References

- [IMPL-0021](../impl/0021-post-impl-0019-hardening-and-structural-cleanup-inv-0011-group.md)
  — where both items were found and deliberately deferred (Phase 4, Phase 5)
- [INV-0011](0011-tech-debt-cleanup-inventory-post-impl-0019.md) — finding
  A7, the single alert this generalizes
- [IMPL-0015](../impl/0015-stale-sweep-cutover-and-repository-discovery.md) —
  Phase 1, which introduced the BudgetTracker
- [DESIGN-0017](../design/0017-stale-sweep-cutover-and-repository-discovery.md)
  — the design the tracker implements
- `docs/operations/scaling.md` § Discoverer + BudgetTracker — the operator
  documentation currently describing an inert component
