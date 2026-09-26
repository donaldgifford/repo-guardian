guardian {
  dry_run       = false
  skip_forks    = true
  skip_archived = true
  log_level     = "info"
}

ignore {
  repos = ["acme/sandbox-*"]
}

defaults {
  pr {
    title  = "chore: add missing repository files"
    labels = ["repo-guardian"]
  }
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS", ".github/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"
}

rule "file" "renovate" {
  check    = "contains"
  paths    = ["renovate.json"]
  target   = "renovate.json"
  template = "renovate"

  assertion {
    pattern = "extends"
    message = "renovate.json must extend a preset"
  }
}

rule "setting" "vuln_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = true
}

rule "branch_protection" "main" {
  branch             = "main"
  require_pr         = true
  required_approvals = 1
}
