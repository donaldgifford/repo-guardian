org "sandbox" {
  exclude "repo_settings@1" {
    reason = "sandbox repositories keep their wikis"
  }

  exclude_repos {
    match  = ["tmp-*"]
    reason = "throwaway repositories"
  }
}
