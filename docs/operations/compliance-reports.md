# Compliance reports

`repo-guardian report` writes one markdown compliance report per
organisation from the controls compliance query: for each assigned
control, how many assignments pass, fail, wait for a first evaluation,
are stale or cannot be measured; how much of the org is actually
measured; and which repositories and controls are excluded, and why.

See DESIGN-0030 § Compliance queries (D5, D16) and IMPL-0029 task 6.9.

## Quick start

```bash
export STORE_DSN='postgres://rg_owner:...@postgres:5432/repo_guardian?sslmode=require'
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

The rc's `--with-pr-links` no-op is gone: it now fails as an unknown
flag. The report makes no GitHub API calls and needs no App
credentials.

## What the command does not do

**It does not run migrations.** The report is read-only and the DSN you
hand it may have no DDL rights. It checks the controls schema version
instead and refuses a database `repo-guardian migrate --chain controls`
has not brought up to date. More importantly, an operator running a
newer binary from a laptop would otherwise migrate the schema forward
underneath a running older server. Schema changes belong to the
deployment, not to a reporting CLI. See
[Postgres schema operations](migrations.md).

**It does not load the server configuration.** No webhook secret, no
Temporal address, no App credentials, no policy root.

**It does not need the server to be stopped**, or a leader, or a lock.

## Reading the report

### The headline

```
Compliance: 82.0% (82 of 100 measured assignments pass).
Coverage: 98.0% of 105 assignments (2 pending, 0 stale, 0 unmeasurable, 0 unknown); 2 excluded.
```

Assignments, not repositories. A repository with three controls is
three assignments. **Compliance** is Passing / (Passing + Failing) over
every control in the org; **coverage** is measured (Passing + Failing +
N/A) over assigned. The two are always printed together: a fleet with
one evaluated repository and ninety-nine pending ones reads as "100.0%
(1 of 1)" beside "Coverage: 1.0%", never as a measured fleet.

### The control tables

Two tables, one per lineage:

- **Baseline controls** — the enterprise baseline, including an org's
  **replaced** version of a baseline control (the decision column says
  `replaced`).
- **Org-added controls** — controls the org assigned on its own.

Each row is one (control, decision) pair with every bucket:

| Column | Meaning |
|---|---|
| Failing / Passing | The last result, at the current assignment epoch and revision. |
| Pending | Assigned, not evaluated yet. |
| Stale | Evaluated under an older assignment epoch or control revision. |
| Unmeasurable | Access lost (`access_denied` / `unknown` park), the Evaluation App's installation suspended, or an ownership conflict. The last results are kept but nothing new was learned, so they count here, never as passing. |
| N/A / Unknown | The control does not apply / could not decide. |
| Excluded | Excluded by policy; listed in the exclusions table. |

The numbers come from the same SQL the API, the snapshots and the
`controls_status` gauges read, and the percentage is computed there —
floored, never rounded (1999 of 2000 reads 99.9%), and **`no data`**
when nothing is measured. A control assigned everywhere and evaluated
nowhere reads `no data`, never 100%.

### Exclusions

Every excluded assignment and excluded repository with its reason and
where it was excluded, plus conflicted assignments with their error.
Exclusions are never on either side of the percentage (D5): "92%
compliant, 14 exclusions" is honest, folding them in is not. An org
whose every repository is excluded still gets a report, saying so.

### What is not in it

Repositories parked `archived`, `fork`, `removed` or
`installation_removed` have left the fleet; their results were cleared
and they appear nowhere. There is no per-repository failing list and no
trend column on the controls line yet: the API and the UI
answer "which repository", and `compliance_snapshots` holds the history.

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

## Retention

**None ships for snapshots, on purpose.** Volume is orgs × controls ×
decisions rows per day — roughly 120/day at target scale, about 44,000 rows a year. At
that size retention machinery costs more to operate and reason about
than the storage it would reclaim, and deleting compliance history is
exactly the wrong default for a table whose entire purpose is answering
"how compliant were we last quarter" after Prometheus retention has long
since dropped the metrics.

Revisit this if the fleet grows by orders of magnitude, not ahead of
need. If you do need to prune, the table is a plain `DELETE` on
`snapshot_at` with no foreign keys pointing at it:

```sql
DELETE FROM compliance_snapshots WHERE snapshot_at < now() - interval '3 years';
```

Take a backup first. There is no other copy of this data — the whole
point is that it outlives the metrics store.

## Snapshot cadence

`compliance_snapshots` is written by the controls snapshot workflow,
one row per `ControlCompliance` row (org, control, decision) in the same
transaction as the read, so a snapshot never disagrees with the live
view. The report reads live state only.

## Troubleshooting

**"no database given; pass --dsn or set STORE_DSN"** — neither was set.
The command refuses rather than guessing at a local Postgres.

**A report is empty / no files written** — nothing is assigned yet.
Assignments are written when the evaluator resolves the policy for each
discovered repository; confirm with
`SELECT count(*) FROM control_assignments;`.

**Everything reads `no data`** — assignments exist and nothing has been
evaluated: the Pending column says how many. First evaluations are
spread across `EVAL_INTERVAL` after discovery or a policy rollout.

**An org you expect is missing** — orgs come from assignments and
exclusions, not from `enterprise.orgs`. An org with neither produces no
file rather than an empty one.

**"database schema is older than this binary"** — run
`repo-guardian migrate --chain controls` (or let the chart's migrate Job
run) first.

## See also

- [Postgres schema operations](migrations.md) — migrations and backups
- [Scaling repo-guardian](scaling.md) — metric catalog
