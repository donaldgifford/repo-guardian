# Homelab CNPG cutover runbook

You are running chart `0.6.x` with `store.backend=memory` (single-pod,
no persistence) and want to switch to `store.backend=postgres` with
`postgres.mode=cnpg` so reconcile state survives pod restarts.

This runbook assumes:

- Chart `0.6.2` or newer (for the `revisionHistoryLimit` knob).
- A Talos / vanilla k8s cluster managed via ArgoCD.
- You're OK rotating the in-memory reconcile state — there's no
  migration from the memory store, you just lose the in-flight
  knowledge of which repos were recently swept (next sweep
  catches everything up).

For the full post-cutover smoke gate (8 checks: schema applied,
leader-election working, queue draining, alerts firing, etc.), see
[`chart-0.5.0-migration.md`](chart-0.5.0-migration.md) "Smoke checks".
This doc focuses on the four homelab-specific gaps that runbook
glosses over.

## Pre-flight (15 min)

### 1. CNPG operator prerequisite

The chart renders a `Cluster` CR (`postgresql.cnpg.io/v1`) and an
optional `Pooler`. Both require the CNPG operator to be installed
**before** you apply the chart values change, or ArgoCD will sync
the Application into a `ComparisonError` state.

```bash
# Verify the CRDs are present.
kubectl get crd | grep postgresql.cnpg.io
# Expected:
#   backups.postgresql.cnpg.io
#   clusters.postgresql.cnpg.io
#   poolers.postgresql.cnpg.io
#   scheduledbackups.postgresql.cnpg.io
```

If missing, install the operator first. The maintained path is
the upstream Helm chart:

```bash
helm repo add cnpg https://cloudnative-pg.github.io/charts
helm upgrade --install cnpg cnpg/cloudnative-pg \
  --namespace cnpg-system \
  --create-namespace \
  --version 0.22.x
```

Pin to a known-good operator version (the chart was tested against
CNPG operator `1.24.x` lineage). The CNPG operator runs cluster-wide
and only needs installing once per cluster.

### 2. Pick a StorageClass explicitly

The chart's `store.postgres.cnpg.storage.storageClass` defaults to
`""`, which falls back to the cluster's default StorageClass. On
Talos with multiple SCs (rook-ceph, local-path, longhorn, etc.) the
"default" can drift silently — better to pin it.

```bash
kubectl get sc
# Pick the one you want; check it supports ReadWriteOnce.
```

Add to your values:

```yaml
store:
  postgres:
    cnpg:
      storage:
        size: 10Gi
        storageClass: rook-ceph-block   # or whatever you picked
```

### 3. CNPG-created Secret timing

When `mode=cnpg`, the chart wires `STORE_DSN` from a Secret that the
**CNPG operator** creates after it provisions the Cluster — typically
named `<release-name>-postgres-app`, key `uri`. This secret does not
exist at the moment ArgoCD applies the manifests; CNPG creates it
during cluster bootstrap (10-60 seconds typical).

Consequence: the `repo-guardian` Deployment will `CrashLoopBackOff`
during initial sync because its `STORE_DSN` env var references a
Secret key that doesn't exist yet. This is **expected** and self-heals
within 1-2 reconcile loops once CNPG finishes bootstrapping.

If you want the bring-up to look clean in ArgoCD's UI:

- Add `argocd.argoproj.io/sync-wave` annotations so the CNPG
  `Cluster` resource lands before the `Deployment`. The chart
  doesn't ship these by default (they're cluster-policy-specific).
- Or, use `argocd.argoproj.io/sync-options: SkipDryRunOnMissingResource=true`
  on the Application to suppress the noise.

For first-time bring-up, just let it CrashLoop for a minute and
verify it recovers on its own.

### 4. revisionHistoryLimit (chart 0.6.2+)

You're already getting this — chart `0.6.2` defaults
`revisionHistoryLimit: 3`. If you want a different value, set:

```yaml
revisionHistoryLimit: 5
```

