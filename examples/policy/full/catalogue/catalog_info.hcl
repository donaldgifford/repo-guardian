control "catalog_info" {
  version     = 1
  type        = "catalog_info"
  title       = "Backstage catalog entry"
  description = "catalog-info.yaml is a Component with its owner and system filled in"
  template    = "catalog-info"

  rule "exists" {
    remediate = true
  }

  rule "kind" {
    title     = "The entity is a Component"
    remediate = true
  }

  rule "owner-is-team" {
    kind     = "field_set"
    title    = "spec.owner names a team"
    field    = "spec.owner"
    contains = "team"
  }

  rule "system-set" {
    kind  = "field_set"
    title = "spec.system is set"
    field = "spec.system"
  }

  rule "no-placeholders" {
    kind  = "no_placeholders"
    title = "No TODO placeholders remain"
  }
}
