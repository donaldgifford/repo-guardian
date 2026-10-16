-- API repositories, evaluations and timeline over the controls schema
-- (DESIGN-0027, DESIGN-0032 § API). Every query carries the scope
-- predicate on lower(r.org); TestAPIQueries_AreScoped enforces it.
-- Pages are keyset, fetched at limit + 1 by the reader.
--
-- evaluable: the Evaluation App can reach the repository; remediable:
-- the Remediation App can and its installation is not suspended. Both
-- read app_repository_access, whose read policy is PUBLIC so the api's
-- read-only role sees every row. last_evaluated_at is the newest
-- successful evaluation (repositories.last_checked_at is rc-only and
-- never written on the controls line); the epoch stands for "never".

-- name: APIRepositories :many
SELECT r.id, r.org, r.name, r.provider_repo_id, r.active, r.park_reason, r.parked_at,
       r.discovered_at, r.next_due_at,
       coalesce((SELECT max(c.finished_at) FROM checks c WHERE c.repository_id = r.id AND c.outcome = 'success'),
                'epoch'::timestamptz)::timestamptz
         AS last_evaluated_at,
       ps.state AS policy_state, ps.reason AS policy_reason,
       EXISTS (SELECT 1 FROM app_repository_access a WHERE a.repository_id = r.id AND a.app = 'eval') AS evaluable,
       EXISTS (SELECT 1 FROM app_repository_access a
               JOIN app_installations i ON (i.app, i.installation_id) = (a.app, a.installation_id)
               WHERE a.repository_id = r.id AND a.app = 'remediate' AND i.suspended_at IS NULL) AS remediable
FROM repositories r
LEFT JOIN repository_policy_state ps ON ps.repository_id = r.id
WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
  AND (sqlc.narg(active)::bool IS NULL OR r.active = sqlc.narg(active)::bool)
  AND (sqlc.narg(park_reason)::text IS NULL OR r.park_reason = sqlc.narg(park_reason)::text)
  AND (sqlc.narg(policy_state)::text IS NULL OR ps.state = sqlc.narg(policy_state)::text)
  AND (sqlc.narg(name_prefix)::text IS NULL
       OR left(lower(r.name), length(sqlc.narg(name_prefix)::text)) = lower(sqlc.narg(name_prefix)::text))
  AND (NOT @has_after::bool OR r.id > @after_id::bigint)
ORDER BY r.id
LIMIT @page_limit;

-- name: APIRepository :one
SELECT r.id, r.org, r.name, r.provider_repo_id, r.active, r.park_reason, r.parked_at,
       r.discovered_at, r.next_due_at,
       coalesce((SELECT max(c.finished_at) FROM checks c WHERE c.repository_id = r.id AND c.outcome = 'success'),
                'epoch'::timestamptz)::timestamptz
         AS last_evaluated_at,
       ps.state AS policy_state, ps.reason AS policy_reason,
       EXISTS (SELECT 1 FROM app_repository_access a WHERE a.repository_id = r.id AND a.app = 'eval') AS evaluable,
       EXISTS (SELECT 1 FROM app_repository_access a
               JOIN app_installations i ON (i.app, i.installation_id) = (a.app, a.installation_id)
               WHERE a.repository_id = r.id AND a.app = 'remediate' AND i.suspended_at IS NULL) AS remediable
FROM repositories r
LEFT JOIN repository_policy_state ps ON ps.repository_id = r.id
WHERE r.id = @id
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]));

-- name: APIRepositoryEvaluations :many
-- The evaluation log, newest first.
SELECT c.id, c.trigger, c.outcome, c.policy_version, c.started_at, c.finished_at, c.error
FROM checks c
JOIN repositories r ON r.id = c.repository_id
WHERE c.repository_id = @repository_id
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (NOT @has_after::bool OR c.id < @after_id::bigint)
ORDER BY c.id DESC
LIMIT @page_limit;

-- name: APIRepositoryEvents :many
-- Control-status and rule-result transitions and repository events
-- merged with UNION ALL, newest first, keyed by (occurred_at, source, id).
-- Remediation events join with IMPL-0030.
SELECT e.source, e.id, e.occurred_at, coalesce(e.control_id, '')::text AS control_id,
       coalesce(e.rule_id, '')::text AS rule_id, e.from_status, e.to_status, e.kind, e.detail
FROM (
    SELECT 'result'::text AS source, ev.id, ev.occurred_at, ev.control_id, ev.rule_id, ev.from_status, ev.to_status,
           NULL::text AS kind, NULL::jsonb AS detail
    FROM result_events ev
    WHERE ev.repository_id = @repository_id
    UNION ALL
    SELECT 'repository'::text, re.id, re.occurred_at, NULL::text, NULL::text, NULL::text, NULL::text, re.kind, re.detail
    FROM repository_events re
    WHERE re.repository_id = @repository_id
) e
JOIN repositories r ON r.id = @repository_id
WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (NOT @has_after::bool
       OR (e.occurred_at, e.source, e.id) < (@after_occurred_at::timestamptz, @after_source::text, @after_id::bigint))
ORDER BY e.occurred_at DESC, e.source DESC, e.id DESC
LIMIT @page_limit;

-- name: APIOrgRepositories :many
-- Repositories in scope per org by active state and park reason.
SELECT r.org::text AS org, r.active, coalesce(r.park_reason, '')::text AS park_reason, count(*)::int AS repositories
FROM repositories r
WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
GROUP BY r.org, r.active, r.park_reason
ORDER BY lower(r.org), r.active DESC, park_reason;

-- name: APIOrgOpenPRs :many
-- Open repo-guardian pull requests per org, over active repositories.
SELECT r.org::text AS org, count(*)::int AS pull_requests
FROM remediations m
JOIN repositories r ON r.id = m.repository_id
WHERE r.active AND m.kind = 'pull_request' AND m.state = 'open'
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
GROUP BY r.org
ORDER BY lower(r.org);
