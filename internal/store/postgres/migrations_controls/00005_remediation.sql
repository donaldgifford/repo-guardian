-- IMPL-0030 (DESIGN-0032 § Data model): additive changes to the schema
-- IMPL-0029 shipped in 00003, so an rc database migrates in place
-- (IMPL-0030 OQ1 kept 00003 as the GA schema; drift is fixed here,
-- never by editing an applied file).

-- +goose Up
-- remediation_maintenance withdraws on a remediate → evaluate downgrade
-- too: an assignment whose effective mode is evaluate (the policy asked
-- for it, or the Remediation App lost access) may no longer remediate,
-- so its open PR or recommendation is withdrawn like an exclusion
-- (IMPL-0030 task 1.3, DESIGN-0032 Testing Strategy "switch it to
-- evaluate"). 00003's definition only covered a missing assignment and
-- an unmanaged repository.
CREATE OR REPLACE VIEW remediation_maintenance AS
SELECT m.* FROM (
    SELECT r.id AS remediation_id, r.repository_id, r.control_id, r.kind, r.state,
           CASE
             WHEN r.state = 'reserved' AND r.reserved_until < now()                                 THEN 'expire'
             WHEN ca.repository_id IS NULL OR ps.state <> 'managed'
               OR ca.effective_mode = 'evaluate'                                                    THEN 'withdraw'
             WHEN r.state = 'recommended' AND cr.intent_revision IS DISTINCT FROM r.intent_revision THEN 'supersede'
             WHEN cr.status = 'compliant'                                                           THEN 'close_compliant'
           END AS action
    FROM remediations r
    LEFT JOIN control_assignments ca
           ON (ca.repository_id, ca.control_id) = (r.repository_id, r.control_id) AND ca.state = 'active'
    LEFT JOIN repository_policy_state ps ON ps.repository_id = r.repository_id
    LEFT JOIN control_results cr
           ON (cr.repository_id, cr.control_id) = (r.repository_id, r.control_id)
    WHERE r.state IN ('reserved', 'open', 'recommended')
) m
WHERE m.action IS NOT NULL;

-- +goose Down
CREATE OR REPLACE VIEW remediation_maintenance AS
SELECT m.* FROM (
    SELECT r.id AS remediation_id, r.repository_id, r.control_id, r.kind, r.state,
           CASE
             WHEN r.state = 'reserved' AND r.reserved_until < now()                                 THEN 'expire'
             WHEN ca.repository_id IS NULL OR ps.state <> 'managed'                                 THEN 'withdraw'
             WHEN r.state = 'recommended' AND cr.intent_revision IS DISTINCT FROM r.intent_revision THEN 'supersede'
             WHEN cr.status = 'compliant'                                                           THEN 'close_compliant'
           END AS action
    FROM remediations r
    LEFT JOIN control_assignments ca
           ON (ca.repository_id, ca.control_id) = (r.repository_id, r.control_id) AND ca.state = 'active'
    LEFT JOIN repository_policy_state ps ON ps.repository_id = r.repository_id
    LEFT JOIN control_results cr
           ON (cr.repository_id, cr.control_id) = (r.repository_id, r.control_id)
    WHERE r.state IN ('reserved', 'open', 'recommended')
) m
WHERE m.action IS NOT NULL;
