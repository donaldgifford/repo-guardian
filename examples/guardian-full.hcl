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
  org               = "myorg"
  log_level         = "info"
  dry_run           = false
  worker_count      = 5
  queue_size        = 1000
  schedule_interval = "168h"
  skip_forks        = true
  skip_archived     = true
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

  pr {
    search_terms = ["codeowners", "CODEOWNERS"]
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

  # Negative assertion — no placeholder values.
  assertion {
    not_pattern = "TODO"
    message     = "catalog-info.yaml must not contain TODO placeholders"
  }

  # Sync ownership metadata to GitHub custom properties after checks pass.
  reconcile "custom_properties" {
    mode  = "api"
    watch = true
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
