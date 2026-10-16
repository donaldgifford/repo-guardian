enterprise {
  orgs     = ["acme"]
  controls = ["codeowners@1"]
  remediation {
    max_open_prs = 5
    reopen_after = "48h"
  }
}
