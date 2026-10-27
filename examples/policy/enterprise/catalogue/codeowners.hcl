control "codeowners" {
  version     = 1
  type        = "codeowners"
  title       = "CODEOWNERS"
  description = "A CODEOWNERS file exists in one of GitHub's locations"
  template    = "codeowners"

  rule "exists" {
    title     = "A CODEOWNERS file exists"
    remediate = true
  }
}

control "codeowners" {
  version     = 2
  type        = "codeowners"
  title       = "CODEOWNERS"
  description = "A CODEOWNERS file exists; its template names the review team"
  template    = "codeowners-team"

  rule "exists" {
    remediate = true
  }
}
