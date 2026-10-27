# renovate: what v1's guardian-renovate.hcl did. Renovate instead of
# Dependabot: the config extends the org preset, the Renovate workflow
# matches the template exactly, and no Dependabot config is left behind
# (v1's absent rule gated on renovate_config is the exclusive rule here).
enterprise {
  orgs     = ["myorg"]
  mode     = "remediate"
  controls = ["codeowners@1", "renovate@1", "renovate_workflow@1"]
}
