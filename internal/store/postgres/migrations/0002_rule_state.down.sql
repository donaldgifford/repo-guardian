-- Reverse of 0002_rule_state.up.sql. Drops in dependency order:
-- the partial index before its table, and the repo_state column last
-- so a partially-applied up leaves nothing behind.

DROP INDEX IF EXISTS idx_rule_state_actionable;
DROP TABLE IF EXISTS compliance_snapshot;
DROP TABLE IF EXISTS rule_state;

ALTER TABLE repo_state DROP COLUMN IF EXISTS catalog_parse_ok;
