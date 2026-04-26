// staging-only.hcl — lighter rules that apply only to myorg-staging.

rule "setting" "delete_branch_on_merge" {
  property  = "delete_branch_on_merge"
  expected  = true
  remediate = false

  scope {
    orgs = ["myorg-staging"]
  }
}
