# v2 migrations (goose)

Schema migrations for repo-guardian v2 (DESIGN-0025, IMPL-0025 Phase 2+).
They are embedded by `internal/store/postgres/goose.go` and applied by
`repo-guardian migrate`, never at pod startup.

- SQL migrations live here as `NNNNN_name.sql` with `-- +goose Up` /
  `-- +goose Down` sections. sqlc reads this directory as its schema.
- Go migrations (adoption, backfill) are registered in code via
  `goose.NewGoMigration` and have no file here.
- The version table is `goose_db_version`. v1's golang-migrate files in
  `../migrations/` stay untouched and are never read by goose.

## Grants

Migrations grant to the migrating role (the application role) and, when
it exists, `repoguardian_ro`. They never `CREATE ROLE`. `finding_events`
is append-only by grant, which binds only a non-superuser: run
migrations as the application role, never as a superuser, or the
revoke is a no-op.

## The v1 backfill (00003)

`00003_backfill_v1` (`backfill_v1.go`) is a Go migration that runs only
when `00001` adopted a v1 database, and only once (`v2_meta` key
`backfilled_v1_at`). It copies `repo_state`, `rule_state` and
`compliance_snapshot` into the v2 tables in one transaction and never
writes a v1 table, so a v1 image still runs against the database
(`rollback_integration_test.go`, which uses the verbatim v1 queries in
`pgtest/v1sql`).

- `--freshness` (default `$RECONCILE_FRESHNESS`, else 24h) reaches the
  migration through the context (`WithBackfillFreshness`) and seeds
  `next_due_at`; never-checked repositories are spread evenly over one
  window in a seeded order, and the seed is logged.
- `migrate --dry-run` (`DryRun`) replays whatever of 00001–00003 is
  pending in one transaction, prints the counts, case collisions,
  multi-owner installations and park-reason histogram, then rolls back.
  Its 00002 replay strips the goose annotations from the embedded file,
  so a new SQL migration before 00003 must be added to `DryRun` too.
