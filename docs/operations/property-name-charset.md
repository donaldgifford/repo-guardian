# Custom-property name charset correction (appVersion 1.10.1)

repo-guardian validates the GitHub custom property names declared in an
`annotation_properties` map against GitHub's charset constraint. That
validation was wrong on both edges until appVersion 1.10.1
([INV-0011](../investigation/0011-tech-debt-cleanup-inventory-post-impl-0019.md)
finding A6).

| | Before (≤ 1.10.0) | After (≥ 1.10.1) |
|---|---|---|
| Pattern | `^[a-zA-Z0-9_.-]{1,75}$` | `^[a-zA-Z0-9_$#-]{1,75}$` |
| `Cost$Center`, `Team#1` | rejected at load | **accepted** |
| `jira.project` | accepted at load, 422 at sync | **rejected at load** |

GitHub's documented set is alphanumerics plus hyphen, underscore, dollar
sign and number sign — no period.

## Does this affect me?

Only if a `guardian.hcl` declares an `annotation_properties` target
containing a period:

```hcl
reconcile "custom_properties" {
  annotation_properties = {
    "jira/project-key" = "jira.project"   # ← period: no longer loads
  }
}
```

Everything else is unaffected. This is validation of the map's *values*
(GitHub property names), not its keys — annotation keys like
`jira/project-key` are unconstrained and keep working.

## What changes for a dotted name

Startup now fails with a load error naming the rule and the offending
value, instead of starting cleanly and then failing every sync with a
`422 Unprocessable Entity` from GitHub. The property was never actually
writable; the only change is *when* you find out.

Failing at load is deliberate: a 422 at sync time is per-repo, buried in
reconcile logs, and easy to mistake for a permissions problem, while a
load error is immediate, names the exact attribute, and cannot be
mistaken for anything else.

## Upgrade steps

1. Grep your policy for dotted targets:

   ```bash
   grep -A 10 'annotation_properties' guardian.hcl
   ```

2. Rename any target containing a period to a supported form — `_`, `-`,
   `$` and `#` are all available (`jira.project` → `JiraProject` or
   `jira-project`).

3. If the old name was already defined in your **org's** custom-property
   schema, add the new one there too and remove the old. GitHub property
   names are unique case-insensitively, so pick the new name first and
   confirm it does not collide.

4. Roll out. The renamed property is a different property from GitHub's
   point of view: the first reconcile after the rename populates it, and
   any value stored under the old dotted name is left untouched (it is
   outside the managed set once the map no longer references it — see
   [policy-reference](../usage/policy-reference.md)). Delete the old
   property from the org schema when you are satisfied.

## Rollback

Downgrading below 1.10.1 restores the old pattern, so a dotted name will
load again — and resume 422-ing at sync time. Names using `$` or `#`
will conversely stop loading. Prefer rolling the policy forward with a
supported name over pinning the binary.
