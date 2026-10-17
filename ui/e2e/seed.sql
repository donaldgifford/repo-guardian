-- Seed for the Playwright suite over the controls schema (IMPL-0029
-- 7.7, 8.8): two orgs, so a leak of a name onto the aggregate-only
-- status page or past the caller's scope (ada sees acme only) is
-- visible. Checks double as the evaluation log; service runs, App
-- installations, assignments, results with evidence, an activated
-- policy version with its summary and thirty days of snapshots feed
-- every view. Times are relative to now() so ages stay stable.

INSERT INTO repositories (org, name, provider_repo_id, active, park_reason, parked_at, discovered_at, next_due_at, last_checked_at)
VALUES
  ('acme', 'web', 501, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '2 hours'),
  ('acme', 'api', 502, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '3 hours'),
  ('acme', 'legacy', 503, false, 'archived', now() - interval '10 days', now() - interval '60 days', NULL, now() - interval '10 days'),
  ('globex', 'site', 601, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '1 hour');

INSERT INTO policy_versions (version, first_seen_at, activated_at, rollout_completed_at, summary) VALUES
  ('controls:e2e', now() - interval '12 days', now() - interval '12 days', now() - interval '11 days', '{
    "version": "controls:e2e",
    "enterprise": {"orgs": ["acme", "globex"], "mode": "evaluate", "controls": ["codeowners@1", "renovate@1"]},
    "orgs": [
      {"org": "acme", "mode": "remediate", "excluded": [{"control": "renovate@1", "reason": "api pins its dependencies"}],
       "repos": [{"label": "services", "match": ["web", "api"]}]},
      {"org": "globex", "added": [{"control": "globex-secret-control@1"}]}
    ],
    "controls": [
      {"control": "codeowners@1", "title": "CODEOWNERS present", "type": "codeowners", "revision": "r-codeowners-1"},
      {"control": "renovate@1", "title": "Dependency updates", "type": "dependency_updates", "revision": "r-renovate-1"},
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
SELECT r.id, CASE WHEN r.active THEN 'managed' ELSE 'parked' END, CASE WHEN r.active THEN NULL ELSE 'archived' END,
       3, interval '7 days', 'controls:e2e', now() - interval '11 days'
FROM repositories r;

-- codeowners and renovate on every active repository; acme/api excludes
-- renovate, globex/site adds a control of its own.
INSERT INTO control_assignments (repository_id, control_id, control_version, revision, state, decision, source, source_block,
                                 ordinal, excluded_by, reason, requested_mode, effective_mode, mode_source, epoch,
                                 policy_version, resolved_at)
SELECT r.id, a.control, 1, a.revision, a.state, a.decision, a.source, a.block, a.ordinal, a.excluded_by, a.reason,
       a.mode, CASE WHEN r.org = 'acme' THEN a.mode ELSE 'evaluate' END, a.source, nextval('assignment_epoch_seq'),
       'controls:e2e', now() - interval '11 days'
FROM repositories r
JOIN (VALUES
  ('web', 'codeowners', 'r-codeowners-1', 'active', 'baseline', 'enterprise', NULL, 1, NULL, NULL, 'remediate'),
  ('web', 'renovate', 'r-renovate-1', 'active', 'baseline', 'enterprise', NULL, 2, NULL, NULL, 'remediate'),
  ('api', 'codeowners', 'r-codeowners-1', 'active', 'baseline', 'enterprise', NULL, 1, NULL, NULL, 'remediate'),
  ('api', 'renovate', 'r-renovate-1', 'excluded', 'excluded', 'orgs/acme', 'exclude "renovate"', 2, 'orgs/acme', 'api pins its dependencies', 'evaluate'),
  ('site', 'codeowners', 'r-codeowners-1', 'active', 'baseline', 'enterprise', NULL, 1, NULL, NULL, 'evaluate'),
  ('site', 'globex-secret-control', 'r-globex-1', 'active', 'org_added', 'orgs/globex', 'control "globex-secret-control"', 2, NULL, NULL, 'evaluate')
) AS a(name, control, revision, state, decision, source, block, ordinal, excluded_by, reason, mode) ON a.name = r.name;

-- Results: acme/web fails renovate; globex/site's own control has not
-- been evaluated yet (pending).
INSERT INTO control_results (repository_id, control_id, control_version, revision, assignment_epoch, status, fingerprint,
                             intent_revision, eval_generation, evaluated_sha, evaluated_at, last_changed_at)
SELECT ca.repository_id, ca.control_id, 1, ca.revision, ca.epoch, x.status, 'fp-' || x.name || '-' || ca.control_id,
       'ir-' || x.name, 1, 'sha-' || x.name, now() - interval '2 hours', now() - interval '3 days'
FROM control_assignments ca
JOIN repositories r ON r.id = ca.repository_id
JOIN (VALUES ('web', 'codeowners', 'compliant'), ('web', 'renovate', 'non_compliant'), ('api', 'codeowners', 'compliant'),
             ('site', 'codeowners', 'compliant')) AS x(name, control, status)
  ON x.name = r.name AND x.control = ca.control_id;

INSERT INTO rule_results (repository_id, control_id, rule_id, status, remediate, evidence, evidence_kind, evidence_version)
SELECT cr.repository_id, cr.control_id, 'exists', CASE cr.status WHEN 'compliant' THEN 'pass' ELSE 'fail' END, true,
       jsonb_build_object('evidence_kind', k.kind, 'evidence_version', 1, 'path', k.path, 'present', cr.status = 'compliant'),
       k.kind, 1
FROM control_results cr
JOIN (VALUES ('codeowners', 'codeowners.exists', '.github/CODEOWNERS'), ('renovate', 'dependency_updates.exists', 'renovate.json'))
  AS k(control, kind, path) ON k.control = cr.control_id;

INSERT INTO result_events (repository_id, control_id, control_version, revision, assignment_epoch, rule_id, from_status, to_status,
                           eval_generation, occurred_at)
SELECT cr.repository_id, cr.control_id, 1, cr.revision, cr.assignment_epoch, NULL, 'compliant', 'non_compliant', 1, now() - interval '3 days'
FROM control_results cr JOIN repositories r ON r.id = cr.repository_id
WHERE r.name = 'web' AND cr.control_id = 'renovate';

INSERT INTO checks (repository_id, check_key, trigger, outcome, error, policy_version, started_at, finished_at)
SELECT r.id, 'e2e/' || r.name || '/' || c.k, c.trigger, c.outcome, c.error, 'controls:e2e', now() - c.ago - interval '1 minute', now() - c.ago
FROM repositories r
JOIN (VALUES
  ('web', 'a', 'schedule', 'success', NULL, interval '2 hours'),
  ('web', 'b', 'push', 'error', 'GET /repos/acme/web/contents: 502 Bad Gateway', interval '1 day'),
  ('api', 'a', 'schedule', 'success', NULL, interval '3 hours'),
  ('site', 'a', 'webhook', 'success', NULL, interval '1 hour')
) AS c(name, k, trigger, outcome, error, ago) ON c.name = r.name;

INSERT INTO service_runs (kind, outcome, started_at, finished_at) VALUES
  ('discovery', 'success', now() - interval '41 minutes', now() - interval '40 minutes'),
  ('snapshot', 'success', now() - interval '3 hours', now() - interval '3 hours');

-- Thirty days of daily snapshots per (org, control), drifting toward
-- the current counts.
INSERT INTO compliance_snapshots (org, control_id, decision, snapshot_at, compliant, non_compliant, pending, stale,
                                  unmeasurable_access_denied, unmeasurable_suspended, unmeasurable_conflict,
                                  not_applicable, unknown, excluded, assigned, percent, coverage)
SELECT o.org, c.control, 'baseline', date_trunc('day', now()) - (d || ' days')::interval,
       o.size - (d % 3), (d % 3), 0, 0, 0, 0, 0, 0, 0, 0, o.size,
       floor_percent(o.size - (d % 3), o.size), 100
FROM (VALUES ('acme', 10), ('globex', 4)) AS o(org, size)
CROSS JOIN (VALUES ('codeowners'), ('renovate')) AS c(control)
CROSS JOIN generate_series(1, 30) AS d;
