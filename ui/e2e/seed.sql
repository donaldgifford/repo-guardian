-- Seed for the Playwright suite (IMPL-0025 22.4): two orgs, so per-org
-- scoping is visible; one of every finding status; hostile evidence to
-- prove repository text renders as text; a parked repository; history.
-- Times are relative to now() so ages and staleness are stable.

INSERT INTO installations (installation_id, account_login, rate_limit, rate_remaining, rate_reset_at, rate_observed_at) VALUES
  (1001, 'acme', 5000, 4200, now() + interval '30 minutes', now() - interval '2 minutes'),
  (1002, 'globex', 5000, 4900, now() + interval '30 minutes', now() - interval '2 minutes');

INSERT INTO repositories (id, org, name, provider_repo_id, installation_id, active, park_reason, parked_at, discovered_at,
                          next_due_at, last_checked_at, last_check_outcome, policy_version) VALUES
  (1, 'acme', 'web', 501, 1001, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '2 hours', 'success', 'v2:e2e'),
  (2, 'acme', 'api', 502, 1001, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '3 hours', 'success', 'v2:e2e'),
  (3, 'acme', 'legacy', 503, 1001, false, 'archived', now() - interval '10 days', now() - interval '60 days', NULL, now() - interval '10 days', 'skipped', 'v2:e2e'),
  (4, 'globex', 'site', 601, 1002, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '1 hour', 'success', 'v2:e2e'),
  (5, 'acme', 'docs', 504, 1001, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '5 days', 'success', 'v1:0123abcd');
SELECT setval('repositories_id_seq', 5);

INSERT INTO findings (repository_id, rule_kind, rule_name, status, reason, remediation, evidence, status_since, last_evaluated_at, policy_version) VALUES
  (1, 'file', 'codeowners', 'compliant', NULL, 'none', '{}', now() - interval '40 days', now() - interval '2 hours', 'v2:e2e'),
  -- Hostile repository text in every field that reaches the UI; the PR
  -- url points elsewhere to prove links are built, not copied.
  (1, 'file', 'renovate', 'non_compliant', 'assertion_failed', 'pr_open',
   jsonb_build_object(
     'path', 'renovate.json',
     'message', '<img src=x onerror="window.__xss=1">extends must include config:base',
     'pr', jsonb_build_object('number', 412, 'url', 'https://evil.example/phish', 'created_at', now() - interval '9 days')),
   now() - interval '9 days', now() - interval '2 hours', 'v2:e2e'),
  (1, 'file', 'dependabot', 'not_applicable', 'gate_closed', 'none', '{"referee": "renovate"}', now() - interval '9 days', now() - interval '2 hours', 'v2:e2e'),
  (2, 'file', 'codeowners', 'non_compliant', 'file_missing', 'pr_open',
   jsonb_build_object('paths_checked', jsonb_build_array('CODEOWNERS', '.github/CODEOWNERS'),
                      'pr', jsonb_build_object('number', 77, 'url', 'https://github.com/acme/api/pull/77', 'created_at', now() - interval '45 days')),
   now() - interval '45 days', now() - interval '3 hours', 'v2:e2e'),
  (2, 'file', 'renovate', 'compliant', NULL, 'none', '{}', now() - interval '50 days', now() - interval '3 hours', 'v2:e2e'),
  (2, 'file', 'dependabot', 'compliant', NULL, 'none', '{}', now() - interval '50 days', now() - interval '3 hours', 'v2:e2e'),
  (4, 'file', 'codeowners', 'non_compliant', 'migrated_from_v1', 'none',
   jsonb_build_object('v1_actionable_since', now() - interval '30 days'), now() - interval '30 days', now() - interval '1 hour', 'v2:e2e'),
  (4, 'file', 'renovate', 'compliant', NULL, 'none', '{}', now() - interval '30 days', now() - interval '1 hour', 'v2:e2e'),
  -- Carried over from v1 and not yet re-checked.
  (5, 'file', 'renovate', 'non_compliant', 'migrated_from_v1', 'none',
   jsonb_build_object('v1_actionable_since', now() - interval '20 days'), now() - interval '20 days', now() - interval '5 days', 'v1:0123abcd');

INSERT INTO checks (repository_id, check_key, trigger, outcome, error, policy_version, started_at, finished_at) VALUES
  (1, 'e2e/1/a', 'schedule', 'success', NULL, 'v2:e2e', now() - interval '2 hours 1 minute', now() - interval '2 hours'),
  (1, 'e2e/1/b', 'push', 'error', 'GET /repos/acme/web/contents: 502 Bad Gateway', 'v2:e2e', now() - interval '1 day 1 minute', now() - interval '1 day'),
  (2, 'e2e/2/a', 'schedule', 'success', NULL, 'v2:e2e', now() - interval '3 hours 1 minute', now() - interval '3 hours'),
  (4, 'e2e/4/a', 'webhook', 'success', NULL, 'v2:e2e', now() - interval '1 hour 1 minute', now() - interval '1 hour');

INSERT INTO finding_events (repository_id, rule_kind, rule_name, from_status, to_status, from_reason, to_reason,
                            from_remediation, to_remediation, policy_version, occurred_at) VALUES
  (1, 'file', 'renovate', 'compliant', 'non_compliant', NULL, 'assertion_failed', 'none', 'pr_open', 'v2:e2e', now() - interval '9 days'),
  (1, 'file', 'dependabot', 'compliant', 'not_applicable', NULL, 'gate_closed', 'none', 'none', 'v2:e2e', now() - interval '9 days');

INSERT INTO repository_events (repository_id, kind, detail, occurred_at) VALUES
  (1, 'discovered', '{}', now() - interval '60 days'),
  (3, 'parked', '{"reason": "archived"}', now() - interval '10 days');

INSERT INTO policy_versions (version, first_seen_at, rollout_completed_at, summary) VALUES
  ('v2:e2e', now() - interval '12 days', now() - interval '11 days',
   '{"rules": [
      {"kind": "file", "name": "codeowners", "description": "Every repository names its owners.", "check_mode": "exists"},
      {"kind": "file", "name": "renovate", "description": "Dependencies are kept current by Renovate.", "check_mode": "contains"},
      {"kind": "file", "name": "dependabot", "check_mode": "exists", "ignore": ["acme/legacy-*"]}
    ]}');

INSERT INTO service_runs (kind, outcome, started_at, finished_at) VALUES
  ('discovery', 'success', now() - interval '41 minutes', now() - interval '40 minutes'),
  ('snapshot', 'success', now() - interval '3 hours', now() - interval '3 hours');

-- Thirty days of daily snapshots per (org, rule), drifting toward the
-- current counts.
INSERT INTO compliance_snapshots (org, rule_kind, rule_name, snapshot_at, compliant, non_compliant, not_applicable, unknown)
SELECT o.org, 'file', r.rule, date_trunc('day', now()) - (d || ' days')::interval,
       o.size - (d % 3), (d % 3), 0, 0
FROM (VALUES ('acme', 10), ('globex', 4)) AS o(org, size)
CROSS JOIN (VALUES ('codeowners'), ('renovate'), ('dependabot')) AS r(rule)
CROSS JOIN generate_series(1, 30) AS d;
