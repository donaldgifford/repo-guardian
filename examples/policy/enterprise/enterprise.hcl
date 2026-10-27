# enterprise: what v1's guardian-enterprise.hcl did. One App installed on
# every org in the enterprise; this list is the subset repo-guardian
# manages, and every listed org gets the baseline whether or not it has
# a file under orgs/. The enterprise starts in evaluate mode; an org
# opts in to remediation, a repos block can narrow it again, and an org
# can replace or exclude a baseline control with a recorded reason.
enterprise {
  orgs     = ["platform", "payments", "data", "sandbox"]
  mode     = "evaluate"
  controls = ["codeowners@1", "dependabot@1", "catalog_info@1", "repo_settings@1"]

  remediation {
    max_open_prs = 2
    reopen_after = "168h"
  }
}
