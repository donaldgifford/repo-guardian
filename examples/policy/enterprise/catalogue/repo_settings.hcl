control "repo_settings" {
  version     = 1
  type        = "repo_settings"
  title       = "Repository settings"
  description = "Dependabot alerts on and merged branches deleted"

  rule "vulnerability-alerts" {
    kind      = "equals"
    property  = "vulnerability_alerts"
    value     = true
    remediate = true
  }

  rule "delete-branch-on-merge" {
    kind      = "equals"
    property  = "delete_branch_on_merge"
    value     = true
    remediate = true
  }

  rule "no-wiki" {
    kind      = "equals"
    property  = "has_wiki"
    value     = false
    remediate = true
  }
}
