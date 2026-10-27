# Policy reference (policy directory)

The specification of repo-guardian's controls policy: the files in the
policy directory, the blocks and attributes each one holds, how a
repository's controls are resolved, and what fails load. The attributes
of each control type live in the generated
[Control-type reference](controls-reference.md); this page does not
repeat them.

Source of truth: `internal/policy/` (`load.go`, `enterprise.go`,
`org.go`, `catalogue.go`, `layers.go`, `resolve.go`, `validate.go`,
`ownership.go`). When this page and the loader disagree, the loader wins.

The worked examples are the policy roots under `examples/policy/`. Every
one of them is loaded by the test suite, so the snippets quoted below
parse as written.

| Example | What it shows |
| ------- | ------------- |
| `examples/policy/minimal` | one org, two baseline controls |
| `examples/policy/full` | every built-in control type once, `exclude_repos` and a `repos` block |
| `examples/policy/multi-org` | two orgs adding different controls, one of them evaluate-only |
| `examples/policy/enterprise` | several orgs using `replace`, `exclude`, modes and remediation settings |
| `examples/policy/renovate` | Renovate in place of Dependabot |

## The policy directory

`GUARDIAN_CONFIG` names the policy root, a directory. Ingest, both
controls workers and the migrate Job read it. The layout is fixed:

```text
<root>/
  enterprise.hcl        # required: exactly one enterprise block
  catalogue/*.hcl       # control definitions
  orgs/*.hcl            # one org block per managed org that varies the baseline
  templates/            # optional: file and PR templates the catalogue names
```

- **`enterprise.hcl`** must exist at the root.
- **`catalogue/` and `orgs/`** hold `.hcl` files one level deep. File
  names are free; the blocks inside decide what they mean.
- **Any other `.hcl` file** anywhere under the root fails load.
- **Files that are not `.hcl`** are not parsed. They can still be read
  with `file()` or as templates.
- **Size limit:** every file is capped at 1 MiB, and the whole root has
  to fit one ConfigMap.

The root is read once at startup. There is no hot reload: a change takes
effect when the process restarts.

### Expressions: literals and `file()`

Attribute values are literals: strings, numbers, booleans, lists and
objects. The one function is `file("<path>")`, which returns the content
of a file under the root.

- The path is relative to the root and must stay inside it. An absolute
  path or a `..` that leaves the root fails load.
- `file()` reads from the same pass as everything else, so the file is
  part of the policy version.

There are no variables, no locals and no other functions.

### Symlinks and the kubelet `..data` layout

The root may be a ConfigMap projection.

- **Symlinks:** every file is resolved with `EvalSymlinks` and must land
  inside the resolved root. A link that escapes the root fails load.
- **`..data`:** when the root holds kubelet's `..data` link, the loader
  walks that one consistent snapshot.
- **Names starting with `..`:** skipped either way.

With the Helm chart, `policy.files` is the root as a nested map: a
string is a file, a map is a directory. Each file is mounted back at its
path. `policy.existingConfigMap` mounts an existing ConfigMap instead.

```yaml
policy:
  files:
    enterprise.hcl: |
      enterprise { ... }
    catalogue:
      codeowners.hcl: |
        control "codeowners" { ... }
    orgs:
      myorg.hcl: |
        org "myorg" { ... }
```

### Templates

A control's `template = "<name>"` resolves in this order:

1. `templates/<name>.tmpl` under the policy root.
2. `templates/<name>` under the policy root.
3. The embedded template of that name.

The root's templates override the embedded ones per policy. The embedded
templates a policy uses are hashed into its version, so upgrading the
binary with a changed embedded template counts as a policy change.
`examples/policy/enterprise/templates/codeowners-team.tmpl` is a root
template that `codeowners@2` names.

### The policy version

The loaded policy has a version: `controls:` followed by a sha256 over
these inputs:

- every file under the root;
- the embedded templates used;
- every definition's revision.

The migrate Job activates the version, and a new version starts a
rollout that re-evaluates the fleet. Editing a comment is a new version.

## `enterprise.hcl`

```hcl
enterprise {
  orgs     = ["myorg-prod", "myorg-staging"]
  mode     = "remediate"
  controls = ["codeowners@1", "dependabot@1"]
}
```

| Attribute / block | Type | Required | Meaning |
| ----------------- | ---- | -------- | ------- |
| `orgs` | list(string) | yes | The orgs repo-guardian manages. This list is onboarding: an org not listed is unmanaged, whatever else the policy says. |
| `mode` | string | no | `evaluate` or `remediate`. The fallback mode for every assignment. Unset means `evaluate`. |
| `controls` | list(string) | no | The baseline: `slug@version` references into the catalogue. Every listed org gets them, with or without a file under `orgs/`. |
| `remediation {}` | block | no | `max_open_prs` (number, at least 1) and `reopen_after` (duration string, at least `1h`). |

