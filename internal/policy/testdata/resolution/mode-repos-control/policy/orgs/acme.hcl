org "acme" {
  repos "quiet" {
    match = ["api"]
    mode  = "evaluate"
    control "deps@1" {
      mode = "remediate"
    }
  }
}
