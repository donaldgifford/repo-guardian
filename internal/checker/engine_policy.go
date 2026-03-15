package checker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/rules"
)

// checkRepoWithPolicy runs policy-based evaluation for a repository.
func (e *Engine) checkRepoWithPolicy(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	openPRs []*ghclient.PullRequest,
) error {
	actionable, err := e.findActionableRules(ctx, log, client, owner, repo, openPRs)
	if err != nil {
		return err
	}

	switch {
	case len(actionable) == 0:
		log.Info("all required files present")
	case e.dryRun:
		log.Info("dry run: would create PR", "actionable_rules", policyRuleNames(actionable))
	default:
		if err := e.createOrUpdatePRFromPolicy(ctx, client, owner, repo, defaultBranch, actionable, openPRs); err != nil {
			return err
		}
	}

	return e.checkCustomPropertiesIfEnabled(ctx, log, client, owner, repo, defaultBranch, openPRs)
}

// NewEngineFromPolicy creates a new Engine configured from a PolicyConfig.
// The Engine uses policy-based file rules with support for exists, contains,
// and exact check modes.
func NewEngineFromPolicy(
	cfg *policy.PolicyConfig,
	templates *rules.TemplateStore,
	logger *slog.Logger,
	customPropertiesMode string,
) (*Engine, error) {
	// Pre-compile assertions for all rules.
	compiled := make(map[string][]policy.CompiledAssertion)

	for i := range cfg.FileRules {
		r := &cfg.FileRules[i]
		if len(r.Assertions) > 0 {
			ca, err := policy.CompileAssertions(r.Assertions)
			if err != nil {
				return nil, fmt.Errorf("compiling assertions for rule %q: %w", r.Name, err)
			}

			key := r.Type + ":" + r.Name
			compiled[key] = ca
		}
	}

	return &Engine{
		templates:            templates,
		logger:               logger,
		skipForks:            cfg.Guardian.SkipForks,
		skipArchived:         cfg.Guardian.SkipArchived,
		dryRun:               cfg.Guardian.DryRun,
		customPropertiesMode: customPropertiesMode,
		policy:               cfg,
		compiledAssertions:   compiled,
	}, nil
}

// findActionableRules evaluates policy-based file rules against a repository
// and returns the rules that require action (PR creation).
func (e *Engine) findActionableRules(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	openPRs []*ghclient.PullRequest,
) ([]policy.FileRuleConfig, error) {
	var actionable []policy.FileRuleConfig

	for i := range e.policy.FileRules {
		r := &e.policy.FileRules[i]

		if !r.IsEnabled() {
			continue
		}

		ruleLog := log.With("rule", r.Name, "check", r.CheckMode())

		action, err := e.evaluateRule(ctx, ruleLog, client, owner, repo, r, openPRs)
		if err != nil {
			return nil, fmt.Errorf("evaluating rule %q: %w", r.Name, err)
		}

		if action {
			ruleLog.Info("rule requires action")
			metrics.FilesMissingTotal.WithLabelValues(r.Name).Inc()
			actionable = append(actionable, *r)
		}
	}

	return actionable, nil
}

// evaluateRule checks a single policy rule and returns true if action is needed.
func (e *Engine) evaluateRule(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.FileRuleConfig,
	openPRs []*ghclient.PullRequest,
) (bool, error) {
	// Check if there's already a PR for this rule.
	if hasExistingPRForPolicy(openPRs, rule) {
		log.Info("existing PR found, skipping rule")
		return false, nil
	}

	// Check file existence across all paths.
	existingPath, err := findExistingFile(ctx, client, owner, repo, rule.Paths)
	if err != nil {
		return false, err
	}

	switch rule.CheckMode() {
	case policy.CheckExists:
		return e.evaluateExists(log, existingPath), nil
	case policy.CheckContains:
		return e.evaluateContains(ctx, log, client, owner, repo, rule, existingPath)
	case policy.CheckExact:
		return e.evaluateExact(ctx, log, client, owner, repo, rule, existingPath)
	default:
		return e.evaluateExists(log, existingPath), nil
	}
}

func (*Engine) evaluateExists(log *slog.Logger, existingPath string) bool {
	if existingPath != "" {
		log.Debug("file exists, skipping rule")
		return false
	}

	log.Info("file missing, will add to PR")

	return true
}

func (e *Engine) evaluateContains(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.FileRuleConfig,
	existingPath string,
) (bool, error) {
	if existingPath == "" {
		log.Info("file missing, will add to PR")
		return true, nil
	}

	// File exists — run assertions.
	key := rule.Type + ":" + rule.Name
	assertions := e.compiledAssertions[key]

	if len(assertions) == 0 {
		log.Debug("file exists, no assertions to check")
		return false, nil
	}

	content, err := client.GetFileContent(ctx, owner, repo, existingPath)
	if err != nil {
		return false, fmt.Errorf("getting file content for %s: %w", existingPath, err)
	}

	if err := policy.EvaluateAssertions(assertions, content); err != nil {
		log.Info("assertion failed, will create PR", "reason", err.Error())
		return true, nil
	}

	log.Debug("file exists and passes all assertions")

	return false, nil
}

func (e *Engine) evaluateExact(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.FileRuleConfig,
	existingPath string,
) (bool, error) {
	if existingPath == "" {
		log.Info("file missing, will add to PR")
		return true, nil
	}

	content, err := client.GetFileContent(ctx, owner, repo, existingPath)
	if err != nil {
		return false, fmt.Errorf("getting file content for %s: %w", existingPath, err)
	}

	templateContent, err := e.templates.Get(rule.Template)
	if err != nil {
		return false, fmt.Errorf("getting template %q: %w", rule.Template, err)
	}

	differs := compareContent(log, existingPath, content, templateContent)
	if differs {
		return true, nil
	}

	log.Debug("file matches template exactly")

	return false, nil
}

