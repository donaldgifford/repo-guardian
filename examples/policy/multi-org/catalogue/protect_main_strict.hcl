control "protect_main_strict" {
  version     = 1
  type        = "branch_ruleset"
  title       = "Default branch ruleset (production)"
  description = "Two approvals, stale reviews dismissed, linear history"
  name        = "protect-main"

  ruleset = {
    conditions = {
      ref_name = {
        include = ["~DEFAULT_BRANCH"]
        exclude = []
      }
    }
    rules = [
      {
        type = "pull_request"
        parameters = {
          required_approving_review_count = 2
          dismiss_stale_reviews_on_push   = true
        }
      },
      { type = "required_linear_history" },
      { type = "deletion" },
    ]
  }

  rule "exists" {
    remediate = true
  }

  rule "matches" {
    remediate = true
  }

  remediation {
    apply = "direct"
  }
}
