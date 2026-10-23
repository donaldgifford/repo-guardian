-- Seed for the Playwright suite over the controls schema (IMPL-0029
-- 7.7, 8.8; grown by IMPL-0032 8.1): two orgs, so a leak of a name onto
-- the aggregate-only status page or past the caller's scope (ada sees
-- acme only) is visible. Checks double as the evaluation log; service
-- runs, App installations, assignments, results with evidence, an
-- activated policy version with its summary, thirty days of snapshots
-- and acme's remediations feed every view. Times are relative to now()
-- so ages stay stable.
--
-- acme (the Remediation App's org):
--   web      codeowners ok, renovate failing (3d), labels failing (15d, failed apply held on permission)
--   api      codeowners ok, labels ok; renovate excluded by the org layer
--   mobile   codeowners failing (45d, exists fails and valid errors), renovate failing (120d), labels failing (12h, recommended)
--   docs     codeowners ok (a PR the user closed), renovate ok, labels failing (200d, failed direct apply)
--   infra    codeowners held conflict, renovate failing with a 45-day-old open PR (stale)
--   data     codeowners held foreign_branch, renovate held stale_branch, labels ok
--   ops      codeowners held pr_cap, renovate failing with a 3-day-old open PR, labels ok
--   tools    codeowners failing with a 12-day-old open PR, renovate ok (merged PR), labels ok
--   legacy   parked archived; old-fork parked fork; vault parked access_denied (keeps a failing result)
--   sandbox  excluded by the org layer
-- globex: site, with an org-added control not yet evaluated. Never visible to ada.

INSERT INTO repositories (org, name, provider_repo_id, active, park_reason, parked_at, discovered_at, next_due_at, last_checked_at)
VALUES
  ('acme', 'web', 501, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '2 hours'),
  ('acme', 'api', 502, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '3 hours'),
  ('acme', 'legacy', 503, false, 'archived', now() - interval '10 days', now() - interval '60 days', NULL, now() - interval '10 days'),
  ('acme', 'mobile', 504, true, NULL, NULL, now() - interval '200 days', now() + interval '18 hours', now() - interval '4 hours'),
  ('acme', 'docs', 505, true, NULL, NULL, now() - interval '220 days', now() + interval '12 hours', now() - interval '5 hours'),
  ('acme', 'infra', 506, true, NULL, NULL, now() - interval '90 days', now() + interval '10 hours', now() - interval '6 hours'),
  ('acme', 'data', 507, true, NULL, NULL, now() - interval '90 days', now() + interval '9 hours', now() - interval '7 hours'),
  ('acme', 'ops', 508, true, NULL, NULL, now() - interval '30 days', now() + interval '8 hours', now() - interval '1 hour'),
  ('acme', 'tools', 509, true, NULL, NULL, now() - interval '40 days', now() + interval '7 hours', now() - interval '90 minutes'),
  ('acme', 'old-fork', 510, false, 'fork', now() - interval '25 days', now() - interval '25 days', NULL, now() - interval '25 days'),
  ('acme', 'vault', 511, false, 'access_denied', now() - interval '4 days', now() - interval '60 days', NULL, now() - interval '4 days'),
  ('acme', 'sandbox', 512, true, NULL, NULL, now() - interval '15 days', NULL, NULL),
  ('globex', 'site', 601, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '1 hour');

INSERT INTO policy_versions (version, first_seen_at, activated_at, rollout_completed_at, summary) VALUES
  ('controls:e2e', now() - interval '12 days', now() - interval '12 days', now() - interval '11 days', '{
    "version": "controls:e2e",
    "enterprise": {"orgs": ["acme", "globex"], "mode": "evaluate", "controls": ["codeowners@1", "renovate@1"]},
    "orgs": [
      {"org": "acme", "mode": "remediate",
       "added": [{"control": "labels@1"}],
       "excluded": [{"control": "renovate@1", "reason": "api pins its dependencies"}],
       "excluded_repos": [{"match": ["sandbox"], "reason": "sandbox is not a service"}],
       "repos": [{"label": "services", "match": ["web", "api"]}]},
      {"org": "globex", "added": [{"control": "globex-secret-control@1"}]}
    ],
    "controls": [
      {"control": "codeowners@1", "title": "CODEOWNERS present", "type": "codeowners", "revision": "r-codeowners-1"},
      {"control": "renovate@1", "title": "Dependency updates", "type": "dependency_updates", "revision": "r-renovate-1"},
      {"control": "labels@1", "title": "Standard labels", "type": "labels", "revision": "r-labels-1"},
      {"control": "globex-secret-control@1", "type": "file", "revision": "r-globex-1"}
    ]}');

-- The Evaluation App reaches every repository; the Remediation App is
-- installed on acme only.
INSERT INTO app_installations (app, installation_id, account_login, repository_selection, seen_at) VALUES
  ('eval', 1, 'acme', 'all', now()),
  ('eval', 2, 'globex', 'all', now()),
  ('remediate', 11, 'acme', 'all', now());

INSERT INTO app_repository_access (repository_id, app, installation_id, seen_at)
SELECT r.id, 'eval', CASE r.org WHEN 'acme' THEN 1 ELSE 2 END, now() FROM repositories r
UNION ALL
SELECT r.id, 'remediate', 11, now() FROM repositories r WHERE r.org = 'acme';

INSERT INTO repository_policy_state (repository_id, state, reason, max_open_prs, reopen_after, policy_version, resolved_at)
SELECT r.id,
       CASE WHEN NOT r.active THEN 'parked' WHEN r.name = 'sandbox' THEN 'excluded' ELSE 'managed' END,
       CASE WHEN NOT r.active THEN r.park_reason WHEN r.name = 'sandbox' THEN 'sandbox is not a service' ELSE NULL END,
       3, interval '7 days', 'controls:e2e', now() - interval '11 days'
FROM repositories r;

-- codeowners and renovate on every repository (acme/api excludes
-- renovate), labels added by acme's layer, globex/site's own control.
-- The excluded sandbox has no assignments.
INSERT INTO control_assignments (repository_id, control_id, control_version, revision, state, decision, source, source_block,
                                 ordinal, excluded_by, reason, requested_mode, effective_mode, mode_source, epoch,
                                 policy_version, resolved_at)
SELECT r.id, a.control, 1, a.revision, a.state, a.decision, a.source, a.block, a.ordinal, a.excluded_by, a.reason,
       a.mode, CASE WHEN r.org = 'acme' THEN a.mode ELSE 'evaluate' END, a.source, nextval('assignment_epoch_seq'),
       'controls:e2e', now() - interval '11 days'
FROM repositories r
JOIN LATERAL (
  SELECT * FROM (VALUES
    ('codeowners', 'r-codeowners-1', 'active', 'baseline', 'enterprise', NULL, 1, NULL, NULL, 'remediate'),
    ('renovate', 'r-renovate-1', 'active', 'baseline', 'enterprise', NULL, 2, NULL, NULL, 'remediate'),
    ('labels', 'r-labels-1', 'active', 'org_added', 'orgs/acme', 'control "labels"', 3, NULL, NULL, 'remediate')
  ) AS v(control, revision, state, decision, source, block, ordinal, excluded_by, reason, mode)
  WHERE r.org = 'acme' AND r.name NOT IN ('sandbox') AND NOT (r.name = 'api' AND v.control = 'renovate')
  UNION ALL
  SELECT * FROM (VALUES
    ('renovate', 'r-renovate-1', 'excluded', 'excluded', 'orgs/acme', 'exclude "renovate"', 2, 'orgs/acme', 'api pins its dependencies', 'evaluate')
  ) AS e(control, revision, state, decision, source, block, ordinal, excluded_by, reason, mode)
  WHERE r.name = 'api'
  UNION ALL
  SELECT * FROM (VALUES
    ('codeowners', 'r-codeowners-1', 'active', 'baseline', 'enterprise', NULL, 1, NULL, NULL, 'evaluate'),
    ('globex-secret-control', 'r-globex-1', 'active', 'org_added', 'orgs/globex', 'control "globex-secret-control"', 2, NULL, NULL, 'evaluate')
  ) AS g(control, revision, state, decision, source, block, ordinal, excluded_by, reason, mode)
  WHERE r.name = 'site'
) AS a ON true;

-- Results. globex/site's own control has not been evaluated yet
-- (pending); old-fork's were cleared by its park; vault's survive its
-- access_denied park. last_changed_at spans every finding-age bucket.
INSERT INTO control_results (repository_id, control_id, control_version, revision, assignment_epoch, status, fingerprint,
                             intent_revision, eval_generation, evaluated_sha, evaluated_at, last_changed_at, hold, hold_since)
SELECT ca.repository_id, ca.control_id, 1, ca.revision, ca.epoch, x.status, 'fp-' || x.name || '-' || ca.control_id,
       'ir-' || x.name || '-' || ca.control_id, 1, 'sha-' || x.name, now() - interval '2 hours', now() - x.changed, x.hold,
       CASE WHEN x.hold IS NULL THEN NULL ELSE now() - x.held END
FROM control_assignments ca
JOIN repositories r ON r.id = ca.repository_id
JOIN (VALUES
  ('web', 'codeowners', 'compliant', interval '3 days', NULL, NULL),
  ('web', 'renovate', 'non_compliant', interval '3 days', NULL, NULL),
  ('web', 'labels', 'non_compliant', interval '15 days', NULL, NULL),
  ('api', 'codeowners', 'compliant', interval '3 days', NULL, NULL),
  ('api', 'labels', 'compliant', interval '3 days', NULL, NULL),
  ('mobile', 'codeowners', 'non_compliant', interval '45 days', NULL, NULL),
  ('mobile', 'renovate', 'non_compliant', interval '120 days', NULL, NULL),
  ('mobile', 'labels', 'non_compliant', interval '12 hours', NULL, NULL),
  ('docs', 'codeowners', 'compliant', interval '8 days', NULL, NULL),
  ('docs', 'renovate', 'compliant', interval '30 days', NULL, NULL),
  ('docs', 'labels', 'non_compliant', interval '200 days', NULL, NULL),
  ('infra', 'codeowners', 'non_compliant', interval '40 days', 'conflict', interval '10 days'),
  ('infra', 'renovate', 'non_compliant', interval '50 days', NULL, NULL),
  ('data', 'codeowners', 'non_compliant', interval '35 days', 'foreign_branch', interval '20 days'),
  ('data', 'renovate', 'non_compliant', interval '9 days', 'stale_branch', interval '5 days'),
  ('data', 'labels', 'compliant', interval '9 days', NULL, NULL),
  ('ops', 'codeowners', 'non_compliant', interval '2 days', 'pr_cap', interval '1 day'),
  ('ops', 'renovate', 'non_compliant', interval '6 days', NULL, NULL),
  ('ops', 'labels', 'compliant', interval '6 days', NULL, NULL),
  ('tools', 'codeowners', 'non_compliant', interval '14 days', NULL, NULL),
  ('tools', 'renovate', 'compliant', interval '2 days', NULL, NULL),
  ('tools', 'labels', 'compliant', interval '14 days', NULL, NULL),
  ('vault', 'codeowners', 'non_compliant', interval '30 days', NULL, NULL),
  ('site', 'codeowners', 'compliant', interval '3 days', NULL, NULL)
) AS x(name, control, status, changed, hold, held)
  ON x.name = r.name AND x.control = ca.control_id;

-- Rule results with one evidence kind per control; mobile's codeowners
-- also has a rule in error, and renovate's config rule reads the file.
INSERT INTO rule_results (repository_id, control_id, rule_id, status, remediate, evidence, evidence_kind, evidence_version)
SELECT cr.repository_id, cr.control_id, 'exists', CASE cr.status WHEN 'compliant' THEN 'pass' ELSE 'fail' END, true,
       jsonb_build_object('evidence_kind', k.kind, 'evidence_version', 1, 'path', k.path, 'present', cr.status = 'compliant'),
       k.kind, 1
FROM control_results cr
JOIN (VALUES ('codeowners', 'codeowners.exists', '.github/CODEOWNERS'), ('renovate', 'dependency_updates.exists', 'renovate.json'))
  AS k(control, kind, path) ON k.control = cr.control_id
UNION ALL
SELECT cr.repository_id, cr.control_id, 'standard_set', CASE cr.status WHEN 'compliant' THEN 'pass' ELSE 'fail' END, true,
       jsonb_build_object('evidence_kind', 'labels.set', 'evidence_version', 1, 'missing', CASE cr.status WHEN 'compliant' THEN '' ELSE 'bug, security' END, 'extra', 'wontfix'),
       'labels.set', 1
FROM control_results cr WHERE cr.control_id = 'labels'
UNION ALL
SELECT cr.repository_id, cr.control_id, 'valid', 'error', false,
       jsonb_build_object('evidence_kind', 'codeowners.syntax', 'evidence_version', 1, 'reason', 'parse_error', 'line', 7, 'message', 'unknown owner @acme/nobody'),
       'codeowners.syntax', 1
FROM control_results cr JOIN repositories r ON r.id = cr.repository_id WHERE r.name = 'mobile' AND cr.control_id = 'codeowners'
UNION ALL
SELECT cr.repository_id, cr.control_id, 'config', 'fail', true,
       jsonb_build_object('evidence_kind', 'dependency_updates.config', 'evidence_version', 1, 'path', 'renovate.json', 'extends', 'config:base', 'schedule', 'missing'),
       'dependency_updates.config', 1
FROM control_results cr JOIN repositories r ON r.id = cr.repository_id WHERE r.name = 'mobile' AND cr.control_id = 'renovate';

-- Result events: each finding's last change, and one rule-level flip.
INSERT INTO result_events (repository_id, control_id, control_version, revision, assignment_epoch, rule_id, from_status, to_status,
                           eval_generation, occurred_at)
SELECT cr.repository_id, cr.control_id, 1, cr.revision, cr.assignment_epoch, NULL, 'compliant', 'non_compliant', 1, cr.last_changed_at
FROM control_results cr WHERE cr.status = 'non_compliant'
UNION ALL
SELECT cr.repository_id, cr.control_id, 1, cr.revision, cr.assignment_epoch, 'exists', 'pass', 'fail', 1, cr.last_changed_at
FROM control_results cr JOIN repositories r ON r.id = cr.repository_id WHERE r.name = 'web' AND cr.control_id = 'renovate';

INSERT INTO checks (repository_id, check_key, trigger, outcome, error, policy_version, started_at, finished_at)
SELECT r.id, 'e2e/' || r.name || '/' || c.k, c.trigger, c.outcome, c.error, 'controls:e2e', now() - c.ago - interval '1 minute', now() - c.ago
FROM repositories r
JOIN (VALUES
  ('web', 'a', 'schedule', 'success', NULL, interval '2 hours'),
  ('web', 'b', 'push', 'error', 'GET /repos/acme/web/contents: 502 Bad Gateway', interval '1 day'),
  ('api', 'a', 'schedule', 'success', NULL, interval '3 hours'),
  ('mobile', 'a', 'schedule', 'success', NULL, interval '4 hours'),
  ('docs', 'a', 'schedule', 'success', NULL, interval '5 hours'),
  ('infra', 'a', 'schedule', 'success', NULL, interval '6 hours'),
  ('data', 'a', 'schedule', 'success', NULL, interval '7 hours'),
  ('ops', 'a', 'pr_event', 'success', NULL, interval '1 hour'),
  ('tools', 'a', 'schedule', 'success', NULL, interval '90 minutes'),
  ('site', 'a', 'webhook', 'success', NULL, interval '1 hour')
) AS c(name, k, trigger, outcome, error, ago) ON c.name = r.name;

INSERT INTO service_runs (kind, outcome, started_at, finished_at) VALUES
  ('discovery', 'success', now() - interval '41 minutes', now() - interval '40 minutes'),
  ('snapshot', 'success', now() - interval '3 hours', now() - interval '3 hours'),
  ('maintenance', 'success', now() - interval '50 minutes', now() - interval '49 minutes'),
  ('sweep', 'success', now() - interval '49 minutes', now() - interval '48 minutes');

-- Thirty days of daily snapshots per (org, control), drifting toward
-- the current counts; labels for acme alone.
INSERT INTO compliance_snapshots (org, control_id, decision, snapshot_at, compliant, non_compliant, pending, stale,
                                  unmeasurable_access_denied, unmeasurable_suspended, unmeasurable_conflict,
                                  not_applicable, unknown, excluded, assigned, percent, coverage)
SELECT o.org, c.control, c.decision, date_trunc('day', now()) - (d || ' days')::interval,
       o.size - (d % 3), (d % 3), 0, 0, 0, 0, 0, 0, 0, 0, o.size,
       floor_percent(o.size - (d % 3), o.size), 100
FROM (VALUES ('acme', 10), ('globex', 4)) AS o(org, size)
CROSS JOIN (VALUES ('codeowners', 'baseline'), ('renovate', 'baseline')) AS c(control, decision)
CROSS JOIN generate_series(1, 30) AS d
UNION ALL
SELECT 'acme', 'labels', 'org_added', date_trunc('day', now()) - (d || ' days')::interval,
       8 - (d % 4), (d % 4), 0, 0, 0, 0, 0, 0, 0, 0, 8, floor_percent(8 - (d % 4), 8), 100
FROM generate_series(1, 30) AS d;

-- acme's remediations (IMPL-0032 8.1): open PRs at several ages (one
-- past 30 days), a merged and a user-closed PR, a recommendation with
-- a proposal, a failed direct apply with steps, and one held on
-- permission. The branch holds live on control_results above.
INSERT INTO remediations (repository_id, control_id, assignment_epoch, operation_key, kind, state, intent_revision, base_sha, branch,
                          pr_number, pr_url, observed_head_sha, observed_at, acknowledged_head_sha, proposal, remediated_generation,
                          hold, created_at, last_run_at, closed_at, close_reason)
