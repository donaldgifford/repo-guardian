control "codeowners" {
  version     = 1
  type        = "codeowners"
  title       = "CODEOWNERS"
  description = "A CODEOWNERS file exists in one of GitHub's locations"
  template    = "codeowners"

  rule "exists" {
    number    = "1.1"
    title     = "A CODEOWNERS file exists"
    remediate = true
  }

  pr {
    title = "chore({{ .Repo }}): adopt CODEOWNERS"
  }
}
