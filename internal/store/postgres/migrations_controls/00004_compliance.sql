-- DESIGN-0032 § Data model and DESIGN-0030 AR-0030-05, 00004_compliance
-- (IMPL-0029 4.3): compliance snapshots keyed by baseline lineage, with
-- every posture bucket beside the percentage.

-- +goose Up

-- The one percentage definition (DESIGN-0030 § Compliance queries):
-- num / den floored, never rounded, to one decimal, NULL on an empty
-- denominator. The compliance query, snapshots and coverage all call it,
-- and store.CompliantPercent is its Go mirror (an agreement test feeds
-- both the same counts).
-- +goose StatementBegin
CREATE FUNCTION floor_percent(num BIGINT, den BIGINT) RETURNS NUMERIC
LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE WHEN den = 0 THEN NULL ELSE floor(num * 1000.0 / den) / 10 END
$$;
-- +goose StatementEnd

CREATE TABLE compliance_snapshots (
    org                        TEXT NOT NULL,
    control_id                 TEXT NOT NULL,
    decision                   TEXT NOT NULL CHECK (decision IN ('baseline', 'replaced', 'org_added')),
    snapshot_at                TIMESTAMPTZ NOT NULL,
    compliant                  INT NOT NULL,
    non_compliant              INT NOT NULL,
    -- assigned, no result yet
    pending                    INT NOT NULL,
    -- a result from an older epoch or revision
    stale                      INT NOT NULL,
    unmeasurable_access_denied INT NOT NULL,
    unmeasurable_suspended     INT NOT NULL,
    unmeasurable_conflict      INT NOT NULL,
    not_applicable             INT NOT NULL,
    unknown                    INT NOT NULL,
    -- excluded assignments: reported beside, never in the denominator (D5)
    excluded                   INT NOT NULL,
    assigned                   INT NOT NULL,
    -- compliant / (compliant + non_compliant), floored to one decimal;
    -- NULL on an empty denominator, never 100.
    percent                    NUMERIC(4, 1),
    -- measured / assigned, the same rounding; NULL when nothing is assigned.
    coverage                   NUMERIC(4, 1),
    PRIMARY KEY (org, control_id, decision, snapshot_at)
);

CREATE INDEX compliance_snapshots_time ON compliance_snapshots (snapshot_at DESC);

GRANT SELECT ON compliance_snapshots TO rg_evaluator, rg_remediator;
GRANT INSERT ON compliance_snapshots TO rg_evaluator;

-- +goose Down
DROP TABLE IF EXISTS compliance_snapshots;
DROP FUNCTION IF EXISTS floor_percent(BIGINT, BIGINT);
