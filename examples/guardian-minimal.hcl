# Minimal guardian.hcl — mirrors the built-in defaults.
#
# This is what repo-guardian does out of the box when no config is provided.
# Use this as a starting point and customize from here.
#
# Usage:
#   export GUARDIAN_CONFIG=examples/guardian-minimal.hcl
#
# Or with the Helm chart:
#   policy:
#     config: |
#       <contents of this file>

guardian {
  log_level         = "info"
  dry_run           = false
  worker_count      = 5
  queue_size        = 1000
  schedule_interval = "168h"
  skip_forks        = true
  skip_archived     = true
}

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
