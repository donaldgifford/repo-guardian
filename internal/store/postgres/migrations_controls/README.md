# Controls schema chain

The controls line's goose chain (DESIGN-0032 § Data model, IMPL-0028
Phase 6). It is applied to an **empty database** (D27) by
`repo-guardian migrate --chain controls`, and never to a database that
holds the rc line's `migrations_v2` chain: the two share table names.

| File | Owner | Contents |
| --- | --- | --- |
| `00001_core.sql` | IMPL-0028 | repositories, repository_events, policy_versions (with `activated_at`), checks, service_runs, and their grants |
| `00002_controls_policy.sql` | IMPL-0029 | repository_policy_state, control_assignments, the epoch sequence, `assignment_changed`, app_installations and app_repository_access (RLS: each App writes its own rows, both read all) |
| `00003_controls_results.sql` | IMPL-0029 | control_results, rule_results, result_events, remediations (RLS: remediator deletes only reservations), remediation_steps, remediation_events, the remediation_due and remediation_maintenance views, and the full grant matrix |
| `00004_compliance.sql` | IMPL-0029 | compliance_snapshots keyed `(org, control_id, decision, snapshot_at)` with every posture bucket, the excluded count, percent and coverage |
| `00005_remediation.sql` | IMPL-0030 | additive changes over 00003 (which stays the GA schema, OQ1): remediation_maintenance withdraws on an evaluate downgrade |
| `00006_remediation_due_no_row.sql` | IMPL-0030 | remediation_due: a control with no remediation row is due on its generation alone (no NULL intent comparison) |

Run it as the schema owner (no `CREATEROLE`). `rg_evaluator` and
`rg_remediator` must exist first: the chain grants to them and never
creates them (D31). The version table is `goose_controls_version`, so
pointing this chain at an rc database fails on the first `CREATE TABLE`
instead of reading the rc's version.
