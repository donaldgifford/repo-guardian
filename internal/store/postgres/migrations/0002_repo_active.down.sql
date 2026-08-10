DROP INDEX IF EXISTS idx_repo_state_active_freshness;

ALTER TABLE repo_state
    DROP COLUMN active;
