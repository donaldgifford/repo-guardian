-- IMPL-0030 6.9 (OQ19): RecordEvaluation stamps
-- repositories.last_checked_at, so a repository's last evaluation
-- survives the checks prune (6.8) and the API reads it without a join.
-- 00001 granted the evaluator no write on the column.

-- +goose Up
GRANT UPDATE (last_checked_at) ON repositories TO rg_evaluator;

-- +goose Down
REVOKE UPDATE (last_checked_at) ON repositories FROM rg_evaluator;
