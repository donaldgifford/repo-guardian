// guardian-enterprise.hcl — GitHub Enterprise App, installed on every org
// in the enterprise, run against only the configured subset.
//
// Topology:
//   - One GitHub App at the enterprise level (single auth, GITHUB_APP_ID +
//     private key).
//   - The App is installed on every org in the enterprise (operator-side
//     concern via the Enterprise admin UI).
//   - repo-guardian receives webhooks + discovery rows for every install.
//   - This policy enumerates which orgs to actually reconcile, and each
//     configured org gets its own per-rule scope so policies can diverge.
//
// Why this shape and not legacy mode (no top-level scope):
//   - Legacy mode would apply every rule to every repo across every org
//     the App is installed on, with no way to vary policy per-org. Fine
//     for small enterprises with one policy; insufficient when one team
//     wants stricter branch protection than another.
//   - Strict mode gives you per-org carveouts AND the discipline that
//     every rule must explicitly declare its scope. New rules added by
//     a teammate can't accidentally bleed into orgs you didn't intend.
//
// Efficiency note:
//   Repos from unconfigured orgs still create `repo_state` rows (via
//   discovery + webhooks) and get briefly processed before the scope
//   gate skips them. The engine increments
//   `repo_guardian_out_of_scope_total{level="policy"}` for visibility.
//   For an enterprise with hundreds of orgs and only a few configured,
//   this is wasteful — track the metric and file a feature request for
//   discovery-time scope gating if it becomes load-bearing.

guardian {
  // Owner of any PRs we create. Override per-rule if you need to.
  org = "myent-platform"
}

// Top-level scope: ONLY the orgs we want repo-guardian to reconcile.
// Add orgs here as teams onboard. Orgs the App is installed on but
// NOT listed here will be silently skipped (visible via
// repo_guardian_out_of_scope_total{level="policy"}).
scope {
  orgs = [
    "myent-platform",   // platform / infra org — strictest baseline
    "myent-product",    // product engineering — baseline + Renovate
    "myent-sandbox",    // experimentation org — relaxed rules
  ]
}

// =========================================================================
// Shared baseline — applies to every configured org.
// =========================================================================
// Use the literal ["*"] at the rule level to mean "every org in the
// top-level scope." This is the idiom that survives org additions without
// touching every rule.

rule "file" "codeowners" {
  paths    = ["CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  scope {
    orgs = ["*"]
  }
}

rule "file" "dependabot" {
  paths    = [".github/dependabot.yml", ".github/dependabot.yaml"]
  target   = ".github/dependabot.yml"
  template = "dependabot"

  scope {
    orgs = ["*"]
  }
}

// =========================================================================
// Platform org — stricter requirements.
// =========================================================================
// Branch protection only applies to the platform org's repos. Product and
// sandbox orgs are NOT in this rule's scope.

rule "branch_protection" "main_required" {
  branch                = "main"
  require_pr            = true
  required_approvals    = 2
  dismiss_stale_reviews = true
  enforce_admins        = true

  scope {
    orgs = ["myent-platform"]
  }
}

// Vulnerability alerts must be on for platform repos.
rule "setting" "vulnerability_alerts" {
  property = "vulnerability_alerts_enabled"
  expected = true
  remediate = true

  scope {
    orgs = ["myent-platform"]
  }
}

// =========================================================================
// Product org — baseline + Renovate workflow.
// =========================================================================

rule "file" "renovate_workflow" {
  paths    = [".github/workflows/renovate.yml"]
  target   = ".github/workflows/renovate.yml"
  template = "renovate-workflow"

  scope {
    orgs = ["myent-product"]
  }

  reconcile "workflow_sync" {
    watch = true
  }
}

rule "file" "renovate_config" {
  paths    = ["renovate.json", ".github/renovate.json"]
  target   = "renovate.json"
  template = "renovate-config"
  check    = "contains"

  scope {
    orgs = ["myent-product"]
  }

  assertion {
    yaml_path = "extends"
    contains  = "github>myent-product/renovate-config"
    message   = "renovate.json must extend the shared org preset"
  }
}

// =========================================================================
// Sandbox org — opt out of strict rules.
// =========================================================================
// No rules with scope.orgs = ["myent-sandbox"] means: only the universal
// (["*"]) baseline rules apply to sandbox. No branch protection, no
// vuln-alerts enforcement, no Renovate. The org is in scope (so we'll
// still reconcile its repos) but the policy is intentionally minimal.

// =========================================================================
// Notes on growing this file:
// =========================================================================
//   - To onboard a new org: add it to top-level scope.orgs. Universal
//     rules (["*"]) automatically apply.
//   - To add an org-specific rule: declare scope.orgs = ["<org>"] on the
//     rule. Universal rules continue to apply.
//   - To opt an org out of a universal rule: the simplest path is to
//     split the rule into two with explicit subsets (e.g., ["myent-prod",
//     "myent-staging"]) instead of ["*"]. Re-introducing the universal
//     bypasses the carveout, which the engine doesn't prevent.
//   - For very large directory-style configs, split into a directory of
//     HCL files (one per org or per concern) and point GUARDIAN_CONFIG
//     at the directory. See examples/guardian-multi-org/ for the pattern.
