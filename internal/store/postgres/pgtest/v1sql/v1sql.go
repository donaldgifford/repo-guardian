// Package v1sql holds verbatim copies of the v1 store's two hot queries,
// the stale sweep and the rule_state upsert. The rollback test runs them
// against a v2-migrated database to prove a v1 image still works there;
// copying them keeps that proof alive after IMPL-0025 Phase 16 deletes
// the v1 store. Do not edit them to track v2: they are what v1 shipped.
package v1sql

// StaleRepos is v1's stale-sweep query. Args: cutoff time, current
// policy version, limit.
const StaleRepos = `
	SELECT installation_id, owner, repo,
	       last_checked_at, last_check_status, last_error, policy_version
	FROM repo_state
	WHERE active
	  AND (last_checked_at IS NULL
	       OR last_checked_at < $1
	       OR policy_version <> $2)
	ORDER BY last_checked_at NULLS FIRST
	LIMIT $3
`

// UpsertRuleState is v1's rule_state upsert. Args: installation id,
// owner, repo, rule name, rule kind, actionable, policy version.
const UpsertRuleState = `
	INSERT INTO rule_state (
		installation_id, owner, repo, rule_name, rule_kind,
		actionable, actionable_since, policy_version, updated_at
	) VALUES (
		$1, $2, $3, $4, $5,
		$6, CASE WHEN $6 THEN now() ELSE NULL END, $7, now()
	)
	ON CONFLICT (installation_id, owner, repo, rule_name) DO UPDATE SET
		actionable = EXCLUDED.actionable,
		actionable_since = CASE
			WHEN NOT rule_state.actionable AND EXCLUDED.actionable THEN now()
			WHEN NOT EXCLUDED.actionable                           THEN NULL
			ELSE rule_state.actionable_since
		END,
		rule_kind      = EXCLUDED.rule_kind,
		policy_version = EXCLUDED.policy_version,
		updated_at     = now()
`
