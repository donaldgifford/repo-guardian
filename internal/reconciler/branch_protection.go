package reconciler

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// branchProtectionFile is the YAML schema for branch protection settings.
type branchProtectionFile struct {
	Rules []branchProtectionEntry `yaml:"rules"`
}

// branchProtectionEntry defines a single branch protection rule in YAML.
type branchProtectionEntry struct {
	Branch               string   `yaml:"branch"`
	RequirePR            bool     `yaml:"require_pr"`
	RequiredApprovals    int      `yaml:"required_approvals"`
	DismissStaleReviews  bool     `yaml:"dismiss_stale_reviews"`
	RequireStatusChecks  []string `yaml:"require_status_checks"`
	RequireLinearHistory bool     `yaml:"require_linear_history"`
}

// branchProtectionReconciler applies branch protection settings from a YAML file.
type branchProtectionReconciler struct{}

// NewBranchProtectionReconciler creates a branch protection reconciler.
func NewBranchProtectionReconciler(_ policy.ReconcilerConfig) (Reconciler, error) {
	return &branchProtectionReconciler{}, nil
}

func (*branchProtectionReconciler) Name() string { return "branch_protection" }

func (*branchProtectionReconciler) Reconcile(ctx context.Context, params *ReconcileParams) error {
	log := params.Logger

	var bpf branchProtectionFile
	if err := yaml.Unmarshal([]byte(params.Content), &bpf); err != nil {
		return fmt.Errorf("parsing branch protection file: %w", err)
	}

	if len(bpf.Rules) == 0 {
		log.Info("branch protection file has no rules, nothing to sync")
		return nil
	}

	rulesets, err := params.Client.ListRepositoryRulesets(ctx, params.Owner, params.Repo)
	if err != nil {
		return fmt.Errorf("listing rulesets: %w", err)
	}

	for i := range bpf.Rules {
		entry := &bpf.Rules[i]

		if err := reconcileBranchProtection(ctx, params, rulesets, entry); err != nil {
			return fmt.Errorf("reconciling branch %q: %w", entry.Branch, err)
		}
	}

	return nil
}

func reconcileBranchProtection(
	ctx context.Context,
	params *ReconcileParams,
	rulesets []*ghclient.Ruleset,
	entry *branchProtectionEntry,
) error {
	log := params.Logger.With("branch", entry.Branch)
	desired := entryToRuleset(entry)

	existing := findRulesetForBranch(rulesets, entry.Branch)

	if existing != nil && rulesetMatches(existing, desired) {
		log.Debug("branch protection matches, no changes needed")
		return nil
	}

	if existing != nil {
		return updateBranchRuleset(ctx, params, log, existing.ID, desired)
	}

	return createBranchRuleset(ctx, params, log, desired)
}

func updateBranchRuleset(
	ctx context.Context,
	params *ReconcileParams,
	log interface{ Info(string, ...any) },
	rulesetID int64,
	desired *ghclient.Ruleset,
) error {
	log.Info("updating branch protection ruleset")

	if params.DryRun {
		log.Info("dry run: would update branch protection ruleset")
		return nil
	}

	if _, err := params.Client.UpdateRepositoryRuleset(
		ctx, params.Owner, params.Repo, rulesetID, desired,
	); err != nil {
		return fmt.Errorf("updating ruleset: %w", err)
	}

	return nil
}

func createBranchRuleset(
	ctx context.Context,
	params *ReconcileParams,
	log interface{ Info(string, ...any) },
	desired *ghclient.Ruleset,
) error {
	log.Info("creating branch protection ruleset")

	if params.DryRun {
		log.Info("dry run: would create branch protection ruleset")
		return nil
	}

	if _, err := params.Client.CreateRepositoryRuleset(
		ctx, params.Owner, params.Repo, desired,
	); err != nil {
		return fmt.Errorf("creating ruleset: %w", err)
	}

	return nil
}

func entryToRuleset(entry *branchProtectionEntry) *ghclient.Ruleset {
	rs := &ghclient.Ruleset{
		Name:        "repo-guardian-bp-" + entry.Branch,
		Enforcement: "active",
		Target:      "branch",
		Conditions: &ghclient.RulesetConditions{
			IncludePatterns: []string{"refs/heads/" + entry.Branch},
		},
		RequireLinearHistory: entry.RequireLinearHistory,
	}

	if entry.RequirePR {
		rs.RequirePullRequest = &ghclient.RulesetPullRequest{
			RequiredApprovals:   entry.RequiredApprovals,
			DismissStaleReviews: entry.DismissStaleReviews,
		}
	}

	if len(entry.RequireStatusChecks) > 0 {
		rs.RequireStatusChecks = &ghclient.RulesetStatusChecks{
			RequiredChecks:     entry.RequireStatusChecks,
			StrictStatusChecks: true,
		}
	}

	return rs
}

func findRulesetForBranch(rulesets []*ghclient.Ruleset, branch string) *ghclient.Ruleset {
	refPattern := "refs/heads/" + branch

	for _, rs := range rulesets {
		if rs.Conditions == nil {
			continue
		}

		for _, pattern := range rs.Conditions.IncludePatterns {
			if pattern == branch || pattern == refPattern {
				return rs
			}
		}
	}

	return nil
}

func rulesetMatches(existing, desired *ghclient.Ruleset) bool {
	if existing.RequireLinearHistory != desired.RequireLinearHistory {
		return false
	}

	if desired.RequirePullRequest != nil {
		if existing.RequirePullRequest == nil {
			return false
		}

		ep := existing.RequirePullRequest
		dp := desired.RequirePullRequest

		if ep.RequiredApprovals != dp.RequiredApprovals ||
			ep.DismissStaleReviews != dp.DismissStaleReviews {
			return false
		}
	}

	return true
}
