package postgres

// MigrateDown exposes the unexported migrateDown to the external
// postgres_test package. Reversing migrations is a test-only
// capability by design — the documented operator rollback is manual
// psql (docs/operations/migrations.md) — so it stays off the shipped
// API surface and lives here, where the compiler only builds it into
// the test binary.
var MigrateDown = migrateDown
