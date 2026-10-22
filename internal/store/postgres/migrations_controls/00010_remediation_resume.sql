-- Issue #235: a run GitHub throttled between pushing its branch and
-- opening its pull request never came back. remediation_due had no arm
-- for a reserved row, so the deferred run's next Remediate read Due =
-- false and returned; an hour later ExpireReservations deleted the row
-- and its journal (ON DELETE CASCADE), and the next run held the App's
-- own branch as foreign_branch. Held results also stayed due, so every
-- hourly sweep started a run that could not progress.
--
-- remediation_due: a reserved row is due, so its run (or the next one)
-- resumes from the journal; the claim's SKIP LOCKED keeps two runs off
-- one row. A held result is due again only once its hold is a day old,
-- except pr_cap (a slot can free at any time) and conflict (the human's
-- fix arrives as a head the evaluator observes).
--
-- remediation_maintenance: a reservation whose journal records a branch
-- or commit is not expired while its control is still assigned for
-- remediation and failing (the run resumes it); ExpireReservations
-- applies the same rule.

-- +goose Up
-- +goose StatementBegin
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
  AND (cr.hold IS NULL OR cr.hold IN ('pr_cap', 'conflict') OR cr.hold_since <= now() - interval '1 day')
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
        WHEN r.state IN ('failed', 'reserved')
             THEN true
        ELSE false
      END;


CREATE OR REPLACE VIEW remediation_maintenance AS
SELECT m.* FROM (
    SELECT r.id AS remediation_id, r.repository_id, r.control_id, r.kind, r.state,
           CASE
             WHEN r.state = 'reserved' AND r.reserved_until < now()
              AND NOT COALESCE(ca.effective_mode = 'remediate' AND ps.state = 'managed' AND cr.status = 'non_compliant'
                               AND EXISTS (SELECT 1 FROM remediation_steps s
                                           WHERE s.remediation_id = r.id AND s.status = 'done'
                                             AND s.kind IN ('branch_created', 'committed')), false)        THEN 'expire'
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
-- +goose StatementEnd
