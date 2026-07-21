package reconciler

import (
	"context"
	"fmt"
	"sort"
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

	// annotationProps maps a catalog-info annotation key to the GitHub
	// custom property name it populates (DESIGN-0019). Nil/empty means
	// only Owner/Component are managed.
	annotationProps map[string]string

	// managedNames is the sorted, deduplicated set of annotationProps
	// values — the mapped-property half of the managed set (Owner and
	// Component are always managed and are handled separately).
	// Precomputed once at construction since annotationProps never
	// changes for the lifetime of the reconciler.
	managedNames []string
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
		mode:            mode,
		templates:       templates,
		annotationProps: config.AnnotationProperties,
		managedNames:    managedPropertyNames(config.AnnotationProperties),
	}, nil
}

// managedPropertyNames returns the sorted, deduplicated set of GitHub
// custom property names targeted by annotationProps (its values, not
// its keys). Deduplication is defensive: policy.Validate already
// rejects duplicate targets at load time.
func managedPropertyNames(annotationProps map[string]string) []string {
	if len(annotationProps) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(annotationProps))
	names := make([]string, 0, len(annotationProps))

	for _, name := range annotationProps {
		if seen[name] {
			continue
		}

		seen[name] = true

		names = append(names, name)
	}

	sort.Strings(names)

	return names
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

	desired := catalog.Parse(content, r.annotationProps)

	current, err := params.Client.GetCustomPropertyValues(ctx, params.Owner, params.Repo)
	if err != nil {
		return fmt.Errorf("reading custom properties: %w", err)
	}

	if !r.diffProperties(desired, current) {
		log.Info("custom properties already correct")
		metrics.PropertiesAlreadyCorrectTotal.Inc()

		return nil
	}

	log.Info("custom properties need update",
		"desired_owner", desired.Owner,
		"desired_component", desired.Component,
		"desired_properties", desired.Extra,
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
			Owner:      desired.Owner,
			Component:  desired.Component,
			Properties: managedPropertiesMap(desired, r.managedNames),
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

	fallbackBody := buildPropertiesPRBody(desired, r.managedNames, "github-action")

	prText, err := resolveReconcilerPR(log, params, "custom_properties", PropertiesPRTitle, fallbackBody)
	if err != nil {
		return fmt.Errorf("resolving reconciler PR template: %w", err)
	}

	pr, err := params.Client.CreatePullRequest(ctx, params.Owner, params.Repo, prText.Title, prText.Body, PropertiesBranchName, params.DefaultBranch)
	if err != nil {
		return fmt.Errorf("creating properties PR: %w", err)
	}

	applyLabels(ctx, log, params.Client, params.Owner, params.Repo, pr.Number, prText.Labels)

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

	props := desiredToPropertyValues(desired, r.managedNames)
	sets, clears := splitSetsAndClears(props)

	if params.DryRun {
		log.Info("dry run: would set custom properties via API",
			"properties_to_set", sets,
			"properties_to_clear", clears,
			"catalog_found", catalogFound,
		)

		return nil
	}

	if err := params.Client.SetCustomPropertyValues(ctx, params.Owner, params.Repo, props); err != nil {
		return fmt.Errorf("setting custom properties: %w", err)
	}

	metrics.PropertiesSetTotal.Inc()

	if len(clears) > 0 {
		metrics.CustomPropertyClearedTotal.WithLabelValues(params.Owner).Add(float64(len(clears)))
	}

	logArgs := make([]any, 0, 2)
	if len(clears) > 0 {
		logArgs = append(logArgs, "cleared_properties", clears)
	}

	log.Info("set custom properties via API", logArgs...)

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

	fallbackBody := buildPropertiesPRBody(nil, r.managedNames, "api")

	prText, err := resolveReconcilerPR(log, params, "custom_properties", CatalogInfoPRTitle, fallbackBody)
	if err != nil {
		return fmt.Errorf("resolving reconciler PR template: %w", err)
	}

	pr, err := params.Client.CreatePullRequest(ctx, params.Owner, params.Repo, prText.Title, prText.Body, CatalogInfoBranchName, params.DefaultBranch)
	if err != nil {
		return fmt.Errorf("creating catalog-info PR: %w", err)
	}

	applyLabels(ctx, log, params.Client, params.Owner, params.Repo, pr.Number, prText.Labels)

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

// currentValue dereferences a possibly-nil current property value,
// treating nil (unset/null on GitHub) the same as an empty string.
func currentValue(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}

// diffProperties returns true if any managed property differs from
// current values. The managed set is {Owner, Component} plus
// r.managedNames; properties outside that set are never compared,
// regardless of what GetCustomPropertyValues returns.
func (r *CustomPropertiesReconciler) diffProperties(
	desired *catalog.Properties,
	current []*ghclient.CustomPropertyValue,
) bool {
	currentMap := make(map[string]*string, len(current))
	for _, p := range current {
		currentMap[p.PropertyName] = p.Value
	}

	if currentValue(currentMap["Owner"]) != desired.Owner {
		return true
	}

	if currentValue(currentMap["Component"]) != desired.Component {
		return true
	}

	for _, name := range r.managedNames {
		value, present := desired.Extra[name]
		cur := currentValue(currentMap[name])

		if present {
			if cur != value {
				return true
			}

			continue
		}

		// Annotation removed or empty: a non-empty current value must
		// be cleared (DESIGN-0019 full state sync).
		if cur != "" {
			return true
		}
	}

	return false
}

// desiredToPropertyValues converts catalog Properties into the GitHub
// CustomPropertyValue payload for the managed set: Owner, Component,
// then managedNames in sorted order. A managed name absent from
// desired.Extra carries a nil Value, which SetCustomPropertyValues
// sends as an explicit JSON null (clear).
func desiredToPropertyValues(desired *catalog.Properties, managedNames []string) []*ghclient.CustomPropertyValue {
	owner := desired.Owner
	component := desired.Component

	props := make([]*ghclient.CustomPropertyValue, 0, len(managedNames)+2)
	props = append(props,
		&ghclient.CustomPropertyValue{PropertyName: "Owner", Value: &owner},
		&ghclient.CustomPropertyValue{PropertyName: "Component", Value: &component},
	)

	for _, name := range managedNames {
		if value, ok := desired.Extra[name]; ok {
			props = append(props, &ghclient.CustomPropertyValue{PropertyName: name, Value: &value})
			continue
		}

		props = append(props, &ghclient.CustomPropertyValue{PropertyName: name, Value: nil})
	}

	return props
}

// managedPropertiesMap builds the template-facing property map for
// GHA-mode rendering: every managed name is present, using
// desired.Extra's value or the empty string when absent (the
// template's clear signal — see set-custom-properties.tmpl).
func managedPropertiesMap(desired *catalog.Properties, managedNames []string) map[string]string {
	if len(managedNames) == 0 {
		return nil
	}

	props := make(map[string]string, len(managedNames))
	for _, name := range managedNames {
		props[name] = desired.Extra[name]
	}

	return props
}

// splitSetsAndClears partitions a CustomPropertyValue payload into the
// properties being set (name -> value) and the properties being
// cleared (nil Value), for logging and metrics.
func splitSetsAndClears(props []*ghclient.CustomPropertyValue) (map[string]string, []string) {
	sets := make(map[string]string, len(props))

	var clears []string

	for _, p := range props {
		if p.Value == nil {
			clears = append(clears, p.PropertyName)
			continue
		}

		sets[p.PropertyName] = *p.Value
	}

	return sets, clears
}

// buildPropertiesPRBody generates the markdown PR body for custom properties PRs.
func buildPropertiesPRBody(props *catalog.Properties, managedNames []string, mode string) string {
	var sb strings.Builder

	switch mode {
	case ModeGHA:
		buildGHABody(&sb, props, managedNames)
	default:
		buildCatalogInfoBody(&sb)
	}

	sb.WriteString("---\n")
	sb.WriteString("*Automated by [repo-guardian](https://github.com/apps/repo-guardian). ")
	sb.WriteString("Questions? Reach out in #platform-engineering.*\n")

	return sb.String()
}

func buildGHABody(sb *strings.Builder, props *catalog.Properties, managedNames []string) {
	sb.WriteString("## Repo Guardian — Set Custom Properties\n\n")
	sb.WriteString("This PR was automatically created by **repo-guardian** to set repository\n")
	sb.WriteString("custom properties via a GitHub Actions workflow.\n\n")
	sb.WriteString("### Properties to be set\n\n")
	writePropertyList(sb, props, managedNames)
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

// writePropertyList renders Owner, Component, then every managed
// mapped property — present values inline, absent ones flagged as a
// pending clear (DESIGN-0019 full state sync).
func writePropertyList(sb *strings.Builder, props *catalog.Properties, managedNames []string) {
	if props == nil {
		return
	}

	fmt.Fprintf(sb, "- **Owner:** `%s`\n", props.Owner)
	fmt.Fprintf(sb, "- **Component:** `%s`\n", props.Component)

	for _, name := range managedNames {
		if value, ok := props.Extra[name]; ok {
			fmt.Fprintf(sb, "- **%s:** `%s`\n", name, value)
			continue
		}

		fmt.Fprintf(sb, "- **%s:** _(will clear)_\n", name)
	}
}
