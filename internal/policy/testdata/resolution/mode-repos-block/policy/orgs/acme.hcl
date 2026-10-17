org "acme" {
  control "deps@1" {
    mode = "remediate"
  }
  repos "quiet" {
    match = ["api"]
    mode  = "evaluate"
  }
}