The enterprise policy is the baseline and has no variations: `exclude`,
`replace`, `exclude_repos`, `repos` and `control` blocks are refused here
with an error that says so.

Remediation settings resolve org, then enterprise, then the default:
`max_open_prs = 3`, `reopen_after = "336h"`.

## `catalogue/*.hcl`

A catalogue file holds `control` blocks. Each one defines one version of
one control.

```hcl
control "codeowners" {
  version     = 1
  type        = "codeowners"
  title       = "CODEOWNERS"
  description = "A CODEOWNERS file exists in one of GitHub's locations"
  template    = "codeowners"

  rule "exists" {
    number    = "1.1"
    title     = "A CODEOWNERS file exists"
    remediate = true
  }

  pr {
    title = "chore({{ .Repo }}): adopt CODEOWNERS"
  }
}
```

The slug is the block label. It matches `^[a-z][a-z0-9]*([_-][a-z0-9]+)*$`.
A control is referenced everywhere else as `slug@version`, for example
`codeowners@1`. One slug may have several versions in the catalogue
(`examples/policy/enterprise/catalogue/codeowners.hcl` defines `@1` and
`@2`). The same `slug@version` defined twice fails load.

| Attribute / block | Required | Meaning |
| ----------------- | -------- | ------- |
| `version` | yes | A positive integer. |
| `type` | yes | A built-in control type (see the [Control-type reference](controls-reference.md)). An unknown type fails load. |
| `title`, `description` | no | Display text. |
| `template` | no | The template the type renders for remediation (see [Templates](#templates)). |
| `rule "<id>" {}` | per type | One typed check. |
| `remediation { apply = "…" }` | no | How an API type writes: `recommend`, `direct` or, where the type supports it, `workflow`. |
| `pr {}` | no | Pull request text: `title`, `body` (templates, compiled at load) and `labels`. |
| any other attribute | per type | A control-level parameter of the type, e.g. `path` on `file`, `tool` and `renovate` on `dependency_updates`, `name` and `ruleset` on `branch_ruleset`, `annotation_properties` and `properties` on `custom_properties`. The type validates them when it builds the definition. |

### Rules

```hcl
rule "no-wiki" {
  kind      = "equals"
  property  = "has_wiki"
  value     = false
  remediate = true
}
```

- **The id** is the block label. It follows the slug pattern and is
  unique within the control.
- **`kind`** is one of the type's rule kinds. A rule without `kind`
  takes its id as its kind, which works only when the id is a kind of
  that type (`rule "exists"`, `rule "matches"`).
- **`remediate = true`** asks for the rule to be fixed in remediate mode.
  It is allowed only on a remediable kind.
- **`number` and `title`** are display text.
- **Any other attribute** is a parameter of the rule kind. Unknown
  parameters, missing required ones and wrong types fail load. The
  per-kind parameter tables are in the
  [Control-type reference](controls-reference.md).

### `apply` is for API types only

Pull request types (`file`, `codeowners`, `catalog_info`,
`dependency_updates`) remediate by pull request and refuse an `apply`
value. API types (`repo_settings`, `branch_ruleset`, `labels`,
`custom_properties`) take one.

- `recommend` reports the change without making it.
- `direct` writes it through the Remediation App.
- `workflow` is accepted only by a type that supports it (today
  `custom_properties`).

### A definition must pass its own template

At load, each definition goes through a dry run:

1. Evaluate an empty repository.
2. Remediate it.
3. Apply the rendered files.
4. Evaluate again.

A remediable rule that the rendered template leaves non-compliant fails
load. A template that cannot fix what it is meant to fix never reaches a
repository.

## `orgs/*.hcl`

An org file holds `org "<name>"` blocks. A missing org file is fine:
every org in `enterprise.orgs` gets the baseline as it is.

- **Org names** compare case-insensitively, as GitHub logins do.
- **An org not in `enterprise.orgs`** fails load. Onboarding is the
  enterprise list.
- **Each org has at most one block**, across all files.

```hcl
org "myorg" {
  exclude_repos {
    match  = [".github", "terraform-*", "archive-*"]
    reason = "org config, infrastructure and archives are managed elsewhere"
  }

  repos "legacy" {
    match = ["legacy-*"]

    exclude "protect_main@1" {
      reason = "legacy repositories predate the review policy"
    }
  }
}
```

| Attribute / block | In `org` | In `repos` | Meaning |
| ----------------- | -------- | ---------- | ------- |
| `mode` | yes | yes | `evaluate` or `remediate` for this layer. |
| `controls` | yes | yes | Controls this layer adds, as `slug@version`. |
| `replace "<slug@N>" { with, reason }` | yes | yes | Swap an assigned control for another. `with` is required. |
| `exclude "<slug@N>" { reason }` | yes | yes | Turn an assigned control off. |
| `control "<slug@N>" { mode }` | yes | yes | Set the mode of one control. `mode` is required. |
| `exclude_repos { match, reason }` | yes | no | Take matching repositories out of management entirely. `match` is required. |
| `repos "<label>" { match, … }` | yes | no | A group of repositories with its own layer. `match` is required. |
| `remediation {}` | yes | no | Overrides the enterprise's `max_open_prs` / `reopen_after`. |

`replace`, `exclude` and `exclude_repos` must carry a non-empty `reason`.
The reason is recorded on the assignment and shown in the report, the API
and the UI.

`match` is a list of globs (Go `path.Match` syntax) against the
repository name without its org, compared case-insensitively.
`examples/policy/enterprise/orgs/` shows `replace` (`payments.hcl`),
`exclude` with `exclude_repos` (`sandbox.hcl`) and a `repos` block
narrowing the mode (`platform.hcl`).

## Resolution

Each repository is resolved from its stored row and the two Apps' access
to it. Resolution makes no API calls. `Snapshot.Resolve` implements steps
1–6 below; step 7, the assignment epochs, belongs to the store. The
results are pinned by the goldens in `internal/policy/testdata/resolution`.

Before the steps:

- **Unmanaged:** a repository whose org is not in `enterprise.orgs` has
  no assignments.
- **Excluded:** a repository matching an `exclude_repos` block of its org
  is excluded, with that block's reason, and has no assignments. The
  first matching block wins.
- **Parked:** an inactive repository (archived, fork, removed, access
  lost) still resolves, so its assignments are known, but its state is
  `parked`.

Everything else is `managed`. The steps:

1. **Baseline.** Every control in `enterprise.controls` is assigned with
   decision `baseline`, in list order.
2. **Layers.** The org layer applies, then each `repos` block whose
   `match` covers the repository, in file order. Within one layer the
   operations apply in the order they appear in the file:
   - `controls` adds a control. An `exclude` of the same control in the
     same layer beats the addition.
   - `replace` swaps an active control for `with`. The replacement keeps
     the source's place and lineage, with decision `replaced`.
   - `exclude` marks an active control excluded, with decision
     `excluded` and its reason. The assignment row stays, so the
     exclusion is visible.
3. **One row per slug.** Each slug has at most one assignment.
   - Adding another version of a slug that is already assigned replaces
     that row in place.
   - A slug the enterprise names stays `baseline`, whichever version is
     assigned.
   - A slug only an org or block adds is `org_added`.
4. **Requested mode.** The first of these that is set wins:
   1. a `control {}` override in a matching `repos` block;
   2. that block's `mode`;
   3. an org-level `control {}` override;
   4. the org `mode`;
   5. the enterprise `mode`;
   6. `evaluate`.

   Among matching `repos` blocks the later one in the file wins. The
   assignment records which layer decided (`mode_source`).
5. **Effective mode.** Usually the requested mode. A `remediate` request
   drops to `evaluate` when the Remediation App cannot reach the
   repository:
   - no access gives `mode_reason = "remediation_app_no_access"`;
   - a suspended installation gives `remediation_app_suspended`.

   Evaluation never depends on the Remediation App.
6. **Conflicts.** Two active assignments that own the same resource, for
   example two controls writing one file or one ruleset, both become
   `conflict`. Each carries an error naming the resource and the
   claimants. A conflicting assignment is not evaluated; compliance reports
   it as unmeasurable.

In `examples/policy/multi-org`:

- Repositories in `myorg-prod` get `codeowners@1` and `dependabot@1` as
  `baseline`, plus `protect_main_strict@1` and `catalog_info@1` as
  `org_added`. All four are in `remediate`, from the enterprise.
- In `myorg-staging` the org's `mode = "evaluate"` covers both its
  baseline and `protect_main@1`.

## Validation

Load collects every error, joins them and fails. Each error carries
`file:line`. Warnings are logged once at load and do not stop it.

**Fails load:**

- A layout problem:
  - no `enterprise.hcl`;
  - an `.hcl` file outside the three places;
  - a file over 1 MiB, not a regular file, or resolving outside the
    root;
  - a `file()` path outside the root or missing.
- HCL that does not parse, an unknown block or attribute where the
  schema is closed, or an expression other than a literal or `file()`.
- More than one `enterprise` block, or a variation block inside it.
- An org block for an org not in `enterprise.orgs`, or a second block
  for the same org.
- A catalogue problem:
  - an invalid slug or rule id;
  - `version` below 1;
  - an unknown `type`;
  - a `slug@version` defined twice;
  - an unknown rule kind;
  - a rule without `kind` whose id is not a kind;
  - `remediate = true` on a kind that is not remediable;
  - an unknown, missing or mistyped parameter;
  - a bad `apply`;
  - a PR template that does not parse;
  - a template that fails its own remediable rules.
- A reference to a control or version the catalogue lacks. The error
  lists the versions that exist.
- A `mode` other than `evaluate` or `remediate`.
- `max_open_prs` below 1, or a `reopen_after` that is not a duration of
  at least `1h`.
- `replace`, `exclude` or `exclude_repos` without a reason.
- Two `repos` blocks with one label in an org, or a glob that does not
  parse.
- A `replace` whose source is not assigned at that layer. For a `repos`
  block, "at that layer" means the org result plus whatever earlier
  blocks add.
- A `replace` whose two controls own no resource in common.
- A `control {}` override naming a control no layer of the org assigns.

**Warns only:**

- An `exclude` of a control not assigned at that layer, since it does
  nothing.
- One control's mode declared twice in one scope with different values.
  The later declaration wins.
- Two `repos` blocks whose globs may overlap and that declare different
  modes, block-wide or for one control. The later block wins.
- A `repos` block that re-adds a control an earlier overlapping block
  excluded. Block order decides.
- **Ownership overlaps.** For every org the loader resolves:
  - the org layer alone;
  - the org layer with each `repos` block;
  - the org layer with each pair of `repos` blocks.

  It warns when two active controls would own the same resource. These
  are warnings because which blocks a repository meets is unknown until
  discovery. Resolution step 6 is the authority, and a real overlap
  shows up there as a `conflict` assignment.
- After discovery: a `repos` block that matches no known repository.

To load a policy without starting a worker, run
`repo-guardian monitoring generate --config <root> --out <dir>`. It runs
the same `policy.Load` and fails with the same errors.

## From `guardian.hcl`

There is no automatic translation (DESIGN-0029 D1). v1 rules described
paths and regexes; controls are typed checks, and a mechanical
translation would produce generic `file` controls and lose what the
types know. Rewrite a v1 policy by hand with this table. The
`examples/policy/` roots are the v1 examples rewritten: `minimal`,
`full`, `multi-org` and `renovate` each name the v1 file they replace in
their `enterprise.hcl` comment.

| v1 | Controls |
| -- | -------- |
| `rule "file"` with `check = "exists"` | a `file` control, `rule "exists"`: one `path`, one `template`. Prefer a dedicated type where one exists (`codeowners` knows every location GitHub reads it from). |
| `rule "file"` with `check = "exact"` | a `file` control, `rule "exact"` (`examples/policy/full/catalogue/renovate_workflow.hcl`). |
| `rule "file"` with `check = "contains"` and `assertion {}` blocks | the dedicated type that owns the file, whose rules are the typed checks: `codeowners`, `catalog_info` (`field_set`, `annotation_set`, `no_placeholders`), `dependency_updates` (`configured`, `extends`). |
| `check = "absent"` and `when { rule_satisfied }` | absorbed by the type that owns the opinion. "No Dependabot config once Renovate is configured" is `dependency_updates`' `exclusive` rule (`examples/policy/renovate`). |
| `rule "setting"` | `repo_settings`, one `equals` rule per property, remediated through the API. |
| `rule "branch_protection"` and the `branch_protection` reconciler | one `branch_ruleset` control (`exists`, `matches`) with the ruleset declared inline. |
| `reconcile "custom_properties"` | a `custom_properties` control reading `catalog-info.yaml`; `annotation_properties` keeps its v1 meaning. `mode = "github-action"` becomes `remediation { apply = "workflow" }`. |
| `reconcile "label_sync"` | a `labels` control, one `present` or `absent` rule per label. |
| `reconcile "workflow_sync"` | nothing. Its only job was feeding the push-watched path set; every control now declares what it owns and reads, and a push re-evaluates only the controls it touched. |
| top-level `scope {}` and rule `scope {}` | `enterprise.orgs` for the org universe, and org files that add, replace or exclude controls per org (`examples/policy/multi-org`). |
| global and rule `ignore {}` | `exclude_repos` for whole repositories, or `exclude` in a `repos` block for one control (`examples/policy/full/orgs/myorg.hcl`). Every exclusion now needs a `reason`. |
| `pr {}` at `defaults` / rule / reconcile scope | a `pr {}` block on the control definition. PRs are per control, on branch `repo-guardian/<slug>`, so there is no multi-rule bundle. |
| `dry_run` / `DRY_RUN` | `mode = "evaluate"`, at enterprise, org, `repos` block or single-control scope. |
| `guardian { skip_forks, skip_archived }` | not policy. Archived repositories and forks are parked by the runtime. |
| `templates.files` in the chart | `templates/` under the policy root. |
