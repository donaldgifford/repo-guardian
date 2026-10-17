control "renovate_workflow" {
  version     = 1
  type        = "file"
  title       = "Renovate workflow"
  description = "The self-hosted Renovate workflow matches the template"
  template    = "renovate-workflow"
  path        = ".github/workflows/renovate.yml"

  rule "exact" {
    remediate = true
  }
}
