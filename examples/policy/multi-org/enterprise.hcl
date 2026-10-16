# multi-org: what v1's strict scope mode did.
# The enterprise list is the onboarding list; the baseline applies to
# both orgs, and each org adds its own default-branch ruleset: two
# approvals in production, one in staging, which is only evaluated.
enterprise {
  orgs     = ["myorg-prod", "myorg-staging"]
  mode     = "remediate"
  controls = ["codeowners@1", "dependabot@1"]
}
