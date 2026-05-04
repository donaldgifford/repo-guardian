package reconciler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/catalog"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

const (
	// ModeAPI is the API mode for custom properties.
	ModeAPI = "api"

	// ModeGHA is the GitHub Actions mode for custom properties.
	ModeGHA = "github-action"

	// PropertiesBranchName is the branch used for custom properties PRs (github-action mode).
	PropertiesBranchName = "repo-guardian/set-custom-properties"

	// CatalogInfoBranchName is the branch used for catalog-info.yaml PRs (api mode).
	CatalogInfoBranchName = "repo-guardian/add-catalog-info"

	// PropertiesPRTitle is the PR title for custom properties workflows.
	PropertiesPRTitle = "chore: set repository custom properties"

	// CatalogInfoPRTitle is the PR title for catalog-info.yaml additions.
	CatalogInfoPRTitle = "chore: add catalog-info.yaml"
)

// CustomPropertiesReconciler reads catalog-info.yaml content, extracts
// custom property values, and syncs them to GitHub repository properties.
type CustomPropertiesReconciler struct {
	mode      string // "api" or "github-action"
	templates *rules.TemplateStore
}

// NewCustomPropertiesReconciler creates a custom_properties reconciler from config.
func NewCustomPropertiesReconciler(
	config policy.ReconcilerConfig,
	templates *rules.TemplateStore,
) (Reconciler, error) {
	mode := config.Mode
	if mode != ModeAPI && mode != ModeGHA {
		return nil, fmt.Errorf("custom_properties: mode must be %q or %q, got %q", ModeAPI, ModeGHA, mode)
	}

	return &CustomPropertiesReconciler{
		mode:      mode,
		templates: templates,
	}, nil
}

// Name returns the reconciler type name.
func (*CustomPropertiesReconciler) Name() string {
	return "custom_properties"
}

// Reconcile reads catalog-info.yaml content, diffs custom properties, and
// either sets them via API or creates a PR with a workflow.
func (r *CustomPropertiesReconciler) Reconcile(ctx context.Context, params *ReconcileParams) error {
	log := params.Logger.With("reconciler", "custom_properties", "mode", r.mode)
	metrics.PropertiesCheckedTotal.Inc()

	content, catalogFound, err := resolveCatalogContent(ctx, params)
	if err != nil {
		return err
	}

	desired := catalog.Parse(content)

	current, err := params.Client.GetCustomPropertyValues(ctx, params.Owner, params.Repo)
	if err != nil {
		return fmt.Errorf("reading custom properties: %w", err)
	}

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

	switch r.mode {
	case ModeGHA:
		return r.handleGHAMode(ctx, params, desired)
	case ModeAPI:
		return r.handleAPIMode(ctx, params, desired, catalogFound)
	default:
		return nil
	}
}

// resolveCatalogContent returns the catalog-info content and whether it was found.
// If params.Content is provided, it uses that directly. Otherwise, it tries
// reading catalog-info.yaml then catalog-info.yml from the repo.
func resolveCatalogContent(
	ctx context.Context,
	params *ReconcileParams,
) (string, bool, error) {
	if params.Content != "" {
		return params.Content, true, nil
	}

	content, err := params.Client.GetFileContent(ctx, params.Owner, params.Repo, "catalog-info.yaml")
	if err != nil {
		return "", false, fmt.Errorf("reading catalog-info.yaml: %w", err)
	}

	if content != "" {
		return content, true, nil
	}

	content, err = params.Client.GetFileContent(ctx, params.Owner, params.Repo, "catalog-info.yml")
	if err != nil {
		return "", false, fmt.Errorf("reading catalog-info.yml: %w", err)
	}

	return content, content != "", nil
}

