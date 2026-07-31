// guardian-enterprise.hcl — GitHub Enterprise App, installed on every org
// in the enterprise, run against only the configured subset.
//
// Setup runbook for the enterprise topology (App registration, scripted
// installs, verification): docs/operations/ent-setup.md.
//
// Topology:
//   - One GitHub App at the enterprise level (single auth, GITHUB_APP_ID +
//     private key).
//   - The App is installed on every org in the enterprise (operator-side
//     concern via the Enterprise admin UI or the install script in
//     ent-setup.md).
//   - repo-guardian receives webhooks + discovery rows for every install.
//   - This policy enumerates which orgs to actually reconcile, and each
//     configured org gets its own per-rule scope so policies can diverge.
//
// Why this shape and not legacy mode (no top-level scope):
//   - Legacy mode would apply every rule to every repo across every org
//     the App is installed on, with no way to vary policy per-org. Fine
//     for small enterprises with one policy; insufficient when one team
//     wants stricter branch protection than another.
//   - Strict mode gives you per-org carveouts AND the discipline that
//     every rule must explicitly declare its scope. New rules added by
//     a teammate can't accidentally bleed into orgs you didn't intend.
//
// Efficiency note:
//   Repos from unconfigured orgs still create `repo_state` rows (via
//   discovery + webhooks) and get briefly processed before the scope
//   gate skips them. The engine increments
//   `repo_guardian_out_of_scope_total{level="policy"}` for visibility.
//   For an enterprise with hundreds of orgs and only a few configured,
//   this is wasteful — track the metric and file a feature request for
//   discovery-time scope gating if it becomes load-bearing.

guardian {
  log_level         = "info"
  dry_run           = false
  worker_count      = 5
  queue_size        = 1000
  schedule_interval = "168h"
  skip_forks        = true
  skip_archived     = true
}

// Top-level scope: ONLY the orgs we want repo-guardian to reconcile.
// Add orgs here as teams onboard. Orgs the App is installed on but
// NOT listed here will be silently skipped (visible via
// repo_guardian_out_of_scope_total{level="policy"}).
scope {
  orgs = [
    "myent-platform",   // platform / infra org — strictest baseline
    "myent-product",    // product engineering — baseline + Renovate
    "myent-sandbox",    // experimentation org — relaxed rules
  ]
}

// =========================================================================
// PR template defaults — and the two template variable sets.
// =========================================================================
// There are TWO distinct template contexts, and they do not share fields:
//
//   pr {} blocks (title/body) render with the PR context:
//     .Owner .Repo .DefaultBranch .Date   — repo identity
//     .Rule {Name Target Action}          — single-rule PRs
//     .Rules                              — bundled multi-rule PRs (slice)
//     .Files                              — every path in the PR
//     .Reconciler                         — set for reconciler-opened PRs
//
//   File templates (the rendered CODEOWNERS / catalog-info.yaml / etc.,
//   supplied via chart `templates.files` or the embedded defaults) render
//   with the file context, which additionally has:
//     .Org       — alias for .Owner (file templates ONLY)
//     .Catalog   — parsed catalog-info fields, where applicable
//
// THE TRAP: `.Org` does not exist in pr {} blocks. Use `.Owner` there.
// A pr title/body referencing `.Org` fails at render time (or at startup
// under STRICT_TEMPLATES=true, which you should run in CI — see
// --strict-templates in the chart values).
//
// Helpers available in both contexts: env, default, join, lower, upper,
// title. NEVER reference secret env vars in PR text — rendered output is
// visible to PR reviewers.
defaults {
  pr {
    title = "[{{ env \"JIRA_PROJECT\" | default \"GUARDIAN\" }}] guardian: {{ .Owner }}/{{ .Repo }}"
    body = <<EOT
## Repo Guardian

This PR was opened automatically by **repo-guardian** for
`{{ .Owner }}/{{ .Repo }}` (default branch: `{{ .DefaultBranch }}`).

### Files changed
{{ range .Files }}- `{{ . }}`
{{ end }}

### What to do
1. Review each file for your team's needs.
2. Merge when ready — these are sensible defaults, not one-size-fits-all.

---
*Need help? Reach out in #platform-engineering.*
EOT
    labels = ["automated", "guardian"]
  }
}

// Global ignore list — these repos are skipped for ALL rules, in every org.
ignore {
  repos = [
    "*/.github",         // org meta-repos manage their own config
    "*/archive-*",
  ]
}

// =========================================================================
// Shared baseline — applies to every configured org.
// =========================================================================
// Use the literal ["*"] at the rule level to mean "every org in the
// top-level scope." This is the idiom that survives org additions without
// touching every rule.

rule "file" "codeowners" {
  paths    = ["CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  scope {
    orgs = ["*"]
  }

  // Partial override: only `title` is set here; body and labels inherit
  // from defaults.pr (inherits defaults to true).
  pr {
    search_terms = ["codeowners", "CODEOWNERS"]
    title        = "chore({{ .Repo }}): adopt CODEOWNERS"
  }
}

rule "file" "dependabot" {
  paths    = [".github/dependabot.yml", ".github/dependabot.yaml"]
  target   = ".github/dependabot.yml"
  template = "dependabot"

  scope {
    orgs = ["*"]
  }

  pr {
    search_terms = ["dependabot"]
  }
}

