control "custom_properties" {
  version     = 1
  type        = "custom_properties"
  title       = "Custom properties"
  description = "Repository custom properties match catalog-info.yaml"

  annotation_properties = {
    "jira/project-key" = "JiraProject"
    "jira/label"       = "JiraLabel"
  }

  properties = {
    Owner = {
      pattern = "^[a-z0-9-]+$"
    }
  }

  rule "matches" {
    remediate = true
  }

  rule "value_pattern" {
    title = "Every value matches its pattern"
  }

  rule "defined" {
    title = "The org schema defines every property"
  }

  remediation {
    apply = "direct"
  }
}
