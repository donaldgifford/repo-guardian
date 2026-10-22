-- Issue #232: RequireControlsSchema runs as each controls worker's own
-- role (rg_evaluator, rg_remediator; rg_all inherits both) and reads
-- goose_controls_version, so those roles need USAGE on schema public and
-- SELECT on the version table. Baked mode granted USAGE in its init
-- script, CNPG's managed.roles granted nothing, and no mode granted the
-- table, so an rc.13+ pod on CNPG crash-looped on "permission denied for
-- schema public".
--
-- The owner runs this. Under CNPG it owns public (pg_database_owner), so
-- the USAGE grant takes; where it holds USAGE without GRANT OPTION
-- (baked's rg_owner, external setups) Postgres warns and grants nothing,
-- which is fine because the admin SQL already granted it there. The
-- version table is created by goose before this runs; ControlsDryRun
-- replays Up sections without it, hence the to_regclass guard.

-- +goose Up
-- +goose StatementBegin
GRANT USAGE ON SCHEMA public TO rg_evaluator, rg_remediator;

DO $$
BEGIN
    IF to_regclass('public.goose_controls_version') IS NOT NULL THEN
        GRANT SELECT ON public.goose_controls_version TO rg_evaluator, rg_remediator;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF to_regclass('public.goose_controls_version') IS NOT NULL THEN
        REVOKE SELECT ON public.goose_controls_version FROM rg_evaluator, rg_remediator;
    END IF;
END
$$;
-- +goose StatementEnd
