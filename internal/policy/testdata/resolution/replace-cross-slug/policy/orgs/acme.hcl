org "acme" {
  replace "legacy_codeowners@1" {
    with   = "codeowners@1"
    reason = "migrated"
  }
}
