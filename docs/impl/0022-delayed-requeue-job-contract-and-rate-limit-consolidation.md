---
id: IMPL-0022
title: "Delayed-requeue job contract and rate-limit consolidation"
status: Draft
author: Donald Gifford
created: 2026-08-02
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0022: Delayed-requeue job contract and rate-limit consolidation

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-08-02

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Sequencing and Release Shape](#sequencing-and-release-shape)
- [Implementation Phases](#implementation-phases)
  - [Phase 0: Stop the bleeding (bundled with Phase 1)](#phase-0-stop-the-bleeding-bundled-with-phase-1)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 1: Job contract](#phase-1-job-contract)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 2: Delayed set and promotion](#phase-2-delayed-set-and-promotion)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 3: Throttle signal path](#phase-3-throttle-signal-path)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 4: Worker requeue and attempt cap](#phase-4-worker-requeue-and-attempt-cap)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 5: Observability](#phase-5-observability)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 6: Remove the superseded layers](#phase-6-remove-the-superseded-layers)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 7: Docs and chart](#phase-7-docs-and-chart)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement
[DESIGN-0021](../design/0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md)
(all open questions resolved 2026-08-02): replace the three
non-composing rate-limit layers with one delayed-requeue mechanism. A
worker that cannot proceed returns the job to a new Valkey delayed
ZSET with a due-time and frees its slot; the reaper promotes due jobs;
`queue.Job` gains `Attempts`/`AvailableAt` with a `MAX_JOB_ATTEMPTS`
cap whose terminal disposition writes to `repo_state` (OQ2 → a). The
transport's in-handler sleep — the live duplicate-amplification hazard
(INV-0012 finding I) — is first capped (Phase 0) and then removed
(Phase 3). The BudgetTracker and the sweep reserve gate are deleted
(Phase 6), leaving exactly one rate-limit mechanism.

**Implements:** DESIGN-0021 (resolved: 1a, 2a, 3a, 4a+rider, 5a, 6a,
7a, 8b)

## Scope

### In Scope

- `internal/github/ratelimit.go`: Phase 0 sleep cap, then Phase 3
  `ThrottledError` return replacing the pre-emptive sleep and the
  wait-pair recording.
- `internal/queue`: `Job.Attempts`/`Job.AvailableAt`,
  `RetryAfterError`, `EnqueueAfter` (OQ6 → a), rewritten interface
  contract doc.
- `internal/queue/valkey`: delayed ZSET, `deferScript` /
  `promoteScript` Lua, promotion from `reapOnce`, key-construction
  centralisation (DESIGN-0015 forward hook).
- `internal/worker`: `ThrottledError` → `RetryAfterError` translation,
  attempt accounting, terminal disposition via the existing
  `writeBack` path.
- Phase 5 metrics + alerts (including re-pointing
  `RepoGuardianRateLimitThrottling`), Phase 6 removals
  (`internal/budget`, sweep gate, budget metrics/alert/config/chart
  values, wait-pair definitions), Phase 7 docs/chart.

### Out of Scope

- Per-installation fairness/partitioning (DESIGN-0015 — this work only
  leaves its forward hooks: key centralisation and
  `queue_wait_seconds`).
- The transport's reactive 403 retry handling — stays as-is.
- Store/scheduler/discovery changes beyond deleting the budget gates.
- OTEL client instrumentation of the transport
  (IMPL-0023 Phase 3 — only the ordering contract matters here).

## Sequencing and Release Shape

- **Phases 0+1 land as one PR** (design OQ8 → b): one review of the
  whole rate-limit-path change. Phase 0 is superseded by Phase 3 and
  is deliberately crude.
- **Phases 1–5 ship as one binary minor** (design rollout step 2).
  Release labeling across the per-phase PRs is Open Question 1.
- **Soak before Phase 6**: observe `queue_delayed_total` /
  `queue_delayed_depth` in the homelab across a full reconcile cycle;
  force a deferral with a deliberately low `RATE_LIMIT_THRESHOLD`
  since organic deferrals may not occur at current scale.
- **Phase 6 ships as its own minor** with a prominent upgrade note
  (OQ7 → a) — it removes published chart values.
- Coordination with IMPL-0023: whichever of this Phase 3 and
  IMPL-0023 Phase 3 lands second adds the transport-ordering test
  (`otelhttp` outermost, rate-limit transport inside).

Run `make fmt` + `make lint` after each task; commit per numbered task
with conventional commits.

---

## Implementation Phases

### Phase 0: Stop the bleeding (bundled with Phase 1)

The transport (`internal/github/ratelimit.go`) sleeps in-handler for
up to `untilReset` (1h) inside `waitIfNeeded` and the
`rateLimitDelay` + `sleepWithContext` retry path (lines 73, 147) while
`JOB_ACK_TIMEOUT` is 5m — so the reaper clones the sleeping job every
`REAPER_INTERVAL`. Cap the sleep below the lease.

#### Tasks

- [x] 0.1 Add a package const `maxRateLimitSleep = 60 * time.Second`
      (hardcoded, not a knob — Open Question 2) and cap **both** sleep
      sites (`waitIfNeeded` and the 403 retry delay). *Amended during
      implementation:* the original task text left the reactive path
      alone, but the reactive primary-limit delay is also
      until-reset (~1h) and violates the phase's "no code path
      outlives the lease" success criterion — both are capped.
- [x] 0.2 When the computed delay exceeds the cap, return an error
      **without sleeping at all** (sleeping the cap first would burn a
      worker slot for 60s with the error already inevitable) — the
      job nacks and the reaper retries it. Crude but correct until
      Phase 4. Wait metrics increment only on real sleeps.
- [x] 0.3 Test: a computed delay above the cap does not sleep past it
      (injected `recordingSleeper` observes requested delays as
      virtual time) — `TestRateLimitTransport_PreemptiveSleepCap` +
      `TestRateLimitTransport_RetryDelayAboveCap_FailsFast`.
- [x] 0.4 Test reproducing the INV-0012 finding I timeline:
      `TestRateLimitTransport_FindingITimeline_CapPreventsDoubleClaim`
      (virtual blocked time vs the 5m lease, plus a static guard that
      `maxRateLimitSleep` stays below `JOB_ACK_TIMEOUT`). Non-vacuity
      verified two ways: raising the const trips the static guard;
      disabling the cap branch makes the blocked-time assertion fire
      at 49m59s. This is the canonical regression test for the whole
      design — it must survive every later phase.

#### Success Criteria

- No code path can sleep longer than `JOB_ACK_TIMEOUT` while holding
  an in-flight claim.
- The finding-I timeline test fails with task 0.1 reverted, passes
  with it (non-vacuity verified).
- `make ci` passes.

---

### Phase 1: Job contract

All changes in `internal/queue/queue.go` plus mock/fake fallout.

#### Tasks

- [x] 1.1 Add `Attempts int` and `AvailableAt time.Time` to
      `queue.Job`. *Amended:* the existing fields are untagged (JSON
      uses Go field names), so the new fields follow that convention
      rather than gaining tags; the zero-value upgrade semantics are
      documented on the struct.
- [x] 1.2 Add `queue.RetryAfterError{After, Reason, Err}` with an
      `Unwrap() error` method.
- [x] 1.3 Rewrite the `Queue` interface doc comment: the three handler
      outcomes (ack / nack / defer), attempt counting on both defer
      and reaper requeue, terminal disposition = write
      `StatusError` to `repo_state` and drop (design OQ2 → a). Stale
      in-memory-implementation parenthetical deleted.
- [x] 1.4 Add `EnqueueAfter(ctx, j, at)` to the interface (OQ6 → a).
      Valkey impl: past `at` falls through to `Enqueue`; future `at`
      errors explicitly until task 2.2 (nothing calls it yet; stub
      lands in the same Phase 0+1 PR as required).
- [x] 1.5 `make mocks` (queue mock regenerated with `EnqueueAfter`);
      the three test-local `recordingQueue` fakes
      (`internal/checker/sweep_test.go`,
      `internal/webhook/handler_test.go`,
      `internal/worker/worker_test.go`) gain a due-time-stamping
      `EnqueueAfter`; `slowQueue` inherits via embedding. *Amended:*
      `internal/scheduler/sweep_test.go` has no queue fake — the
      compiler surfaced the true list.
- [x] 1.6 `internal/queue/queue_test.go`:
      `TestJob_OldPayloadDecodesWithZeroRetryFields` (old six-field
      payload → `Attempts=0`, zero `AvailableAt`), round-trip of the
      new fields, and `errors.As`/`Unwrap` chain behaviour for
      `RetryAfterError`.

#### Success Criteria

- The interface documents delay, backoff, attempt cap, and terminal
  disposition — the contract INV-0012 finding K found missing.
- Old-format payloads decode correctly.
- `make ci` passes.

---

### Phase 2: Delayed set and promotion

All changes in `internal/queue/valkey/` (`valkey.go`, `reaper.go`).

#### Tasks

- [x] 2.1 Add `DelayedKey` to `valkey.Options` (default
      `repo-guardian:queue:delayed`) and centralise construction of
      all four keys (jobs, in-flight, delayed, reaper lock) in one
      helper — the DESIGN-0015 partition hook; Lua scripts stay
      key-parametric via `KEYS[]`. (Helper is
      `Options.applyKeyDefaults`; package doc updated to the
      four-key layout.)
- [x] 2.2 `deferScript` (`ZREM` in-flight + `ZADD` delayed, atomic,
      mirroring `requeueScript` at valkey.go:90) wired to
      `EnqueueAfter`. (Script takes separate remove/park members so
      the Phase-4 defer path can re-serialise with `Attempts+1`
      without touching the Lua; `deferPayload` helper is the shared
      invocation point.)
- [x] 2.3 `promoteScript` (`ZRANGEBYSCORE` due + `ZREM` + `LPUSH`,
      atomic) called from `reapOnce` (reaper.go:107) under the
      existing leader lock — no new goroutine, no new election.
      (`reapOnce` split into `requeueStuck` + `promoteDue`; package
      doc gains a "Delayed jobs" section noting the REAPER_INTERVAL
      dual duty per DESIGN-0021 OQ3.)
- [x] 2.4 Publish `queue_delayed_depth` from the reaper tick (`ZCARD`,
      alongside the existing depth accounting). (Originally
      leader-published; amended in 5.3 to publish from EVERY pod's
      tick, before the leader lock — a leader-only gauge goes stale on
      non-leader replicas' /metrics endpoints, and a Prometheus scrape
      of a stale replica during a leadership flap would pin
      `RepoGuardianQueueBackpressure` in an unresolvable firing state.
      Still best-effort: a failed ZCARD logs Warn without failing the
      reap. `Queue.Delayed` mirrors `Depth`/`InFlight`.)
- [x] 2.5 Integration test (`integration` tag, real Valkey): a job
      deferred 2s is not delivered before due-time, is delivered
      after. (`TestValkey_DeferredJobNotDeliveredEarly`; non-vacuity
      verified — neutralising the due-time bound in `promoteScript`
      fails the test with a 1.74s-early delivery.)
- [x] 2.6 Integration test: a job is in exactly one of `jobs`,
      `in-flight`, `delayed` at every lifecycle point.
      (`TestValkey_ExactlyOneKeyInvariant`, four checkpoints:
      parked/promoted/claimed/acked; non-vacuity verified — dropping
      the ZREM from `promoteScript` fails checkpoint 2 with 36
      duplicate promotions.)
- [x] 2.7 Integration test: promotion is leader-gated — two reapers,
      one promotion. (`TestValkey_PromotionLeaderGated`: two reapers
      on one lock, five parked jobs, each delivered exactly once,
      keyspace empty afterwards.)

#### Success Criteria

- A deferred job is never delivered before `AvailableAt`; no job is
  ever in two keys at once; promotion happens exactly once with
  multiple replicas.
- `make ci` passes; integration tests pass against real Valkey.

---

### Phase 3: Throttle signal path

`internal/github/ratelimit.go` + an end-to-end chain test. Removes
what Phase 0 capped.

#### Tasks

- [x] 3.1 Add exported `github.ThrottledError{ResetAt, Remaining,
      Limit}` with an `Error()` naming the reset time.
- [x] 3.2 Replace the pre-emptive sleep (`waitIfNeeded` plus the
      Phase 0 cap) with a `ThrottledError` return from `RoundTrip`.
      The reactive 403 retry paths stay untouched. (`waitIfNeeded` →
      `shouldThrottle`; below-threshold-with-budget now defers whole
      jobs instead of spread-pacing requests — the threshold is a
      hard reserve. `maxRateLimitSleep` stays, reactive-only, with
      its comment rewritten as permanent. Tests:
      `TestRateLimitTransport_PreemptiveNeverSleeps` table replaces
      the Phase-0 sleep-cap table; `PreemptiveThrottle` asserts the
      deferral signal; finding-I timeline test now also asserts
      `errors.As` recovers `*ThrottledError`.)
- [x] 3.3 Remove the `github_rate_limit_waits_total` /
      `github_rate_limit_wait_seconds` recording along with the sleep
      — no would-be-delay bridge (design amendment 2026-08-02; the
      deferral is measured once, by Phase 5's `queue_delayed_*`).
      `github_rate_remaining` emission is untouched. (Pre-emptive
      recording went with `waitIfNeeded` in 3.2; the reactive-path
      recording is removed here. Definitions stay until Phase 6;
      the contrib alert re-points in 5.2b.)
- [x] 3.4 End-to-end invariant test: drive `Engine.CheckRepo` against
      an `httptest` server returning exhausted rate-limit headers and
      assert `errors.As` recovers `*ThrottledError` at the worker
      boundary through the full wrap chain (url.Error → go-github →
      client → engine). (**Discovery**: the test's first run caught a
      real fourth signal path — go-github's own `checkRateLimitBeforeDo`
      short-circuits above our transport with `*gh.RateLimitError`
      whenever its cache saw `remaining=0`, unbypassable in v68. Added
      `github.AsThrottled(err)` normalising both shapes; the chain
      test (`TestCheckRepo_ThrottledErrorSurvivesWrapChain` in
      `internal/checker/ratelimit_chain_test.go`) is a two-timeline
      table pinning the reserve-threshold case (direct
      `*ThrottledError`) AND the exhausted case (converted
      `RateLimitError`). Design amendment recorded in DESIGN-0021's
      signal-path section; Phase 4 task 4.1 must use `AsThrottled`,
      not raw `errors.As`. Enabler: exported
      `github.NewClientForBaseURL` so the checker-package test can
      build a real transport-wired client against httptest.)
- [x] 3.5 Non-vacuity check for 3.4: neutralise one `%w` on the path,
      confirm the test fails, restore. Record the check in the PR
      description. (Neutralised the engine's `listing open PRs: %w`
      → `%v`: both subtests fail with "AsThrottled(...) = false";
      restored, green.)

#### Success Criteria

- The transport never sleeps pre-emptively.
- `errors.As` recovers `*ThrottledError` from a fully-wrapped
  `CheckRepo` error, proven by the chain test.
- The two retained quota alerts (`RepoGuardianRateLimitLow`,
  `RepoGuardianRateLimitNearExhaustion`) still receive samples;
  nothing feeds the wait pair.
- `make ci` passes.

---

### Phase 4: Worker requeue and attempt cap

`internal/worker/worker.go` (`processJob`) +
`internal/queue/valkey/valkey.go` (`processPayload`) + config.

#### Tasks

- [x] 4.1 `processJob` translates `*github.ThrottledError` into
      `*queue.RetryAfterError{Reason: "rate_limit"}` with the jittered
      due-time: `due = now + delay + rand[0, min(delay/4, 60s))`.
      (Uses `ghclient.AsThrottled` per the 3.4 amendment so both
      throttle shapes translate. Deferral skips error metrics and
      repo_state write-back — the check never ran. Jitter follows
      the repo's crypto/rand convention. `delay <= 0` falls through
      to nack until 4.5 adds backoff.)
- [x] 4.2 The Valkey `processPayload` handler-return path recognises
      `*RetryAfterError` via `errors.As` and takes the defer path
      (task 2.2) instead of ack or nack-by-leaving-in-flight.
      (`deferInFlight` re-serialises with `Attempts+1` +
      `AvailableAt` and swaps the in-flight entry atomically via
      `deferScript`'s two-member form; marshal/script failure
      degrades to nack. New `QueueAckedTotal{outcome="deferred"}`
      accounting.)
- [x] 4.3 Increment `Attempts` on every defer and every reaper
      requeue (the requeue side re-serialises the payload — extend
      `requeueScript`'s caller accordingly). (Defer half landed in
      4.2's `deferInFlight`; requeue half is `requeuePayload` in
      reaper.go with `requeueScript` extended to two members.
      Undecodable payloads requeue verbatim — the claim path drops
      garbage at decode.)
- [x] 4.4 `MAX_JOB_ATTEMPTS` config (default 10) in
      `internal/config/config.go` + validation. On exceeding the cap:
      write `StatusError` with a descriptive `LastError` to
      `repo_state` via the existing best-effort `writeBack`, drop the
      job, increment `queue_attempts_exhausted_total` (OQ2 → a — the
      next sweep re-enqueues naturally if the repo is still stale;
      this makes the enterprise-migration nack-loop self-healing).
      (Cap enforced at delivery time in `processJob` →
      `dropExhausted` returns nil so the queue acks-and-drops.
      `worker.New` gained the `maxJobAttempts` param; validation
      rejects < 1; metric defined here rather than 5.1 since 4.4
      increments it. `TestLoadMaxJobAttempts` covers default +
      rejection.)
- [x] 4.5 Exponential backoff for deferrals with no server-supplied
      time: constants per Open Question 3, same jitter shape as 4.1.
      (`backoffDelay(attempts) = min(30s × 2^attempts, 30m)` replaces
      the 4.1 fall-through-to-nack when `time.Until(ResetAt) <= 0`.)
- [x] 4.6 Test: a throttled job is deferred, not nacked — in-flight
      empty, delayed has one entry. (`TestValkey_DeferredNotNacked`,
      integration; also pins parked `Attempts=1` + `AvailableAt`.
      Non-vacuity: removing `deferInFlight`'s increment fails it
      with "parked Attempts = 0".)
- [x] 4.7 Test: the worker slot frees immediately on deferral — a
      second job is processed while the first is parked.
      (`TestValkey_DeferralFreesWorkerSlot`, integration: one
      Subscribe loop, no reaper — the second job can only be
      processed if the deferral freed the loop.)
- [x] 4.8 Test: attempts accumulate across defers and reaper
      requeues; the cap triggers the terminal disposition exactly
      once, and the `repo_state` row carries `StatusError`.
      (Queue half: `TestValkey_AttemptsAccumulateAcrossNackAndRequeue`
      — nack → reaper requeue (Attempts=1 observed at redelivery) →
      defer (parked Attempts=2); non-vacuity verified on
      `requeuePayload`'s increment. Worker half:
      `TestPool_AttemptCap_TerminalDisposition` — nil engine/ghClient
      prove the cap refuses before processing; exactly one
      StatusError write naming MAX_JOB_ATTEMPTS; exhausted counter
      = 1; nil handler return = ack-and-drop.)

#### Success Criteria

- A throttled job never occupies a worker slot while waiting.
- Attempts are counted across both paths; no job retries forever.
- A dead-installation job (the enterprise-migration incident shape)
  reaches the cap and drops with a store record instead of
  nack-looping indefinitely.
- `make ci` passes.

---

### Phase 5: Observability

`internal/metrics/metrics.go`, both alert packs, scaling docs.

#### Tasks

- [x] 5.1 Add `queue_delayed_total{reason, installation_id}`
      (Counter), `queue_delay_seconds{reason}` (Histogram),
      `queue_attempts_exhausted_total{installation_id}` (Counter).
      (`queue_delayed_depth` landed in 2.4.)
      *Done. `queue_attempts_exhausted_total` landed early in 4.4
      (defined alongside its only producer, `dropExhausted`).
      `queue_delayed_total` + `queue_delay_seconds` are incremented in
      `deferInFlight` after a successful defer — never on the marshal-
      or Lua-failure nack paths, so counts reflect jobs actually
      parked. Both use the shared `queueRetrySecondsBuckets` layout
      (OQ4a: 1s → 4h, matched to rate-limit reset windows and the 30m
      backoff cap).*
- [x] 5.2 Add `queue_wait_seconds{installation_id}` (Histogram)
      observed at claim time as `now − EnqueuedAt` — the DESIGN-0015
      go/no-go datum. Bucket layout per Open Question 4.
      *Done. Observed in `processPayload` right after the single
      payload decode, guarded on non-zero `EnqueuedAt` (pre-4.2
      payloads in a live queue lack the stamp; a zero would record
      ~56 years). Parked time deliberately counts — the tenant
      experienced it as queue wait — with the onboarding top-bucket
      skew caveat documented on the bucket layout and slated for
      scaling.md in 5.6.*
- [x] 5.3 Add `RepoGuardianQueueBackpressure`
      (`queue_delayed_depth` sustained) and
      `RepoGuardianJobsExhausted` (any
      `queue_attempts_exhausted_total` increase) to **both**
      `charts/repo-guardian/templates/prometheusrule.yaml` and
      `contrib/prometheus/alerts.yaml`; windows must outlive `for`
      and match real emission cadence (INV-0012 findings C/E),
      reasoned explicitly in the PR.
      *Done. Cadence reasoning (also inlined as comments in both
      packs): (C) Backpressure alerts on a gauge published by EVERY
      pod's reaper tick — the delayed-depth ZCARD was moved BEFORE
      the leader lock in `reapOnce` for exactly this reason (a
      leader-only gauge goes stale on non-leader replicas and pins
      the alert firing across a leadership flap; task 2.4 note
      amended). No rate() window to outlive; `for: 30m` on a
      60s-fresh series is safe. (E) JobsExhausted is two-legged:
      `increase(...[1h]) > 0 or sum(metric unless metric offset 1h)
      > 0` — increase() cannot see a brand-new CounterVec series'
      first increment (born at 1, diffs to 0), and the `unless
      offset` leg covers exactly the sub-1h window increase() is
      blind in, then self-retires. Both packs promtool-checked
      (contrib directly: 24 rules SUCCESS; chart via `helm template`
      + spec.groups extraction: 11 rules SUCCESS) — first promtool
      validation the chart pack has ever had (INV-0012 open lead).*
- [x] 5.4 Re-point `RepoGuardianRateLimitThrottling`
      (contrib pack only, alerts.yaml:115) from the removed wait
      counter to `queue_delayed_total{reason="rate_limit"}`.
      *Done. Expression changed from `rate(waits[15m]) > 0.5 / for:
      10m` to `increase(queue_delayed_total{reason="rate_limit"}[1h])
      > 10 / for: 30m` — deferrals are per-JOB, not per-request, so
      rates run orders lower than the old wait counter (a rate
      threshold carried over blindly would never fire), and the 1h
      window covers the re-deferral cadence (a throttled tenant
      re-defers each reset window, up to 1h apart; a 15m window would
      flap and never hold through `for` — finding C). Also re-pointed
      the four wait-pair Grafana panels in
      `contrib/grafana/repo-guardian-dashboard.json` (style-review
      catch): panel 42 → "Job Deferrals by Reason"
      (`queue_delayed_total`), panel 43 → "Job Deferral Horizon
      p50/p95/p99" (`queue_delay_seconds_bucket`), both on 15m
      windows for the sparser per-job cadence. promtool re-checked
      (24 rules SUCCESS); zero `rate_limit_wait` references remain in
      contrib.*
- [x] 5.5 helm-unittest assertions on rendered alert *expressions*,
      not just names (IMPL-0021 A7 convention).
      *Done. Five new cases in `tests/prometheusrule_test.yaml`:
      QueueBackpressure full-expression (gauge threshold + for +
      severity), threshold override (500), disable; JobsExhausted
      exact two-legged multi-line expression (block scalar matches
      the rendered `expr: |` byte-for-byte), disable. Non-vacuity
      verified: deleting the second leg from the template fails
      exactly the two-legged-expression case (1 failed / 95 passed),
      restore → 96/96.*
- [x] 5.6 Document the new metrics in `docs/operations/scaling.md`
      (healthy vs backpressured reference values, including the
      expected `queue_wait_seconds` top-bucket skew during fleet
      onboarding / policy-version upgrades — OQ4 caveat) and add
      `contrib/README.md` rows.
      *Done. New scaling.md "§ Delayed requeue (IMPL-0022)" — this is
      the section both new alerts' descriptions point at — with a
      five-metric Healthy/Backpressured table, the onboarding
      top-bucket-skew caveat as a highlighted paragraph, and a
      "Reading the go/no-go datum" PromQL recipe (per-installation
      p99 divergence, not absolute value, is the DESIGN-0015
      partition signal). contrib/README.md gains a "Delayed requeue"
      table + example queries, and `queue_acked_total`'s outcome
      enumeration now includes `deferred`. The wait-pair rows in
      contrib/README.md §GitHub API stay until Phase 6 removes the
      definitions (they still exist as metrics until then).*

#### Success Criteria

- An operator can answer "are we hitting API limits, how hard, for
  whom" from metrics alone.
- All touched alerts are fireable under real emission cadence.
- `make ci` and helm-unittest pass.

---

### Phase 6: Remove the superseded layers

Only after the soak (see Sequencing). Ships as its own minor.

#### Tasks

- [x] 6.1 Delete `internal/budget/` (budget.go, labels.go, tests) and
      its `budget.New` wiring in `cmd/repo-guardian/main.go.bringUp`.
      *Done (one commit with 6.2 — deleting the package and removing
      its consumers are inseparable if every commit is to compile;
      same precedent as the 5.1+5.2 commit).*
- [x] 6.2 Remove `Budget` from `StaleSweeperOptions` and
      `DiscovererOptions` and both `budgetAllows` gates.
      *Done. `allowedByBudget` (sweep.go) and `budgetAllows`
      (discoverer.go) removed along with the `budget` fields,
      `Budget` options, `Decrement` call, and the `gated_budget` log
      field. Discoverer doc updated: discovery is not throttle-gated
      — it lists once per installation and any real pressure
      surfaces via the delayed-requeue path on check jobs. Four
      budget-gating tests + two package-local fake clients deleted
      with their subjects; `TestStaleSweeper_EnqueuesAllWhenBudgetIsAmple`
      renamed `...WhenRateLimitAmple` (it exercises the rate-limit
      gate, which survives until 6.3).*
- [x] 6.3 Remove the layer-2 sweep gate
      (`StaleSweeper.allowedByRateLimit`, sweep.go:243) and
      `rate_limit_reserve_blocked_total` — **preserving the
      per-installation `RateLimitRemaining` sampling call in the sweep
      loop** (design OQ4 rider: that call is the only live producer of
      `rate_limit_remaining{installation_id}`; the gate goes, the
      sampling stays, `RepoGuardianRateLimitNearExhaustion` keeps its
      feed).
- [x] 6.4 Remove the nine `api_budget_*` /
      `enqueue_gated_by_budget_total` metric definitions, the now-unfed
      wait-pair definitions
      (`github_rate_limit_waits_total` / `_wait_seconds`), and
      `RepoGuardianBudgetGated` from both alert files (it is already
      commented out in contrib).
      *Done. Six `api_budget_*`/`enqueue_gated_by_budget_total`
      definitions (the 9 series count includes per-label expansion)
      plus `github_rate_limit_waits_total` / `_wait_seconds`
      removed from metrics.go. `RepoGuardianBudgetGated` deleted from
      the chart template and its two helm-unittest cases; the contrib
      commented-out block became a tombstone comment naming the
      replacement alerts, so an operator diffing an old pack learns
      why it vanished. contrib/README.md's wait-pair rows replaced
      with a pointer to `queue_delayed_total`.*
- [x] 6.5 Remove `DISCOVERY_RESERVE_FRACTION` and
      `DISCOVERY_ESTIMATED_COST_PER_REPO` from config, validation, and
      tests. *Done — including the whole `validateDiscovery` helper
      and its call site, which had no other invariants left.*
- [x] 6.6 Remove `discovery.reserveFraction` /
      `discovery.estimatedCostPerRepo` from `values.yaml`,
      `values.schema.json`, and `tests/deployment_env_test.yaml`.
      *Done, plus `staleSweep.rateLimitReserve` (the 6.3 chart-side
      half) and the three matching `deployment.yaml` env blocks. A new
      unittest case asserts none of the three env vars render — a
      removal is only as durable as the guard against its return.*
- [x] 6.7 `make ci` plus a `deadcode` pass clean after removal.
      *Done. `make ci` exit 0. `deadcode ./cmd/...` reports one entry,
      `github.NewClientForBaseURL` — expected and not a regression:
      it is the Phase-3 chain-test constructor, reachable only from
      tests, which the binary-rooted analysis cannot see. No
      budget-related dead code remains.*
- [x] 6.8 Mark DESIGN-0002 Superseded by DESIGN-0021; update the
      CLAUDE.md BudgetTracker architecture notes.
      *Done. `.docz.yaml` does not offer a `Superseded` status for
      the `design` type (Draft / In Review / Approved / Implemented /
      Abandoned), and "Abandoned" would misdescribe a design that
      shipped — so DESIGN-0002 follows the DESIGN-0005 precedent
      instead: `status: Implemented` plus a blockquote banner naming
      DESIGN-0021, spelling out what survives (RoundTripper
      placement, reactive 403/secondary retry, header scraping) and
      what is gone (the pre-emptive sleep and the wait pair).
      CLAUDE.md's IMPL-0015 Phase 1 entry is rewritten Discoverer-only
      with an explicit REMOVED paragraph so a future reader hitting a
      stale `api_budget_*` reference knows it is stale; the
      StaleSweeper entry now documents the sampling-not-gating
      contract and why the call cannot be deleted.*

#### Success Criteria

- Exactly one rate-limit mechanism remains.
- `rate_limit_remaining{installation_id}` still receives samples
  every sweep (rider honoured — no unfed-gauge alert).
- No dangling config, chart value, schema entry, metric, or alert
  referencing the removed layers; `helm template` renders without the
  removed values.

---

### Phase 7: Docs and chart

#### Tasks

- [x] 7.1 Chart version + appVersion bump; `make helm-docs` (edit
      `README.md.gotmpl`, never the rendered README).
      *Done last within Phase 7 so the regenerated README captures
      every chart edit in one pass. Chart `1.0.0-rc.9 → 1.0.0-rc.10`;
      appVersion `1.10.1 → 1.11.0`, matching the tag a `minor` PR
      label will cut (`deployment.yaml` defaults the image tag to
      `.Chart.AppVersion`, and IMPL-0017 shipped an appVersion that
      never corresponded to a real tag — don't repeat that). The
      `deployment_test.yaml` image-tag regex pins appVersion, so it
      was bumped in the same commit. README regenerated via
      `make helm-docs`; the rendered file was not hand-edited.*
- [x] 7.2 `MAX_JOB_ATTEMPTS` chart value with `values.schema.json`
      range validation and helm-unittest case.
      *Done. `config.maxJobAttempts: 10` in values.yaml, rendered to
      the `MAX_JOB_ATTEMPTS` env var, with a new `config` block in
      values.schema.json (`minimum: 1`). Verified the schema actually
      bites: `--set config.maxJobAttempts=0` fails render with "at
      '/config/maxJobAttempts': minimum: got 0, want 1" instead of
      reaching the pod. The binary's own `< 1` startup error from
      task 4.4 remains the second line of defence for operators
      bypassing the chart. Two helm-unittest cases (default + a 3
      override).*
- [x] 7.3 Operator runbook: what a deferred job looks like in
      Valkey/metrics/logs, reading the new metrics, responding to the
      backpressure and exhausted alerts.
      *Done: `docs/operations/delayed-requeue-runbook.md`, added to
      the mkdocs nav. Covers the six-step lifecycle, the exact two log
      lines (worker decision then queue confirmation, verified against
      the source strings), `redis-cli` recipes against the real
      prefixed keys, per-alert response procedures with PromQL and the
      `repo_state` SQL for reading a terminal drop, and a
      force-a-deferral procedure. That last one uses `extraEnv` because
      `RATE_LIMIT_THRESHOLD` has no dedicated chart value — a claim
      checked against the templates rather than assumed.*
- [x] 7.4 Update `docs/operations/scaling.md` and
      `docs/operations/migrations.md` for the removed knobs, and
      document `REAPER_INTERVAL`'s dual duty — lease reaping *and*
      promotion cadence (design OQ3 → a).
      *Done. scaling.md was updated during Phase 6 (sizing table,
      cold-start table, the enterprise-bottleneck lever, and the
      BudgetTracker section replaced with a Discoverer-only one).
      migrations.md gains "Removing the rate-limit reserve knobs
      (IMPL-0022)": removed-value table, the five metric families that
      go silent (a forked dashboard degrades quietly, so this is
      called out explicitly), confirmation that
      `rate_limit_remaining` survives, plus the new `MAX_JOB_ATTEMPTS`
      knob and the `REAPER_INTERVAL` dual-duty note.
      **Correction found while writing it:** the Phase 6 commit message
      claimed `values.schema.json` rejects the removed knobs. It does
      not — the chart has no `additionalProperties: false`, so a stale
      values file rendered clean (verified by `helm template --set
      staleSweep.rateLimitReserve=0.1`, exit 0). Rather than document
      a silent no-op, added
      `repo-guardian.validateRemovedValues` to `_helpers.tpl`
      (same shape as `validateBackendSecrets`, included from
      `deployment.yaml`) so each removed knob fails render with a
      message naming the removal and linking the migration section.
      Four helm-unittest cases cover the three failures and the clean
      path.*
- [x] 7.5 CLAUDE.md: the delayed-requeue contract and the
      one-mechanism rule (no future in-handler blocking).
      *Done. Seven invariants, led by "NOTHING BLOCKS IN-HANDLER,
      EVER" with the finding-I amplification story attached so the
      rule reads as a consequence rather than a preference, and an
      explicit instruction for what to do instead when a future
      backpressure source shows up (another `Reason`, not another
      layer). Also pins: the four-key/exactly-one-key invariant and
      its single construction point, `Attempts` accounting and why
      `dropExhausted` returns nil, why a deferral skips both
      `ErrorsTotal` and the `repo_state` write-back (verified against
      the source ordering), `REAPER_INTERVAL`'s dual duty, and the
      `AsThrottled` requirement with the go-github v68 pre-check path
      that makes a bare `errors.As` silently wrong.*
- [ ] 7.6 Flip INV-0012 to Concluded and DESIGN-0021 to Implemented;
      `docz update design inv impl`.

#### Success Criteria

- Chart renders and installs with the new values.
- mkdocs holds the 14-warning baseline.
- Every removed knob has a documented upgrade path.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/github/ratelimit.go` | Modify | P0 sleep cap → P3 `ThrottledError`; wait-pair recording removed |
| `internal/queue/queue.go` | Modify | Job fields, `RetryAfterError`, `EnqueueAfter`, contract doc |
| `internal/queue/valkey/valkey.go` | Modify | key centralisation, `deferScript`, `EnqueueAfter`, defer path in `processPayload` |
| `internal/queue/valkey/reaper.go` | Modify | `promoteScript` in `reapOnce`, delayed-depth gauge, attempt increment on requeue |
| `internal/worker/worker.go` | Modify | throttle translation, attempt cap, terminal disposition via `writeBack` |
| `internal/config/config.go` | Modify | `MAX_JOB_ATTEMPTS`; P6 removes two discovery knobs |
| `internal/metrics/metrics.go` | Modify | P5 adds four queue metrics; P6 removes budget + wait-pair definitions |
| `internal/checker/sweep.go` | Modify | P6: gate removed, sampling call retained |
| `internal/scheduler/discoverer.go` | Modify | P6: budget gate removed |
| `internal/budget/` | Delete | P6 |
| `charts/repo-guardian/templates/prometheusrule.yaml` | Modify | two new alerts; `BudgetGated` removed |
| `contrib/prometheus/alerts.yaml` | Modify | two new alerts; `RateLimitThrottling` re-pointed |
| `charts/repo-guardian/values.yaml` + `values.schema.json` | Modify | `MAX_JOB_ATTEMPTS` in; two discovery knobs out |
| `docs/operations/scaling.md`, `migrations.md` | Modify | new metrics, removed knobs, `REAPER_INTERVAL` dual duty |

## Testing Plan

- [ ] Phase 0 finding-I timeline regression test (fake clock),
      preserved through all later phases.
- [ ] Old-payload JSON decode compatibility test (1.6).
- [ ] Valkey integration tests: due-time honoured, exactly-one-key
      invariant, leader-gated promotion (2.5–2.7) — Lua atomicity is
      only provable against real Valkey.
- [ ] End-to-end wrap-chain test (3.4) with recorded non-vacuity
      check (3.5).
- [ ] Worker defer/attempt/terminal tests (4.6–4.8), including the
      dead-installation self-heal shape.
- [ ] Mock fidelity: promotion-then-delivery is list-then-act — fakes
      must reflect prior writes or the due-time assertion is vacuous
      (CLAUDE.md rule).
- [ ] helm-unittest on rendered alert expressions.
- [ ] Each behavioural test neutralise-verify-restore per standing
      practice.

## Dependencies

- INV-0012 (Concluded by Phase 7) — findings A–K are the evidence
  base.
- No dependency on IMPL-0023 except the transport-ordering test
  (whichever Phase 3 lands second adds it).
- DESIGN-0015 stays Draft; this work only leaves its hooks.

## Open Questions

1. **Release labeling across the per-phase PRs.**
   **Resolved 2026-08-02 → (a).**
   (a) Label the Phase 0+1, 2, 3, and 4 PRs `dont-release`; the
   Phase 5 PR carries `minor` and cuts the one binary release for
   phases 1–5 (matching the design's rollout step 2). Phase 6's PR
   carries its own `minor`. Keeps released binaries at exactly the
   design's two behavioural checkpoints instead of shipping a
   half-built defer path.
   (b) Each phase PR carries `patch`/`minor` and releases
   independently — more releases, each smaller, but versions ship
   with `EnqueueAfter` present and nothing calling it.
   (c) One giant PR for phases 1–5 — single review, single release,
   but far too large to review well.
   other:

2. **Phase 0 cap: constant or knob.**
   **Resolved 2026-08-02 → (a).**
   (a) Hardcoded `maxPreemptiveSleep = 60s` const — it is scaffolding
   that Phase 3 deletes weeks later; an env knob would need schema,
   chart plumbing, and a deprecation path for something with a
   planned lifespan of one phase.
   (b) `RATE_LIMIT_MAX_SLEEP` env var — tunable if the cap misbehaves
   in the homelab, at the cost of shipping-then-removing a documented
   knob.
   other:

3. **Backoff constants for deferrals with no server-supplied time.**
   **Resolved 2026-08-02 → (a).**
   (a) `base 30s, factor 2, cap 30m`, jitter `rand[0, min(delay/4,
   60s))` — reaches the 30m ceiling on attempt 7 of 10, so a job
   exhausts in roughly 2–3 hours; hardcoded consts next to the
   translation in `internal/worker`, no config surface until someone
   needs one.
   (b) `base 60s, factor 2, cap 1h` — gentler on the API, but a
   10-attempt exhaustion stretches past 8 hours, longer than the gap
   between sweeps' fresh enqueues, muddying "who re-enqueued this."
   (c) Make base/factor/cap env-configurable now — three knobs nobody
   has asked for.
   other:

4. **`queue_wait_seconds` bucket layout.**
   **Resolved 2026-08-02 → (a)**, with a caveat to carry into task
   5.6's docs: during initial fleet onboarding or a policy-version
   upgrade the entire fleet re-enqueues at once, so waits legitimately
   pile into the top buckets — that skew is expected, not a
   regression, and the low-end granularity is mostly noise in those
   windows. If the fine low-end buckets prove useless once steady
   state is reached, collapse them in a follow-up (bucket changes are
   non-breaking; only `histogram_quantile` precision shifts).
   (a) Custom buckets `[1s, 5s, 15s, 60s, 5m, 15m, 1h, 4h]` — the
   decision this histogram exists for (DESIGN-0015 go/no-go) lives in
   the minutes-to-hours range that `prometheus.DefBuckets` (capped at
   10s) cannot see; 8 buckets × ~25 installations stays well inside
   cardinality budget.
   (b) `prometheus.ExponentialBuckets(1, 4, 8)` (1s → ~4.5h) — same
   coverage, less legible boundaries.
   other:

## References

- [DESIGN-0021](../design/0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md)
  — the design; all OQs resolved 2026-08-02
- [INV-0012](../investigation/0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
  — findings A–K (amplification timeline = finding I; missing
  contract = finding K)
- [INV-0013](../investigation/0013-state-vs-event-metrics-dashboard-suite-and-system-observability.md)
  — Finding G one-source rule behind task 3.3
- [IMPL-0023](0023-compliance-posture-state-dashboard-suite-and-otel-first.md)
  — shared transport-ordering contract (its Phase 3)
- [IMPL-0011](0011-persistent-reconcile-state-and-multi-replica-coordination.md)
  — introduced the queue, lease, and reaper being extended
- `docs/operations/ent-setup.md` — the dead-installation incident that
  resolved design OQ2 to (a)