SELECT ca.repository_id, ca.control_id, ca.epoch, 'e2e/' || r.name || '/' || ca.control_id, m.kind, m.state,
       'ir-' || r.name || '-' || ca.control_id, CASE m.kind WHEN 'pull_request' THEN 'base-' || r.name END,
       CASE m.kind WHEN 'pull_request' THEN 'repo-guardian/' || ca.control_id END,
       m.pr, CASE WHEN m.pr IS NULL THEN NULL ELSE 'https://github.com/acme/' || r.name || '/pull/' || m.pr END,
       CASE WHEN m.pr IS NULL THEN NULL ELSE 'head-' || r.name END, CASE WHEN m.pr IS NULL THEN NULL ELSE now() - interval '2 hours' END,
       CASE WHEN m.pr IS NULL THEN NULL ELSE 'head-' || r.name END, m.proposal, 1, m.hold,
       now() - m.created, now() - m.ran, CASE WHEN m.closed IS NULL THEN NULL ELSE now() - m.closed END, m.close_reason
FROM control_assignments ca
JOIN repositories r ON r.id = ca.repository_id
JOIN (VALUES
  ('infra', 'renovate', 'pull_request', 'open', 101, NULL::jsonb, NULL, interval '45 days', interval '2 days', NULL, NULL),
  ('ops', 'renovate', 'pull_request', 'open', 102, NULL, NULL, interval '3 days', interval '1 hour', NULL, NULL),
  ('tools', 'codeowners', 'pull_request', 'open', 103, NULL, NULL, interval '12 days', interval '1 day', NULL, NULL),
  ('tools', 'renovate', 'pull_request', 'merged', 104, NULL, NULL, interval '20 days', interval '2 days', interval '2 days', 'merged'),
  ('docs', 'codeowners', 'pull_request', 'closed_by_user', 105, NULL, NULL, interval '30 days', interval '8 days', interval '8 days', 'closed_by_user'),
  ('mobile', 'labels', 'api', 'recommended', NULL,
   '[{"key": "labels/bug", "op": "create", "after": {"color": "d73a4a", "description": "Something is not working"}},
     {"key": "labels/security", "op": "create", "after": {"color": "b60205"}},
     {"key": "labels/wontfix", "op": "delete", "before": {"color": "ffffff"}}]'::jsonb,
   NULL, interval '5 days', interval '5 days', NULL, NULL),
  ('docs', 'labels', 'api', 'failed', NULL, NULL, NULL, interval '4 days', interval '4 days', NULL, NULL),
  ('web', 'labels', 'api', 'failed', NULL, NULL, 'permission', interval '15 days', interval '15 days', NULL, NULL)
) AS m(name, control, kind, state, pr, proposal, hold, created, ran, closed, close_reason)
  ON m.name = r.name AND m.control = ca.control_id;

