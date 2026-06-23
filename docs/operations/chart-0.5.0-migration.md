# Chart 0.5.0 migration runbook (IMPL-0011)

> [!NOTE]
> **Superseded by chart `1.0.0-rc.1` (IMPL-0016).** The
> memory/ticker defaults this runbook describes are no longer
> valid. The chart now ships Postgres + Valkey as the only
> supported backends. See
> [migrations.md#removing-memory-backend](migrations.md#removing-memory-backend)
> for the current migration path. The 0.5.0 runbook is retained
> for historical context only.

Chart `0.5.0` ships [IMPL-0011](../impl/0011-persistent-reconcile-state-and-multi-replica-coordination.md):
durable Postgres-backed `Store`, Valkey-backed `Queue` with in-flight
reaper, and SETNX leader-elected `Scheduler` — the multi-replica
foundation.

**(Historical) Defaults at the time of 0.5.0 release.**
`store.backend=memory`, `queue.backend=memory`,
`scheduler.backend=ticker`. Operators upgrading from chart `0.3.x`
or `0.4.x` at `replicaCount: 1` got bit-identical behaviour with
no values changes. Those backends were removed in chart
`1.0.0-rc.1`.

This runbook drives the upgrade in **two stages** so any regression is
bisectable: no-op upgrade first, then multi-replica enablement.

## Pre-flight (5 min)

- [ ] **Cosign-verify the chart artifact:**

  ```bash
  cosign verify \
    --certificate-identity-regexp '^https://github.com/donaldgifford/repo-guardian/.+' \
    --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
    ghcr.io/donaldgifford/charts/repo-guardian:0.5.0
  ```

- [ ] **Snapshot current cluster state** (rollback baseline):

  ```bash
  kubectl get deploy/repo-guardian -n repo-guardian -o yaml > /tmp/rg-pre-upgrade.yaml
  kubectl get pods -n repo-guardian
  ```

- [ ] **Read** [`scaling.md`](scaling.md) and [`migrations.md`](migrations.md)
  for context on sizing knobs and Postgres schema lifecycle.

## Stage 1 — No-op upgrade to 0.5.0

Proves the chart-render upgrade path is clean. Defaults unchanged:
`store.backend=memory`, `queue.backend=memory`,
`scheduler.backend=ticker`, `replicaCount: 1`.

- [ ] **Bump the chart version** in your ArgoCD `Application` (or
  kustomize `helmCharts[0].version`) from your current pin to `0.5.0`.
  **Do not change values yet.**

- [ ] **`helm template` first** and diff against current cluster state:

  ```bash
  helm template repo-guardian oci://ghcr.io/donaldgifford/charts/repo-guardian \
    --version 0.5.0 \
    -n repo-guardian \
    -f your-values.yaml > /tmp/rg-0.5.0.yaml

  diff <(kubectl get deploy/repo-guardian -n repo-guardian -o yaml) /tmp/rg-0.5.0.yaml | less
  ```

  Expected diffs:

  - `terminationGracePeriodSeconds: 60` (was 30)
  - New env vars: `STORE_BACKEND=memory`, `QUEUE_BACKEND=memory`,
    `SCHEDULER_BACKEND=ticker`, `WORKER_CONCURRENCY=4`,
    `RECONCILE_FRESHNESS=7m`, `RATE_LIMIT_RESERVE=200`
  - Image tag: `1.6.0`

- [ ] **Apply** (sync the ArgoCD app, or `helm upgrade`).

- [ ] **Verify pod restarts cleanly** and image is correct:

  ```bash
  kubectl rollout status deploy/repo-guardian -n repo-guardian
  kubectl get pod -n repo-guardian -o jsonpath='{.items[*].spec.containers[0].image}'
  # expect: ghcr.io/donaldgifford/repo-guardian:1.6.0
  ```

- [ ] **Smoke test** — push a no-op commit to `donaldgifford/logpush`.
  Confirm webhook returns 202 and reconcile completes:

  ```bash
  kubectl logs -n repo-guardian deploy/repo-guardian --tail=100 \
    | grep -E 'webhook|reconcile|enqueue'
  ```

- [ ] **Bake for 24h.** If anything misbehaves, roll the chart version
  pin back; defaults guarantee a clean rollback.

## Stage 2 — Enable multi-replica

The IMPL-0011 payoff. Pick the Postgres mode based on CNPG availability:

| CNPG operator installed? | Use this mode |
|---|---|
| Yes | `cnpg` (CNPG manages HA replicas, backups, monitoring) |
| No  | `baked` (chart renders a single-pod Postgres StatefulSet) |

- [ ] **Add the new values** to your values file. Reference for
  `cnpg + baked Valkey`:

  ```yaml
  replicaCount: 3
  store:
    backend: postgres
    postgres:
      mode: cnpg            # or "baked" if no CNPG
  queue:
    backend: valkey
    valkey:
      mode: baked
  scheduler:
    backend: valkey
  prometheusRule:
    enabled: true            # 5 starter alerts
  ```

  Reference for `external` (managed Postgres + Redis-compatible service)
  is documented in the chart [README](../../charts/repo-guardian/README.md#choosing-a-deployment-shape).

- [ ] **`helm template` again** and diff. Expected new resources:

  - `Cluster` + optional `Pooler` (CNPG) **OR** `StatefulSet` /
    `Service` / `Secret` (baked Postgres)
  - `StatefulSet` / `Service` / `Secret` (Valkey)
  - `PrometheusRule` (5 alerts)
  - `Deployment` `replicas: 3`

- [ ] **Apply.** Watch the bring-up:

  ```bash
  kubectl get pods -n repo-guardian -w
  ```

  Postgres comes up first; the `repo-guardian` deployment blocks on
  its readiness via the in-process migration runner. Expect 30-60s
  for the first pod to reach Ready.

## Smoke checks (8 gates)

### 1. Postgres schema applied

```bash
kubectl exec -n repo-guardian -it <postgres-pod> -- \
  psql -U repoguardian -d repoguardian -c '\dt'
# expect: repo_state, schema_migrations
```

### 2. Leader election holds

Exactly one of the three pods grabs the sweep lock per tick:

```bash
kubectl logs -n repo-guardian -l app.kubernetes.io/name=repo-guardian \
  --tail=200 --prefix | grep "scheduler tick acquired lock"
# expect: log lines from one pod only at any given moment
```

### 3. Webhook returns 202

Push a commit to `donaldgifford/logpush`:

```bash
kubectl logs -n repo-guardian -l app.kubernetes.io/name=repo-guardian \
  --tail=100 | grep "POST /webhook"
# expect: status=202
```

### 4. Reconcile completes & state row created

```bash
kubectl exec -n repo-guardian -it <postgres-pod> -- \
  psql -U repoguardian -d repoguardian \
  -c "SELECT installation_id, owner, repo, last_checked_at, last_check_status, policy_version
      FROM repo_state
      WHERE owner='donaldgifford' AND repo='logpush';"
# expect: one row, last_checked_at recent, last_check_status=success
```

### 5. Metrics endpoint exposes the new families

```bash
kubectl port-forward -n repo-guardian svc/repo-guardian 9090:9090 &
curl -s localhost:9090/metrics \
  | grep -E '^(queue_|scheduler_|store_query_seconds|rate_limit_)' \
  | head -20
# expect: queue_depth, queue_enqueued_total, scheduler_lock_acquired_total,
#         store_query_seconds_*, rate_limit_remaining
```

### 6. PrometheusRule loaded

(Skip if the Prometheus operator is not installed.)

```bash
kubectl get prometheusrule -n repo-guardian repo-guardian -o yaml \
  | grep -E '^\s+- alert:'
# expect 5 alerts: QueueDepthGrowing, QueueEnqueueErrors,
#                  SchedulerLeaderFlapping, StoreSlowQueries,
#                  RateLimitNearExhaustion
```

### 7. Leader failover within 30s

Kill the current leader; verify another pod picks up:

```bash
LEADER=$(kubectl logs -n repo-guardian -l app.kubernetes.io/name=repo-guardian \
  --tail=50 --prefix | grep "scheduler tick acquired lock" \
  | head -1 | awk -F'pod/' '{print $2}' | awk '{print $1}')

kubectl delete pod -n repo-guardian "$LEADER"
sleep 35

kubectl logs -n repo-guardian -l app.kubernetes.io/name=repo-guardian \
  --tail=50 --prefix | grep "scheduler tick acquired lock" | tail -3
# expect: lock acquired by a different pod within 30s
```

### 8. No duplicate reconciles

Wait through one full sweep cycle (default 7m) and check for duplicates:

```bash
kubectl logs -n repo-guardian -l app.kubernetes.io/name=repo-guardian \
  --tail=500 --prefix | grep "reconciled" | sort | uniq -c | sort -rn | head -10
# expect: count of 1 per (owner/repo) per cycle, not 2-3
```

## Rollback

**Fast rollback to single-replica.** Flip values back to defaults; redeploy.
The Postgres + Valkey StatefulSets/CRs stay in cluster (state preserved)
but the binary stops using them:

```yaml
replicaCount: 1
store: { backend: memory }
queue: { backend: memory }
scheduler: { backend: ticker }
```

**Hard rollback to your previous chart version.** Pin the chart version
back; sync. The 0.5.0 backing resources (Postgres data, Valkey state)
become orphaned but harmless — clean up later once you confirm the
older chart is healthy:

```bash
kubectl delete sts,svc,secret -l app.kubernetes.io/instance=repo-guardian \
  -n repo-guardian
```

## Watch-list during the first 24h post-Stage-2

- `queue_depth` — should oscillate between 0 and ~N (your repo count)
  per sweep cycle, not climb monotonically.
- `scheduler_lock_acquired_total` — should increment ~1× per tick
  (every 7m by default), only on the leader.
- `rate_limit_reserve_blocked_total` — non-zero is fine if you have
  many installations; persistent climbing means lower
  `STORE_SWEEP_BATCH_SIZE` (or raise `RATE_LIMIT_RESERVE`).
- `repo_state.last_checked_at` — for `donaldgifford/logpush` and
  `donaldgifford/repo-guardian-test-repo`, should refresh every ~7m.

## References

- [IMPL-0011](../impl/0011-persistent-reconcile-state-and-multi-replica-coordination.md)
  — implementation plan with phase-by-phase task list.
- [DESIGN-0012](../design/0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — design doc with the durable backend rationale (Postgres + Valkey,
  not NATS + embedded).
- [`scaling.md`](scaling.md) — sizing knobs for `replicaCount`,
  `workerCount`, `maxConns`, `staleSweep.*`.
- [`migrations.md`](migrations.md) — Postgres schema operations,
  out-of-band migration runs, backups, schema rollback.
- [Chart README — Choosing a deployment shape](../../charts/repo-guardian/README.md#choosing-a-deployment-shape)
  — the four shapes with use-case mapping.
