org "acme" {
  replace "legacy_codeowners@1" {
    with   = "codeowners@1"
    reason = "migrated"
  }
  repos "holdout" {
    match = ["api"]
    exclude "codeowners@1" {
      reason = "not migrated yet"
    }
    controls = ["legacy_codeowners@1"]
  }
}
