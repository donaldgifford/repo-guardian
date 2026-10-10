# Controls schema chain

The controls line's goose chain (DESIGN-0032 § Data model, IMPL-0028
Phase 6). It is applied to an **empty database** (D27) by
`repo-guardian migrate --chain controls`, and never to a database that
holds the rc line's `migrations_v2` chain: the two share table names.

| File | Owner | Contents |
| --- | --- | --- |
| `00001_core.sql` | IMPL-0028 | repositories, repository_events, policy_versions (with `activated_at`), checks, service_runs, and their grants |
| `00002_controls_policy.sql` | IMPL-0029 | reserved |
| `00003_controls_results.sql` | IMPL-0029 | reserved |
| `00004_compliance.sql` | IMPL-0030 | reserved |

Run it as the schema owner (no `CREATEROLE`). `rg_evaluator` and
`rg_remediator` must exist first: the chain grants to them and never
creates them (D31). The version table is `goose_controls_version`, so
pointing this chain at an rc database fails on the first `CREATE TABLE`
instead of reading the rc's version.
