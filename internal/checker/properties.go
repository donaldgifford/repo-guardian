package checker

import (
	"context"
	"fmt"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/catalog"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

const (
	// PropertiesBranchName is the branch used for custom properties PRs (github-action mode).
	PropertiesBranchName = "repo-guardian/set-custom-properties"

	// CatalogInfoBranchName is the branch used for catalog-info.yaml PRs (api mode).
	CatalogInfoBranchName = "repo-guardian/add-catalog-info"

	// PropertiesPRTitle is the PR title for custom properties workflows.
	PropertiesPRTitle = "chore: set repository custom properties"

	// CatalogInfoPRTitle is the PR title for catalog-info.yaml additions.
	CatalogInfoPRTitle = "chore: add catalog-info.yaml"
)

// CheckCustomProperties reads the repo's catalog-info.yaml, extracts desired
// custom property values, and either creates a PR (github-action mode) or sets
// them directly via API (api mode).
func (e *Engine) CheckCustomProperties(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	openPRs []*ghclient.PullRequest,
) error {
	log := e.logger.With("owner", owner, "repo", repo, "mode", e.customPropertiesMode)
	metrics.PropertiesCheckedTotal.Inc()

	// Try to read catalog-info.yaml (then .yml).
	content, err := client.GetFileContent(ctx, owner, repo, "catalog-info.yaml")
	if err != nil {
		return fmt.Errorf("reading catalog-info.yaml: %w", err)
	}

	catalogFound := content != ""
	if !catalogFound {
		content, err = client.GetFileContent(ctx, owner, repo, "catalog-info.yml")
		if err != nil {
			return fmt.Errorf("reading catalog-info.yml: %w", err)
		}

		catalogFound = content != ""
	}

	// Parse content (returns Unclassified defaults if empty/invalid).
	desired := catalog.Parse(content)

	// Read current custom properties.
	current, err := client.GetCustomPropertyValues(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("reading custom properties: %w", err)
	}

	// Diff desired vs current.
	if !diffProperties(desired, current) {
		log.Info("custom properties already correct")
		metrics.PropertiesAlreadyCorrectTotal.Inc()

		return nil
	}

	log.Info("custom properties need update",
		"desired_owner", desired.Owner,
		"desired_component", desired.Component,
		"catalog_found", catalogFound,
	)

	switch e.customPropertiesMode {
	case "github-action":
		return e.handleGHAMode(ctx, client, owner, repo, defaultBranch, desired, openPRs)
	case "api":
		return e.handleAPIMode(ctx, client, owner, repo, defaultBranch, desired, catalogFound, openPRs)
	default:
		return nil
	}
}

func (e *Engine) handleGHAMode(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	desired *catalog.Properties,
	openPRs []*ghclient.PullRequest,
) error {
	log := e.logger.With("owner", owner, "repo", repo)

	// Check for existing PR.
	existingPR := findPropertiesPR(openPRs, PropertiesBranchName)
	if existingPR != nil {
		log.Info("properties PR already exists", "pr_number", existingPR.Number)
		return nil
	}

	if e.dryRun {
		log.Info("dry run: would create properties PR",
			"owner_value", desired.Owner,
			"component_value", desired.Component,
		)

		return nil
	}

	// Handle stale branch cleanup.
	if err := e.cleanupStaleBranch(ctx, client, owner, repo, PropertiesBranchName); err != nil {
		return err
	}

	// Render template with actual values.
	tmplContent, err := e.templates.Get("set-custom-properties")
	if err != nil {
		return fmt.Errorf("getting set-custom-properties template: %w", err)
	}

	rendered := renderTemplate(tmplContent, map[string]string{
		"OWNER_VALUE":        desired.Owner,
		"COMPONENT_VALUE":    desired.Component,
		"JIRA_PROJECT_VALUE": desired.JiraProject,
		"JIRA_LABEL_VALUE":   desired.JiraLabel,
	})

	// Create branch from default branch HEAD.
	baseSHA, err := client.GetBranchSHA(ctx, owner, repo, defaultBranch)
	if err != nil {
		return fmt.Errorf("getting default branch SHA: %w", err)
	}

	if baseSHA == "" {
		return fmt.Errorf("default branch %s has no SHA", defaultBranch)
	}

	if err := client.CreateBranch(ctx, owner, repo, PropertiesBranchName, baseSHA); err != nil {
		return fmt.Errorf("creating properties branch: %w", err)
	}

	// Commit the workflow file.
	commitMsg := "chore: add workflow to set custom properties"
	targetPath := ".github/workflows/set-custom-properties.yml"

	if err := client.CreateOrUpdateFile(ctx, owner, repo, PropertiesBranchName, targetPath, rendered, commitMsg); err != nil {
		return fmt.Errorf("creating workflow file: %w", err)
	}

	// Create PR.
	body := buildPropertiesPRBody(desired, "github-action")

	pr, err := client.CreatePullRequest(ctx, owner, repo, PropertiesPRTitle, body, PropertiesBranchName, defaultBranch)
	if err != nil {
		return fmt.Errorf("creating properties PR: %w", err)
	}

	metrics.PropertiesPRsCreatedTotal.Inc()
	log.Info("created properties PR", "pr_number", pr.Number)

	return nil
}

func (e *Engine) handleAPIMode(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	desired *catalog.Properties,
	catalogFound bool,
	openPRs []*ghclient.PullRequest,
) error {
	log := e.logger.With("owner", owner, "repo", repo)

	if e.dryRun {
		log.Info("dry run: would set custom properties via API",
			"owner_value", desired.Owner,
			"component_value", desired.Component,
			"catalog_found", catalogFound,
		)

		return nil
	}

	// Set properties via API.
	props := desiredToPropertyValues(desired)

	if err := client.SetCustomPropertyValues(ctx, owner, repo, props); err != nil {
		return fmt.Errorf("setting custom properties: %w", err)
	}

	metrics.PropertiesSetTotal.Inc()
	log.Info("set custom properties via API")

	// If catalog-info.yaml was not found, create a PR with a template.
	if !catalogFound {
		return e.createCatalogInfoPR(ctx, client, owner, repo, defaultBranch, openPRs)
	}

	return nil
}

func (e *Engine) createCatalogInfoPR(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	openPRs []*ghclient.PullRequest,
) error {
	log := e.logger.With("owner", owner, "repo", repo)

	// Check for existing catalog-info PR.
	existingPR := findPropertiesPR(openPRs, CatalogInfoBranchName)
	if existingPR != nil {
		log.Info("catalog-info PR already exists", "pr_number", existingPR.Number)
		return nil
	}

	if e.dryRun {
		log.Info("dry run: would create catalog-info PR")
		return nil
	}

	// Handle stale branch cleanup.
	if err := e.cleanupStaleBranch(ctx, client, owner, repo, CatalogInfoBranchName); err != nil {
		return err
	}

	// Render catalog-info template.
	tmplContent, err := e.templates.Get("catalog-info")
	if err != nil {
		return fmt.Errorf("getting catalog-info template: %w", err)
	}

	rendered := renderTemplate(tmplContent, map[string]string{
		"REPO_NAME": repo,
		"ORG_NAME":  owner,
	})

	// Create branch from default branch HEAD.
	baseSHA, err := client.GetBranchSHA(ctx, owner, repo, defaultBranch)
	if err != nil {
		return fmt.Errorf("getting default branch SHA: %w", err)
	}

	if baseSHA == "" {
		return fmt.Errorf("default branch %s has no SHA", defaultBranch)
	}

	if err := client.CreateBranch(ctx, owner, repo, CatalogInfoBranchName, baseSHA); err != nil {
		return fmt.Errorf("creating catalog-info branch: %w", err)
	}

	// Commit the catalog-info.yaml file.
	commitMsg := "chore: add catalog-info.yaml"

	if err := client.CreateOrUpdateFile(ctx, owner, repo, CatalogInfoBranchName, "catalog-info.yaml", rendered, commitMsg); err != nil {
		return fmt.Errorf("creating catalog-info.yaml: %w", err)
	}

	// Create PR.
	body := buildPropertiesPRBody(nil, "api")

	pr, err := client.CreatePullRequest(ctx, owner, repo, CatalogInfoPRTitle, body, CatalogInfoBranchName, defaultBranch)
	if err != nil {
		return fmt.Errorf("creating catalog-info PR: %w", err)
	}

	metrics.PropertiesPRsCreatedTotal.Inc()
	log.Info("created catalog-info PR", "pr_number", pr.Number)

	return nil
}

// cleanupStaleBranch deletes a branch if it exists but has no open PR.
// This follows the same pattern as createOrUpdatePR in engine.go.
func (e *Engine) cleanupStaleBranch(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, branchName string,
) error {
	branchSHA, err := client.GetBranchSHA(ctx, owner, repo, branchName)
	if err != nil {
		return fmt.Errorf("checking for existing branch %s: %w", branchName, err)
	}

	if branchSHA != "" {
		e.logger.Info("deleting stale branch from previously closed PR",
			"owner", owner, "repo", repo, "branch", branchName,
		)

		if err := client.DeleteBranch(ctx, owner, repo, branchName); err != nil {
			return fmt.Errorf("deleting stale branch %s: %w", branchName, err)
		}
	}

	return nil
}

// findPropertiesPR finds an open PR whose head branch matches the given name.
// Mirrors findOurPR from engine.go for consistency.
func findPropertiesPR(openPRs []*ghclient.PullRequest, branchName string) *ghclient.PullRequest {
	for _, pr := range openPRs {
		if pr.Head == branchName {
			return pr
		}
	}

	return nil
}

// diffProperties returns true if any desired property differs from current values.
// JiraProject and JiraLabel are only compared when the desired value is non-empty.
func diffProperties(desired *catalog.Properties, current []*ghclient.CustomPropertyValue) bool {
	currentMap := make(map[string]string, len(current))
	for _, p := range current {
		currentMap[p.PropertyName] = p.Value
	}

	// Always compare Owner and Component.
	if currentMap["Owner"] != desired.Owner {
		return true
	}

	if currentMap["Component"] != desired.Component {
		return true
	}

	// Only compare Jira fields when desired value is non-empty.
	if desired.JiraProject != "" && currentMap["JiraProject"] != desired.JiraProject {
		return true
	}

	if desired.JiraLabel != "" && currentMap["JiraLabel"] != desired.JiraLabel {
		return true
	}

	return false
}

// renderTemplate performs simple string replacement of placeholders in template content.
func renderTemplate(content string, replacements map[string]string) string {
	result := content
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}

// desiredToPropertyValues converts catalog Properties to GitHub CustomPropertyValue slice.
func desiredToPropertyValues(desired *catalog.Properties) []*ghclient.CustomPropertyValue {
	props := []*ghclient.CustomPropertyValue{
		{PropertyName: "Owner", Value: desired.Owner},
		{PropertyName: "Component", Value: desired.Component},
	}

	if desired.JiraProject != "" {
		props = append(props, &ghclient.CustomPropertyValue{
			PropertyName: "JiraProject",
			Value:        desired.JiraProject,
		})
	}

	if desired.JiraLabel != "" {
		props = append(props, &ghclient.CustomPropertyValue{
			PropertyName: "JiraLabel",
			Value:        desired.JiraLabel,
		})
	}

	return props
}

// buildPropertiesPRBody generates the markdown PR body for custom properties PRs.
func buildPropertiesPRBody(props *catalog.Properties, mode string) string {
	var sb strings.Builder

	switch mode {
	case "github-action":
		buildGHABody(&sb, props)
	default:
		buildCatalogInfoBody(&sb)
	}

	sb.WriteString("---\n")
	sb.WriteString("*Automated by [repo-guardian](https://github.com/apps/repo-guardian). ")
	sb.WriteString("Questions? Reach out in #platform-engineering.*\n")

	return sb.String()
}

func buildGHABody(sb *strings.Builder, props *catalog.Properties) {
	sb.WriteString("## Repo Guardian — Set Custom Properties\n\n")
	sb.WriteString("This PR was automatically created by **repo-guardian** to set repository\n")
	sb.WriteString("custom properties via a GitHub Actions workflow.\n\n")
	sb.WriteString("### Properties to be set\n\n")
	writePropertyList(sb, props)
	sb.WriteString("\n### What happens when merged\n\n")
	sb.WriteString("The included GitHub Actions workflow runs once on push to `main` and sets\n")
	sb.WriteString("the above custom properties on this repository. The workflow can be safely\n")
	sb.WriteString("deleted after it runs.\n\n")
}

func buildCatalogInfoBody(sb *strings.Builder) {
	sb.WriteString("## Repo Guardian — Add catalog-info.yaml\n\n")
	sb.WriteString("This PR was automatically created by **repo-guardian** because this\n")
	sb.WriteString("repository is missing a `catalog-info.yaml` file.\n\n")
	sb.WriteString("### What to do\n\n")
	sb.WriteString("1. Fill in the `TODO` placeholders with your team's information.\n")
	sb.WriteString("2. Review and merge when ready.\n\n")
	sb.WriteString("Once merged, repo-guardian will read the file on the next reconciliation\n")
	sb.WriteString("cycle and update custom properties with the correct values.\n\n")
}

func writePropertyList(sb *strings.Builder, props *catalog.Properties) {
	if props == nil {
		return
	}

	fmt.Fprintf(sb, "- **Owner:** `%s`\n", props.Owner)
	fmt.Fprintf(sb, "- **Component:** `%s`\n", props.Component)

	if props.JiraProject != "" {
		fmt.Fprintf(sb, "- **JiraProject:** `%s`\n", props.JiraProject)
	}

	if props.JiraLabel != "" {
		fmt.Fprintf(sb, "- **JiraLabel:** `%s`\n", props.JiraLabel)
	}
}
