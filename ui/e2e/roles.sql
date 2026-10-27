-- The controls chain grants to these roles and never creates them
-- (IMPL-0028 6): the e2e stack creates them before `migrate` runs, the
-- way the chart's roles hook does.
CREATE ROLE rg_evaluator NOLOGIN;
CREATE ROLE rg_remediator NOLOGIN;
GRANT USAGE ON SCHEMA public TO rg_evaluator, rg_remediator;
