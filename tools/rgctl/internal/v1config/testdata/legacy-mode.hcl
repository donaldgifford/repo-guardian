# Legacy mode: no top-level scope block, so every rule applies to every
# installed org.
guardian {
  dry_run       = true
  auto_close_pr = false
}

locals {
  owner = "platform"
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS", ".github/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"
  enabled  = local.enabled
  pr {
    title = "chore: add CODEOWNERS for ${local.owner}"
  }
}

rule "setting" "wiki_off" {
  property  = "has_wiki"
  expected  = false
  remediate = true
}
