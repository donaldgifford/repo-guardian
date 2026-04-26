// guardian-multi-org.hcl — strict-mode multi-org example.
//
// Declaring a top-level scope { } block engages strict mode: every
// rule must declare its own scope. Scope orgs accept the same glob
// patterns as ignore.repos (`*`, `?`, `[abc]`).
//
// Use the literal ["*"] at the rule level to apply to every org
// declared in the top-level scope. Use a subset (e.g.,
// ["myorg-prod"]) to target a single org.
//
// Single-org users should NOT use this file as a template — the
// legacy mode (no top-level scope) is the simpler default. See
// guardian-minimal.hcl or guardian-full.hcl instead.

guardian {
  org = "myorg-prod"
}

scope {
  orgs = ["myorg-prod", "myorg-staging"]
}

// Shared: applies to every in-scope org.
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = "CODEOWNERS"
  template = "codeowners"

  scope {
    orgs = ["*"]
  }
}

rule "file" "dependabot" {
  paths    = [".github/dependabot.yml"]
  target   = ".github/dependabot.yml"
  template = "dependabot"

  scope {
    orgs = ["*"]
  }
}

// Prod-only: stricter requirements for production org.
rule "branch_protection" "main_required" {
  branch                 = "main"
  require_pr             = true
  required_approvals     = 2
  dismiss_stale_reviews  = true
  enforce_admins         = true
  require_linear_history = true
  remediate              = true

  scope {
    orgs = ["myorg-prod"]
  }
}

rule "setting" "vulnerability_alerts" {
  property  = "vulnerability_alerts_enabled"
  expected  = true
  remediate = true

  scope {
    orgs = ["myorg-prod"]
  }
}

// Staging-only: lighter touch for staging org.
rule "setting" "delete_branch_on_merge" {
  property  = "delete_branch_on_merge"
  expected  = true
  remediate = false

  scope {
    orgs = ["myorg-staging"]
  }
}
