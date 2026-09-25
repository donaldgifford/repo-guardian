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
