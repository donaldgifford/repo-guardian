org "platform" {
  mode     = "remediate"
  controls = ["protect_main@1"]

  repos "experiments" {
    match = ["exp-*"]
    mode  = "evaluate"
  }
}
