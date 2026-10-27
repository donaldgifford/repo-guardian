org "myorg" {
  exclude_repos {
    match  = [".github", "terraform-*", "archive-*"]
    reason = "org config, infrastructure and archives are managed elsewhere"
  }

  repos "legacy" {
    match = ["legacy-*"]

    exclude "protect_main@1" {
      reason = "legacy repositories predate the review policy"
    }
  }
}
