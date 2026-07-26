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
  - [G. Rate-limit protection is three layers, and the other two work](#g-rate-limit-protection-is-three-layers-and-the-other-two-work)
  - [H. HasFreshSnapshot is a phantom method](#h-hasfreshsnapshot-is-a-phantom-method)
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
   open, and the four budget gauges are never published. Corollary
   assumed at the outset: that this leaves rate-limit pressure
   under-observed.
2. Adding promtool coverage for the chart's `PrometheusRule` would close
   the alert gap.

Hypothesis 1's mechanism is confirmed below, but **its corollary is
refuted** (finding G): two other rate-limit layers work, and the operator
can already see API pressure without the tracker. **Hypothesis 2 is
refuted outright** — promtool passes the exact defect INV-0011 A7 fixed.

Those two refutations are the most useful things in this document. The
first means the tracker's disposition turns on queue health and
forward-looking capacity, not on protection. The second means "add
promtool to CI" is a floor, not the remediation.

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
is dead weight at runtime. Nothing is *broken* by this — falling open is
the documented fail-safe, and two other rate-limit layers work (finding G,
which revises this paragraph's first draft, where I had counted one) — but
operators are tuning `DISCOVERY_RESERVE_FRACTION` and
`DISCOVERY_ESTIMATED_COST_PER_REPO` against a component that never reads
them, and an empty budget dashboard reads as "healthy" rather than "not
wired".

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

### G. Rate-limit protection is three layers, and the other two work

Finding A called the `StaleSweeper` reserve gate "a separate, working
defence." That understated it — there are **two** working layers, and the
one that most directly answers "are we hammering the API?" is the one this
investigation nearly missed:

| # | Layer | Where | Behaviour under pressure | Metrics | Status |
|---|---|---|---|---|---|
| 1 | Transport limiter | `internal/github/ratelimit.go`, wrapping **every** client (app + per-installation, `client.go:59` and `:1019`) | pre-emptively *sleeps* `untilReset / remaining` per request; retries primary (403 + `Remaining: 0`) and secondary (`Retry-After`) limits | `github_rate_remaining`, `github_rate_limit_waits_total{reason}`, `github_rate_limit_wait_seconds` | **works** |
| 2 | Sweep reserve gate | `StaleSweeper.allowedByRateLimit`, IMPL-0011 P5 | *skips* the repo when `remaining < reserve × limit` | `rate_limit_remaining`, `rate_limit_reserve_blocked_total` | **works** |
| 3 | BudgetTracker | IMPL-0015 P1 | would *decline the enqueue* before work is created | the 9 `api_budget_*` / `enqueue_gated_by_budget_total` metrics | **inert** (finding A) |

Two consequences that change the disposition question:

- **The "smashing into API limits" signal already exists.**
  `github_rate_limit_waits_total` literally counts throttle events by
  reason, and `RepoGuardianRateLimitLow` / `RepoGuardianRateLimitThrottling`
  / `RepoGuardianRateLimitNearExhaustion` all fire off working metrics.
  Losing layer 3 does not blind the operator to rate-limit pressure.
- **The `Discoverer` is not ungated.** It has no layer-2 equivalent
  (`DiscovererOptions` carries `Budget` but no `RateLimit`), but layer 1
  wraps its `ListInstallationRepos` calls like everything else. Under
  pressure discovery goes *slow*, not unbounded.

What layer 3 uniquely adds is therefore **not** protection or basic
visibility, but two narrower things:

1. *Forward-looking capacity* — `api_budget_spendable` answers "how many
   repos can I still afford this hour", which neither reactive layer
   does, and `estimatedCostPerRepo` is the knob for calibrating it
   against observed consumption.
2. *Avoiding layer 1's degenerate mode* — layer 1 spreads a depleted
   budget by sleeping `untilReset / remaining` **per request**. With a
   full queue that parks worker goroutines in long sleeps while holding
   queue leases, which is a throughput collapse plus reaper churn, not a
   graceful slowdown. Layer 3 prevents the enqueue instead, so the work
   is never created. This is the strongest argument for wiring it, and
   it is about *queue health*, not about the rate limit itself.

### H. `HasFreshSnapshot` is a phantom method

`RefreshFromAPI`'s doc comment instructs callers to avoid redundant
refreshes by calling a method that does not exist:

```go
// Always replaces the existing snapshot; callers that want to avoid
// redundant refreshes should call HasFreshSnapshot first.
```

`Tracker` has exactly four methods — `RefreshFromAPI`,
`SpendableForEnqueue`, `Decrement`, `publishGauges`. Minor on its own, but
it matters for the wiring option: the guard the design assumed would keep
a per-tick refresh cheap has to be written, not just called.

## Conclusion

**Answer to Q1: confirmed — the BudgetTracker is inert.** No production
code path writes a snapshot, so all nine budget metrics stay unpublished,
both gates permanently fall open, and `RepoGuardianBudgetGated` cannot
fire.

The severity is lower than it first reads, though, and finding G is why:
rate-limit protection is **three** layers deep, not two, and the other two
work. The transport limiter (`internal/github/ratelimit.go`) wraps every
API call with pre-emptive throttling and 403 retry; the `StaleSweeper`
reserve gate skips repos below the reserve floor. Between them the
operator can already answer "are we hammering the API?" —
`github_rate_limit_waits_total` counts throttle events directly, and three
alerts fire off working metrics. So this is wasted capability plus a
misleading empty dashboard, not an unprotected system.

What is genuinely lost is narrower and worth naming precisely: the
forward-looking `api_budget_spendable` view, the ability to calibrate
`estimatedCostPerRepo` against real consumption, and — the one that would
actually bite at fleet scale — protection against layer 1's degenerate
mode, where a depleted budget parks worker goroutines in per-request
sleeps while they hold queue leases.

**Answer to Q2: the original framing was wrong.** Chart alerts genuinely
have no promtool coverage, but promtool does not catch the class of defect
that motivated the question — it passes A7's exact shape. Trustworthiness
needs three separate mechanisms, one per class in finding C, and only the
cheapest is a promtool job.

## Recommendation

Split into two efforts; they share only the `RepoGuardianBudgetGated`
alert and can proceed independently.

**1. Decide the BudgetTracker's fate (needs a decision, not just code).**

Finding G reframes this. The question is *not* "do we need rate-limit
protection" — two working layers already provide it, and the operator can
already see API pressure via `github_rate_limit_waits_total` and
`rate_limit_reserve_blocked_total`. The question is whether the two things
only layer 3 offers are worth the wiring: forward-looking spendable
capacity, and keeping depleted-budget work *out of the queue* rather than
letting layer 1 park workers in multi-minute sleeps while holding leases.

- *Wire it.* Add a leader-scoped refresh — the natural home is the
  `stale-sweep` handler refreshing per installation before its enqueue
  loop, which is what both consumer comments already assume exists.
  Requires writing the `HasFreshSnapshot` guard the design assumed
  (finding H) so a per-tick refresh does not itself become the cost it is
  meant to protect: at the planned 20+ orgs that is 20 extra calls per
  tick against the very budget in question.
- *Delete it.* Layers 1 and 2 cover protection; the unique value is
  narrow. Removes 9 metrics, 1 alert, 4 env vars, 2 chart values, and
  their schema ranges. Honest about what ships, at the cost of a
  user-facing removal of published chart values.
- *Leave it, documented.* Worst option — an operator tuning
  `DISCOVERY_RESERVE_FRACTION` deserves to know nothing reads it.

Recommend **wire it**, but on the queue-health argument (G.2), not the
"otherwise unprotected" one — that argument does not survive finding G.
If the wiring turns out to need more than a refresh call plus the
`HasFreshSnapshot` guard, delete rather than leave: a half-wired budget
gate is worse than an honest absence, because its metrics look like
coverage.

A cheaper middle option worth considering explicitly: **wire the refresh
for the metrics and leave both gates falling open.** That buys the
observability the feature was designed to provide (G.1) with none of the
behavioural risk of a gate that has never run in production against a
real fleet — and the gates can be enabled later once
`estimatedCostPerRepo` has been calibrated against observed consumption,
which is exactly what the design says the knob is for.

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

1. **BudgetTracker disposition** — (a) wire the refresh **and** both
   gates, via a leader-scoped refresh in the `stale-sweep` handler;
   (b) wire the refresh for metrics only, leaving both gates falling open
   until `estimatedCostPerRepo` is calibrated against observed
   consumption; (c) delete the feature and its user-facing knobs;
   (d) leave it and document that it is inert. other:
1a. **If wiring — who owns the refresh?** (a) `stale-sweep` refreshes and
   `Discoverer` reuses the shared snapshot (matches what the
   `Discoverer` comment already claims); (b) each refreshes
   independently, doubling the calls but removing the ordering
   dependency between two separately-scheduled handlers. other:
1b. **If wiring — refresh cadence.** `HasFreshSnapshot` does not exist
   (finding H) and must be written. What counts as fresh: (a) one
   refresh per installation per tick; (b) TTL-based, decoupled from tick
   cadence; (c) refresh only when `SpendableForEnqueue` reports
   `ErrNoSnapshot`, which is what the current `resetAt`-elapsed branch
   already implies. other:
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
