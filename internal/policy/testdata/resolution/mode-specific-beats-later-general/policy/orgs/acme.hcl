org "acme" {
  repos "a" {
    match = ["api"]
    control "deps@1" {
      mode = "remediate"
    }
  }
  repos "b" {
    match = ["a*"]
    mode  = "evaluate"
  }
}
