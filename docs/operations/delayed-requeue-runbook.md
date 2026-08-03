# Delayed requeue — operator runbook

repo-guardian defers work it cannot do right now instead of blocking on
it. When a job's GitHub calls run into the rate limit, the worker parks
the whole job in a Valkey delayed set with a due-time and frees its
slot immediately; the reaper promotes due jobs back onto the pending
list. This replaced three separate throttling layers (transport sleep,
sweep reserve gate, BudgetTracker) with one mechanism — see
[DESIGN-0021](../design/0021-delayed-requeue-job-contract-and-rate-limit-consolidation.md).

This runbook covers what a deferral looks like from the outside and
what to do when the two new alerts fire.

## The lifecycle in one pass

1. A worker claims a job. The job moves from
   `repo-guardian:queue:jobs` (LIST) to
   `repo-guardian:queue:in-flight` (ZSET, scored by claim time).
2. The GitHub transport sees remaining quota at or below
   `RATE_LIMIT_THRESHOLD × limit` and returns a `ThrottledError`
   instead of sleeping.
3. The worker computes a due-time — the rate-limit reset, plus jitter;
   or exponential backoff (30s doubling, capped at 30m) if the reset
   is already stale — and returns a `RetryAfterError`.
4. The queue atomically moves the payload from
   `repo-guardian:queue:in-flight` to `repo-guardian:queue:delayed`
   (ZSET, scored by due-time nanos) with `Attempts`
   incremented. **The job is in exactly one key at every point.**
5. Each `REAPER_INTERVAL` tick, the reaper leader promotes every
   delayed entry whose due-time has passed back onto
   `repo-guardian:queue:jobs`.
6. If a job accumulates `MAX_JOB_ATTEMPTS` deliveries, the next
   delivery drops it: a terminal `StatusError` is written to
   `repo_state` naming `MAX_JOB_ATTEMPTS`, and the job is acked rather
   than retried. The next stale sweep re-enqueues the repo naturally.

Delivery lags due-time by up to one `REAPER_INTERVAL` (default 60s).
That is the intended trade: one leader election and one cadence serve
both stuck-job reaping and delayed promotion.

## What a deferral looks like

### In logs

Two Info lines, in order. The worker decides:

```json
{"level":"INFO","msg":"rate limit throttled; deferring job",
 "reset_at":"2026-08-02T15:04:05Z","due":"2026-08-02T15:04:41Z",
 "attempts":1,"remaining":42,"limit":5000}
```

Then the queue confirms the park:

```json
{"level":"INFO","msg":"job deferred to delayed set",
 "job_id":"stale-sweep/42/org/repo/1754...","owner":"org","repo":"repo",
 "due":"2026-08-02T15:04:41Z","reason":"rate_limit","attempts":1}
```

`due` minus `reset_at` on the first line is the jitter. A deferral is
**not** an error: it does not increment `errors_total`, and it does not
write to `repo_state` (the check never ran, so there is no outcome to
record).

### In Valkey

Keys are prefixed `repo-guardian:` by default (`DefaultDelayedKey` and
friends in `internal/queue/valkey`); adjust if you overrode them.

```bash
# How many jobs are parked
redis-cli ZCARD repo-guardian:queue:delayed

# The next few due, with their due-times as unix nanos
redis-cli ZRANGE repo-guardian:queue:delayed 0 4 WITHSCORES

# Anything already due but not yet promoted (should be near-empty
# between reaper ticks)
redis-cli ZCOUNT repo-guardian:queue:delayed 0 "$(date +%s)000000000"
```

The members are the JSON job payloads, so `ZRANGE` output shows
`Attempts` and `AvailableAt` directly.

### In metrics

| Metric | Read it as |
|---|---|
| `queue_delayed_depth` | How much is parked right now. Published by every pod each reaper tick. |
| `queue_delayed_total{reason, installation_id}` | How often work is deferred, and for whom. |
| `queue_delay_seconds{reason}` | How far ahead deferrals are parked. |
| `queue_wait_seconds{installation_id}` | Enqueue-to-claim latency, parked time included. |
| `queue_attempts_exhausted_total{installation_id}` | Jobs dropped at the cap. |
| `queue_acked_total{outcome="deferred"}` | Should track `queue_delayed_total` 1:1. |