INSERT INTO remediation_steps (remediation_id, operation_key, seq, kind, resource_key, blob_sha, committed_sha, pr_number, status, before, after, error, occurred_at)
SELECT m.id, m.operation_key, s.seq, s.kind, s.resource_key, s.blob, s.committed, s.pr, s.status, s.before, s.after, s.error,
       m.created_at + (s.seq || ' minutes')::interval
FROM remediations m
JOIN repositories r ON r.id = m.repository_id
JOIN (VALUES
  ('infra', 'renovate', 1, 'branch_created', NULL, NULL, 'base-infra', NULL, 'done', NULL::jsonb, NULL::jsonb, NULL),
  ('infra', 'renovate', 2, 'committed', 'renovate.json', 'blob-infra', 'head-infra', NULL, 'done', NULL, NULL, NULL),
  ('infra', 'renovate', 3, 'pr_created', NULL, NULL, NULL, 101, 'done', NULL, NULL, NULL),
  ('ops', 'renovate', 1, 'branch_created', NULL, NULL, 'base-ops', NULL, 'done', NULL, NULL, NULL),
  ('ops', 'renovate', 2, 'committed', 'renovate.json', 'blob-ops', 'head-ops', NULL, 'done', NULL, NULL, NULL),
  ('ops', 'renovate', 3, 'pr_created', NULL, NULL, NULL, 102, 'done', NULL, NULL, NULL),
  ('tools', 'codeowners', 1, 'branch_created', NULL, NULL, 'base-tools', NULL, 'done', NULL, NULL, NULL),
  ('tools', 'codeowners', 2, 'committed', '.github/CODEOWNERS', 'blob-tools', 'head-tools', NULL, 'done', NULL, NULL, NULL),
  ('tools', 'codeowners', 3, 'pr_created', NULL, NULL, NULL, 103, 'done', NULL, NULL, NULL),
  ('tools', 'renovate', 1, 'pr_created', NULL, NULL, NULL, 104, 'done', NULL, NULL, NULL),
  ('docs', 'codeowners', 1, 'pr_created', NULL, NULL, NULL, 105, 'done', NULL, NULL, NULL),
  ('docs', 'codeowners', 2, 'pr_closed', NULL, NULL, NULL, 105, 'done', NULL, NULL, NULL),
  ('docs', 'labels', 1, 'api_write', 'labels/bug', NULL, NULL, NULL, 'done', '{}'::jsonb, '{"color": "d73a4a"}'::jsonb, NULL),
  ('docs', 'labels', 2, 'api_write', 'labels/security', NULL, NULL, NULL, 'failed', '{}'::jsonb, '{"color": "b60205"}'::jsonb, 'POST /repos/acme/docs/labels: 422 Validation Failed'),
  ('web', 'labels', 1, 'api_write', 'labels/bug', NULL, NULL, NULL, 'failed', '{}'::jsonb, '{"color": "d73a4a"}'::jsonb, 'POST /repos/acme/web/labels: 403 Resource not accessible by integration')
) AS s(name, control, seq, kind, resource_key, blob, committed, pr, status, before, after, error)
  ON s.name = r.name AND s.control = m.control_id;
