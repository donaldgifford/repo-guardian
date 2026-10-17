control "protect_main" {
  version     = 1
  type        = "branch_ruleset"
  title       = "Default branch ruleset"
  description = "The default branch needs a reviewed pull request and linear history"
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
          required_approving_review_count = 1
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
    title     = "The ruleset matches the declaration"
    remediate = true
  }

  remediation {
    apply = "direct"
  }
}
