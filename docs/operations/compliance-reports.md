# Compliance reports

`repo-guardian report` writes one markdown compliance report per
organisation: what percentage of repositories satisfy each rule, how
that moved since the last snapshot, and — the part no metric can give
you — **which** repositories are failing **which** rules, **since
when**.

A gauge knows how many. It never knows which. This is the command for
the second question.

See DESIGN-0022 §Compliance snapshots and the per-org report, and
IMPL-0023 Phase 4.

## Quick start

```bash
export STORE_DSN='postgres://repo-guardian:...@postgres:5432/repo_guardian?sslmode=require'
repo-guardian report --out ./reports
```

Writes `./reports/<org>.md`, one file per org, and prints each path on
stdout. This subcommand logs to **stderr** — unlike the server, which
logs to stdout — so the path list is safe to pipe:

```bash
repo-guardian report --out ./reports | xargs -I{} cp {} /srv/compliance/
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--out` | `./reports` | Directory to write into. Created if absent (mode `0750`). |
| `--dsn` | `$STORE_DSN` | Postgres DSN. The command fails immediately if neither is set. |
| `--orgs` | every org | Comma-separated orgs to report on (case-insensitive). |
| `--with-pr-links` | off | **Deprecated no-op** (v2): warns and does nothing. Removed in the next release. |

PR links come from each finding's recorded evidence, so the report makes
no GitHub API calls and needs no App credentials. The PR column shows
`open` for repo-guardian's own PR, `human PR` for a PR the rule yields
to (`foreign_pr`), `dry run` or `remediation off` when no PR will be
opened, and an em dash otherwise.

## What the command does not do

**It does not run migrations.** The report is read-only and the DSN you
hand it may have no DDL rights. It checks the schema version instead and
refuses a database `repo-guardian migrate` has not brought up to date. More importantly, an operator running a
newer binary from a laptop would otherwise migrate the schema forward
underneath a running older server. Schema changes belong to the
deployment, not to a reporting CLI. See
[Postgres schema operations](migrations.md).

**It does not load the server configuration.** No webhook secret, no
Valkey DSN, no App credentials. A
read-only report has no use for any of it, and being told to set a
webhook secret for a command that never serves a webhook would be
absurd.

**It does not need the server to be stopped**, or a leader, or a lock.
It is one consistent read.

## Reading the report

### The headline

```
21 of 24 rule evaluations pass across 3 rule(s).
```

Evaluations, not repositories. A repository failing two rules
contributes two failing evaluations. **"How many repositories fail at
least one rule" is deliberately not reported** — it is not derivable
from per-rule counts, because the overlap between rules is unknown:
summing over-counts and taking the maximum under-counts. The findings
table below the headline is where you count repositories.

### Compliance by rule

The numbers come from one shared query (`queries/compliance.sql`) that
the compliance snapshots and the API also read, so the three always
agree. Each rule counts its findings on active repositories by status:
**Failing** (non-compliant), **Passing** (compliant), **N/A** (the rule
does not apply: out of scope, ignored, gate closed, branch missing) and
**Unknown** (it could not be evaluated). Compliant is
Passing / (Passing + Failing); N/A and Unknown sit beside it, never in
it. The denominator is **per rule**, not per org.

This matters for scoped rules. A rule that applies to 10 of 100
repositories and fails on 5 reads as **50%** here. Against an org-wide
denominator the same rule would read as 5%, which describes the
organisation rather than the rule and makes real non-compliance look
like rounding error.

Percentages are **floored, never rounded**. 1999 of 2000 reads as
99.9%, not 100.0%. A report calling a fleet fully compliant while one
repository is not has told a lie somebody will act on.

A rule with **`n/a`** in the compliance column passed or failed on no
repository — typically every finding is N/A. It is unmeasured, not
perfect.

### Trend

Each trend cell carries the date it was compared against:

```
| codeowners | file | 2 | 8 | 0 | 0 | 80.0% | 3 fewer since 2026-08-03 |
```

The baseline is **per rule**, not per report. A rule that was disabled
when the last snapshot ran is compared against the last run that
actually measured it, which can be an older date than its neighbours'.
That is why the date is in every cell rather than in the header.

A rule reads **`new`** when no snapshot exists for it — never `0` and
never "no change", both of which would claim a measurement nobody took.

The trend column is absent entirely until at least two snapshots exist.
The report says so in place of the column.

A rule that appears in the history but is no longer evaluated is
**omitted**, not shown at its stale value. It stopped being measured;
reporting a percentage for it would describe a measurement nobody took
today.

### Findings

One row per (repository, rule) currently failing, with its reason code
(`file_missing`, `assertion_failed`, `setting_mismatch`, …) and the date
the failure started. The date survives repeated sweeps: "missing since
2026-06-14" does not reset to today every time the sweeper confirms it
is still missing. It **is** cleared when the repository complies, so a
later regression starts a fresh clock rather than reporting a
months-old date for a failure that was fixed in between.

A reason of **`migrated_from_v1`** means repo-guardian v1 recorded the
failure and v2 has not re-checked the repository since the upgrade; the
date is v1's original one. The first v2 check replaces it with a real
reason.

### What is excluded

