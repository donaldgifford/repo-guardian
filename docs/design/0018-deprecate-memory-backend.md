---
id: DESIGN-0018
title: "Deprecate memory backend"
status: Approved
author: Donald Gifford
created: 2026-06-22
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0018: Deprecate memory backend

**Status:** Approved
**Author:** Donald Gifford
**Date:** 2026-06-22

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [Audit summary](#audit-summary)
- [Detailed Design](#detailed-design)
  - [What gets removed](#what-gets-removed)
  - [What gets reshaped](#what-gets-reshaped)
  - [Startup validation](#startup-validation)
  - [Local development](#local-development)
- [API / Interface Changes](#api--interface-changes)
  - [CLI / env vars](#cli--env-vars)
  - [Chart values](#chart-values)
  - [Go interfaces](#go-interfaces)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
  - [Removed](#removed)
  - [Rewritten](#rewritten)
  - [Kept (unchanged)](#kept-unchanged)
  - [New](#new)
- [Migration / Rollout Plan](#migration--rollout-plan)
  - [Phase 0 — Deprecation notice (one release cycle)](#phase-0--deprecation-notice-one-release-cycle)
  - [Phase 1 — Removal (one release cycle later)](#phase-1--removal-one-release-cycle-later)
  - [Rollback](#rollback)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Remove the in-memory `store` / `queue` / `scheduler` backends. Production
deployments require Postgres + Valkey (or managed equivalents like AWS RDS /
ElastiCache per `docs/operations/aws.md`); the memory backends are
maintenance dead weight that complicates every state-management change. Net
~1,600 LOC removal, a smaller test matrix, and tighter startup validation.

Surfaced during the DESIGN-0017 pre-implementation review (question (e)) —
the operator proposed dropping memory backend entirely instead of designing
around it. This design carries that decision through.

**Sequencing:** IMPL-0016 (this design's implementation) ships **first**,
before IMPL-0015 (DESIGN-0017's implementation). Both bundle into the
1.0.0 release, validated via manually-tagged 1.0.0-rc.N versions in
homelab. See resolved [OQ (g)](#open-questions) and the [Migration /
Rollout Plan](#migration--rollout-plan). The deprecation-warning Phase 0
originally proposed here was dropped per resolved
[OQ (a)](#open-questions) — no audience for the warning.

## Goals and Non-Goals

### Goals

- **Single production code path.** Postgres + Valkey only. No more
  `if storeBackend == "memory"` branching scattered across `main.go`,
  config validation, chart helpers, and helm-unittest cases.
- **Remove dead code.** ~1,600 LOC across `internal/store/memory/`,
  `internal/queue/memory/`, `internal/scheduler/ticker/` (967 LOC of
  package code + 634 LOC of unit tests).
- **Simpler test matrix.** Today integration tests must cover both the
  in-process and durable shapes; after this design, contract tests run
  only against the durable backends.
- **Tighter startup validation.** No silent defaults: missing or invalid
  `STORE_BACKEND` / `QUEUE_BACKEND` / `SCHEDULER_BACKEND` fails fast with a
  clear message. No more "service silently runs in memory mode and loses
  state on restart" failure mode.
- **Closes DESIGN-0017 (e).** The deferred question becomes "no longer
  applicable — memory mode removed."

### Non-Goals

- Adding a new in-process test fake. Tests that need a Store stub should
  use the mockery-generated mocks from DESIGN-0012 conventions. We're not
  building a "memory backend for tests only" replacement.
- Supporting more backends. Postgres + Valkey already cover managed
  equivalents (RDS, RDS Proxy, Aurora, ElastiCache for Valkey) via the
  same client code. No new backend types in scope.
- Removing testcontainers from integration tests. The
  `_integration_test.go` build-tagged suites continue to spin up real
  Postgres / Valkey in Docker; this design doesn't touch them.
- Touching the documentation in `docs/legacy/`. That tree was already
  retired in IMPL-0014.

## Background

IMPL-0011 (DESIGN-0012) introduced multi-backend support. The shape:

| Concern | In-process backend | Durable backend |
|---|---|---|
| Per-repo state (`internal/store/`) | `memory/` — `sync.Map` of `RepoState` rows | `postgres/` — pgx + `repo_state` table |
| Job queue (`internal/queue/`) | `memory/` — buffered channel + in-mem slice | `valkey/` — LIST + ZSET + SETNX leader reaper |
| Scheduler (`internal/scheduler/`) | `ticker/` — `time.Ticker` loop | `valkey/` — SETNX leader election + Ticker on the leader |

The intent at the time: ship multi-replica deployments without forcing a
hard Postgres + Valkey dependency on operators running single-replica
homelabs. The chart defaulted to the in-process shape so the "just install"
path Just Worked.

That tradeoff hasn't borne out. The two production deployments
(`donaldgifford/repo-guardian`, `donaldgifford/logpush`) both run on
Postgres + Valkey via CNPG. The aws.md guide assumes managed equivalents. No
known deployment uses memory backend in production. The chart's homelab
smoke runbook (`charts/repo-guardian/docs/homelab-smoke.md`) walks operators
through the Postgres + Valkey path on first install.

Maintenance friction surfaced during the DESIGN-0017 pre-implementation
audit. Every Phase 0 change had to articulate "and what does memory backend
do here?" — the answer was always "no-op Store, no-op Queue, no behavioural
change," but the audit had to verify it for each new write site. The audit
explicitly recommended deferring (e) to this design because folding memory
removal into IMPL-0015 would have doubled its scope.

### Audit summary

The pre-implementation audit (referenced in DESIGN-0017) mapped the full
removal scope:

| Category | Removed | Added |
|---|---|---|
| `internal/{store,queue,scheduler}/{memory,memory,ticker}/` packages | 967 LOC | — |
| Unit tests inside those packages | 634 LOC | — |
| `main.go` backend-dispatch branches + imports | 17 LOC | ~5-10 LOC straight-line code |
| `internal/config/` constants + validation cases | 6 LOC | ~20 LOC tightened error messages |
| Helm-unittest cases in `tests/backend_shapes_test.yaml` | ~100 LOC (1 deleted, 3 rewritten) | ~50-80 LOC |
| Documentation | string deltas across ~7 files | — |
| **Total** | **~1,700 LOC** | **~100 LOC** |
| **Net** | | **~1,600 LOC removed** |

No cross-package coupling. No hidden imports outside `main.go`. No interface
assertions tying memory packages to contract compliance (mocks are
mockery-generated per DESIGN-0012). The audit found a clean removal path.

## Detailed Design

### What gets removed

Three packages, deleted whole:

| Package | Files | LOC |
|---|---|---|
| `internal/store/memory/` | `memory.go`, `memory_test.go` | 329 |
| `internal/queue/memory/` | `memory.go`, `memory_test.go` | 387 |
| `internal/scheduler/ticker/` | `ticker.go`, `ticker_test.go` | 251 |

Plus the ticker-arm of the scheduler contract test
(`internal/scheduler/contract_test.go` ~22 LOC at line 94) — the
valkey-arm in `internal/scheduler/valkey/valkey_integration_test.go`
becomes the single source of truth.

### What gets reshaped

**`cmd/repo-guardian/main.go`** (~17 LOC removed):

- `newStore()` (lines 354-368): delete the memory branch; keep
  postgres-only path. Returns `(*postgres.Store, error)` directly.
- `newQueue()` (lines 282-314): delete memory branch; valkey-only.
- `newScheduler()` (lines 320-337): delete ticker branch; valkey-only.
- Imports (lines 25, 30, 33): remove `memqueue`, `ticker`, `memstore`.

The three helpers shrink to straight-line code with no conditionals. They
remain as functions (rather than inlined) to keep `main()` under the
`funlen` threshold.

**`internal/config/`**:

- Delete constants `StoreBackendMemory`, `QueueBackendMemory`,
  `SchedulerBackendTicker` (lines 137-143).
- `loadBackendConfig()` no longer sets memory/ticker defaults; the env
  vars become required. Empty / unset is a validation error.
- `validateBackends()` (lines 327-355) drops the memory/ticker switch
  cases. Error messages tighten to "STORE_BACKEND must be 'postgres'."

**`internal/scheduler/sweep.go`** survives unchanged. The legacy Sweeper
accepts the abstract `queue.Queue` interface and is queue-agnostic. It
remains the discovery path on Postgres+Valkey deployments (per
DESIGN-0017's Discoverer rename and per the chart default-flip in
DESIGN-0017 Phase 0).

**Chart values** (`charts/repo-guardian/values.yaml`):

- Line 217: `store.backend: memory` → `store.backend: postgres`.
- Line 294: `queue.backend: memory` → `queue.backend: valkey`.
- Line 328: `scheduler.backend: ticker` → `scheduler.backend: valkey`.

The chart's baked CNPG / baked Valkey resources are the new
out-of-the-box experience. Operators who upgrade past the deprecation
window without setting `store.postgres.mode` get baked CNPG by default.

### Startup validation

After this design ships, the binary refuses to start with
memory/ticker backend values:

```
$ STORE_BACKEND=memory ./repo-guardian
{"level":"ERROR","msg":"config validation failed",
 "error":"STORE_BACKEND='memory' is no longer supported; set STORE_BACKEND=postgres and configure STORE_DSN. See https://github.com/donaldgifford/repo-guardian/blob/main/docs/operations/migrations.md#removing-memory-backend"}
exit status 1
```

The validation error includes a URL to the migration runbook. The chart
contains the corresponding values changes so chart-managed deployments
upgrade cleanly; bare-metal / docker deployments need an operator step
to update env vars.

### Local development

Removing memory backend means `make run-local` needs a real Postgres +
Valkey. Two paths:

1. **Docker Compose stub** (recommended). Ship a
   `docker-compose.dev.yaml` in the repo root with Postgres + Valkey
   containers wired with sensible defaults. A new `make dev-services`
   target brings them up; `make run-local` depends on it.
2. **Use testcontainers via `mise tasks`.** Add `mise run dev-up` /
   `mise run dev-down` shortcuts that spin the same containers via the
   existing testcontainers tooling. Operators already use `mise` for
   tool management; less mental overhead than docker compose.

Open question — see below.

## API / Interface Changes

### CLI / env vars

- `STORE_BACKEND` — required; only valid value is `postgres`.
- `QUEUE_BACKEND` — required; only valid value is `valkey`.
- `SCHEDULER_BACKEND` — required; only valid value is `valkey`.
- `STORE_DSN`, `QUEUE_DSN`, `SCHEDULER_DSN` — required when the
  corresponding backend is set. Empty string → validation error at startup.

No CLI flag changes. No HCL `guardian {}` block changes.

### Chart values

Breaking: chart-major bump because defaults change and unset values fail
startup. Pre-deprecation operators who didn't touch backend values
silently got memory mode; post-deprecation they get baked Postgres +
Valkey or — if they opt out via `*.enabled: false` — a startup error
demanding explicit external DSNs.

```yaml
# Pre-DESIGN-0018 defaults (current):
store:
  backend: memory
queue:
  backend: memory
scheduler:
  backend: ticker

# Post-DESIGN-0018 defaults:
store:
  backend: postgres
  postgres:
    mode: baked  # operator can override to cnpg or external
queue:
  backend: valkey
  valkey:
    mode: baked  # operator can override to external
scheduler:
  backend: valkey
```

### Go interfaces

`store.Store`, `queue.Queue`, `scheduler.Scheduler` interfaces are
unchanged. Only the implementations go away.

## Data Model

No schema changes. The Postgres `repo_state` table is unchanged. The
Valkey key layout (per DESIGN-0015) is unchanged.

## Testing Strategy

### Removed

- `internal/store/memory/memory_test.go` — 196 LOC, 11 cases.
- `internal/queue/memory/memory_test.go` — 274 LOC, ~10 cases.
- `internal/scheduler/ticker/ticker_test.go` — 142 LOC, 7 cases.
- Ticker arm of `internal/scheduler/contract_test.go` (~22 LOC).
- Helm-unittest case "memory shape renders only the Deployment, no
  backing services" in `tests/backend_shapes_test.yaml`.

### Rewritten

Three helm-unittest cases in `tests/backend_shapes_test.yaml` mixed
postgres + memory queue or memory + memory shapes. They need rewriting
to require valkey queue:

- "postgres backend injects STORE_DSN env var…" (lines 145-150)
- "cnpg mode points STORE_DSN…" (lines 168-183)
- "termination grace period…" (lines 185-194)

### Kept (unchanged)

- `internal/store/postgres/postgres_integration_test.go` —
  testcontainers Postgres, unchanged.
- `internal/queue/valkey/valkey_integration_test.go` — testcontainers
  Valkey, unchanged.
- `internal/scheduler/valkey/valkey_integration_test.go` —
  testcontainers Valkey, unchanged.

### New

- Config validation tests: assert that unset / `memory` / `ticker`
  values produce a friendly error at startup with the migration URL.
- Helm-unittest case for "chart with no backend values set produces
  baked CNPG + baked Valkey resources" (codifying the new default).
- Helm-unittest case for "chart with backend explicitly set to memory
  surfaces a chart-level lint failure" (NOTES.txt or schema-level — see
  open question).

## Migration / Rollout Plan

**Single phase (Phase 0 absorbed into Phase 1 per resolved
[OQ (a)](#open-questions)).** IMPL-0016 ships first as part of the 1.0.0
release cycle, removing the memory backend entirely in one cut. The
1.0.0-rc.N manual-tagging workflow gives the homelab operator validation
checkpoints; the final 1.0.0 cut is the stable break.

### Single removal phase

- **Chart-major bump** (e.g., `0.7.x` → `1.0.0`). The 1.0.0 release is
  the meaningful operator-facing cut: "we commit to Postgres + Valkey
  as the production path."
- **Manual RC tagging** during validation:
  - `1.0.0-rc.1` — IMPL-0016 work done (memory backend removed, chart
    defaults flipped, schema validation added, docker-compose.dev.yaml
    shipped).
  - `1.0.0-rc.N` — subsequent IMPL-0015 phase completions (state
    writeback, Discoverer, BudgetTracker).
  - `1.0.0` — stable cut when all phases of both IMPLs validated.
- Delete the three backend packages
  (`internal/store/memory/`, `internal/queue/memory/`,
  `internal/scheduler/ticker/`) and reshape
  `cmd/repo-guardian/main.go` per the [audit
  summary](#audit-summary).
- Tighten `internal/config/` validation; emit hard-fail with a
  migration URL pointing at the
  `docs/operations/migrations.md#removing-memory-backend` runbook.
- Flip chart defaults to `store.backend=postgres` /
  `queue.backend=valkey` / `scheduler.backend=valkey`. Add baked
  `mode=baked` for both Postgres and Valkey.
- Add `values.schema.json` rejection of `memory` / `ticker` values so
  operators see the error at `helm install` time, not pod
  CrashLoopBackoff time.
- Delete and rewrite the helm-unittest cases per the audit.
- Documentation sweep — see the audit's documentation list (README,
  CLAUDE.md, operations docs, examples).
- Add `docker-compose.dev.yaml` at the repo root + `make
  dev-services` / `make dev-stop` targets so `make run-local` works
  without memory backend.

### Rollback

No in-binary fallback path. Operators encountering issues with the
1.0.0 release pin to the latest 0.7.x chart version while they
migrate. The `docs/operations/migrations.md#removing-memory-backend`
runbook ships alongside 1.0.0-rc.1 with the env var diff, chart values
diff, and migration recipe.

There is no deprecation window — see resolved [OQ (a)](#open-questions).
The only known repo-guardian deployment runs Postgres + Valkey today;
the deprecation warning would have had no audience.

## Open Questions

**(a)** ✅ **Resolved (revised 2026-06-23).** Deprecation window length —
how long do operators get between the warning (Phase 0) and the hard-fail
(Phase 1)?

- (a) One chart release cycle.
- (b) Two chart release cycles.
- **(c) = Skip Phase 0 entirely — rip it out in IMPL-0016 (chosen).**
  There are no known external operators (only the homelab Postgres+Valkey
  deployment), so the deprecation window has no audience. Combined with
  the (g) reversal, this collapses IMPL-0016 to a single removal phase
  that ships as part of the 1.0.0 release. Originally chose (a); revised
  during IMPL planning when the dual-doc-update tax outweighed the
  theatrical deprecation window benefit.
- other:

**(b)** ✅ **Resolved.** Default `store.postgres.mode` after the flip.

- **(a) = `baked` (chosen).** Out-of-the-box install on a fresh
  cluster spins a single Postgres pod managed by the chart. Matches the
  current homelab-smoke runbook.
- (b) `cnpg`. Assumes operators have CNPG installed; nicer multi-replica
  story but adds an external dependency to "just install."
- (c) `external`. Forces operators to bring their own Postgres. Most
  conservative; matches AWS-style deployments.
- (d) No default — fail startup until operator picks. Most explicit but
  hurts the "kick the tires" experience.
- other:

**(c)** ✅ **Resolved.** Default `queue.valkey.mode` after the flip.

- **(a) = `baked` (chosen).** Symmetric with (b); chart-managed
  Valkey pod.
- (b) `external`. Forces operators to bring their own Valkey.
- other:

**(d)** ✅ **Resolved.** Local development experience — how do `make
run-local` / `make test` work without memory backend?

- **(a) = Docker Compose stub committed to the repo (chosen).** Ship
  a `docker-compose.dev.yaml` with Postgres + Valkey. Add `make
  dev-services` / `make dev-stop` targets. `make run-local` depends on
  `dev-services`.
- (b) Use the existing testcontainers tooling. Add `mise run dev-up` /
  `dev-down` shortcuts. Operators already use mise for tool management.
- (c) Document "spin a kind cluster + helm install" as the local-dev
  path. Heavier setup but matches production exactly.
- other:

**(e)** ✅ **Resolved.** Chart deprecation surface — where does the Phase 0
warning live?

- **(a) = Both NOTES.txt and a binary-startup slog.Warn (chosen).**
  Operators see it both at `helm install` time and at pod startup. Belt
  and suspenders.
- (b) NOTES.txt only. Operators reading helm output catch it; pod logs
  stay quiet.
- (c) slog.Warn only. Pod logs surface it; helm install output stays
  quiet.
- other:

**(f)** ✅ **Resolved.** Schema-level lint for deprecated values.

- **(a) = JSON schema validation in `values.schema.json` (chosen).**
  Reject memory/ticker values at `helm install` / `helm upgrade` time
  before the pod starts. Operators see the error in `helm install`
  output, not in pod CrashLoopBackoff.
- (b) Don't add schema validation; rely on the binary startup error to
  surface the problem.
- other:

**(g)** ✅ **Resolved (revised 2026-06-23).** Sequencing with IMPL-0015
(DESIGN-0017).

- (a) Independent sequencing.
- **(b) = Land DESIGN-0018 first; IMPL-0015 then doesn't need to bother
  with the no-op memory paths (chosen).** IMPL-0016 ships first as part
  of the 1.0.0-rc.N cycle, removing the memory backend entirely.
  IMPL-0015 then implements its phases on a clean Postgres+Valkey-only
  codebase — Phase 0 no longer needs to articulate "and what does memory
  backend do here?" for every write site; tests are single-backend;
  docs don't get updated twice. Both IMPLs bundle into the 1.0.0
  release, validated via manually-tagged 1.0.0-rc.N versions in homelab.
  Originally chose (a); revised during IMPL planning when the cleaner
  break and earlier issue surfacing outweighed the smaller-PR argument
  for independent sequencing.
- other:

**(h)** ✅ **Resolved.** Should we delete `docker-compose.dev.yaml` after
testcontainers matures, or keep it as the canonical local-dev shape?

- **(a) = Keep `docker-compose.dev.yaml` indefinitely (chosen).**
  testcontainers requires Go test wrapper code. Operators iterating on
  the binary outside `make test` benefit from a long-lived compose file
  they can leave running.
- (b) Delete after one release cycle once testcontainers wrapping is
  smooth. Reduces surface but kills a useful escape hatch.
- other:

## References

- [DESIGN-0012: Persistent reconcile state and multi-replica
  coordination](0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — introduced the multi-backend pattern this design retires.
- [DESIGN-0017: Stale-sweep cutover and repository discovery](0017-stale-sweep-cutover-and-repository-discovery.md)
  — surfaced this design via question (e); pre-implementation audit
  motivated the cleanup.
- `docs/operations/aws.md` — operator-facing AWS deployment guide;
  already assumes managed Postgres + Valkey equivalents.
- `docs/operations/scaling.md` — multi-replica scaling guide; already
  assumes durable backends.
- `charts/repo-guardian/docs/homelab-smoke.md` — first-install runbook;
  already walks operators through Postgres + Valkey path.
- `internal/store/memory/` — the package this design removes.
- `internal/queue/memory/` — same.
- `internal/scheduler/ticker/` — same.
