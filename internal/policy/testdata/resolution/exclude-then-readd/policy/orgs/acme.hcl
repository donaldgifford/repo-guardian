org "acme" {
  replace "codeowners@1" {
    with   = "codeowners@2"
    reason = "stricter"
  }
  repos "old" {
    match = ["api"]
    exclude "codeowners@2" {
      reason = "frozen"
    }
    controls = ["codeowners@1"]
  }
}