This applies to the `repo-guardian` Deployment only. The CNPG-managed
Postgres Cluster has its own retention via the operator; not exposed
through the repo-guardian chart.

## Values diff (homelab reference)

```yaml
# Before (chart 0.6.x, memory store)
replicaCount: 1
store:
  backend: memory

# After (chart 0.6.2, CNPG store)
replicaCount: 1                         # bump to 3 if you also want HA
revisionHistoryLimit: 3                 # chart 0.6.2 default; explicit for clarity
store:
  backend: postgres
  postgres:
    mode: cnpg
    cnpg:
      instances: 1                      # bump to 3 for CNPG HA replicas
      storage:
        size: 10Gi
        storageClass: rook-ceph-block   # pin explicitly
      pooler:
        enabled: false                  # optional PgBouncer; skip for single-replica
```

If you also want to flip the queue + scheduler to durable backends
in the same change (recommended for `replicaCount > 1`, optional for
single-replica), add:

```yaml
queue:
  backend: valkey
  valkey:
    mode: baked                         # single-pod Valkey from the chart
scheduler:
  backend: valkey                       # SETNX leader-election
```

The full multi-replica reference is in
[`chart-0.5.0-migration.md`](chart-0.5.0-migration.md) Stage 2.

## Bring-up sequence

1. Cosign-verify the new chart version:

   ```bash
   cosign verify \
     --certificate-identity-regexp \
       '^https://github.com/donaldgifford/repo-guardian/.+' \
     --certificate-oidc-issuer \
       'https://token.actions.githubusercontent.com' \
     ghcr.io/donaldgifford/charts/repo-guardian:0.6.2
   ```

2. Render the chart locally and diff against the live state to
   confirm only expected resources change:

   ```bash
   helm template repo-guardian \
     oci://ghcr.io/donaldgifford/charts/repo-guardian \
     --version 0.6.2 \
     --namespace repo-guardian \
     -f values.yaml > /tmp/rg-after.yaml
   diff <(kubectl get -n repo-guardian deploy,svc,sts,cluster.postgresql.cnpg.io \
     -o yaml) /tmp/rg-after.yaml
   ```

   Expect: new `Cluster` resource, new `Service` for Postgres, env
   var changes on the Deployment (`STORE_BACKEND`, `STORE_DSN`).

3. Sync the ArgoCD Application or run `helm upgrade`.

4. Watch the rollout:

   ```bash
   kubectl get pods -n repo-guardian -w
   # Order: CNPG cluster pod(s) → repo-guardian (will crashloop briefly)
   #        → repo-guardian Ready once Postgres is up.
   ```

5. Once both pods are Ready, run the [`chart-0.5.0-migration.md`
   smoke checks](chart-0.5.0-migration.md#smoke-checks-8-gates):

   - Schema applied (`\dt` shows `repo_state`, `schema_migrations`)
   - Pod restart no longer triggers a full re-sweep (the original
     reason for this cutover)
   - `store_query_seconds` metric is populated
   - No flapping in `RepoGuardianStoreUnavailable` alert

## Rollback

If something goes wrong, the rollback is values-only:

```yaml
store:
  backend: memory
```

After applying, the chart drops the `Cluster` resource. **The CNPG
operator does not auto-delete the PVC**; you'll need to do that by
hand if you want the storage reclaimed:

```bash
kubectl delete pvc -n repo-guardian -l \
  cnpg.io/cluster=repo-guardian-postgres
```

The reconcile state in Postgres is lost on rollback (the memory
store starts empty), but that's the same state you started with
before this runbook.

## See also

- [`chart-0.5.0-migration.md`](chart-0.5.0-migration.md) — full
  multi-replica migration runbook (covers Valkey + scheduler too)
- [`scaling.md`](scaling.md) — replica counts, batch size, freshness
  tuning
- [`migrations.md`](migrations.md) — in-process schema migration
  runner behavior
- [DESIGN-0012](../design/0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — backend mode rationale
- Chart values: `charts/repo-guardian/values.yaml` (lines 211-259
  for the CNPG knobs)
