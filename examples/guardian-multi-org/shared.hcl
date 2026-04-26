// shared.hcl — rules that apply to every in-scope org.
//
// The literal ["*"] in scope.orgs means "every org listed in the
// top-level scope block."

rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = "CODEOWNERS"
  template = "codeowners"

  scope {
    orgs = ["*"]
  }
}

rule "file" "dependabot" {
  paths    = [".github/dependabot.yml"]
  target   = ".github/dependabot.yml"
  template = "dependabot"

  scope {
    orgs = ["*"]
  }
}
