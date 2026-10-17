org "payments" {
  mode = "remediate"

  replace "codeowners@1" {
    with   = "codeowners@2"
    reason = "payments requires a CODEOWNERS entry naming its review team"
  }

  replace "dependabot@1" {
    with   = "renovate@1"
    reason = "payments runs Renovate with the org preset"
  }
}
