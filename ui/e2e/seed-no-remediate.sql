-- The "not enabled" seed (IMPL-0032 8.7, OQ6): applied after seed.sql
-- to the second database, it takes the Remediation App away — its
-- installation, its access rows, every remediation and its journal, the
-- maintenance and sweep runs — and leaves what an evaluation-only
-- deployment would hold: every acme assignment evaluated, not
-- remediated, and no result held on a branch. globex stays invisible to
-- ada.

DELETE FROM remediations;
DELETE FROM service_runs WHERE kind IN ('maintenance', 'sweep');
DELETE FROM app_repository_access WHERE app = 'remediate';
DELETE FROM app_installations WHERE app = 'remediate';

UPDATE control_assignments SET effective_mode = 'evaluate', mode_source = 'no_remediation_app' WHERE effective_mode = 'remediate';
UPDATE control_results SET hold = NULL, hold_since = NULL WHERE hold IS NOT NULL;
UPDATE repository_policy_state SET last_remediation_at = NULL;
