package reconciler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/donaldgifford/repo-guardian/internal/catalog"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// schemaCacheTTL bounds how often GetOrgPropertySchema is called per
// org. The schema rarely changes, and a 30-minute window keeps a
// fleet sweep across many repos in one org down to a single API call
// (Phase 3 success criterion).
const schemaCacheTTL = 30 * time.Minute

// schemaEntry caches one org's custom-property schema lookup, success
// or failure, for schemaCacheTTL. Caching the error too is what makes
// the fail-open warning log once per org per window instead of once
// per repo.
type schemaEntry struct {
	names     map[string]struct{}
	err       error
	fetchedAt time.Time
}

const (
	// propOwner and propComponent are the two always-managed GitHub
	// custom properties, sourced from the Backstage contract
	// (spec.owner and metadata.name respectively).
	propOwner     = "Owner"
	propComponent = "Component"
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

const (
	// propertiesWorkflowPath is the workflow the github-action mode
	// commits to the properties branch.
	propertiesWorkflowPath = ".github/workflows/set-custom-properties.yml"

	propertiesCreateCommitMsg  = "chore: add workflow to set custom properties"
	propertiesRefreshCommitMsg = "chore: refresh custom properties workflow"
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

	// schemaMu guards schemaCache. Reconcile runs concurrently across
	// repos in the worker pool, and multiple repos in the same org can
	// race to populate the same cache entry.
	schemaMu sync.Mutex

	// schemaCache holds one entry per org, keyed by org name, valid
	// for schemaCacheTTL (DESIGN-0019 schema preflight).
	schemaCache map[string]schemaEntry

	// schemaGroup collapses concurrent cache misses for the same org
	// into a single GetOrgPropertySchema call. Without it, a
	// worker-pool burst of repos belonging to the same org — the
	// common case right after a fleet-wide re-check — would each miss
	// the cold cache and fire the fetch (and the fail-open warn log)
	// simultaneously, breaking the "one API call per org per TTL"
	// guarantee. Zero value is ready to use.
	schemaGroup singleflight.Group
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
		schemaCache:     make(map[string]schemaEntry),
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

// RunsOnAbsence reports true: a repo whose catalog-info file has been
// removed has a well-defined desired state — Owner/Component fall back
// to catalog.Defaults() and every mapped property clears — and that
// state can only be reached by reconciling on absence (INV-0011 A3).
func (*CustomPropertiesReconciler) RunsOnAbsence() bool {
	return true
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

	desired := catalog.Defaults()

	if catalogFound {
		parsed, parseErr := catalog.Parse(content, r.annotationProps)

		switch {
		case errors.Is(parseErr, catalog.ErrNotComponent):
			// Not something we manage here — a valid non-Component
			// entity is never a statement of desired property state
			// (IMPL-0020 Decision 1). The YAML itself parsed, so this
			// is not a parse failure for posture purposes; leaving the
			// outcome unset keeps "we don't manage this repo" out of
			// the broken-catalog count.
			log.Info("catalog-info is not a Backstage Component entity; skipping custom-properties reconcile", "err", parseErr)
			return nil
		case parseErr != nil:
			log.Warn("catalog-info parse failed; skipping reconcile to avoid clearing properties", "err", parseErr)
			metrics.CatalogParseFailedTotal.WithLabelValues(params.Owner).Inc()
			params.Outcome.SetCatalogParseOK(false)

			return nil
		}

		params.Outcome.SetCatalogParseOK(true)

		desired = parsed
	}

	current, err := params.Client.GetCustomPropertyValues(ctx, params.Owner, params.Repo)
	if err != nil {
		return fmt.Errorf("reading custom properties: %w", err)
	}

	// Resolve the org's property schema once and apply the same view to
	// both the drift computation and the payload. Filtering only the
	// payload (the pre-IMPL-0021 shape) left a schema-missing mapped
	// property reporting drift on every sweep, re-PATCHing the
	// already-correct defined properties forever (INV-0011 A5).
	defined := r.definedProperties(ctx, log, params.Client, params.Owner)
	r.reportMissingSchema(log, params.Owner, defined)

	if !r.diffProperties(desired, current, defined) {
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
		return r.handleAPIMode(ctx, params, desired, catalogFound, defined)
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

	rendered, err := r.renderPropertiesWorkflow(params, desired)
	if err != nil {
		return err
	}

	fallbackBody := buildPropertiesPRBody(desired, r.managedNames, ModeGHA)

	prText, err := resolveReconcilerPR(log, params, "custom_properties", PropertiesPRTitle, fallbackBody)
	if err != nil {
		return fmt.Errorf("resolving reconciler PR template: %w", err)
	}

	if existingPR := findPropertiesPR(params.OpenPRs, PropertiesBranchName); existingPR != nil {
		return refreshPropertiesPR(ctx, log, params, existingPR, rendered, prText)
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

	err = params.Client.CreateOrUpdateFile(
		ctx, params.Owner, params.Repo, PropertiesBranchName,
		propertiesWorkflowPath, rendered, propertiesCreateCommitMsg,
	)
	if err != nil {
		return fmt.Errorf("creating workflow file: %w", err)
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

// renderPropertiesWorkflow renders the set-custom-properties workflow
// for the current desired state. Shared by the create and refresh paths
// so a refreshed PR can never drift from what a freshly-created one
// would have contained.
func (r *CustomPropertiesReconciler) renderPropertiesWorkflow(
	params *ReconcileParams,
	desired *catalog.Properties,
) (string, error) {
	compiled, err := r.templates.Get("set-custom-properties")
	if err != nil {
		return "", fmt.Errorf("getting set-custom-properties template: %w", err)
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
		return "", fmt.Errorf("rendering set-custom-properties template: %w", err)
	}

	return rendered, nil
}

// refreshPropertiesPR brings an already-open properties PR back in line
// with the current desired state.
//
// Before IMPL-0021 this path returned early, so an annotation edited
// after the PR opened was never reflected in the branch workflow or the
// PR body: the PR sat advertising stale values, and merging it wrote
// the *old* properties (INV-0011 A4).
//
// Both writes are idempotent, so a steady-state sweep over an open PR
// costs zero mutating calls: CreateOrUpdateFile skips byte-identical
// content (INV-0003), and the PR is PATCHed only when the rendered
// title or body actually differs from what GitHub holds. Auto-close for
// properties PRs stays out of scope (IMPL-0021 Decision 2).
func refreshPropertiesPR(
	ctx context.Context,
	log *slog.Logger,
	params *ReconcileParams,
	pr *ghclient.PullRequest,
	rendered string,
	prText renderedPR,
) error {
	if params.DryRun {
		log.Info("dry run: would refresh properties PR", "pr_number", pr.Number)
		return nil
	}

	err := params.Client.CreateOrUpdateFile(
		ctx, params.Owner, params.Repo, PropertiesBranchName,
		propertiesWorkflowPath, rendered, propertiesRefreshCommitMsg,
	)
	if err != nil {
		return fmt.Errorf("refreshing workflow file: %w", err)
	}

	if pr.Title == prText.Title && pr.Body == prText.Body {
		log.Info("properties PR already current", "pr_number", pr.Number)
		return nil
	}

	if err := params.Client.UpdatePullRequest(ctx, params.Owner, params.Repo, pr.Number, prText.Title, prText.Body); err != nil {
		return fmt.Errorf("refreshing properties PR: %w", err)
	}

	log.Info("refreshed stale properties PR", "pr_number", pr.Number)

	return nil
}

func (r *CustomPropertiesReconciler) handleAPIMode(
	ctx context.Context,
	params *ReconcileParams,
	desired *catalog.Properties,
	catalogFound bool,
	defined map[string]struct{},
) error {
	log := params.Logger.With("owner", params.Owner, "repo", params.Repo)

	props := filterBySchema(defined, desiredToPropertyValues(desired, r.managedNames))

	// Defensive: since IMPL-0021 the same schema view gates the drift
	// computation, so a fully-filtered payload can no longer reach this
	// far (nothing defined ⇒ nothing drifts). The guard stays so a
	// future caller can never send GitHub an empty PATCH.
	if len(props) == 0 {
		log.Info("no managed properties present in org schema; nothing to sync")
		return nil
	}

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
		// The engine reached us *because* the file is missing, so the
		// file rule that triggered this reconciler is already opening a
		// PR that adds it. Opening a second one on a second branch
		// would race that PR to add the same path (IMPL-0021 A3).
		if params.FileAbsent {
			log.Info("catalog-info missing; properties cleared, file rule owns adding the file")
			return nil
		}

		return r.createCatalogInfoPR(ctx, params)
	}

	return nil
}

// definedProperties returns the set of custom-property names the org
// schema defines, or nil when the schema is unavailable. A nil set is
// the fail-open signal: every managed property is then treated as
// defined, preserving pre-preflight behavior rather than blocking a
// sync on a broken schema endpoint.
func (r *CustomPropertiesReconciler) definedProperties(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	org string,
) map[string]struct{} {
	defined, err := r.orgSchema(ctx, log, client, org)
	if err != nil {
		return nil
	}

	return defined
}

// propertyDefined reports whether the org schema defines name. A nil
// defined set means the schema lookup failed, in which case every
// property is treated as defined (fail open).
func propertyDefined(defined map[string]struct{}, name string) bool {
	if defined == nil {
		return true
	}

	_, ok := defined[name]

	return ok
}

// filterBySchema drops properties the org's custom-property schema does
// not define. A nil defined set (schema unavailable) passes the payload
// through unfiltered.
func filterBySchema(
	defined map[string]struct{},
	props []*ghclient.CustomPropertyValue,
) []*ghclient.CustomPropertyValue {
	if defined == nil {
		return props
	}

	filtered := make([]*ghclient.CustomPropertyValue, 0, len(props))

	for _, p := range props {
		if _, ok := defined[p.PropertyName]; ok {
			filtered = append(filtered, p)
		}
	}

	return filtered
}

// managedSetNames returns the full managed property set in payload
// order: Owner, Component, then the sorted mapped names.
func (r *CustomPropertiesReconciler) managedSetNames() []string {
	names := make([]string, 0, len(r.managedNames)+2)
	names = append(names, propOwner, propComponent)
	names = append(names, r.managedNames...)

	return names
}

// reportMissingSchema warns once and increments
// CustomPropertyMissingSchemaTotal once per managed property the org
// schema does not define.
//
// This runs on every reconcile, independent of whether any drift was
// found, because the A5 fix deliberately stops schema-missing
// properties from registering as drift. Leaving the report on the
// drift path (its pre-IMPL-0021 home) would have silenced the signal
// entirely and left RepoGuardianPropertySchemaMissing unfireable.
func (r *CustomPropertiesReconciler) reportMissingSchema(
	log *slog.Logger,
	org string,
	defined map[string]struct{},
) {
	if defined == nil {
		return
	}

	var missing []string

	for _, name := range r.managedSetNames() {
		if _, ok := defined[name]; ok {
			continue
		}

		missing = append(missing, name)
		metrics.CustomPropertyMissingSchemaTotal.WithLabelValues(org, name).Inc()
	}

	if len(missing) == 0 {
		return
	}

	// "org" is added explicitly even though the caller's logger
	// already carries the same value as "owner": this is the literal
	// Loki-matching-contract line operators query on
	// (docs/operations/scaling.md), and it must carry "org" +
	// "missing_properties" as flat fields independent of whatever
	// context happens to be chained in via .With().
	log.Warn("custom properties missing from org schema",
		"org", org,
		"missing_properties", missing,
	)
}

// orgSchema returns the org's custom-property schema names as a set,
// using a schemaCacheTTL cache so N repos processed for the same org
// within the window cost exactly one GetOrgPropertySchema call. A
// fetch failure is cached too, and logged exactly once per org per
// window — repeat callers within the TTL get the cached error
// silently, letting filterBySchema fail open without re-logging.
//
// schemaGroup.Do collapses a cold-cache burst of concurrent callers
// for the same org (the common case: the worker pool processes many
// repos from one org at once) into a single fetch and a single
// fail-open log line, rather than one per goroutine that missed the
// cache before the first fetch completed.
func (r *CustomPropertiesReconciler) orgSchema(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	org string,
) (map[string]struct{}, error) {
	r.schemaMu.Lock()
	entry, ok := r.schemaCache[org]
	r.schemaMu.Unlock()

	if ok && time.Since(entry.fetchedAt) < schemaCacheTTL {
		return entry.names, entry.err
	}

	result, err, _ := r.schemaGroup.Do(org, func() (any, error) {
		return r.fetchOrgSchema(ctx, log, client, org), nil
	})
	if err != nil {
		// Unreachable: the Do closure above always returns a nil
		// error itself. The real fetch error lives inside the
		// returned schemaEntry instead, so every waiter observes it
		// (not just the leader goroutine that ran the closure).
		return nil, err
	}

	fetched, ok := result.(schemaEntry)
	if !ok {
		return nil, fmt.Errorf("orgSchema: unexpected singleflight result type %T", result)
	}

	return fetched.names, fetched.err
}

// fetchOrgSchema calls the client, builds the schemaEntry, caches it,
// and logs the fail-open warning on error. Split out of orgSchema so
// the singleflight.Group.Do closure stays a single call.
func (r *CustomPropertiesReconciler) fetchOrgSchema(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	org string,
) schemaEntry {
	names, err := client.GetOrgPropertySchema(ctx, org)

	entry := schemaEntry{fetchedAt: time.Now()}

	if err != nil {
		entry.err = fmt.Errorf("fetching org property schema: %w", err)
		log.Warn("fetching org custom property schema failed; sending unfiltered properties",
			"org", org,
			"error", err,
		)
	} else {
		set := make(map[string]struct{}, len(names))
		for _, name := range names {
			set[name] = struct{}{}
		}

		entry.names = set
	}

	r.schemaMu.Lock()
	r.schemaCache[org] = entry
	r.schemaMu.Unlock()

	return entry
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
//
// Properties the org schema does not define are skipped: no PATCH can
// ever satisfy them, so counting them as drift would re-PATCH the
// already-correct defined properties on every sweep (INV-0011 A5). A
// nil defined set fails open and compares the whole managed set.
func (r *CustomPropertiesReconciler) diffProperties(
	desired *catalog.Properties,
	current []*ghclient.CustomPropertyValue,
	defined map[string]struct{},
) bool {
	currentMap := make(map[string]*string, len(current))
	for _, p := range current {
		currentMap[p.PropertyName] = p.Value
	}

	if propertyDefined(defined, propOwner) && currentValue(currentMap[propOwner]) != desired.Owner {
		return true
	}

	if propertyDefined(defined, propComponent) && currentValue(currentMap[propComponent]) != desired.Component {
		return true
	}

	for _, name := range r.managedNames {
		if !propertyDefined(defined, name) {
			continue
		}

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
		&ghclient.CustomPropertyValue{PropertyName: propOwner, Value: &owner},
		&ghclient.CustomPropertyValue{PropertyName: propComponent, Value: &component},
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
