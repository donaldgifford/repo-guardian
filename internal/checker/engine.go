// Package checker implements the core check-and-PR engine for repo-guardian.
// It inspects repositories for missing configuration files and creates pull
// requests to add sensible defaults.
package checker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/rules"
)

const (
	// BranchName is the deterministic branch name used by repo-guardian.
	BranchName = "repo-guardian/add-missing-files"

	// PRTitle is the title used for repo-guardian pull requests.
	PRTitle = "chore: add missing repo configuration files"
)

// Engine is the core checker that evaluates repositories against the rule
// registry and creates PRs for missing files.
type Engine struct {
	registry             *rules.Registry
	templates            *rules.TemplateStore
	logger               *slog.Logger
	skipForks            bool
	skipArchived         bool
	dryRun               bool
	customPropertiesMode string
}

// NewEngine creates a new checker Engine.
func NewEngine(
	registry *rules.Registry,
	templates *rules.TemplateStore,
	logger *slog.Logger,
	skipForks, skipArchived, dryRun bool,
	customPropertiesMode string,
) *Engine {
	return &Engine{
		registry:             registry,
		templates:            templates,
		logger:               logger,
		skipForks:            skipForks,
		skipArchived:         skipArchived,
		dryRun:               dryRun,
		customPropertiesMode: customPropertiesMode,
	}
}

// CheckRepo evaluates a single repository against all enabled rules and
// creates a PR if any required files are missing.
func (e *Engine) CheckRepo(ctx context.Context, client ghclient.Client, owner, repo string) error {
	log := e.logger.With("owner", owner, "repo", repo)

	// Get repository metadata.
	repoInfo, err := client.GetRepository(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("getting repository info: %w", err)
	}

	// Authoritative skip checks — the scheduler pre-filters as an
	// optimization, but the engine is the single source of truth.
	if skip, reason := e.shouldSkip(repoInfo); skip {
		log.Info(reason)
		return nil
	}

	// Check each enabled rule.
	openPRs, err := client.ListOpenPullRequests(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("listing open PRs: %w", err)
	}

	missing, err := e.findMissingFiles(ctx, log, client, owner, repo, openPRs)
	if err != nil {
		return err
	}

	switch {
	case len(missing) == 0:
		log.Info("all required files present")
	case e.dryRun:
		log.Info("dry run: would create PR", "missing_files", ruleNames(missing))
	default:
		if err := e.createOrUpdatePR(ctx, client, owner, repo, repoInfo.DefaultRef, missing, openPRs); err != nil {
			return err
		}
	}

	return e.checkCustomPropertiesIfEnabled(ctx, log, client, owner, repo, repoInfo.DefaultRef, openPRs)
}

// shouldSkip returns true and a reason if the repository should be skipped.
func (e *Engine) shouldSkip(repo *ghclient.Repository) (bool, string) {
	if e.skipArchived && repo.Archived {
		return true, "skipping archived repository"
	}

	if e.skipForks && repo.Fork {
		return true, "skipping forked repository"
	}

	if !repo.HasBranch || repo.DefaultRef == "" {
		return true, "skipping empty repository with no default branch"
	}

	return false, ""
}

// findMissingFiles checks each enabled rule and returns rules whose files are missing.
func (e *Engine) findMissingFiles(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	openPRs []*ghclient.PullRequest,
) ([]rules.FileRule, error) {
	enabledRules := e.registry.EnabledRules()
	missing := make([]rules.FileRule, 0, len(enabledRules))

	for _, rule := range enabledRules {
		ruleLog := log.With("rule", rule.Name)

		exists, err := checkFileExists(ctx, client, owner, repo, &rule)
		if err != nil {
			return nil, fmt.Errorf("checking file existence for rule %s: %w", rule.Name, err)
		}

		if exists {
			ruleLog.Debug("file exists, skipping rule")
			continue
		}

		if hasExistingPR(openPRs, &rule) {
			ruleLog.Info("existing PR found, skipping rule")
			continue
		}

		ruleLog.Info("file missing, will add to PR")
		metrics.FilesMissingTotal.WithLabelValues(rule.Name).Inc()
		missing = append(missing, rule)
	}

	return missing, nil
}

func (e *Engine) checkCustomPropertiesIfEnabled(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	openPRs []*ghclient.PullRequest,
) error {
	if e.customPropertiesMode == "" {
		return nil
	}

	if err := e.CheckCustomProperties(ctx, client, owner, repo, defaultBranch, openPRs); err != nil {
		log.Error("custom properties check failed", "error", err)
	}

	return nil
}

func checkFileExists(
	ctx context.Context,
	client ghclient.Client,
	owner, repo string,
	rule *rules.FileRule,
) (bool, error) {
	for _, path := range rule.Paths {
		exists, err := client.GetContents(ctx, owner, repo, path)
		if err != nil {
			return false, fmt.Errorf("checking %s: %w", path, err)
		}

		if exists {
			return true, nil
		}
	}

	return false, nil
}

