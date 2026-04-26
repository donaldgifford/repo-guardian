// prod-only.hcl — stricter rules that apply only to myorg-prod.

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
