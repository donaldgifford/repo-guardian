---
id: IMPL-0016
title: "Deprecate memory backend"
status: Draft
author: Donald Gifford
created: 2026-06-23
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0016: Deprecate memory backend

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-06-23

<!--toc:start-->
- [Objective](#objective)
- [Sequencing](#sequencing)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Removal](#phase-1-removal)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement [DESIGN-0018: Deprecate memory
backend](../design/0018-deprecate-memory-backend.md). Remove the in-memory
`store` / `queue` / `scheduler` backends in a single removal release. Net
~1,600 LOC removed from the codebase plus a simpler test matrix and
tighter startup validation.

**Implements:** DESIGN-0018 (Approved 2026-06-23)

Surfaced during the DESIGN-0017 pre-implementation review (question (e))
— the maintenance friction of keeping the dual-backend code path drove
the user to propose memory removal. This IMPL retires
`internal/store/memory/`, `internal/queue/memory/`,
`internal/scheduler/ticker/`, the `main.go` backend-dispatch branches,
and the helm-unittest cases that exercise memory shapes.

## Sequencing

**IMPL-0016 ships first**, before IMPL-0015. Per DESIGN-0018 resolved
[OQ (g)](../design/0018-deprecate-memory-backend.md#open-questions), the
memory backend is removed before IMPL-0015's state-management work
starts. This avoids carrying "what does memory backend do here?" branches
through every IMPL-0015 task; IMPL-0015 lands on a clean Postgres+Valkey
codebase.

The deprecation-warning Phase 0 originally proposed here was dropped per
DESIGN-0018 resolved [OQ
(a)](../design/0018-deprecate-memory-backend.md#open-questions) — there
are no known external operators (only the homelab Postgres+Valkey
deployment), so the deprecation window had no audience. No
`slog.Warn`, no `NOTES.txt` banner, no chart-minor bump for warning. The
removal lands directly as **single Phase 1**, tagged `1.0.0-rc.1`.

Both IMPLs bundle into the **1.0.0 release**, validated via
manually-tagged `1.0.0-rc.N` versions:

| RC | What landed |
|---|---|
| `1.0.0-rc.1` | **This IMPL** — memory backend removed; chart defaults flipped; schema validation added; `docker-compose.dev.yaml` shipped |
| `1.0.0-rc.2` | IMPL-0015 Phase 0 (state writeback prerequisites) |
| `1.0.0-rc.3+` | IMPL-0015 Phase 1 (Discoverer + BudgetTracker) |
| `1.0.0` | Stable cut when all phases validated in homelab |

Binary `appVersion` stays on the 1.x line (1.8.1 → 1.9.0). Chart version
moves to 1.0.0 to signal the meaningful operator-facing release. Binary
v2.0.0 was deferred to avoid Go module `/v2` path migration; revisit if
we ever expose a public library.

## Scope

### In Scope

- Delete the three backend packages
  (`internal/store/memory/`, `internal/queue/memory/`,
  `internal/scheduler/ticker/`).
- Simplify `cmd/repo-guardian/main.go` backend-dispatch helpers
  (`newStore`, `newQueue`, `newScheduler`) into straight-line code.
- Tighten `internal/config/` validation; `STORE_BACKEND=memory` (or
  any deprecated value) hard-fails at startup with a migration URL.
- Flip chart defaults to Postgres + Valkey; add baked `mode=baked`
  for both Postgres and Valkey.
- Add `values.schema.json` schema-level rejection of memory/ticker
  values so operators see the error at `helm install` time, not pod
  CrashLoopBackoff time.
- New `docker-compose.dev.yaml` at repo root + `make dev-services` /
  `make dev-stop` targets so `make run-local` continues to work
  without memory backend.
- Documentation sweep: README, CLAUDE.md, operations docs, chart
  README, examples — remove memory-mode references.
- Migration runbook at
  `docs/operations/migrations.md#removing-memory-backend` shipped
  with the removal so operators upgrading from 0.7.x have a path.
- Chart version flip to 1.0.0 (via 1.0.0-rc.N validation); binary
  `appVersion` bump to 1.9.0.
- Tighten the `tests/backend_shapes_test.yaml` helm-unittest matrix.

### Out of Scope

- Deprecation warning. No `slog.Warn`, no `NOTES.txt` banner. No
  audience for the warning — see [Sequencing](#sequencing) and
  DESIGN-0018 resolved
  [OQ (a)](../design/0018-deprecate-memory-backend.md#open-questions).
- The state-management work covered by IMPL-0015. IMPL-0015 lands
  in subsequent `1.0.0-rc.N` tags after this IMPL ships as
  `1.0.0-rc.1`. See [Sequencing](#sequencing).
- Removing testcontainers-based integration tests. They continue to
  spin up real Postgres / Valkey containers in Docker.
- Adding a new in-process test fake to replace the memory backend.
  Tests use mockery-generated mocks per DESIGN-0012 conventions; this
  IMPL doesn't build a "memory backend for tests only" replacement.
- Supporting more backends (Redis, etcd, in-cluster sqlite, etc.).
  Postgres + Valkey are the production targets.
- Touching `docs/legacy/` — that tree was retired in IMPL-0014.

## Implementation Phases

Single phase. The previous IMPL plan had a Phase 0 deprecation warning,
dropped during planning per DESIGN-0018 resolved
[OQ (a)](../design/0018-deprecate-memory-backend.md#open-questions). See
[Sequencing](#sequencing) for the full release narrative.

---

### Phase 1: Removal

Delete the three backend packages, simplify the dispatch helpers,
tighten config validation, flip chart defaults, and add
`docker-compose.dev.yaml` for local development. Chart-major bump.

#### Tasks

**1.1 — `docker-compose.dev.yaml` + make targets**

- [x] Create `docker-compose.dev.yaml` at the repo root (see Open
  Question 8 for placement). Services: Postgres + Valkey with
  sensible defaults matching `make test` env vars.
- [x] Add `make dev-services` target that runs `docker compose -f
  docker-compose.dev.yaml up -d`.
- [x] Add `make dev-stop` target that runs `docker compose -f
  docker-compose.dev.yaml down`.
- [x] Update `make run-local` to depend on `dev-services` so
  newcomers can `make run-local` and have services come up
  automatically.
- [x] Update README's dev-setup section to document the new
  workflow.
- [ ] Manually validate the compose flow end-to-end on a clean
  checkout: `make dev-services && make run-local`. (operator-side)

**1.2 — Delete `internal/store/memory/`**

- [x] Delete the directory entirely (329 LOC: `memory.go` 133 LOC +
  `memory_test.go` 196 LOC).
- [x] Remove the import from `cmd/repo-guardian/main.go` (line 30
  per the audit).
- [x] Verify no other files import `internal/store/memory` via
  `grep -r "internal/store/memory"`.
- [x] `go build ./...` succeeds.

**1.3 — Delete `internal/queue/memory/`**

- [x] Delete the directory (387 LOC: `memory.go` 113 + `memory_test.go`
  274).
- [x] Remove the import from `main.go` (line 25 per audit).
- [x] Verify no other importers.
- [x] `go build ./...` succeeds.

**1.4 — Delete `internal/scheduler/ticker/`**

- [ ] Delete the directory (251 LOC: `ticker.go` 109 + `ticker_test.go`
  142).
- [ ] Remove the import from `main.go` (line 33 per audit).
- [ ] Verify no other importers.
- [ ] `go build ./...` succeeds.

**1.5 — Delete ticker arm of scheduler contract test**

- [x] Remove the `ticker.New()`-based test factory from
  `internal/scheduler/contract_test.go` (line 94 per audit, ~22
  LOC). The whole file was deleted — the `runSchedulerContract`
  helper was unused once the ticker arm was gone.
- [x] The valkey-arm in
  `internal/scheduler/valkey/valkey_integration_test.go` is now the
  single source of truth for contract compliance.

**1.6 — Simplify `cmd/repo-guardian/main.go` dispatch helpers**

- [ ] `newStore()` (lines 354-368): delete the memory branch (lines
  355-358). Function becomes straight-line Postgres-only.
- [ ] `newQueue()` (lines 282-314): delete the memory branch (lines
  283-287). Function becomes straight-line Valkey-only.
- [ ] `newScheduler()` (lines 320-337): delete the ticker branch
  (lines 321-325). Function becomes straight-line Valkey-only.
- [ ] Verify `funlen` lint doesn't trip on `main()` after the
  simplifications (the helpers shrink; `main()` may also need
  recheck).
- [ ] `make lint && make test` pass.

**1.7 — Tighten `internal/config/` validation**

- [ ] Delete the constants `StoreBackendMemory`, `QueueBackendMemory`,
  `SchedulerBackendTicker` from `internal/config/config.go` (lines
  137-143 per audit).
- [ ] `loadBackendConfig()` (lines 247-249): no longer sets memory /
  ticker defaults. Env vars become required; empty / unset is now
  a validation error.
- [ ] `validateBackends()` (lines 327-355): drop the memory /
  ticker switch cases. Error messages tighten:
  - `STORE_BACKEND` must be `postgres`.
  - `QUEUE_BACKEND` must be `valkey`.
  - `SCHEDULER_BACKEND` must be `valkey`.
- [ ] Error messages include the migration URL.
- [ ] `STORE_DSN` becomes required when `STORE_BACKEND=postgres`;
  same for the other two backends. Validate at startup.
- [ ] Update / add unit tests covering:
  - Unset backend env var → friendly startup error
  - `STORE_BACKEND=memory` → friendly startup error with URL
  - `STORE_BACKEND=postgres` + missing `STORE_DSN` → friendly
    startup error
  - All-valid configuration → no error

**1.8 — Flip chart defaults**

- [ ] `charts/repo-guardian/values.yaml`:
  - Line 217: `store.backend: memory` → `store.backend: postgres`.
  - Add `store.postgres.mode: baked` as the default mode.
  - Line 294: `queue.backend: memory` → `queue.backend: valkey`.
  - Add `queue.valkey.mode: baked` as the default mode.
  - Line 328: `scheduler.backend: ticker` → `scheduler.backend: valkey`.
- [ ] Regenerate chart README via `make helm-docs`.
- [ ] Verify the rendered Deployment template populates the env
  vars from the new defaults.
- [ ] Confirm baked Postgres + baked Valkey resources render
  out-of-the-box (`helm template charts/repo-guardian` with no
  values overrides).

**1.9 — JSON schema validation**

- [ ] Confirm whether `charts/repo-guardian/values.schema.json`
  already exists (see Open Question 4). If absent, create it with
  a full schema; if present, extend the relevant sections.
- [ ] Reject `memory` and `ticker` values in the schema:
  - `store.backend`: enum `["postgres"]`
  - `queue.backend`: enum `["valkey"]`
  - `scheduler.backend`: enum `["valkey"]`
- [ ] Add a helm-unittest case asserting that `helm install` with
  `--set store.backend=memory` produces a schema error before pod
  startup.
- [ ] Document the schema validation in chart README.

**1.10 — Helm-unittest rewrites**

- [ ] Delete the test case `memory shape renders only the
  Deployment, no backing services` in
  `tests/backend_shapes_test.yaml` (lines 20-43 per audit).
- [ ] Rewrite the `postgres backend injects STORE_DSN env var...`
  case (lines 145-150) to require valkey queue.
- [ ] Rewrite the `cnpg mode points STORE_DSN...` case (lines
  168-183) to require valkey queue.
- [ ] Rewrite the `termination grace period...` case (lines
  185-194) to require postgres + valkey.
- [ ] Add new case: `chart with no backend values set produces
  baked CNPG + baked Valkey resources` — codifies the new default.
- [ ] Add new case: `chart with memory value fails schema
  validation`.

**1.11 — Documentation sweep**

- [ ] `README.md`: remove all memory-mode references; ensure
  Quickstart points at postgres+valkey path.
- [ ] `CLAUDE.md`: update the Architecture section's backend list;
  remove memory backend mention; replace the Phase 0 deprecation
  note with "memory backend removed in IMPL-0016".
- [ ] `docs/operations/scaling.md`: remove memory mode from any
  scaling matrix; confirm single-backend story.
- [ ] `docs/operations/aws.md`: verify no memory-mode references
  (already managed but double-check).
- [ ] `docs/operations/chart-0.5.0-migration.md`: add a note that
  memory backend was the IMPL-0011 migration target; now removed
  in IMPL-0016 (chart 1.0).
- [ ] `docs/operations/cnpg-homelab-cutover.md`: verify references
  are clean.
- [ ] `charts/repo-guardian/README.md.gotmpl`: remove memory mode
  from values description; document the new default.
- [ ] `examples/`: update any example HCL or scripts that mention
  memory backend (likely empty per the audit, but check).
- [ ] `docs/operations/migrations.md#removing-memory-backend`:
  update to reflect that Phase 1 has shipped; remove the rollback
  advice now that no in-binary fallback exists.

**1.12 — Chart-major version bump**

- [ ] Bump chart `version` to major (e.g., 0.x.y → 1.0.0).
  Rationale: defaults change + validation tightens +
  values.schema.json rejects previously-valid configurations =
  breaking change for any operator who had memory/ticker
  explicitly set.
- [ ] Bump `appVersion` for the binary changes.
- [ ] Update CHANGELOG entries (root + chart) with a
  breaking-change callout.

**1.13 — CHANGELOG breaking-change callouts**

- [ ] Root `CHANGELOG.md`: `Removed` section entry; reference
  DESIGN-0018 + IMPL-0016.
- [ ] Chart `CHANGELOG.md`: `Breaking changes` section entry with
  the values.yaml diff and the env var diff. (git-cliff regenerates
  on publish; the manual entry seeds the section.)

#### Success Criteria

- All Phase 1 tasks checked off.
- `make ci` passes.
- Net LOC reduction is in the ~1,600 LOC ballpark per the audit
  (967 LOC packages + 634 LOC tests + ~17 LOC main.go + ~6 LOC
  config constants + ~100 LOC helm-unittest matrix, minus ~100 LOC
  additions for `docker-compose.dev.yaml` + schema + new test cases).
- `STORE_BACKEND=memory` (or any deprecated value) on Phase 1
  binary → startup error with migration URL; binary refuses to
  start.
- `helm install` with `--set store.backend=memory` → schema error
  before pod creation.
- A fresh `helm install` (no values overrides) on Postgres + Valkey
  produces a working deployment with baked Postgres + baked Valkey
  resources.
- `make dev-services && make run-local` works on a fresh checkout
  without manual setup.
- Chart-major version bumped; CHANGELOG entries published.
- Documentation no longer references memory mode anywhere outside
  historical context (CHANGELOG, design docs, migration runbook).

---

## File Changes

All changes ship in Phase 1 (the single removal phase). See
[Sequencing](#sequencing) for the chart-version progression.

| File | Action | Description |
|------|--------|-------------|
| `docker-compose.dev.yaml` | Create | Postgres + Valkey local-dev stack |
| `Makefile` | Modify | Add `dev-services` / `dev-stop`; update `run-local` |
| `internal/store/memory/` | Delete | 329 LOC (package + tests) |
| `internal/queue/memory/` | Delete | 387 LOC |
| `internal/scheduler/ticker/` | Delete | 251 LOC |
| `internal/scheduler/contract_test.go` | Modify | Drop ticker arm (~22 LOC) |
| `cmd/repo-guardian/main.go` | Modify | Simplify `newStore` / `newQueue` / `newScheduler` to straight-line code; remove memory/ticker imports |
| `internal/config/config.go` | Modify | Delete `StoreBackendMemory` / `QueueBackendMemory` / `SchedulerBackendTicker` constants; tighten validation; emit migration-URL errors |
| `internal/config/config_test.go` | Modify | Add deprecated-value-error tests |
| `charts/repo-guardian/values.yaml` | Modify | Flip defaults to postgres + valkey + baked modes |
| `charts/repo-guardian/values.schema.json` | Create or Modify | Reject memory/ticker values (see OQ 4) |
| `charts/repo-guardian/Chart.yaml` | Modify | Bump `version` to 1.0.0-rc.1 (then through rc.N to 1.0.0); `appVersion` to 1.9.0 |
| `charts/repo-guardian/tests/backend_shapes_test.yaml` | Modify | Delete memory-shape test case; rewrite three mixed-backend cases; add baked-defaults + schema-rejection cases |
| `charts/repo-guardian/README.md.gotmpl` | Modify | Remove memory-mode docs; document new defaults |
| `docs/operations/migrations.md` | Modify | New `Removing memory backend` section as upgrade guide |
| `docs/operations/scaling.md` | Modify | Remove memory mode; single-backend story |
| `docs/operations/aws.md` | Modify | Verify no memory refs (already managed but double-check) |
| `docs/operations/chart-0.5.0-migration.md` | Modify | Note 1.0.0 removal supersedes the IMPL-0011 multi-backend doc |
| `docs/operations/cnpg-homelab-cutover.md` | Modify | Verify references are clean |
| `README.md` | Modify | Remove memory-mode references; quickstart points at postgres+valkey path |
| `CLAUDE.md` | Modify | Update Architecture section; remove memory backend mentions |
| `examples/` | Modify | Update any memory-backend refs (likely none per audit) |
| `CHANGELOG.md` (root) | Modify | `Removed` section entry referencing DESIGN-0018 + IMPL-0016 |
| `charts/repo-guardian/CHANGELOG.md` | Modify | Breaking-changes entry with values.yaml + env var diff |

## Testing Plan

- [ ] Unit tests for config validation: each deprecated backend value
  path returns a friendly error with the migration URL.
- [ ] Helm-unittest cases for the default flip: fresh `helm template`
  produces baked Postgres + baked Valkey resources; setting
  memory/ticker via `--set` fails schema validation.
- [ ] Manual test of `make dev-services && make run-local` on a
  fresh repo checkout (no pre-existing services).
- [ ] CI runs `go build ./...` to confirm no orphaned imports after
  package deletes.
- [ ] No coverage regression — postgres + valkey backends are
  already covered by integration tests via testcontainers.
- [ ] End-to-end validation in homelab after the `1.0.0-rc.1` tag
  deploys: `helm install` with old (memory) values fails at schema
  validation; fresh install works out of the box; binary starts
  with no warnings on a Postgres+Valkey-only deployment.

## Dependencies

- **This IMPL ships FIRST**, before IMPL-0015. Per DESIGN-0018
  resolved [OQ (g)](../design/0018-deprecate-memory-backend.md#open-questions)
  (revised 2026-06-23), the memory backend is removed before IMPL-0015's
  state-management work begins. IMPL-0015 then implements its phases on
  a clean Postgres+Valkey-only codebase. See
  [Sequencing](#sequencing).
- DESIGN-0018 (Approved 2026-06-23 in PR #128).
- DESIGN-0017 (sibling design; surfaced this IMPL via question (e)).
- The pre-implementation audit findings (referenced in DESIGN-0018
  Background).
- No dependency on a prior deprecation phase — Phase 0 was dropped
  per DESIGN-0018 resolved
  [OQ (a)](../design/0018-deprecate-memory-backend.md#open-questions).

## Open Questions

**1.** ⛔ **Superseded.** Sequencing IMPL-0016 Phase 0 chart bump with
IMPL-0015's chart bumps.

Phase 0 was dropped entirely per DESIGN-0018 resolved
[OQ (a)](../design/0018-deprecate-memory-backend.md#open-questions). The
chart progression is now 0.7.x → 1.0.0-rc.1 (this IMPL) → 1.0.0-rc.N
(IMPL-0015 phases) → 1.0.0 stable. See [Sequencing](#sequencing).

**2.** ⛔ **Superseded.** Where does the deprecation `slog.Warn` live in
`main.go`?

No deprecation warning ships. Phase 0 dropped per DESIGN-0018 resolved
[OQ (a)](../design/0018-deprecate-memory-backend.md#open-questions).

**3.** ✅ **Resolved.** Migration runbook location — new section in
`docs/operations/migrations.md` or a new file?

- **(a) = New section in existing `docs/operations/migrations.md`
  (chosen).** Operators have one place to look for migration content;
  the file is already in nav. Single anchor URL for the
  configuration-error messages.

**4.** ✅ **Resolved.** `charts/repo-guardian/values.schema.json`
existence — does it exist today?

- **(a) = Verify first; create or extend as needed (chosen).** First
  step of Task 1.9: `ls charts/repo-guardian/values.schema.json`. If
  absent, author a full schema; if present, extend the
  `store.backend` / `queue.backend` / `scheduler.backend` enum
  fields. Schema validation at `helm install` time is the friendlier
  error path for operators.

**5.** ✅ **Resolved.** Order of operations within Phase 1 — package
deletes first or chart changes first?

- **(a) = Package deletes first → main.go → config → chart →
  schema (chosen).** Lockstep "code is correct first, then chart"
  ordering. Reduces risk of a chart upgrade landing on operators
  before the binary refuses memory values. Task sequence (1.2 →
  1.7 → 1.8 → 1.9) already follows this order in the phase
  outline above.

**6.** ⛔ **Superseded.** Phase 0 PR strategy — single PR or split?

Phase 0 was dropped. Only Phase 1's PR strategy is relevant — see
question 7.

**7.** ✅ **Resolved.** Phase 1 PR strategy — Phase 1 is much larger.

- **(a) = Single PR (chosen).** Phase 1 is a cohesive change
  ("remove memory backend"); reviewer reads DESIGN-0018 once and
  sees the full removal in one diff. The single-PR shape matches
  the `1.0.0-rc.1` tag — a single squash-merge is the boundary
  between "memory backend exists" and "memory backend does not."
  Risk of a large diff is mitigated by the deletion-heavy LOC
  count (~1,600 LOC removed, only a few hundred added).

**8.** ✅ **Resolved.** `docker-compose.dev.yaml` placement — repo
root or sub-directory?

- **(a) = Repo root as `docker-compose.dev.yaml` (chosen).**
  Convention; CI tooling, IDEs (VS Code Docker extension, IntelliJ
  Docker plugin), and editors expect compose files at the root.
  `docker compose -f docker-compose.dev.yaml up -d` works from any
  shell with no path gymnastics. Easiest for newcomers cloning the
  repo for the first time.

## References

- [DESIGN-0018: Deprecate memory
  backend](../design/0018-deprecate-memory-backend.md) — the design
  this IMPL implements. Read first.
- [DESIGN-0017: Stale-sweep cutover and repository
  discovery](../design/0017-stale-sweep-cutover-and-repository-discovery.md)
  — sibling design that surfaced this work via question (e).
- [IMPL-0015: Stale-sweep cutover and repository
  discovery](0015-stale-sweep-cutover-and-repository-discovery.md) —
  ships **after** this IMPL on the path to 1.0.0. See
  [Sequencing](#sequencing).
- [DESIGN-0012: Persistent reconcile state and multi-replica
  coordination](../design/0012-persistent-reconcile-state-and-multi-replica-coordination.md)
  — introduced the multi-backend pattern this IMPL retires.
- DESIGN-0018 audit summary — references file:line citations for
  every code path this IMPL touches; reference during Phase 1
  execution.
- `docs/operations/aws.md` — operator-facing AWS deployment guide
  already assumes Postgres + Valkey (managed equivalents); ground
  truth for the post-removal architecture.
- `charts/repo-guardian/docs/homelab-smoke.md` — first-install
  runbook already walks operators through Postgres + Valkey path.