func hasExistingPR(openPRs []*ghclient.PullRequest, rule *rules.FileRule) bool {
	for _, pr := range openPRs {
		titleLower := strings.ToLower(pr.Title)
		branchLower := strings.ToLower(pr.Head)

		for _, term := range rule.PRSearchTerms {
			termLower := strings.ToLower(term)
			if strings.Contains(titleLower, termLower) || strings.Contains(branchLower, termLower) {
				return true
			}
		}
	}

	return false
}

func (e *Engine) createOrUpdatePR(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	missing []rules.FileRule,
	openPRs []*ghclient.PullRequest,
) error {
	log := e.logger.With("owner", owner, "repo", repo)

	// Check if our branch already exists.
	branchSHA, err := client.GetBranchSHA(ctx, owner, repo, BranchName)
	if err != nil {
		return fmt.Errorf("checking for existing branch: %w", err)
	}

	// Check if we already have an open PR.
	existingPR := findOurPR(openPRs)

	// If branch exists but no open PR, delete the stale branch.
	if branchSHA != "" && existingPR == nil {
		log.Info("deleting stale branch from previously closed PR")

		if err := client.DeleteBranch(ctx, owner, repo, BranchName); err != nil {
			return fmt.Errorf("deleting stale branch: %w", err)
		}

		branchSHA = ""
	}

	// Get the default branch SHA to create our branch from.
	baseSHA, err := client.GetBranchSHA(ctx, owner, repo, defaultBranch)
	if err != nil {
		return fmt.Errorf("getting default branch SHA: %w", err)
	}

	if baseSHA == "" {
		return fmt.Errorf("default branch %s has no SHA", defaultBranch)
	}

	// Create or recreate the branch.
	if branchSHA == "" {
		if err := client.CreateBranch(ctx, owner, repo, BranchName, baseSHA); err != nil {
			return fmt.Errorf("creating branch: %w", err)
		}

		log.Info("created branch", "branch", BranchName)
	}

	// Commit each missing file.
	for _, rule := range missing {
		content, err := e.templates.Get(rule.DefaultTemplateName)
		if err != nil {
			return fmt.Errorf("getting template for %s: %w", rule.Name, err)
		}

		msg := fmt.Sprintf("chore: add %s", rule.TargetPath)

		if err := client.CreateOrUpdateFile(ctx, owner, repo, BranchName, rule.TargetPath, content, msg); err != nil {
			return fmt.Errorf("creating file %s: %w", rule.TargetPath, err)
		}

		log.Info("added file", "path", rule.TargetPath)
	}

	// Create PR if we don't already have one.
	if existingPR == nil {
		body := BuildPRBody(missing)

		pr, err := client.CreatePullRequest(ctx, owner, repo, PRTitle, body, BranchName, defaultBranch)
		if err != nil {
			return fmt.Errorf("creating PR: %w", err)
		}

		metrics.PRsCreatedTotal.Inc()
		log.Info("created PR", "pr_number", pr.Number)
	} else {
		metrics.PRsUpdatedTotal.Inc()
		log.Info("updated existing PR", "pr_number", existingPR.Number)
	}

	return nil
}

func findOurPR(openPRs []*ghclient.PullRequest) *ghclient.PullRequest {
	for _, pr := range openPRs {
		if pr.Head == BranchName {
			return pr
		}
	}

	return nil
}

// BuildPRBody generates the PR body markdown for the given missing rules.
func BuildPRBody(missing []rules.FileRule) string {
	var sb strings.Builder

	sb.WriteString("## Repo Guardian — Missing Configuration Files\n\n")
	sb.WriteString("This PR was automatically created by **repo-guardian** because the following\n")
	sb.WriteString("required configuration files were not found in this repository:\n\n")
	sb.WriteString("### Added Files\n\n")

	for _, rule := range missing {
		fmt.Fprintf(&sb, "- `%s` — %s\n", rule.TargetPath, rule.Name)
	}

	sb.WriteString("\n> **Note:** The CODEOWNERS file contains a placeholder (`@org/CHANGEME`).\n")
	sb.WriteString("> Please replace it with your actual team before merging.\n\n")

	sb.WriteString("### What to do\n\n")
	sb.WriteString("1. Review the default file contents and adjust for your team's needs.\n")
	sb.WriteString("2. Merge when ready — these are sensible defaults, not one-size-fits-all.\n\n")
	sb.WriteString("---\n")
	sb.WriteString("*Automated by [repo-guardian](https://github.com/apps/repo-guardian). ")
	sb.WriteString("Questions? Reach out in #platform-engineering.*\n")

	return sb.String()
}

func ruleNames(rr []rules.FileRule) []string {
	names := make([]string, len(rr))
	for i, r := range rr {
		names[i] = r.Name
	}

	return names
}
