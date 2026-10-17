# Compliance report: acme

Generated 2026-08-10 14:30:00 UTC by repo-guardian.

Compliance: 82.0% (82 of 100 measured assignments pass).
Coverage: 98.0% of 105 assignments (2 pending, 0 stale, 0 unmeasurable, 0 unknown); 2 excluded.

## Baseline controls

| Control | Decision | Failing | Passing | Pending | Stale | Unmeasurable | N/A | Unknown | Excluded | Compliant | Coverage |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| codeowners | baseline | 8 | 40 | 2 | 0 | 0 | 0 | 0 | 0 | 83.3% | 96.0% |
| renovate | replaced | 10 | 30 | 0 | 0 | 0 | 0 | 0 | 2 | 75.0% | 100.0% |

## Org-added controls

| Control | Decision | Failing | Passing | Pending | Stale | Unmeasurable | N/A | Unknown | Excluded | Compliant | Coverage |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| labels | org_added | 0 | 12 | 0 | 0 | 0 | 3 | 0 | 0 | 100.0% | 100.0% |

## Exclusions

| Repository | Control | State | Reason | Excluded by |
|---|---|---|---|---|
| legacy | renovate | excluded | frozen until Q4 | orgs/acme.hcl |
| sandbox | all controls | excluded | throwaway experiments |  |

---

Compliant is Passing / (Passing + Failing), floored to one decimal, and
reads "no data" when nothing is measured. Coverage is measured
(Passing + Failing + N/A) over assigned. Pending (assigned, not yet
evaluated), Stale (evaluated under an older assignment or revision),
Unmeasurable (access lost, installation suspended, or an ownership
conflict) and Unknown are counted beside the percentage, never in it.

A replaced control is the org's version of a baseline control and
counts in the baseline lineage. Exclusions are never in either side of
the percentage: they are listed above with their reasons.
