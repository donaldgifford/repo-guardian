# guardian.hcl — Enables Renovate file management.
#
# This config adds two Renovate rules on top of the standard CODEOWNERS and
# Dependabot checks. repo-guardian will PR a Renovate CI workflow and config
# into any repo that's missing them.
#
# Prerequisites:
#   - A GitHub App registered for Renovate (separate from repo-guardian)
#   - Org-level secrets: RENOVATE_APP_ID and RENOVATE_APP_PRIVATE_KEY
#   - An org preset repo at <org>/renovate-config with a default.json
#
# Usage:
#   export GUARDIAN_CONFIG=examples/guardian-renovate.hcl
#   export GITHUB_ORG=myorg        # or set org below

guardian {
  org = "myorg"   # used in the assertion pattern below
}

# --- Standard file rules (enabled by default) ---

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
}

# --- Renovate rules (opt-in) ---

# Ensures every repo has the standard Renovate CI workflow.
# check = "exact" means the file must match the template byte-for-byte
# (YAML semantic comparison). Any manual edits trigger a corrective PR.
rule "file" "renovate_workflow" {
  enabled  = true
  check    = "exact"
  paths    = [".github/workflows/renovate.yml"]
  target   = ".github/workflows/renovate.yml"
  template = "renovate-workflow"

  # workflow_sync reconciler watches for push events that modify this file.
  # If someone changes it on the default branch, repo-guardian re-checks.
  reconcile "workflow_sync" {
    watch = true
  }
}

# Ensures every repo has a renovate.json that extends the org preset.
# check = "contains" means the file must exist AND pass the assertion.
# Teams can add overrides (labels, automerge rules, schedule) as long as
# the org preset reference is present.
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
    message = "renovate.json must extend the org preset (github>myorg/renovate-config)"
  }
}
