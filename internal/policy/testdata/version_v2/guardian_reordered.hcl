guardian {
  log_level     = "info"
  skip_archived = true
  skip_forks    = true
  dry_run       = false
}

ignore {
  repos = ["acme/sandbox-*"]
}

defaults {
  pr {
    labels = ["repo-guardian"]
    title  = "chore: add missing repository files"
  }
}

rule "file" "codeowners" {
  template = "codeowners"
  target   = ".github/CODEOWNERS"
  paths    = ["CODEOWNERS", ".github/CODEOWNERS"]
}

rule "file" "renovate" {
  assertion {
    message = "renovate.json must extend a preset"
    pattern = "extends"
  }

  template = "renovate"
  target   = "renovate.json"
  paths    = ["renovate.json"]
  check    = "contains"
}

rule "setting" "vuln_alerts" {
  remediate = true
  expected  = true
  property  = "vulnerability_alerts_enabled"
}

rule "branch_protection" "main" {
  required_approvals = 1
  require_pr         = true
  branch             = "main"
}
