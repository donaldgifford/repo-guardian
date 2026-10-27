control "repo_settings" {
  version     = 1
  type        = "repo_settings"
  title       = "Repository settings"
  description = "Alerts on, merged branches deleted, no wiki, squash merges allowed"

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

  rule "squash-merge" {
    kind     = "equals"
    title    = "Squash merges are allowed (reported, never changed)"
    property = "allow_squash_merge"
    value    = true
  }

  remediation {
    apply = "recommend"
  }
}