func (r *CustomPropertiesReconciler) handleGHAMode(
	ctx context.Context,
	params *ReconcileParams,
	desired *catalog.Properties,
) error {
	log := params.Logger.With("owner", params.Owner, "repo", params.Repo)

	existingPR := findPropertiesPR(params.OpenPRs, PropertiesBranchName)
	if existingPR != nil {
		log.Info("properties PR already exists", "pr_number", existingPR.Number)
		return nil
	}

	if params.DryRun {
		log.Info("dry run: would create properties PR",
			"owner_value", desired.Owner,
			"component_value", desired.Component,
		)

		return nil
	}

	if err := cleanupStaleBranch(ctx, log, params.Client, params.Owner, params.Repo, PropertiesBranchName); err != nil {
		return err
	}

	compiled, err := r.templates.Get("set-custom-properties")
	if err != nil {
		return fmt.Errorf("getting set-custom-properties template: %w", err)
	}

	rendered, err := compiled.Render(tmpl.FileVars{
		Common: tmpl.Common{
			Owner:         params.Owner,
			Repo:          params.Repo,
			DefaultBranch: params.DefaultBranch,
			Date:          time.Now().UTC().Format(time.RFC3339),
		},
		Org: params.Owner,
		Catalog: &tmpl.CatalogInfo{
			Owner:       desired.Owner,
			Component:   desired.Component,
			JiraProject: desired.JiraProject,
			JiraLabel:   desired.JiraLabel,
		},
	})
	if err != nil {
		return fmt.Errorf("rendering set-custom-properties template: %w", err)
	}

	baseSHA, err := params.Client.GetBranchSHA(ctx, params.Owner, params.Repo, params.DefaultBranch)
	if err != nil {
		return fmt.Errorf("getting default branch SHA: %w", err)
	}

	if baseSHA == "" {
		return fmt.Errorf("default branch %s has no SHA", params.DefaultBranch)
	}

	if err := params.Client.CreateBranch(ctx, params.Owner, params.Repo, PropertiesBranchName, baseSHA); err != nil {
		return fmt.Errorf("creating properties branch: %w", err)
	}

	commitMsg := "chore: add workflow to set custom properties"
	targetPath := ".github/workflows/set-custom-properties.yml"

	if err := params.Client.CreateOrUpdateFile(ctx, params.Owner, params.Repo, PropertiesBranchName, targetPath, rendered, commitMsg); err != nil {
		return fmt.Errorf("creating workflow file: %w", err)
	}

	body := buildPropertiesPRBody(desired, "github-action")

	pr, err := params.Client.CreatePullRequest(ctx, params.Owner, params.Repo, PropertiesPRTitle, body, PropertiesBranchName, params.DefaultBranch)
	if err != nil {
		return fmt.Errorf("creating properties PR: %w", err)
	}

	metrics.PropertiesPRsCreatedTotal.Inc()
	log.Info("created properties PR", "pr_number", pr.Number)

	return nil
}

func (r *CustomPropertiesReconciler) handleAPIMode(
	ctx context.Context,
	params *ReconcileParams,
	desired *catalog.Properties,
	catalogFound bool,
) error {
	log := params.Logger.With("owner", params.Owner, "repo", params.Repo)

	if params.DryRun {
		log.Info("dry run: would set custom properties via API",
			"owner_value", desired.Owner,
			"component_value", desired.Component,
			"catalog_found", catalogFound,
		)

		return nil
	}

	props := desiredToPropertyValues(desired)

	if err := params.Client.SetCustomPropertyValues(ctx, params.Owner, params.Repo, props); err != nil {
		return fmt.Errorf("setting custom properties: %w", err)
	}

	metrics.PropertiesSetTotal.Inc()
	log.Info("set custom properties via API")

	if !catalogFound {
		return r.createCatalogInfoPR(ctx, params)
	}

	return nil
}

