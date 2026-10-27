control "renovate" {
  version     = 1
  type        = "dependency_updates"
  title       = "Renovate"
  description = "Renovate is configured, extends the org preset, and is the only dependency bot"
  template    = "renovate"
  tool        = "renovate"
  renovate = {
    extends = "github>{{ .Owner }}/renovate-config"
  }

  rule "configured" {
    remediate = true
  }

  rule "extends" {
    title     = "The config extends the org preset"
    remediate = true
  }

  rule "exclusive" {
    title     = "No Dependabot config is left behind"
    remediate = true
  }
}