Parked repositories — archived, forked, or unreadable by the App
(INV-0015) — are excluded from every number in the report, which says
so in its footer. A repository nobody can measure is neither compliant
nor failing, and counting it either way would be a guess. Use the
`repos_parked_total{org, reason}` metric to watch that population.

## Distribution

**One file per org is an access-control decision, not a formatting
one.** A combined document invites sending the whole fleet's weaknesses
to a team that should only see their own. Reports are written `0640` in
a `0750` directory for the same reason: they name internal repositories
and their specific weaknesses.

The command refuses an org name containing a path separator rather than
sanitizing it. Sanitizing is the tempting choice and the wrong one —
any rewriting rule can map two distinct orgs onto one filename, so one
org's report would silently overwrite another's. GitHub org names
cannot contain a separator, so this is unreachable in practice and
exists to stay that way.

A scheduled run as a `CronJob` is the intended shape:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: repo-guardian-report
  namespace: repo-guardian
spec:
  schedule: "0 6 * * 1"          # Monday 06:00, after the weekend's snapshots
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: report
              # Pin to the same tag as the running Deployment. A
              # report from a different binary can disagree with the
              # server about what the schema means.
              image: ghcr.io/donaldgifford/repo-guardian:<appVersion>
              args: ["report", "--out", "/reports"]
              env:
                - name: STORE_DSN
                  valueFrom:
                    secretKeyRef:
                      name: repo-guardian-store
                      key: STORE_DSN
              volumeMounts:
                - name: reports
                  mountPath: /reports
          volumes:
            - name: reports
              persistentVolumeClaim:
                claimName: repo-guardian-reports
```

Give the CronJob a **read-only database role**. It never writes, and a
role that cannot write is the cheapest possible guarantee that a
reporting job can never damage the state the server depends on.

## Snapshot cadence

The trend column reads from `compliance_snapshot`, written by the
`compliance-snapshot` schedule handler inside the server. It is
independent of the report command — the report only reads what the
handler already stored.

| Knob | Default | Notes |
|---|---|---|
| `COMPLIANCE_SNAPSHOT_INTERVAL` | `24h` | Cadence between snapshots. |

Daily is the floor that makes quarter-over-quarter comparison possible
without making the table large. Going faster does not make the report
more accurate — the live numbers in the report are computed fresh on
every run, from the same query the snapshot uses. Only the **trend
baseline** comes from history, and a baseline finer than daily is a
baseline nobody asked a question about.

!!! note "Chart wiring"
    `COMPLIANCE_SNAPSHOT_INTERVAL` is not yet a first-class chart
    value; set it through `extraEnv` until the chart bump lands
    (IMPL-0023 task 7.3):

    ```yaml
    extraEnv:
      - name: COMPLIANCE_SNAPSHOT_INTERVAL
        value: "24h"
    ```

### Leader-gating is load-bearing here

The handler runs under the same Valkey leader election as `stale-sweep`
and the posture exporter, but for a stronger reason. Running the
posture exporter on every replica merely duplicates effort. Running
**this** on every replica would corrupt the history: each replica
inserts its own rows at its own timestamp, so a quarter-over-quarter
query would count the same state N times, or once, depending on whether
the clocks happened to agree.

A failed snapshot is logged and not retried within the interval. A
missed snapshot leaves a visible, harmless gap in a daily series; a
retry loop against a database that is already struggling is neither.

## Retention

**None ships, on purpose.** Volume is orgs × rules rows per day —
roughly 120/day at target scale, about 44,000 rows a year. At that size
retention machinery costs more to operate and reason about than the
storage it would reclaim, and deleting compliance history is exactly
the wrong default for a table whose entire purpose is answering "how
compliant were we last quarter" after Prometheus retention has long
since dropped the gauges.

Revisit this if the fleet grows by orders of magnitude, not ahead of
need. If you do need to prune, the table is a plain `DELETE` on
`snapshot_at` with no foreign keys pointing at it:

```sql
DELETE FROM compliance_snapshot WHERE snapshot_at < now() - interval '3 years';
```

Take a backup first. There is no other copy of this data — the whole
point is that it outlives the metrics store.

## Troubleshooting

**"no database given; pass --dsn or set STORE_DSN"** — neither was set.
The command refuses rather than guessing at a local Postgres.

**Every rule reads `new`, no trend column** — the server has not taken
two snapshots yet. Check that a leader exists
(`scheduler_is_leader{name="compliance-snapshot"}`) and that
`COMPLIANCE_SNAPSHOT_INTERVAL` has elapsed twice since the schema
migration.

**A report is empty / no files written** — nothing has been evaluated
yet. `findings` is written by each check, so a fresh deployment has no
rows until the first checks complete. Confirm with
`SELECT count(*) FROM findings;`.

**An org you expect is missing** — orgs come from what was actually
evaluated, not from the policy's `scope` block. An org with no
evaluated rules produces no file rather than an empty one. Check
whether its repositories are all parked
(`repos_parked_total{org="..."}`) or out of scope.

**"database schema is older than this binary"** — the database has not been
migrated to this binary's schema. Run `repo-guardian migrate` (or let
the chart's migrate Job run) first.

## See also

- [Scaling repo-guardian](scaling.md) — posture exporter and metric catalog
- [Postgres schema operations](migrations.md) — migrations and backups
- [Delayed requeue runbook](delayed-requeue-runbook.md) — why a repository may be stale rather than failing
