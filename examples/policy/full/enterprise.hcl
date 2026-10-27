# full: what v1's guardian-full.hcl did, one control per concern, every
# built-in control type used once. The org file carries v1's global
# ignore list and the legacy carve-out from branch protection.
enterprise {
  orgs = ["myorg"]
  mode = "remediate"
  controls = [
    "codeowners@1",
    "renovate@1",
    "renovate_workflow@1",
    "catalog_info@1",
    "custom_properties@1",
    "labels@1",
    "repo_settings@1",
    "protect_main@1",
  ]

  remediation {
    max_open_prs = 3
    reopen_after = "336h"
  }
}
