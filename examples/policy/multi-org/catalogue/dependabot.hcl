control "dependabot" {
  version     = 1
  type        = "dependency_updates"
  title       = "Dependabot"
  description = "Dependabot version updates are configured"
  template    = "dependabot"
  tool        = "dependabot"

  rule "configured" {
    title     = "A Dependabot config exists"
    remediate = true
  }
}
