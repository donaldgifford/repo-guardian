---
id: DESIGN-0021
title: "Delayed-requeue job contract and rate-limit consolidation"
status: Draft
author: Donald Gifford
created: 2026-07-26
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0021: Delayed-requeue job contract and rate-limit consolidation

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-26

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [The contract](#the-contract)
  - [Signal path: transport to worker](#signal-path-transport-to-worker)
  - [Valkey key layout](#valkey-key-layout)
  - [Promotion](#promotion)
  - [Backoff and attempt cap](#backoff-and-attempt-cap)
  - [Interaction with the in-flight lease](#interaction-with-the-in-flight-lease)
  - [Layer disposition](#layer-disposition)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Observability](#observability)
- [Implementation Phases](#implementation-phases)
  - [Phase 0: Stop the bleeding](#phase-0-stop-the-bleeding)
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
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Risks](#risks)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

repo-guardian has three independent mechanisms for handling GitHub API
rate limits. They were built against two different execution models three
months apart, they do not compose, and the oldest actively breaks the
newest: the HTTP transport sleeps in-handler for up to an hour, while the
durable queue it now runs under considers a job abandoned after five
minutes and hands it to another worker.

This design replaces all three with one mechanism. A worker that cannot
proceed because of rate limits **returns the job to the queue with a
due-time and frees its slot** instead of blocking. The queue grows a
delayed set, promoted by the reaper that already exists. `queue.Job`
grows the fields a retry contract needs, and the `queue.Queue` doc
comment stops gesturing at retry semantics and starts specifying them.

The observability the inert BudgetTracker was built to provide falls out
of this for free, and is better: deferred work is *measured* rather than
modelled from a hand-tuned cost estimate that has never been calibrated.

## Goals and Non-Goals

### Goals

- No worker ever blocks on a rate limit while holding a queue lease.
- A defined retry contract — delay, backoff, attempt cap, terminal
  disposition — specified on the interface rather than implied.
- One rate-limit mechanism instead of three.
- The operator can answer "are we hitting API limits, how hard, for which
  installation" from metrics, and alert before backpressure bites.
- No regression in the at-least-once delivery guarantee.

### Non-Goals

- Rewriting the worker pool's concurrency model. `WORKER_COUNT`
  goroutines consuming `Subscribe` stays.
- Per-installation fair scheduling or priority queues. A single FIFO with
  a delayed side-set is the scope; fairness is
  [DESIGN-0015](0015-per-installation-valkey-queue-partitioning.md)'s
  territory (re-baselined against this design 2026-07-29). This design
  deliberately stays partition-compatible — key construction is
  centralised (task 2.1), the Lua scripts are key-parametric, and under
  partitioning the delayed set splits per installation inside the same
  hash tag — and Phase 5's `queue_wait_seconds{installation_id}` supplies
  the measurement that decides whether 0015 is ever needed. Worth noting:
  this design also *narrows* 0015's motivation — a rate-limited noisy org
  can no longer park the worker pool, because its jobs defer from cached
  transport state with zero API calls; what remains for 0015 is plain
  FIFO burst starvation by an unthrottled large org.
- Removing the transport's 403 retry handling. Retrying a *rejected*
  request is a transport concern and stays there — only the pre-emptive
  sleep moves.
- Changing the Store, the scheduler, or the discovery path beyond
  deleting the budget gate.
- Adding a second queue backend. Valkey remains the only one (IMPL-0016).

## Background

[INV-0012](../investigation/0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
established the following, all verified against the code rather than
inferred.

**Three layers exist and do not compose.**

| # | Layer | Introduced | Behaviour under pressure | Status |
|---|---|---|---|---|
| 1 | Transport limiter (`internal/github/ratelimit.go`) | 2026-02-10 `7c777e5`, per [DESIGN-0002](0002-github-api-rate-limit-handling.md) | pre-emptively *sleeps* `untilReset / remaining`; retries 403s | works |
| 2 | Sweep reserve gate (`StaleSweeper.allowedByRateLimit`) | 2026-05-06, IMPL-0011 P5 | *skips* the repo | works |
| 3 | BudgetTracker (`internal/budget`) | IMPL-0015 P1 | would *decline* the enqueue | **inert** — `RefreshFromAPI` has no production caller |

**Layer 1 predates the queue by three months.** DESIGN-0002 specified
"sleep to spread budget until reset… if `remaining == 0`, sleep until
reset" in a world where work was an in-process buffered channel
(`internal/checker/queue.go`, 2026-02-08). Blocking a goroutine cost one
goroutine, and there was no lease to hold. The durable queue, leases, and
reaper arrived 2026-05-06 (IMPL-0011) and nothing revisited the
assumption. DESIGN-0002 is not *wrong*; it is correct for a system that
no longer exists.

**The consequence is duplicate amplification.** No handler timeout exists
anywhere. With `JOB_ACK_TIMEOUT=5m`, `REAPER_INTERVAL=1m`,
`WORKER_COUNT=5`, and a transport sleep of up to `untilReset` (1h when
`remaining == 0`):

```text
t=0     worker A claims J, ZADD in-flight, transport sleeps 50m
t=5m    reaper: J older than ack timeout → LPUSH + ZREM (atomic)
t=5m    worker B claims J, also sleeps
t=10m   reaper requeues again → worker C …
```

Roughly ten clones of one job over a 50-minute exhaustion window against
five worker slots, each waking to issue *more* calls against the
exhausted budget. Positive feedback triggered by exactly the condition
layer 3 was designed to prevent.

**There is no contract to break.** `queue.Queue` is three methods, and
the only statement of nack semantics is a parenthetical:

```go
// A nil return from handler is an implicit ack; an error is a nack
// (durable implementations may retry; the in-memory implementation
// logs and drops).
```

Stale on both halves — IMPL-0016 deleted the in-memory implementation,
and "may retry" specifies no delay, no backoff, and no attempt cap.
`queue.Job` carries neither `Attempts` nor `AvailableAt`, and `reapOnce`
has no attempt cap and no dead-letter path.

## Detailed Design

### The contract

Three handler outcomes replace today's two:

| Handler returns | Meaning | Queue action |
|---|---|---|
| `nil` | done | `ZREM` in-flight (ack) |
| `error` | failed, retry promptly | leave in-flight; reaper resurfaces after `JOB_ACK_TIMEOUT` |
| `*queue.RetryAfterError` | cannot proceed yet | move to the delayed set with a due-time, free the slot **now** |

The third is new. It is a *deliberate deferral* carrying a due-time,
distinct from a failure: the job did not fail, it is not yet runnable.

```go
// RetryAfterError signals that a job could not proceed and must not be
// retried before After. Returning it is not a failure — the job is
// deferred rather than nacked, and Attempts is incremented.
type RetryAfterError struct {
    After  time.Time
    Reason string // "rate_limit", "secondary_limit", … — used as a metric label
    Err    error  // optional underlying cause
}
```

### Signal path: transport to worker

The transport knows about throttling; the worker owns the queue
interaction. They are separated by the whole engine and the go-github
client, so the signal has to travel.

The transport's pre-emptive branch stops calling `sleepWithContext` and
returns a typed error carrying GitHub's own reset time:

```go
// internal/github/ratelimit.go — RoundTrip
if resetAt, remaining, limit, throttled := t.shouldThrottle(); throttled {
    return nil, &ThrottledError{ResetAt: resetAt, Remaining: remaining, Limit: limit}
}
```

The chain from `RoundTrip` to the worker was audited rather than assumed,
because a break in it is silent — the job would nack normally instead of
deferring, which reads as ordinary retry rather than a bug. Three hops,
all verified:

1. **`net/http.Client.Do`** wraps a `RoundTrip` error in `*url.Error`,
   which implements `Unwrap`.
2. **go-github's `BareDo`** returns that `*url.Error` unchanged apart
   from URL sanitisation (`github.go:856`), so the chain survives the
   third-party hop. One exception, benign: on a cancelled context it
   discards the transport error and returns `ctx.Err()` instead
   (`github.go:850`). Deferral is moot during shutdown, so this needs no
   handling — but it does mean a cancelled job nacks rather than defers.
3. **Our client and engine** wrap with `%w`. Of the 102 `fmt.Errorf`
   calls across `internal/{github,checker,worker,queue}`, 8 omit `%w` and
   all 8 originate a new error with no cause in scope to discard (e.g.
   `unsupported property: %s`). Zero broken wraps.

The chain is also lint-enforced going forward: `errorlint` is enabled
with `errorf: true`, so formatting an error with `%v` instead of `%w`
fails the build. What no enabled linter catches is *discarding* an error
and originating a fresh one — which is exactly why task 3.4 pins the
invariant with an end-to-end test instead of an audit. A test that drives
a real throttle through the real path fails if any link breaks, whichever
link it is; an audit only proves the state of the code on the day it ran.

**Amendment (2026-08-02, discovered by the task 3.4 chain test):** the
audit above missed a fourth signal path. go-github maintains its own
client-side rate cache and its `checkRateLimitBeforeDo` short-circuits
*above* our transport whenever a prior response showed `remaining=0` —
returning `*gh.RateLimitError` without the request ever reaching
`RoundTrip` (the bypass context key is unexported in v68, so this is
unavoidable). The deferral signal therefore has two real shapes:
`*ThrottledError` for the reserve-threshold case (0 < remaining ≤
threshold, where go-github still sends) and `*gh.RateLimitError` for
the exhausted case. `internal/github.AsThrottled(err)` normalises
both into `*ThrottledError` so go-github stays encapsulated in the
client package; the worker translation below uses it instead of a raw
`errors.As`, and the chain test pins both timelines.

One adjacency to keep straight:
[DESIGN-0022](0022-compliance-posture-state-dashboard-suite-and-otel-first.md)
Phase 3 wraps this same installation transport with `otelhttp` client
instrumentation. The ordering contract, specified in both documents:
`otelhttp` outermost, this design's rate-limit transport inside it,
`ghinstallation` innermost — so a `ThrottledError` return surfaces as
an errored client measurement, which is exactly the residual signal
that replaces the retired wait pair.

`internal/github` must not import `internal/queue` — the client is the
lower layer. The worker performs the translation:

```go
// internal/worker — processJob
if thr, ok := ghclient.AsThrottled(err); ok {
    return &queue.RetryAfterError{After: thr.ResetAt, Reason: "rate_limit", Err: err}
}
```

### Valkey key layout

One new key alongside the existing three:

| Key | Type | Purpose |
|---|---|---|
| `repo-guardian:queue:jobs` | LIST | pending (existing) |
| `repo-guardian:queue:in-flight` | ZSET, score = claim nanos | claimed, awaiting ack (existing) |
| `repo-guardian:lock:reaper` | STRING | leader lock (existing) |
| **`repo-guardian:queue:delayed`** | **ZSET, score = due-time nanos** | **deferred, awaiting promotion** |

The delayed set is the same primitive as in-flight with a different score
meaning: in-flight scores are "claimed at", delayed scores are "runnable
at".

### Promotion

Deferral and promotion are Lua scripts, mirroring the existing
`requeueScript`, so each transition is atomic:

```lua
-- deferScript: KEYS[1]=in-flight, KEYS[2]=delayed
--              ARGV[1]=member, ARGV[2]=due-nanos
redis.call("ZREM", KEYS[1], ARGV[1])
redis.call("ZADD", KEYS[2], ARGV[2], ARGV[1])
return 1
```

```lua
-- promoteScript: KEYS[1]=delayed, KEYS[2]=jobs, ARGV[1]=now-nanos
local due = redis.call("ZRANGEBYSCORE", KEYS[1], "0", "(" .. ARGV[1])
for _, m in ipairs(due) do
  redis.call("ZREM", KEYS[1], m)
  redis.call("LPUSH", KEYS[2], m)
end
return #due
```

Promotion runs in the reaper, which is already leader-elected via
`lock:reaper` and already ticks on `REAPER_INTERVAL`. Reusing it means no
new leader election, no new goroutine, and no new failure mode — at the
cost of promotion granularity being bounded by `REAPER_INTERVAL` (60s
default). A job due at t+61s runs at t+120s worst case. That is fine for
reconcile work measured in hours; Open Question 3 covers whether it
should be independently tunable.

### Backoff and attempt cap

The delay comes from GitHub when GitHub supplies one — `resetAt` on the
primary limit, `Retry-After` on the secondary. That is authoritative and
should not be second-guessed. Jitter is then applied so a fleet throttled
simultaneously does not stampede at the same instant, reusing the pattern
`Discoverer` already uses for `LastCheckedAt`:

```text
delay = resetAt - now
due   = now + delay + rand[0, min(delay/4, 60s))
```

For deferrals with no server-supplied time, exponential backoff on
`Attempts` with the same jitter.

`Attempts` increments on every deferral and every reaper requeue. On
exceeding `MAX_JOB_ATTEMPTS` the job takes the terminal disposition —
Open Question 2, the one part of this design with no obviously correct
answer.

### Interaction with the in-flight lease

Deferral removes the in-flight entry, so the reaper never sees a deferred
job and the amplification loop cannot form. A job is in exactly one of
`jobs`, `in-flight`, or `delayed`, and every transition between them is a
single Lua script.

This also resolves INV-0012 finding J incidentally. Duplicate claims
today share a ZSET member because `Job.ID` is a deterministic hash of
`(InstallationID, Owner, Repo)`, so the first worker to wake `ZREM`s the
other's claim. Once nothing sleeps in-handler, jobs leave in-flight in
bounded time and the overlap window closes. It does not *fix* the shared
member — two genuinely concurrent claims of the same repo would still
collide — so Open Question 5 asks whether the member should carry a claim
nonce.

### Layer disposition

| Layer | Disposition | Rationale |
|---|---|---|
| 1 — transport | **Keep, minus the sleep.** 403 retries stay; the pre-emptive sleep becomes `ThrottledError`. | Retrying a rejected request is a transport concern. Blocking a leased worker is not. |
| 2 — sweep reserve gate | **Remove** (Open Question 4 offers keeping it). | Skipping and deferring converge, except deferral does not silently drop the repo until the next sweep. |
| 3 — BudgetTracker | **Delete.** | Its unique value was keeping depleted-budget work out of the queue; deferral does that structurally. Its other value — forward-looking capacity — is replaced by measured `queue_delayed_depth`. |

## API / Interface Changes

```go
// internal/queue — Job gains two fields
type Job struct {
    ID             string
    InstallationID int64
    Owner          string
    Repo           string
    Trigger        string
    EnqueuedAt     time.Time
    Attempts       int       // NEW — incremented on defer and on reaper requeue
    AvailableAt    time.Time // NEW — zero means "runnable now"
}

// internal/queue — Queue gains one method
type Queue interface {
    Enqueue(ctx context.Context, j Job) error
    Subscribe(ctx context.Context, handler func(context.Context, Job) error) error
    Close() error

    // EnqueueAfter schedules j to become runnable no earlier than at.
    // Implementations MUST NOT deliver j before at. An at in the past
    // is equivalent to Enqueue.
    EnqueueAfter(ctx context.Context, j Job, at time.Time) error // NEW
}

// internal/github — new exported error type
type ThrottledError struct {
    ResetAt   time.Time
    Remaining int
    Limit     int
}
```

The `Queue` doc comment is rewritten to specify the three outcomes,
attempt counting, and the terminal disposition — the contract INV-0012
finding K established does not exist today.

**Backward compatibility.** `Job` is serialised as JSON into Valkey, and
`encoding/json` tolerates missing fields on decode, so jobs enqueued by
the previous version decode with `Attempts=0` and a zero `AvailableAt`,
both of which are correct. No queue drain is required on upgrade.

## Data Model

No Postgres schema change. `repo_state` is untouched unless Open
Question 2 resolves to (a), which writes terminal failures to
`last_check_status` using the existing `StatusError` value.

Valkey gains one key (`queue:delayed`). Existing keys are unchanged in
type and semantics.

## Observability

New metrics:

| Metric | Type | Labels | Answers |
|---|---|---|---|
| `queue_delayed_total` | Counter | `reason`, `installation_id` | how often is work deferred, and why |
| `queue_delayed_depth` | Gauge | — | how much work is parked right now |
| `queue_delay_seconds` | Histogram | `reason` | how long are the deferrals |
| `queue_attempts_exhausted_total` | Counter | `installation_id` | is anything being dropped |
| `queue_wait_seconds` | Histogram | `installation_id` | enqueue→dispatch latency per tenant — the [DESIGN-0015](0015-per-installation-valkey-queue-partitioning.md) go/no-go datum |

`queue_wait_seconds` is cheap to add here (the job already carries
`InstallationID` and `EnqueuedAt`; the worker observes the delta at
claim time) and does double duty: it directly measures the FIFO
starvation DESIGN-0015 exists to fix, so that design proceeds or stays
Draft on soak data instead of speculation. Cardinality is
installations × buckets (~25 × 12), well within budget.

Together these answer the question the BudgetTracker was built for — "are
we hitting API limits, how hard, and for whom" — from measurement rather
than from a modelled estimate.

Removed: the nine `api_budget_*` / `enqueue_gated_by_budget_total`
metrics and the `RepoGuardianBudgetGated` alert, none of which has ever
emitted a sample in production.

Retained: `github_rate_remaining` and its two threshold alerts
(`RepoGuardianRateLimitLow`, `RepoGuardianRateLimitNearExhaustion`) —
quota headroom stays layer 1's accounting.

Also removed (INV-0013 Finding G amendment, 2026-08-02):
`github_rate_limit_waits_total` and `github_rate_limit_wait_seconds`.
Once nothing sleeps, "wait" semantics vanish, and the earlier plan to
keep the pair fed by recording the *would-be* delay was rejected as a
duplicate: the same deferral event would be measured by both the wait
pair and Phase 5's `queue_delayed_total{reason}` /
`queue_delay_seconds{reason}`, violating the one-source-per-signal
rule. The single alert consuming the pair
(`RepoGuardianRateLimitThrottling`, contrib pack only) is re-pointed
at `queue_delayed_total{reason="rate_limit"}` in task 5.2b; the
residual "slow GitHub call by status" signal arrives with
[DESIGN-0022](0022-compliance-posture-state-dashboard-suite-and-otel-first.md)'s
`otelhttp` client histograms. Phases 1–5 ship as one minor, so metric
death and alert re-point land in the same release — no coverage gap.

Two new starter alerts:

- `RepoGuardianQueueBackpressure` — `queue_delayed_depth` sustained above
  a threshold: deferrals accumulating faster than they drain.
- `RepoGuardianJobsExhausted` — any increase in
  `queue_attempts_exhausted_total`: work is being dropped.

Per INV-0012 findings C and E, both must be authored with a lookback
window that outlives `for` and is compatible with the metric's real
emission cadence.

## Implementation Phases

Each phase is independently mergeable. Phase 0 is bundled with Phase 1
for review (OQ8 → b); phases 1–5 are the build; phase 6 removes what
they replace; phase 7 is documentation. Run `make lint` and `make fmt`
after each task; commit per numbered task.

### Phase 0: Stop the bleeding

The amplification hazard is live today. Per OQ8 → (b) this lands
bundled with Phase 1 so one review covers the whole rate-limit-path
change — but it remains deliberately *not* the real fix; Phase 3
supersedes it.

#### Tasks

- [ ] 0.1 Cap the transport's pre-emptive delay safely below
      `JOB_ACK_TIMEOUT` (proposed `min(computed, 60s)`), so a sleeping
      worker can never outlive its lease.
- [ ] 0.2 When the computed delay exceeds the cap, return an error
      instead of sleeping the remainder — the job nacks and the reaper
      retries it. Crude but correct until Phase 4.
- [ ] 0.3 Test: a computed delay above the cap does not sleep past it.
- [ ] 0.4 Test reproducing the INV-0012 finding I timeline against a fake
      clock — a handler outliving `JOB_ACK_TIMEOUT` is claimed twice —
      and assert the cap prevents it.

#### Success Criteria

- No code path can sleep longer than `JOB_ACK_TIMEOUT` while holding an
  in-flight claim.
- The finding I timeline test fails without 0.1 and passes with it.
- `make ci` passes.

### Phase 1: Job contract

#### Tasks

- [ ] 1.1 Add `Attempts int` and `AvailableAt time.Time` to `queue.Job`.
- [ ] 1.2 Add `queue.RetryAfterError` with `After`, `Reason`, `Err`, and
      an `Unwrap`.
- [ ] 1.3 Rewrite the `queue.Queue` doc comment to specify all three
      handler outcomes, attempt counting, and the terminal disposition.
      Delete the stale in-memory-implementation parenthetical.
- [ ] 1.4 Add `EnqueueAfter` to the `Queue` interface and implement it in
      the Valkey backend (Phase 2 supplies the mechanism; a stub is
      acceptable at this task boundary only if 1.5 lands in the same PR).
- [ ] 1.5 Regenerate mocks (`make mocks`) and fix fallout in the
      test-local `recordingQueue` fakes.
- [ ] 1.6 Test: a `Job` JSON-encoded by the previous version decodes with
      `Attempts=0` and zero `AvailableAt` (upgrade compatibility).

#### Success Criteria

- The interface documents delay, backoff, attempt cap, and terminal
  disposition — the contract INV-0012 finding K found missing.
- Old-format job payloads decode correctly; no queue drain on upgrade.
- `make ci` passes.

### Phase 2: Delayed set and promotion

#### Tasks

- [ ] 2.1 Add `DelayedKey` to `valkey.Options`, defaulting to
      `repo-guardian:queue:delayed`, and centralise construction of all
      queue keys (jobs, in-flight, delayed, reaper lock) in one helper —
      DESIGN-0015's per-installation partitioning then changes key
      selection in exactly one place, since the Lua scripts already take
      their keys as `KEYS[]` parameters.
- [ ] 2.2 Implement `deferScript` (ZREM in-flight + ZADD delayed, atomic)
      and wire it to `EnqueueAfter`.
- [ ] 2.3 Implement `promoteScript` (ZRANGEBYSCORE due + ZREM + LPUSH,
      atomic) and call it from `reapOnce` under the existing leader lock.
- [ ] 2.4 Publish `queue_delayed_depth` from the reaper tick (`ZCARD`).
- [ ] 2.5 Integration test (`integration` tag): a job deferred 2s is not
      delivered before its due-time and is delivered after.
- [ ] 2.6 Integration test: a job is in exactly one of `jobs`,
      `in-flight`, `delayed` at every point in its lifecycle.
- [ ] 2.7 Integration test: promotion is leader-gated — two reapers, one
      promotion.

#### Success Criteria

- A deferred job is never delivered before `available_at`.
- No job is ever simultaneously present in two of the three keys.
- Promotion happens exactly once per job with multiple replicas running.
- `make ci` passes; integration tests pass against a real Valkey.

### Phase 3: Throttle signal path

#### Tasks

- [ ] 3.1 Add `github.ThrottledError` carrying `ResetAt`, `Remaining`,
      `Limit`.
- [ ] 3.2 Replace the transport's pre-emptive `sleepWithContext` with a
      `ThrottledError` return. Leave the 403 primary/secondary retry
      paths untouched.
- [ ] 3.3 Remove the wait-pair recording along with the sleep — no
      would-be-delay bridge. It would double-measure the deferral that
      Phase 5's `queue_delayed_total` / `queue_delay_seconds` own (see
      Observability). `github_rate_remaining` emission is untouched.
- [ ] 3.4 **Pin the chain with an end-to-end test**: drive a real
      `Engine.CheckRepo` against an `httptest` server returning
      rate-limit headers and assert `errors.As` recovers
      `*ThrottledError` at the worker boundary. This is a permanent
      invariant test, not a one-off check — it fails if any link in the
      chain breaks later, whichever link it is.
- [ ] 3.5 Verify the test is non-vacuous by neutralising one `%w` on the
      path, confirming the test fails, and restoring it. Without this the
      assertion can pass by constructing the error too close to the
      assertion rather than driving it through the real chain.

#### Success Criteria

- The transport never sleeps pre-emptively.
- `errors.As` recovers `*ThrottledError` from a fully-wrapped `CheckRepo`
  error, proven by test rather than assumed.
- The two retained quota alerts (`RepoGuardianRateLimitLow`,
  `RepoGuardianRateLimitNearExhaustion`) still receive samples; nothing
  feeds the wait pair.
- `make ci` passes.

### Phase 4: Worker requeue and attempt cap

#### Tasks

- [ ] 4.1 `processJob` translates `*github.ThrottledError` into
      `*queue.RetryAfterError` with the jittered due-time.
- [ ] 4.2 The Valkey `Subscribe` loop handles `*RetryAfterError` via the
      defer path rather than the ack or nack paths.
- [ ] 4.3 Increment `Attempts` on defer and on reaper requeue.
- [ ] 4.4 Add `MAX_JOB_ATTEMPTS` config (proposed default 10) with the
      terminal disposition from Open Question 2.
- [ ] 4.5 Exponential backoff with jitter for deferrals lacking a
      server-supplied time.
- [ ] 4.6 Test: a throttled job is deferred, not nacked — in-flight is
      empty, delayed has one entry.
- [ ] 4.7 Test: the worker slot is released immediately on deferral (a
      second job is processed while the first is parked).
- [ ] 4.8 Test: attempts accumulate across defers and the cap triggers
      the terminal disposition exactly once.

#### Success Criteria

- A throttled job never occupies a worker slot while waiting.
- Attempts are counted across both the defer and reaper-requeue paths.
- A job cannot retry forever.
- `make ci` passes.

### Phase 5: Observability

#### Tasks

- [ ] 5.1 Add `queue_delayed_total{reason, installation_id}`,
      `queue_delay_seconds{reason}`, and
      `queue_attempts_exhausted_total{installation_id}`
      (`queue_delayed_depth` lands in 2.4).
- [ ] 5.1b Add `queue_wait_seconds{installation_id}` — observed at claim
      time as `now - EnqueuedAt`. This is the DESIGN-0015 go/no-go
      measurement; no partitioning is needed to collect it.
- [ ] 5.2 Add `RepoGuardianQueueBackpressure` and
      `RepoGuardianJobsExhausted` to the chart's `prometheusrule.yaml`
      **and** `contrib/prometheus/alerts.yaml` (INV-0012 finding F: the
      two packs are near-duplicates with no drift gate).
- [ ] 5.2b Re-point `RepoGuardianRateLimitThrottling` (contrib pack
      only) from the removed `github_rate_limit_waits_total` to
      `queue_delayed_total{reason="rate_limit"}`, under the same
      window/`for` discipline as 5.3.
- [ ] 5.3 Verify both alerts against INV-0012 finding C — window outlives
      `for`, and the window suits the metric's real emission cadence.
- [ ] 5.4 helm-unittest assertions on the rendered expressions, not just
      the alert names (the IMPL-0021 A7 convention).
- [ ] 5.5 Document the new metrics in `docs/operations/scaling.md`,
      including what healthy vs. backpressured looks like.

#### Success Criteria

- An operator can answer "are we hitting API limits, how hard, for whom"
  from metrics alone.
- Both new alerts are fireable under realistic emission cadence, reasoned
  explicitly in the PR rather than assumed.
- `make ci` and helm-unittest pass.

### Phase 6: Remove the superseded layers

Only after phases 1–5 are merged and the new path has been observed
working — see the rollout plan.

#### Tasks

- [ ] 6.1 Delete `internal/budget` (223 LOC + 242 test LOC + `labels.go`).
- [ ] 6.2 Remove `Budget` from `StaleSweeperOptions` and
      `DiscovererOptions`, and both `budgetAllows` gates.
- [ ] 6.3 Remove the nine `api_budget_*` /
      `enqueue_gated_by_budget_total` metrics and
      `RepoGuardianBudgetGated` from both alert files, plus the
      now-unfed `github_rate_limit_waits_total` /
      `github_rate_limit_wait_seconds` definitions from
      `internal/metrics` (emission stopped in task 3.3; the sole
      consuming alert was re-pointed in 5.2b).
- [ ] 6.4 Remove `DISCOVERY_RESERVE_FRACTION` and
      `DISCOVERY_ESTIMATED_COST_PER_REPO` from config, their validation,
      and their tests.
- [ ] 6.5 Remove `discovery.reserveFraction` and
      `discovery.estimatedCostPerRepo` from `values.yaml`,
      `values.schema.json`, and `tests/deployment_env_test.yaml`.
- [ ] 6.6 Remove the layer-2 sweep reserve gate (`allowedByRateLimit`)
      and `rate_limit_reserve_blocked_total` (OQ4 → a), preserving a
      producer for `rate_limit_remaining{installation_id}`: keep the
      per-installation sampling call in the sweep loop with no gating
      decision, or retire the gauge together with
      `RepoGuardianRateLimitNearExhaustion` — never leave the alert
      consuming an unfed gauge (the INV-0012 A7 class).
- [ ] 6.7 `make ci` and a `deadcode` pass clean after removal.
- [ ] 6.8 Mark [DESIGN-0002](0002-github-api-rate-limit-handling.md)
      Superseded by this document, and update the CLAUDE.md architecture
      notes describing the BudgetTracker.

#### Success Criteria

- Exactly one rate-limit mechanism remains in the codebase.
- No dangling config, chart value, schema entry, metric, or alert
  referencing the removed layers.
- `make ci` passes; `helm template` renders without the removed values.

### Phase 7: Docs and chart

#### Tasks

- [ ] 7.1 Chart version + appVersion bump; `make helm-docs` (edit
      `README.md.gotmpl`, never the rendered README).
- [ ] 7.2 New `MAX_JOB_ATTEMPTS` chart value with `values.schema.json`
      validation.
- [ ] 7.3 Operator runbook: what a deferred job looks like, how to read
      the new metrics, what to do when backpressure alerts fire.
- [ ] 7.4 Update `docs/operations/scaling.md` and
      `docs/operations/migrations.md` for the removed knobs, and
      document `REAPER_INTERVAL`'s dual duty — lease reaping *and*
      delayed-set promotion cadence (OQ3 → a).
- [ ] 7.5 CLAUDE.md: record the delayed-requeue contract and the
      one-mechanism rule, so a future change does not reintroduce
      in-handler blocking.
- [ ] 7.6 Flip INV-0012 to Concluded and this doc to Implemented; run
      `docz update design inv`.

#### Success Criteria

- Chart renders and installs with the new values.
- mkdocs holds at its 14-warning baseline.
- Every removed knob has a documented upgrade path.

## Testing Strategy

- **Unit** — contract translation (`ThrottledError` → `RetryAfterError`),
  backoff and jitter bounds, attempt accounting. Table-driven.
- **Integration (`integration` build tag, real Valkey)** — due-time
  honouring, the exactly-one-key invariant, leader-gated promotion. This
  is where the Lua atomicity claims are actually tested; unit tests
  against a fake cannot establish them. Follows the existing convention
  in `internal/queue/valkey/valkey_integration_test.go`.
- **Regression** — the INV-0012 finding I timeline (task 0.4) is the
  canonical test for this whole design and should survive every later
  phase.
- **Non-vacuity** — per standing practice, each behavioural test is
  verified by neutralising its fix and confirming the test fails. Task
  3.4 especially: an `errors.As` assertion passes trivially if the test
  constructs the error directly instead of driving it through the real
  wrap chain.
- **Mock fidelity** — promotion-then-delivery is a list-then-act path.
  Per CLAUDE.md, any fake used here must reflect prior writes, or the
  "not delivered before due-time" assertion is vacuous.

## Migration / Rollout Plan

1. **Phases 0–1 land together** as the first PR of the sequence
   (OQ8 → b: one review of the whole rate-limit path). Phase 0's cap
   defuses the live hazard from the first release that carries it.
2. **Phases 1–5 ship as a minor.** New behaviour, one scrape-visible
   removal: the wait pair stops receiving samples (task 3.3), and its
   only consuming alert is re-pointed in the same release (task 5.2b).
   The inert budget gates and the new delayed path coexist harmlessly.
3. **Soak.** Observe `queue_delayed_total` and `queue_delayed_depth` in
   the homelab across at least one full reconcile cycle. At current scale
   deferrals may never trigger naturally; forcing one with a deliberately
   low `RATE_LIMIT_THRESHOLD` is the practical check.
4. **Phase 6 ships separately** (Open Question 7 covers major vs. minor),
   since it removes published chart values.
5. **No queue drain required** at any step — old payloads decode with
   zero-valued new fields (task 1.6).

Rollback: phases 1–5 are additive, so reverting the binary suffices.
After phase 6, rollback also requires restoring chart values, which is
why it ships on its own.

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| The typed error does not survive the wrap chain | low — audited clean across all three hops, and `errorlint` guards the common regression | Task 3.4 pins it as a permanent invariant test so a later break fails CI rather than degrading silently |
| Promotion granularity (60s) too coarse | low | Open Question 3; reconcile work is measured in hours |
| A job defers forever without progressing | low | Attempt cap (4.4) plus `queue_attempts_exhausted_total` |
| Partial work repeated after deferral | certain, low impact | The engine is idempotent by design (INV-0003); wasted calls are bounded |
| Removing layer 2 leaves an enqueue-side gap | low | Open Question 4 offers keeping it; deferral covers the same ground downstream |
| Reaper becomes a bottleneck doing both jobs | low | Both operations are single Lua scripts; measure before splitting |
| Deferrals never occur at current scale, so the path is under-exercised in production | high | Rollout step 3 forces one deliberately; integration tests cover the mechanics |

## Open Questions

1. **Phase 0 hotfix shape.**
   **Resolved 2026-08-02 → (a).**
   (a) Cap the transport's pre-emptive delay at `min(computed, 60s)` and
   return an error beyond it — smallest change, lives entirely in the
   layer that already computes the delay, and Phase 3 supersedes it
   cleanly.
   (b) Wrap each job in `context.WithTimeout(JOB_ACK_TIMEOUT)` at the
   worker — catches *any* long handler, not just throttle sleeps, but
   changes cancellation semantics for every job and risks aborting
   legitimate slow work mid-PR-creation.
   (c) Both.
   other:

2. **Terminal disposition at the attempt cap.**
   **Resolved 2026-08-02 → (a)**, on live evidence: during the
   enterprise-app cutover, jobs for a deleted installation nack-looped
   indefinitely and had to be cleared by hand (Valkey flush +
   `repo_state` row deletion, `docs/operations/ent-setup.md`).
   Disposition (a) makes exactly that incident self-healing — the
   attempt cap fires, the failure is written to the store, and the job
   drops.
   (a) Write terminal failure to `repo_state.last_check_status` and drop
   the job — reuses the store as the dead-letter record, keeps Valkey
   free of graveyard keys, and the next sweep re-enqueues naturally if
   the repo is still stale, which is the self-healing behaviour we
   already rely on.
   (b) Move to a `queue:dead` ZSET for manual inspection — better
   forensics, but a new key nothing prunes, duplicating state the store
   already holds.
   (c) Drop with a counter only — cheapest, no forensics.
   other:

3. **Promotion cadence.**
   **Resolved 2026-08-02 → (a)**, with the rider that the knob's dual
   duty is documented: `REAPER_INTERVAL` now controls both lease
   reaping and delayed-set promotion cadence (task 7.4).
   (a) Reuse `REAPER_INTERVAL` (60s) — no new knob, no new goroutine, and
   60s granularity is irrelevant for work scheduled in hours.
   (b) A separate `PROMOTION_INTERVAL` — finer control, one more knob to
   document and validate.
   (c) Promote opportunistically on every `Subscribe` poll as well as on
   the reaper tick — lowest latency, but every worker then does ZSET
   scans and the leader-election guarantee is lost.
   other:

4. **Layer 2, the sweep reserve gate.**
   **Resolved 2026-08-02 → (a)**, with one rider (task 6.6): the
   gate's `RateLimitRemaining` call is the only live producer feeding
   `rate_limit_remaining{installation_id}` — the gauge is set inside
   `Client.RateLimitRemaining` (client.go), and the only other caller
   is the inert BudgetTracker, also deleted in Phase 6. Removing the
   gate must not orphan the gauge: keep the per-installation sampling
   call in the sweep loop (observability-only, no gating decision), or
   retire the gauge together with `RepoGuardianRateLimitNearExhaustion`
   and the DESIGN-0022 E1 headroom panel source. Recommended: keep the
   sampling.
   (a) Remove it in Phase 6 — deferral covers the same ground and does
   not silently drop the repo until the next sweep, so the gate is
   redundant once Phase 4 lands.
   (b) Keep it as cheap enqueue-side admission control — one API call per
   installation per sweep to avoid enqueueing work that will immediately
   defer; costs a call, saves queue churn.
   (c) Decide after observing `queue_delayed_total` in production.
   other:

5. **In-flight member identity (INV-0012 finding J).**
   **Resolved 2026-08-02 → (a).**
   (a) Leave it — once nothing sleeps in-handler the overlap window
   closes on its own, and the residual case (two genuinely concurrent
   claims of the same repo) is harmless because reconcile is idempotent.
   (b) Add a claim nonce to the ZSET member so concurrent claims are
   distinct — correct, but changes the member format and therefore the
   reaper's requeue payload, and needs an upgrade story.
   (c) Deduplicate at claim time by refusing to claim a repo already
   in-flight — strongest, but adds a read to the hot path.
   other:

6. **Shape of the `Queue` interface change.**
   **Resolved 2026-08-02 → (a).**
   (a) Add `EnqueueAfter` as a fourth method — explicit at the call site,
   and the mock regenerates cleanly.
   (b) Overload `Enqueue` to honour a non-zero `Job.AvailableAt` — no
   interface change at all, but makes the delay invisible at the call
   site and easy to set by accident.
   (c) Return the due-time from the handler and let `Subscribe` own
   scheduling entirely — smallest producer surface, but couples deferral
   to the subscribe path so nothing else can ever defer.
   other:

7. **Phase 6 release shape.**
   **Resolved 2026-08-02 → (a).**
   (a) Minor with a prominent upgrade note — the removed knobs were
   provably inert, so no operator's actual behaviour changes; a major for
   a no-op removal overstates the impact.
   (b) Major — it removes published chart values and a documented
   feature, which is what major is for.
   (c) Keep the knobs as accepted-but-ignored no-ops for one release,
   warn on use, then remove.
   other:

8. **Does Phase 0 ship before the rest of this design is approved?**
   **Resolved 2026-08-02 → (b)** — bundled with Phase 1, one review of
   the whole rate-limit path (reflected in the phases intro, the
   Phase 0 preamble, and rollout step 1).
   (a) Yes — it is an independent hotfix for a live hazard and should not
   wait on decisions about the redesign.
   (b) No — bundle it with Phase 1 so there is one review of the whole
   change in the rate-limit path.
   other:

## References

- [INV-0012](../investigation/0012-inert-budgettracker-and-untrustworthy-alert-pack.md)
  — findings A–K; this design implements its recommendation
- [INV-0013](../investigation/0013-state-vs-event-metrics-dashboard-suite-and-system-observability.md)
  — Finding G's one-source-per-signal rule drives the wait-pair
  retirement (tasks 3.3 / 5.2b / 6.3); Finding I slots this design's
  queue metrics into the service tier
- [DESIGN-0022](0022-compliance-posture-state-dashboard-suite-and-otel-first.md)
  — shared installation-transport ordering contract (its Phase 3, this
  design's task 3.3 note); consumes the Phase 5 metrics on its E3
  system dashboard
- [DESIGN-0002](0002-github-api-rate-limit-handling.md) — layer 1, the
  pre-emptive throttle being superseded (Phase 6 task 6.8)
- [DESIGN-0012](0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — the Store/Queue/Scheduler interfaces this extends
- [DESIGN-0017](0017-stale-sweep-cutover-and-repository-discovery.md)
  — Layer 1/Layer 2 budget design whose BudgetTracker is removed here
- [DESIGN-0015](0015-per-installation-valkey-queue-partitioning.md)
  — adjacent queue work, re-baselined 2026-07-29 against this design;
  fairness stays out of scope here, but task 2.1 (key centralisation) and
  task 5.1b (`queue_wait_seconds`) are its forward hooks
- [IMPL-0011](../impl/0011-persistent-reconcile-state-and-multi-replica-coordination.md)
  — introduced the queue, lease, and reaper
- [IMPL-0015](../impl/0015-stale-sweep-cutover-and-repository-discovery.md)
  — introduced the BudgetTracker being removed
- [INV-0003](../investigation/0003-pre-existing-branch-422-on-subsequent-reconciles.md)
  — the engine idempotency this design relies on for safe re-runs
