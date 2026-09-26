-- name: InsertServiceRun :exec
INSERT INTO service_runs (kind, outcome, started_at, finished_at, detail)
VALUES ($1, $2, $3, $4, $5);
