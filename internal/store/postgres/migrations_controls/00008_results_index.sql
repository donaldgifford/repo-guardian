-- IMPL-0030 7.14 (OQ18): /results filters control_results by control and
-- status and pages by last_changed_at. TestAPIPerf's EXPLAIN showed a
-- sequential scan over every result at fleet size; this index lets a
-- control or status filter and the default sort read it in order.

-- +goose Up
CREATE INDEX control_results_control_status_changed ON control_results (control_id, status, last_changed_at);

-- +goose Down
DROP INDEX control_results_control_status_changed;
