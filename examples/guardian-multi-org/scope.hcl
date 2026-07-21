// scope.hcl — top-level scope declaration.
//
// Strict mode requires exactly one top-level scope block across
// all merged files in the directory. The other files (shared.hcl,
// prod-only.hcl, staging-only.hcl) declare rules and reference
// these orgs by name.

scope {
  orgs = ["myorg-prod", "myorg-staging"]
}