func (r *CustomPropertiesReconciler) createCatalogInfoPR(
	ctx context.Context,
	params *ReconcileParams,
) error {
	log := params.Logger.With("owner", params.Owner, "repo", params.Repo)

	existingPR := findPropertiesPR(params.OpenPRs, CatalogInfoBranchName)
	if existingPR != nil {
		log.Info("catalog-info PR already exists", "pr_number", existingPR.Number)
		return nil
	}

	if params.DryRun {
		log.Info("dry run: would create catalog-info PR")
		return nil
	}

	if err := cleanupStaleBranch(ctx, log, params.Client, params.Owner, params.Repo, CatalogInfoBranchName); err != nil {
		return err
	}

	compiled, err := r.templates.Get("catalog-info")
	if err != nil {
		return fmt.Errorf("getting catalog-info template: %w", err)
	}

	rendered, err := compiled.Render(tmpl.FileVars{
		Common: tmpl.Common{
			Owner:         params.Owner,
			Repo:          params.Repo,
			DefaultBranch: params.DefaultBranch,
			Date:          time.Now().UTC().Format(time.RFC3339),
		},
		Org: params.Owner,
	})
	if err != nil {
		return fmt.Errorf("rendering catalog-info template: %w", err)
	}

	baseSHA, err := params.Client.GetBranchSHA(ctx, params.Owner, params.Repo, params.DefaultBranch)
	if err != nil {
		return fmt.Errorf("getting default branch SHA: %w", err)
	}

	if baseSHA == "" {
		return fmt.Errorf("default branch %s has no SHA", params.DefaultBranch)
	}

	if err := params.Client.CreateBranch(ctx, params.Owner, params.Repo, CatalogInfoBranchName, baseSHA); err != nil {
		return fmt.Errorf("creating catalog-info branch: %w", err)
	}

	commitMsg := "chore: add catalog-info.yaml"

	err = params.Client.CreateOrUpdateFile(
		ctx, params.Owner, params.Repo, CatalogInfoBranchName,
		"catalog-info.yaml", rendered, commitMsg,
	)
	if err != nil {
		return fmt.Errorf("creating catalog-info.yaml: %w", err)
	}

	body := buildPropertiesPRBody(nil, "api")

	pr, err := params.Client.CreatePullRequest(ctx, params.Owner, params.Repo, CatalogInfoPRTitle, body, CatalogInfoBranchName, params.DefaultBranch)
	if err != nil {
		return fmt.Errorf("creating catalog-info PR: %w", err)
	}

	metrics.PropertiesPRsCreatedTotal.Inc()
	log.Info("created catalog-info PR", "pr_number", pr.Number)

	return nil
}

// cleanupStaleBranch deletes a branch if it exists but has no open PR.
func cleanupStaleBranch(
	ctx context.Context,
	log interface{ Info(string, ...any) },
	client ghclient.Client,
	owner, repo, branchName string,
) error {
	branchSHA, err := client.GetBranchSHA(ctx, owner, repo, branchName)
	if err != nil {
		return fmt.Errorf("checking for existing branch %s: %w", branchName, err)
	}

	if branchSHA != "" {
		log.Info("deleting stale branch from previously closed PR",
			"owner", owner, "repo", repo, "branch", branchName,
		)

		if err := client.DeleteBranch(ctx, owner, repo, branchName); err != nil {
			return fmt.Errorf("deleting stale branch %s: %w", branchName, err)
		}
	}

	return nil
}

// findPropertiesPR finds an open PR whose head branch matches the given name.
func findPropertiesPR(openPRs []*ghclient.PullRequest, branchName string) *ghclient.PullRequest {
	for _, pr := range openPRs {
		if pr.Head == branchName {
			return pr
		}
	}

	return nil
}

// diffProperties returns true if any desired property differs from current values.
func diffProperties(desired *catalog.Properties, current []*ghclient.CustomPropertyValue) bool {
	currentMap := make(map[string]string, len(current))
	for _, p := range current {
		currentMap[p.PropertyName] = p.Value
	}

	if currentMap["Owner"] != desired.Owner {
		return true
	}

	if currentMap["Component"] != desired.Component {
		return true
	}

	if desired.JiraProject != "" && currentMap["JiraProject"] != desired.JiraProject {
		return true
	}

	if desired.JiraLabel != "" && currentMap["JiraLabel"] != desired.JiraLabel {
		return true
	}

	return false
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
	case ModeGHA:
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
