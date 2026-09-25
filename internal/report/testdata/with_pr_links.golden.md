# Compliance report: acme

Generated 2026-08-10 14:30:00 UTC by repo-guardian.

30 of 33 rule evaluations pass across 4 rule(s).

## Compliance by rule

| Rule | Kind | Failing | Passing | N/A | Unknown | Compliant | Trend |
|---|---|---:|---:|---:|---:|---:|---|
| codeowners | file | 2 | 8 | 0 | 0 | 80.0% | 3 fewer since 2026-08-03 |
| dependabot | file | 0 | 10 | 0 | 0 | 100.0% | no change since 2026-08-03 |
| renovate | file | 1 | 3 | 6 | 0 | 75.0% | new |
| vuln_alerts | setting | 0 | 9 | 0 | 1 | 100.0% | new |


## Findings

| Repository | Rule | Reason | Failing since | PR |
|---|---|---|---|---|
| api | codeowners | file_missing | 2026-07-01 | [open](https://github.example/acme/api/pull/7) |
| web | codeowners | migrated_from_v1 | 2026-05-14 | [human PR](https://github.example/acme/web/pull/3) |
| web | renovate | assertion_failed | 2026-08-09 | dry run |


---

Compliant is Passing / (Passing + Failing). N/A (the rule does not apply
to the repository) and Unknown (it could not be evaluated) are counted
beside the percentage, never in it. A `migrated_from_v1` reason means
repo-guardian v1 recorded the failure and v2 has not re-checked it yet;
its date is v1's.

Parked repositories — archived, forked, or unreadable by the App — are
excluded from every number above. A repository nobody can measure is
neither compliant nor failing, and counting it either way would be a
guess.
