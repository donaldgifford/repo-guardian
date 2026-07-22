# guardian.hcl — Full example showing all rule types and features.
#
# This is a reference config demonstrating every feature of the HCL policy
# engine. Don't deploy this as-is — pick the pieces you need.
#
# Rule types:
#   rule "file"              — file presence/content compliance
#   rule "setting"           — repository setting enforcement
#   rule "branch_protection" — branch protection via rulesets API
#
# Check modes (file rules only):
#   exists   — file must be present (default)
#   contains — file must exist and pass content assertions
#   exact    — file must match the template exactly

guardian {
  log_level         = "info"
  dry_run           = false
  worker_count      = 5
  queue_size        = 1000
  schedule_interval = "168h"
  skip_forks        = true
  skip_archived     = true

  # IMPL-0013 Phase 3: when every file rule referenced by an open
  # repo-guardian PR has been satisfied on the default branch (e.g.,
  # a maintainer hand-merged a CODEOWNERS file on a side branch),
  # auto-close the PR, post a final sticky comment explaining why,
  # and delete the reconcile branch. Default: true.
  #
  # Set to false to preserve the legacy behaviour where the PR stays
  # open until a human closes it. Useful for compliance workflows
  # that require manual PR-close attestation. Env var override:
  # AUTO_CLOSE_PR=false on the Deployment.
  # auto_close_pr = true
}

# Process-wide PR template defaults. Every rule.pr and
# reconcile.pr inherits these unless overridden field-by-field.
# Reconciler PRs deliberately skip rule.pr — they merge
# reconciler.pr → defaults.pr only.
#
# Helpers available in templates: env, default, join, lower,
# upper, title. The `env "VAR"` helper reads the binary's
# process env. NEVER reference secret env vars in PR text —
# rendered output is visible to PR reviewers.
defaults {
  pr {
    title = "[{{ env \"JIRA_PROJECT\" | default \"GUARDIAN\" }}] guardian: {{ .Owner }}/{{ .Repo }}"
    body = <<EOT
## Repo Guardian

This PR was opened automatically by **repo-guardian**.

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

# Global ignore list — these repos are skipped for ALL rules.
ignore {
  repos = [
    "myorg/.github",
    "myorg/terraform-*",
    "myorg/archive-*",
  ]
}

# ---------------------------------------------------------------------------
# File rules
# ---------------------------------------------------------------------------

rule "file" "codeowners" {
  check    = "exists"
  paths    = ["CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  # Partial override: rule sets only `title`. Body and labels
  # inherit from defaults.pr because `inherits` defaults to true.
  pr {
    search_terms = ["codeowners", "CODEOWNERS"]
    title        = "chore({{ .Repo }}): adopt CODEOWNERS"
  }
}

rule "file" "dependabot" {
  check    = "exists"
  paths    = [".github/dependabot.yml", ".github/dependabot.yaml"]
  target   = ".github/dependabot.yml"
  template = "dependabot"

  pr {
    search_terms = ["dependabot"]
  }

  # Per-rule ignore — skip repos that use Renovate instead.
  ignore {
    repos = ["myorg/renovate-*"]
  }
}

rule "file" "renovate_workflow" {
  enabled  = true
  check    = "exact"
  paths    = [".github/workflows/renovate.yml"]
  target   = ".github/workflows/renovate.yml"
  template = "renovate-workflow"

  reconcile "workflow_sync" {
    watch = true
  }
}

rule "file" "renovate_config" {
  enabled = true
  check   = "contains"
  paths   = [
    "renovate.json",
    "renovate.json5",
    ".renovaterc",
    ".renovaterc.json",
    ".github/renovate.json",
    ".github/renovate.json5",
  ]
  target   = "renovate.json"
  template = "renovate"

  pr {
    search_terms = ["renovate"]
  }

  assertion {
    pattern = "github>myorg/renovate-config"
    message = "renovate.json must extend the org preset"
  }
}

# Backstage catalog-info.yaml with content assertions and reconciler.
rule "file" "catalog_info" {
  check    = "contains"
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"

  pr {
    search_terms = ["catalog-info"]
  }

  # Regex assertion — file must contain an owner field.
  assertion {
    pattern = "owner:"
    message = "catalog-info.yaml must have an owner field"
  }

  # YAML path assertion — owner must reference a team.
  assertion {
    yaml_path = "spec.owner"
    contains  = "team"
    message   = "spec.owner must reference a team (e.g., team-platform)"
  }

  # YAML path non-empty assertion — fails when the path is missing or
  # resolves to an empty string. Use this when you just want a field
  # set without enforcing a specific value/substring.
  assertion {
    yaml_path = "spec.system"
    non_empty = true
    message   = "spec.system must be set"
  }

  # Negative assertion — no placeholder values.
  assertion {
    not_pattern = "TODO"
    message     = "catalog-info.yaml must not contain TODO placeholders"
  }

  # Sync ownership metadata to GitHub custom properties after checks pass.
  # Owner/Component are always managed (contract-guaranteed by every
  # Backstage Component entity); annotation_properties below is how you
  # opt additional annotations into the same sync + clear-on-removal
  # behavior. This reproduces the old built-in Jira extraction that
  # DESIGN-0019 replaced — an org not using Jira would simply omit this
  # map (or map different annotations).
  reconcile "custom_properties" {
    mode  = "api"
    watch = true

    annotation_properties = {
      "jira/project-key" = "JiraProject"
      "jira/label"        = "JiraLabel"
    }

    # Reconciler PR opts out of the compliance-flavored defaults
    # (skip parent inheritance entirely). Body falls back to the
    # reconciler's hardcoded text because rule.pr is deliberately
    # skipped for reconciler PRs (DESIGN-0013 Q4 resolution).
    pr {
      title    = "chore({{ .Repo }}): sync custom properties from catalog-info"
      labels   = ["automated", "catalog-sync"]
      inherits = false
    }
  }
}

# Label sync — manage GitHub labels from a YAML file.
rule "file" "labels" {
  check    = "exists"
  paths    = [".github/labels.yml", ".github/labels.yaml"]
  target   = ".github/labels.yml"
  template = "codeowners"   # placeholder, you'd create a labels template

  reconcile "label_sync" {
    watch        = true
    delete_extra = false     # set true to remove labels not in the YAML
  }
}

# ---------------------------------------------------------------------------
# Setting rules — enforce repository properties
# ---------------------------------------------------------------------------

rule "setting" "enable_vuln_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = true
}

rule "setting" "delete_branch_on_merge" {
  property  = "delete_branch_on_merge"
  expected  = true
  remediate = true
}

rule "setting" "disable_wiki" {
  property  = "has_wiki"
  expected  = false
  remediate = true
}

rule "setting" "squash_merge_only" {
  property  = "allow_squash_merge"
  expected  = true
  remediate = false   # report only, don't change
}

# ---------------------------------------------------------------------------
# Branch protection rules — enforce via GitHub rulesets API
# ---------------------------------------------------------------------------

rule "branch_protection" "main_protection" {
  branch                = "main"
  require_pr            = true
  required_approvals    = 1
  dismiss_stale_reviews = true
  require_linear_history = true
  remediate             = true

  # Skip repos where main isn't the default branch.
  ignore {
    repos = ["myorg/legacy-*"]
  }
}