// Backstage catalog-info.yaml with content assertions + custom-property
// sync. The embedded "catalog-info" template renders a skeleton with
// TODO placeholders (metadata.name = {{ .Repo }}, source-location from
// {{ .Owner }}/{{ .Repo }}); the assertions below keep the rule
// actionable until a human replaces the TODOs, so the PR stays open as
// the prompt. To customize the generated file, supply your own template
// under chart `templates.files` — file templates may use `.Org` (alias
// for `.Owner`) in addition to the common fields.
rule "file" "catalog_info" {
  check    = "contains"
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"

  scope {
    orgs = ["*"]
  }

  pr {
    search_terms = ["catalog-info"]
  }

  // Regex assertion — file must contain an owner field at all.
  assertion {
    pattern = "owner:"
    message = "catalog-info.yaml must have an owner field"
  }

  // YAML path assertion — owner must reference a team.
  assertion {
    yaml_path = "spec.owner"
    contains  = "team"
    message   = "spec.owner must reference a team (e.g., team-platform)"
  }

  // Negative assertion — no placeholder values left behind.
  assertion {
    not_pattern = "TODO"
    message     = "catalog-info.yaml must not contain TODO placeholders"
  }

  // Sync ownership metadata to GitHub custom properties after checks
  // pass. Owner/Component are always managed; annotation_properties
  // opts additional annotations into the same sync + clear-on-removal
  // behavior. Requires the org custom-property schema to define the
  // target names (undefined names are skipped with a warning — see
  // docs/operations/scaling.md § schema preflight).
  reconcile "custom_properties" {
    mode  = "api"
    watch = true

    annotation_properties = {
      "jira/project-key" = "JiraProject"
      "jira/label"       = "JiraLabel"
    }

    // Reconciler PRs merge reconciler.pr → defaults.pr only (rule.pr is
    // deliberately skipped). inherits=false opts out of the defaults
    // entirely.
    pr {
      title    = "chore({{ .Repo }}): sync custom properties from catalog-info"
      labels   = ["automated", "catalog-sync"]
      inherits = false
    }
  }
}

// =========================================================================
// Platform org — stricter requirements.
// =========================================================================
// Branch protection only applies to the platform org's repos. Product and
// sandbox orgs are NOT in this rule's scope.

rule "branch_protection" "main_required" {
  branch                = "main"
  require_pr            = true
  required_approvals    = 2
  dismiss_stale_reviews = true
  enforce_admins        = true

  scope {
    orgs = ["myent-platform"]
  }
}

// Vulnerability alerts must be on for platform repos.
rule "setting" "vulnerability_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = true

  scope {
    orgs = ["myent-platform"]
  }
}

// =========================================================================
// Product org — baseline + Renovate, with gated Dependabot removal.
// =========================================================================

rule "file" "renovate_workflow" {
  check    = "exact"
  paths    = [".github/workflows/renovate.yml"]
  target   = ".github/workflows/renovate.yml"
  template = "renovate-workflow"

  scope {
    orgs = ["myent-product"]
  }

  reconcile "workflow_sync" {
    watch = true
  }
}

// NOTE: the template name is "renovate" (the embedded renovate.json
// template), not "renovate-config". Template names resolve at check
// time, not load time — a typo here deploys clean and then errors on
// the first actionable repo. examples_test.go locks every referenced
// name against the embedded set.
rule "file" "renovate_config" {
  check    = "contains"
  paths    = ["renovate.json", ".github/renovate.json"]
  target   = "renovate.json"
  template = "renovate"

  scope {
    orgs = ["myent-product"]
  }

  pr {
    search_terms = ["renovate"]
  }

  assertion {
    yaml_path = "extends"
    contains  = "github>myent-product/renovate-config"
    message   = "renovate.json must extend the shared org preset"
  }
}

// Once Renovate is live and reviewed on a product repo's default branch,
// remove the Dependabot config the universal baseline added. The
// when-gate is fail-closed and evaluates the default branch only, so
// Dependabot is never removed before renovate_config is actually
// satisfied. Absent rules take no target/template/assertion blocks —
// remediation is a file-deletion PR on the reconcile branch.
rule "file" "no_dependabot" {
  check = "absent"
  paths = [".github/dependabot.yml", ".github/dependabot.yaml"]

  when {
    rule_satisfied = "renovate_config"
  }

  scope {
    orgs = ["myent-product"]
  }

  pr {
    // search_terms MUST NOT match the "dependabot" add rule's PR titles,
    // or the removal PR would be mistaken for the add PR and skipped.
    search_terms = ["remove dependabot"]
    title        = "chore({{ .Repo }}): remove Dependabot config (repo uses Renovate)"
  }
}

// =========================================================================
// Sandbox org — opt out of strict rules.
// =========================================================================
// No rules with scope.orgs = ["myent-sandbox"] means: only the universal
// (["*"]) baseline rules apply to sandbox. No branch protection, no
// vuln-alerts enforcement, no Renovate. The org is in scope (so we'll
// still reconcile its repos) but the policy is intentionally minimal.

// =========================================================================
// Notes on growing this file:
// =========================================================================
//   - To onboard a new org: add it to top-level scope.orgs. Universal
//     rules (["*"]) automatically apply.
//   - To add an org-specific rule: declare scope.orgs = ["<org>"] on the
//     rule. Universal rules continue to apply.
//   - To opt an org out of a universal rule: the simplest path is to
//     split the rule into two with explicit subsets (e.g., ["myent-prod",
//     "myent-staging"]) instead of ["*"]. Re-introducing the universal
//     bypasses the carveout, which the engine doesn't prevent.
//   - For very large directory-style configs, split into a directory of
//     HCL files (one per org or per concern) and point GUARDIAN_CONFIG
//     at the directory. See examples/guardian-multi-org/ for the pattern.
//   - Run with STRICT_TEMPLATES=true (chart: templating.strict) so a bad
//     variable in any pr {} block fails at startup instead of at render
//     time on some repo mid-sweep.
