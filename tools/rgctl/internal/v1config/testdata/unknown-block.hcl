scope {
  orgs = ["acme"]
}

widget "sprocket" {
  size = 3
}

guardian {
  dry_run              = false
  webhook_ip_allowlist = ["10.0.0.0/8"]
}

rule "file" "renovate" {
  paths  = ["renovate.json"]
  colour = "blue"
  scope {
    orgs = ["*"]
  }
  reconcile "workflow_sync" {
    watch   = true
    cadence = "daily"
  }
}

rule "teleport" "beam" {
  enabled = true
}
