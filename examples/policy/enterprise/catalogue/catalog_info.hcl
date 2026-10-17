control "catalog_info" {
  version  = 1
  type     = "catalog_info"
  title    = "Backstage catalog entry"
  template = "catalog-info"

  rule "exists" {
    remediate = true
  }

  rule "kind" {
    remediate = true
  }
}
