-- Seed for the Playwright suite over the controls schema (IMPL-0029
-- 7.7): two orgs, so a leak of a name onto the aggregate-only status
-- page is visible; checks, service runs, an activated policy version
-- and thirty days of snapshots feed every status component. Times are
-- relative to now() so ages stay stable.

INSERT INTO repositories (org, name, provider_repo_id, active, park_reason, parked_at, discovered_at, next_due_at, last_checked_at)
VALUES
  ('acme', 'web', 501, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '2 hours'),
  ('acme', 'api', 502, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '3 hours'),
  ('acme', 'legacy', 503, false, 'archived', now() - interval '10 days', now() - interval '60 days', NULL, now() - interval '10 days'),
  ('globex', 'site', 601, true, NULL, NULL, now() - interval '60 days', now() + interval '20 hours', now() - interval '1 hour');

INSERT INTO policy_versions (version, first_seen_at, activated_at, rollout_completed_at) VALUES
  ('controls:e2e', now() - interval '12 days', now() - interval '12 days', now() - interval '11 days');

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
