org "acme" {
  controls = ["deps@1"]
  repos "x" {
    match    = ["api"]
    controls = ["wiki@1"]
    exclude "deps@1" {
      reason = "not here"
    }
    exclude "wiki@1" {
      reason = "opt-out wins"
    }
  }
}
