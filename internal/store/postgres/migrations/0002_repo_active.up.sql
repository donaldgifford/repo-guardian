-- INV-0015: a repository the App cannot read must stop being re-enqueued.
--
-- The check path aborts on GetRepository (engine.go), so an inaccessible
-- repo fails every attempt, exhausts MAX_JOB_ATTEMPTS, and is then handed
-- straight back by the next stale sweep — burning the full attempt budget
-- per cycle, forever, against a repo that will never succeed.
--
-- active gates the sweep. Only discovery may set it back to true: it is
-- the one component that observes the installation's real repo set, so a
-- repo whose permissions are restored rejoins the normal flow on the next
-- discovery pass with no operator action.
ALTER TABLE repo_state
    ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;

-- The sweep reads only active rows, so index the common case rather than
-- the whole table.
CREATE INDEX idx_repo_state_active_freshness
    ON repo_state(last_checked_at NULLS FIRST)
    WHERE active;
