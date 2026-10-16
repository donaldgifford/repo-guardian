org "acme" {
  exclude "catalog@1" {
    reason = "no Backstage yet"
  }
  repos "backstage-legacy" {
    match = ["backstage-legacy"]
    exclude "catalog@1" {
      reason = "being decommissioned"
    }
  }
  repos "backstage" {
    match    = ["backstage-*"]
    controls = ["catalog@1"]
  }
}
