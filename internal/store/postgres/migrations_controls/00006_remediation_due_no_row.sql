-- IMPL-0030 Phase 4: with no remediation row, remediation_due compared
-- the result's intent revision against NULL, which is always distinct,
-- so a control whose run opened nothing (compliant on a fresh read, or a
-- change set with nothing in Fixed) stayed due after RecordRun
-- acknowledged its generation, and every sweep re-ran it. A control with
-- no remediation is due on its generation alone; once a row exists the
-- intent comparison applies as before.

-- +goose Up
CREATE OR REPLACE VIEW remediation_due AS
SELECT cr.repository_id, cr.control_id, ca.ordinal
FROM control_results cr
JOIN control_assignments ca
  ON (ca.repository_id, ca.control_id) = (cr.repository_id, cr.control_id)
 AND ca.state = 'active' AND ca.epoch = cr.assignment_epoch
JOIN repository_policy_state ps ON ps.repository_id = cr.repository_id
LEFT JOIN LATERAL (
    SELECT r.* FROM remediations r
    WHERE (r.repository_id, r.control_id) = (cr.repository_id, cr.control_id)
    ORDER BY r.id DESC LIMIT 1
) r ON true
WHERE ps.state = 'managed'
  AND ca.effective_mode = 'remediate'
  AND cr.status = 'non_compliant'
  AND EXISTS (SELECT 1 FROM rule_results rr
              WHERE (rr.repository_id, rr.control_id) = (cr.repository_id, cr.control_id)
                AND rr.status = 'fail' AND rr.remediate)
  AND CASE
        WHEN r.id IS NULL
             THEN cr.eval_generation > cr.remediated_generation
        WHEN r.state IN ('merged', 'closed_compliant', 'closed_withdrawn', 'superseded', 'withdrawn', 'resolved', 'applied')
             THEN cr.eval_generation > cr.remediated_generation
               OR cr.intent_revision IS DISTINCT FROM r.intent_revision
        WHEN r.state = 'open'
             THEN cr.eval_generation > cr.remediated_generation
               OR cr.intent_revision IS DISTINCT FROM r.intent_revision
               OR r.observed_head_sha IS DISTINCT FROM r.acknowledged_head_sha
        WHEN r.state = 'closed_by_user'
             THEN cr.eval_generation > r.closed_generation OR r.next_eligible_at <= now()
        WHEN r.state = 'failed'
             THEN true
        ELSE false
      END;

-- +goose Down
CREATE OR REPLACE VIEW remediation_due AS
SELECT cr.repository_id, cr.control_id, ca.ordinal
FROM control_results cr
JOIN control_assignments ca
  ON (ca.repository_id, ca.control_id) = (cr.repository_id, cr.control_id)
 AND ca.state = 'active' AND ca.epoch = cr.assignment_epoch
JOIN repository_policy_state ps ON ps.repository_id = cr.repository_id
LEFT JOIN LATERAL (
    SELECT r.* FROM remediations r
    WHERE (r.repository_id, r.control_id) = (cr.repository_id, cr.control_id)
    ORDER BY r.id DESC LIMIT 1
) r ON true
WHERE ps.state = 'managed'
  AND ca.effective_mode = 'remediate'
  AND cr.status = 'non_compliant'
  AND EXISTS (SELECT 1 FROM rule_results rr
              WHERE (rr.repository_id, rr.control_id) = (cr.repository_id, cr.control_id)
                AND rr.status = 'fail' AND rr.remediate)
  AND CASE
        WHEN r.id IS NULL
          OR r.state IN ('merged', 'closed_compliant', 'closed_withdrawn', 'superseded', 'withdrawn', 'resolved', 'applied')
             THEN cr.eval_generation > cr.remediated_generation
               OR cr.intent_revision IS DISTINCT FROM r.intent_revision
        WHEN r.state = 'open'
             THEN cr.eval_generation > cr.remediated_generation
               OR cr.intent_revision IS DISTINCT FROM r.intent_revision
               OR r.observed_head_sha IS DISTINCT FROM r.acknowledged_head_sha
        WHEN r.state = 'closed_by_user'
             THEN cr.eval_generation > r.closed_generation OR r.next_eligible_at <= now()
        WHEN r.state = 'failed'
             THEN true
        ELSE false
      END;