// compareContent checks whether file content matches the template.
// For YAML files, it uses semantic comparison; for others, byte comparison.
func compareContent(log *slog.Logger, path, content, templateContent string) bool {
	if !isYAMLFile(path) {
		if content != templateContent {
			log.Info("file differs from template")
			return true
		}

		return false
	}

	matches, err := yamlSemanticallyEqual(content, templateContent)
	if err != nil {
		log.Warn("YAML comparison failed, falling back to byte comparison", "error", err)

		if content != templateContent {
			log.Info("file differs from template (byte comparison)")
			return true
		}

		return false
	}

	if !matches {
		log.Info("file differs from template (YAML semantic comparison)")
		return true
	}

	return false
}

// findExistingFile checks a list of paths and returns the first one that exists.
func findExistingFile(
	ctx context.Context,
	client ghclient.Client,
	owner, repo string,
	paths []string,
) (string, error) {
	for _, path := range paths {
		exists, err := client.GetContents(ctx, owner, repo, path)
		if err != nil {
			return "", fmt.Errorf("checking %s: %w", path, err)
		}

		if exists {
			return path, nil
		}
	}

	return "", nil
}

func hasExistingPRForPolicy(openPRs []*ghclient.PullRequest, rule *policy.FileRuleConfig) bool {
	if rule.PR == nil {
		return false
	}

	for _, pr := range openPRs {
		titleLower := strings.ToLower(pr.Title)
		branchLower := strings.ToLower(pr.Head)

		for _, term := range rule.PR.SearchTerms {
			termLower := strings.ToLower(term)
			if strings.Contains(titleLower, termLower) || strings.Contains(branchLower, termLower) {
				return true
			}
		}
	}

	return false
}

func isYAMLFile(path string) bool {
	lower := strings.ToLower(path)

	return strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")
}

// yamlSemanticallyEqual parses both strings as YAML and compares the parsed
// structures for semantic equality.
func yamlSemanticallyEqual(a, b string) (bool, error) {
	var aVal, bVal any

	if err := yaml.Unmarshal([]byte(a), &aVal); err != nil {
		return false, fmt.Errorf("parsing first YAML: %w", err)
	}

	if err := yaml.Unmarshal([]byte(b), &bVal); err != nil {
		return false, fmt.Errorf("parsing second YAML: %w", err)
	}

	return yamlDeepEqual(aVal, bVal), nil
}

// yamlDeepEqual compares two parsed YAML values for equality.
func yamlDeepEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// createOrUpdatePRFromPolicy creates or updates a PR for actionable policy rules.
func (e *Engine) createOrUpdatePRFromPolicy(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	actionable []policy.FileRuleConfig,
	openPRs []*ghclient.PullRequest,
) error {
	log := e.logger.With("owner", owner, "repo", repo)

	branchSHA, err := client.GetBranchSHA(ctx, owner, repo, BranchName)
	if err != nil {
		return fmt.Errorf("checking for existing branch: %w", err)
	}

	existingPR := findOurPR(openPRs)

	if branchSHA != "" && existingPR == nil {
		log.Info("deleting stale branch from previously closed PR")

		if err := client.DeleteBranch(ctx, owner, repo, BranchName); err != nil {
			return fmt.Errorf("deleting stale branch: %w", err)
		}

		branchSHA = ""
	}

	baseSHA, err := client.GetBranchSHA(ctx, owner, repo, defaultBranch)
	if err != nil {
		return fmt.Errorf("getting default branch SHA: %w", err)
	}

	if baseSHA == "" {
		return fmt.Errorf("default branch %s has no SHA", defaultBranch)
	}

	if branchSHA == "" {
		if err := client.CreateBranch(ctx, owner, repo, BranchName, baseSHA); err != nil {
			return fmt.Errorf("creating branch: %w", err)
		}

		log.Info("created branch", "branch", BranchName)
	}

	for i := range actionable {
		r := &actionable[i]

		content, err := e.templates.Get(r.Template)
		if err != nil {
			return fmt.Errorf("getting template for %s: %w", r.Name, err)
		}

		msg := fmt.Sprintf("chore: add %s", r.Target)

		if err := client.CreateOrUpdateFile(ctx, owner, repo, BranchName, r.Target, content, msg); err != nil {
			return fmt.Errorf("creating file %s: %w", r.Target, err)
		}

		log.Info("added file", "path", r.Target)
	}

	if existingPR == nil {
		body := buildPRBodyFromPolicy(actionable)

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

func buildPRBodyFromPolicy(actionable []policy.FileRuleConfig) string {
	var sb strings.Builder

	sb.WriteString("## Repo Guardian — Missing Configuration Files\n\n")
	sb.WriteString("This PR was automatically created by **repo-guardian** because the following\n")
	sb.WriteString("required configuration files were not found in this repository:\n\n")
	sb.WriteString("### Added Files\n\n")

	for i := range actionable {
		fmt.Fprintf(&sb, "- `%s` — %s\n", actionable[i].Target, actionable[i].Name)
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

// policyRuleNames extracts rule names from policy file rules.
func policyRuleNames(rr []policy.FileRuleConfig) []string {
	names := make([]string, len(rr))
	for i := range rr {
		names[i] = rr[i].Name
	}

	return names
}
