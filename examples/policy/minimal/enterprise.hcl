# minimal: what v1's guardian-minimal.hcl did. One org, a CODEOWNERS file
# and a Dependabot config on every repository, remediated by PR.
enterprise {
  orgs     = ["myorg"]
  mode     = "remediate"
  controls = ["codeowners@1", "dependabot@1"]
}
