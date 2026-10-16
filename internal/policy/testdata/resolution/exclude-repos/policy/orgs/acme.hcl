org "acme" {
  exclude_repos {
    match  = ["sandbox-*"]
    reason = "scratch repositories"
  }
}
