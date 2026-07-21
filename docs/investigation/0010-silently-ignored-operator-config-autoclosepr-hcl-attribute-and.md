---
id: INV-0010
title: "Silently ignored operator config: auto_close_pr HCL attribute and cross-mode existingSecret"
status: Resolved
author: Donald Gifford
created: 2026-07-20
---
<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0010: Silently ignored operator config: auto_close_pr HCL attribute and cross-mode existingSecret

**Status:** Resolved
**Author:** Donald Gifford
**Date:** 2026-07-20

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Findings](#findings)
  - [Observation 1 — autoclosepr in HCL is dead: two independent gaps](#observation-1--autoclosepr-in-hcl-is-dead-two-independent-gaps)
  - [Observation 2 — the root cause is the guardian block's permissive decode](#observation-2--the-root-cause-is-the-guardian-blocks-permissive-decode)
  - [Observation 3 — chart existingSecret knobs are silently ignored cross-mode](#observation-3--chart-existingsecret-knobs-are-silently-ignored-cross-mode)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Two operator-config surfaces appear to accept configuration that has no
effect. (1) Does `auto_close_pr = false` in the HCL `guardian {}` block
actually work, or is only the `AUTO_CLOSE_PR` env var honored? (2) Does
setting a `store.postgres.existingSecret` / `queue.valkey.existingSecret`
(or their `baked.existingSecret` counterparts) under a mode that doesn't use
them produce any error, or is the value silently ignored?

## Hypothesis

Both are silent no-ops: the loader never wires `auto_close_pr` from HCL, and
the chart's secret-name dispatch selects by `mode` without checking whether
an operator-supplied secret is being ignored. Both bugs share a failure
class — operator intent accepted but discarded without any signal.

## Context

Both surfaced operationally. The `auto_close_pr` gap was found while reading
`setGuardianAttr` during the custom-properties debugging session (2026-07).
The cross-mode `existingSecret` confusion bit the homelab EKS rollout: an
external-style secret was set while `store.postgres.mode=baked`, the chart
silently used its own generated secret instead, and the resulting
username/password mismatch cost a debugging session (context that led to
PR #161, which added `baked.existingSecret` — but added no guard against
setting the wrong knob for the mode).

**Triggered by:** custom-properties debugging session + baked-backend EKS
rollout (PRs #160/#161 context).

## Approach

1. Trace `guardian {}` block decode and merge in `internal/policy/loader.go`.
2. Enumerate `GuardianConfig` fields vs `setGuardianAttr` cases and
   `mergeGuardianConfig` carries.
3. Compare the guardian block's decode style against the other HCL blocks.
4. Enumerate every chart `existingSecret` knob and the mode branches that
   consume it (`_helpers.tpl` dispatch, secret/statefulset templates).

## Findings

### Observation 1 — `auto_close_pr` in HCL is dead: two independent gaps

`GuardianConfig.AutoClosePR *bool` exists with an `hcl:"auto_close_pr,optional"`
tag and a documented HCL contract (`types.go:80-85`), and
`docs/usage/policy-reference.md` documents the attribute. But the string
`auto_close_pr` appears **nowhere in loader.go**:

1. `setGuardianAttr` (`loader.go:383-411`) has no `case "auto_close_pr"` —
   the decoded value is dropped on the floor.
2. `mergeGuardianConfig` (`loader.go:943-986`) never carries `AutoClosePR`
   from the raw HCL guardian block into the effective config — so even with
   gap 1 fixed, the value would be lost at merge time.

Only `applyEnvBoolPtr("AUTO_CLOSE_PR", ...)` (`loader.go:1001`) works today.
An operator writing `auto_close_pr = false` in `guardian.hcl` gets
auto-close behavior anyway (default true via `AutoClosePREnabled()`), with
no error, warning, or log.

### Observation 2 — the root cause is the guardian block's permissive decode

`decodeGuardianBlock` uses `block.Body.JustAttributes()` (`loader.go:364`),
which accepts **any** attribute name; unmatched names fall through
`setGuardianAttr`'s switch silently. Every other HCL block in the loader
(`reconcile`, rule bodies) decodes through a strict `hcl.BodySchema` +
`Content()`, which fails load with an "Unsupported argument" diagnostic for
unknown attributes.

Consequences of the permissive decode:

- The `auto_close_pr` class of bug (declared field, missing switch case) is
  invisible — config is accepted and discarded.
- Typos (`auto_close = false`) and stale attributes are swallowed. A dead
  `org = "myorg"` sat in `examples/guardian-full.hcl` until 2026-07 —
  no `Org` field exists anywhere in `internal/policy` — and nothing ever
  flagged it.

### Observation 3 — chart existingSecret knobs are silently ignored cross-mode

Four knobs exist post-PR #161, each consumed by exactly one mode:

| Knob | Consumed by | Ignored under |
|---|---|---|
| `store.postgres.existingSecret` | `mode=external` (`_helpers.tpl:112-120` `required` guard) | `baked`, `cnpg` |
| `store.postgres.baked.existingSecret` | `mode=baked` | `cnpg`, `external` |
| `queue.valkey.existingSecret` | `mode=external` | `baked` |
| `queue.valkey.baked.existingSecret` | `mode=baked` | `external` |

The `_helpers.tpl` secret-name dispatch branches on `mode` first, so a knob
set under the wrong mode is never read. `values.schema.json` validates enums
and ranges but has no cross-field conditionals. Net effect: `helm template`
succeeds, the deployment comes up using a different credential source than
the operator intended, and the failure surfaces later as auth errors (the
homelab symptom) or as "my secret rotation did nothing."

## Conclusion

**Answer:** Hypothesis confirmed on both counts. (1) `auto_close_pr` is
env-var-only; the HCL attribute is dead via two independent gaps, enabled by
the guardian block's permissive decode. (2) All four chart `existingSecret`
knobs are silently ignored when set under a mode that doesn't consume them;
there is no render-time guard.

## Recommendation

Fix both on one branch/PR (separate commits) per IMPL-0018:

1. **Loader:** wire `auto_close_pr` through `setGuardianAttr` AND
   `mergeGuardianConfig` (preserving the `*bool` set-vs-unset distinction;
   env override continues to win via `applyEnvOverrides` running last).
   Consider converting the guardian decode to a strict `hcl.BodySchema` so
   the whole bug class — and operator typos — fail at load (IMPL-0018 OQ).
2. **Chart:** render-time guard failing `helm template` with an actionable
   message when any `existingSecret` knob is set under a mode that ignores
   it (mechanism and coverage per IMPL-0018 OQs).

## References

- `internal/policy/loader.go` — `decodeGuardianBlock` (:361-381),
  `setGuardianAttr` (:383-411), `mergeGuardianConfig` (:943-986),
  `applyEnvOverrides`/`applyEnvBoolPtr` (:988-1023)
- `internal/policy/types.go:66-100` — `GuardianConfig`, `AutoClosePREnabled`
- `charts/repo-guardian/templates/_helpers.tpl:112-140` — secret dispatch
- `charts/repo-guardian/values.schema.json` — current enum/range-only shape
- PR #161 — added `baked.existingSecret` (fix that exposed the guard gap)
- IMPL-0013 Phase 3 — introduced `AutoClosePR` (`AUTO_CLOSE_PR` env path)
- IMPL-0018 — implementation plan for both fixes