Reference values for healthy vs backpressured are in
[scaling.md § Delayed requeue](scaling.md#delayed-requeue-impl-0022).

## Responding to alerts

### RepoGuardianQueueBackpressure

`max(repo_guardian_queue_delayed_depth) > 100` for 30m.

Deferrals are outpacing promotion: the fleet is asking for more API
budget than it has. The system is not broken — this is backpressure
working — but the reconcile cadence you configured is not achievable.

1. **Find the tenant.** Deferrals are rarely fleet-wide:

   ```promql
   topk(5, sum by (installation_id) (
     increase(repo_guardian_queue_delayed_total{reason="rate_limit"}[1h])))
   ```

2. **Confirm it is rate limits, not a stuck reaper.** If
   `queue_delayed_depth` is high but `queue_delayed_total` is flat,
   nothing new is being deferred — promotion has stalled. Check
   `repo_guardian_scheduler_is_leader` and the reaper's
   `"reaper promoted delayed jobs"` log lines; a pod holding the
   leader lock but wedged will show neither.

3. **If it is rate limits, the levers are the same three as always:**
   raise `staleSweep.freshness` (re-check less often), trim rules you
   do not need (each rule ≈ one API call per repo), or move to per-org
   App credentials so each org gets its own budget (INV-0006). Adding
   replicas or workers does nothing — they will all sit parked.

4. **Do not raise `MAX_JOB_ATTEMPTS` to "fix" this.** The cap is not
   what is holding work back; the API budget is.

### RepoGuardianJobsExhausted

A job hit `MAX_JOB_ATTEMPTS` and was dropped.

A single increment after a GitHub incident is unremarkable — the stale
sweep picks the repo back up. A sustained rate means one installation
is failing every time it is tried.

1. **Identify it:**

   ```promql
   sum by (installation_id) (
     increase(repo_guardian_queue_attempts_exhausted_total[6h]))
   ```

2. **Read the terminal state.** The drop wrote a row you can query:

   ```sql
   SELECT owner, repo, last_error, last_checked_at
     FROM repo_state
    WHERE installation_id = 42
      AND last_check_status = 'error'
      AND last_error LIKE '%MAX_JOB_ATTEMPTS%'
    ORDER BY last_checked_at DESC;
   ```

   `last_error` names the cap but not the underlying failure — that is
   in the worker logs for the same repo, which is where to look next.

3. **Common causes:** a suspended or uninstalled App (every call 401s),
   revoked repo access after an org policy change, or a repo whose
   default branch was deleted. All of these fail identically on every
   attempt, which is exactly what the cap exists to stop.

## Forcing a deferral (to verify the path works)

At small scale organic deferrals may never happen. To exercise the path
deliberately, raise the pre-emptive threshold so ordinary quota looks
exhausted. `RATE_LIMIT_THRESHOLD` is a binary env var with no dedicated
chart value, so set it through `extraEnv`:

```yaml
extraEnv:
  - name: RATE_LIMIT_THRESHOLD
    value: "0.99"   # defer unless >99% of quota remains
```

Roll the deployment, watch `queue_delayed_total` increment and
`queue_delayed_depth` rise, confirm promotion drains it within a
`REAPER_INTERVAL` of the reset, then remove the override. Do this in a
non-production environment first: at 0.99 essentially all work defers.

## Tuning notes

- **`REAPER_INTERVAL` does double duty** — stuck-job reaping *and*
  delayed promotion. Lowering it tightens promotion latency at the cost
  of more Valkey round-trips; raising it does the reverse. There is no
  separate promotion cadence knob, by design (DESIGN-0021 OQ3).
- **`JOB_ACK_TIMEOUT` must stay above the worst-case check duration.**
  Deferral does not change this: a job that defers has already left the
  in-flight set, so it is not at risk of being reaped — but a job doing
  real work still is.
- **`MAX_JOB_ATTEMPTS` counts every delivery**, deferrals and reaper
  requeues alike. A repo behind a slow-recovering rate limit can burn
  attempts without ever having failed a check. The default of 10 is
  generous for that reason; lower it only if you would rather see
  terminal errors sooner.
