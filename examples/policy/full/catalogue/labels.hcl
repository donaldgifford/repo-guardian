control "labels" {
  version     = 1
  type        = "labels"
  title       = "Labels"
  description = "The org's triage labels exist and retired ones are gone"

  rule "bug" {
    kind        = "present"
    name        = "bug"
    color       = "d73a4a"
    description = "Something isn't working"
    remediate   = true
  }

  rule "security" {
    kind        = "present"
    name        = "security"
    color       = "b60205"
    description = "A security issue"
    remediate   = true
  }

  rule "no-wontfix" {
    kind      = "absent"
    name      = "wontfix"
    remediate = true
  }

  remediation {
    apply = "direct"
  }
}
