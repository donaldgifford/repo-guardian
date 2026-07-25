package checker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// checkRepoWithPolicy runs policy-based evaluation for a repository.
func (e *Engine) checkRepoWithPolicy(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	openPRs []*ghclient.PullRequest,
) error {
	if !policyScopeAllows(e.policy, owner) {
		log.Info("repository out of policy scope, skipping all rules")
		recordOutOfScopePolicy(e.policy, owner)

		return nil
	}

	// gate memoizes when-gate referee evaluations for this single
	// repo-check and is threaded into both file-rule passes. It must be
	// per-repo-check (never stored on the shared Engine) — see gate.go.
	gate := newGateEvaluator(e, client, owner, repo)

	actionable, err := e.findActionableRules(ctx, log, client, owner, repo, openPRs, gate)
	if err != nil {
		return err
	}

	ourPR := findOurPR(openPRs)

	switch {
	case len(actionable) == 0:
		// INV-0005 drift surface: if an open repo-guardian PR exists
		// but no rule is actionable, the PR was orphaned by an
		// out-of-band merge to main. IMPL-0013 Phase 3 closes the
		// PR per the auto_close_pr knob.
		if ourPR != nil {
			metrics.PROpenWithEmptyActionableTotal.WithLabelValues(owner).Inc()

			switch {
			case e.dryRun:
				log.Warn("dry run: would auto-close PR — file rules satisfied on default branch",
					"pr_number", ourPR.Number)
			case e.policy.Guardian.AutoClosePREnabled():
				if err := autoClosePR(ctx, log, client, owner, repo, ourPR, e.policy.FileRules); err != nil {
					log.Error("auto-close PR failed; will retry on next sweep",
						"pr_number", ourPR.Number, "err", err)
				}
			default:
				log.Warn("open PR with empty actionable set — file rules satisfied on default branch (auto_close_pr disabled)",
					"pr_number", ourPR.Number)
				// IMPL-0013 Phase 4: log the convergent state via
				// the sticky comment so operators reading the PR
				// understand why no progress is happening despite
				// every rule passing on main.
				events := buildReconcileLogEvents(e.policy.FileRules, actionable, nil, nil, gate)
				upsertReconcileLog(ctx, log, client, owner, repo, ourPR, events)
			}
		} else {
			log.Info("all required files present")
		}
	case e.dryRun:
		log.Info("dry run: would create PR",
			"actionable_rules", policyRuleNames(actionable),
			"planned_deletions", plannedDeletions(actionable))
	default:
		if err := e.createOrUpdatePRFromPolicy(ctx, client, owner, repo, defaultBranch, actionable, openPRs, gate); err != nil {
			return err
		}
	}

	// IMPL-0013 Phase 1: populate OpenPRsByRule for any rule referenced
	// by the open repo-guardian PR. The gauge represents a per-sweep
	// snapshot; sweepers reset the entire gauge at iteration start.
	if ourPR != nil {
		recordOpenPRsByRule(ourPR, actionable, owner)
	}

	// Run reconcilers for rules where the file check passed.
	e.runReconcilers(ctx, log, client, owner, repo, defaultBranch, openPRs, gate)

	// Evaluate setting rules.
	if err := e.evaluateSettingRules(ctx, log, client, owner, repo); err != nil {
		return fmt.Errorf("evaluating setting rules: %w", err)
	}

	// Evaluate branch protection rules.
	if err := e.evaluateBranchProtectionRules(ctx, log, client, owner, repo); err != nil {
		return fmt.Errorf("evaluating branch protection rules: %w", err)
	}

	return nil
}

// runReconcilers executes reconcilers for rules where the file check passed.
// Reconcilers run after file assertions pass — for exists mode when the file
// is present, for contains mode when assertions pass, and for exact mode when
// the file matches the template.
func (e *Engine) runReconcilers(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	openPRs []*ghclient.PullRequest,
	gate *gateEvaluator,
) {
	ownerRepo := owner + "/" + repo
	strict := strictMode(e.policy)

	for i := range e.policy.FileRules {
		r := &e.policy.FileRules[i]

		if !r.IsEnabled() {
			continue
		}

		// Scope was already counted in findActionableRules' rule-level gate
		// for this same rule; this pass only short-circuits the reconciler.
		if !ruleScopeAllows(r.Scope, owner, strict) {
			continue
		}

		if r.Ignore != nil && r.Ignore.Matches(ownerRepo) {
			continue
		}

		// when-gate (IMPL-0019): reuse the memoized result from the primary
		// pass. Silent by design — the RuleGateClosedTotal counter is owned
		// by findActionableRules; incrementing here too would double-count
		// under the file-rule double-iteration contract.
		if open, _ := gate.gateOpen(ctx, log, r); !open {
			continue
		}

		key := r.Type + ":" + r.Name
		recs := e.ruleReconcilers[key]

		if len(recs) == 0 {
			continue
		}

		existingPath, err := findExistingFile(ctx, client, owner, repo, r.Paths)
		if err != nil {
			log.Error("error checking file for reconciler", "rule", r.Name, "error", err)
			continue
		}

		// The file being absent is itself a desired state for some
		// reconcilers (custom_properties clears the managed set), so
		// absence no longer skips the whole rule — it narrows the run
		// to reconcilers that opt in via RunsOnAbsence (INV-0011 A3).
		fileAbsent := existingPath == ""

		var content string

		if !fileAbsent {
			content = e.getFileContentForReconciler(ctx, log, client, owner, repo, existingPath, r)
			if content == "" {
				continue
			}
		}

		params := &reconciler.ReconcileParams{
			Client:        client,
			Owner:         owner,
			Repo:          repo,
			DefaultBranch: defaultBranch,
			Content:       content,
			FileAbsent:    fileAbsent,
			OpenPRs:       openPRs,
			DryRun:        e.dryRun,
			Logger:        log.With("rule", r.Name),
		}

		for _, rec := range recs {
			if fileAbsent && !rec.RunsOnAbsence() {
				continue
			}

			recLog := log.With("rule", r.Name, "reconciler", rec.Name())
			params.PRTemplate = e.policy.ReconcilerPR(r.Name, rec.Name())

			if err := rec.Reconcile(ctx, params); err != nil {
				recLog.Error("reconciler failed", "error", err)
			}
		}
	}
}

// getFileContentForReconciler reads file content and validates assertions
// based on the rule's check mode. Returns empty string if the reconciler
// should not run (assertions failed, content mismatch, etc.).
func (e *Engine) getFileContentForReconciler(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, existingPath string,
	rule *policy.FileRuleConfig,
) string {
	content, err := client.GetFileContent(ctx, owner, repo, existingPath)
	if err != nil {
		log.Error("error reading file for reconciler", "path", existingPath, "error", err)
		return ""
	}

	switch rule.CheckMode() {
	case policy.CheckContains:
		key := rule.Type + ":" + rule.Name
		assertions := e.compiledAssertions[key]

		if len(assertions) > 0 {
			if err := policy.EvaluateAssertions(assertions, content); err != nil {
				return ""
			}
		}
	case policy.CheckExact:
		templateContent, err := e.templates.Raw(rule.Template)
		if err != nil {
			log.Error("error getting template for reconciler", "template", rule.Template, "error", err)
			return ""
		}

		if compareContent(log, existingPath, content, templateContent) {
			return ""
		}
	}

	return content
}

// findActionableRules evaluates policy-based file rules against a repository
// and returns the rules that require action (PR creation).
func (e *Engine) findActionableRules(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	openPRs []*ghclient.PullRequest,
	gate *gateEvaluator,
) ([]policy.FileRuleConfig, error) {
	var actionable []policy.FileRuleConfig

	ownerRepo := owner + "/" + repo
	strict := strictMode(e.policy)

	for i := range e.policy.FileRules {
		r := &e.policy.FileRules[i]

		if !r.IsEnabled() {
			continue
		}

		ruleLog := log.With("rule", r.Name, "check", r.CheckMode())

		if !ruleScopeAllows(r.Scope, owner, strict) {
			ruleLog.Info("rule out of scope for org, skipping")
			metrics.OutOfScopeTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		if r.Ignore != nil && r.Ignore.Matches(ownerRepo) {
			ruleLog.Info("repository matched per-rule ignore list, skipping")
			metrics.IgnoredTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		// when-gate (IMPL-0019): a gated rule fires only when its referee
		// is satisfied on the default branch. A closed gate — including a
		// referee evaluation error (fail-closed) — skips the rule. This is
		// the primary pass, so it owns the RuleGateClosedTotal counter; the
		// runReconcilers pass reads the same memoized result silently.
		if open, reason := gate.gateOpen(ctx, ruleLog, r); !open {
			ruleLog.Info("rule gate closed, skipping rule",
				"referee", r.When.RuleSatisfied, "reason", reason)
			metrics.RuleGateClosedTotal.WithLabelValues(r.Name, owner, reason).Inc()

			continue
		}

		action, err := e.evaluateRule(ctx, ruleLog, client, owner, repo, r, openPRs)
		if err != nil {
			return nil, fmt.Errorf("evaluating rule %q: %w", r.Name, err)
		}

		if action {
			e.recordActionable(ruleLog, r, owner)
			actionable = append(actionable, *r)
		}
	}

	return actionable, nil
}

// recordActionable logs and counts an actionable file rule, routing absent
// rules to FilesForbiddenPresentTotal and all other modes to
// FilesMissingTotal (IMPL-0019 task 1.7).
func (*Engine) recordActionable(ruleLog *slog.Logger, r *policy.FileRuleConfig, owner string) {
	if r.CheckMode() == policy.CheckAbsent {
		ruleLog.Info("forbidden file present, will remove")
		metrics.FilesForbiddenPresentTotal.WithLabelValues(r.Name, owner).Inc()

		return
	}

	ruleLog.Info("rule requires action")
	metrics.FilesMissingTotal.WithLabelValues(r.Name, owner).Inc()
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
	case policy.CheckAbsent:
		return e.evaluateAbsent(log, existingPath), nil
	default:
		return e.evaluateExists(log, existingPath), nil
	}
}

// evaluateAbsent reports whether an absent-mode rule is actionable: true
// iff at least one forbidden path exists on the default branch
// (existence-only; findExistingFile already short-circuits on the first
// hit, so no content is fetched). Remediation — deleting the present
// paths on the reconcile branch — lands in IMPL-0019 Phase 2 (task 2.1).
func (*Engine) evaluateAbsent(log *slog.Logger, existingPath string) bool {
	if existingPath == "" {
		log.Debug("no forbidden files present, absent rule satisfied")

		return false
	}

	// recordActionable owns the Info-level "actionable" log for every check
	// mode; keep the path detail here at Debug to avoid double-logging.
	log.Debug("forbidden file present", "path", existingPath)

	return true
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

	templateContent, err := e.templates.Raw(rule.Template)
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
	gate *gateEvaluator,
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

	// WARN: This loop syncs file content to the reconcile branch but does not
	//   re-base the branch onto the current default-branch HEAD. If the PR sits
	//   open while default advances, the branch's base SHA goes stale. Two
	//   risks if auto-merge is enabled on the repo-guardian PR:
	//   (1) base drift — usually safe (diff is "add this file") but loses
	//       linear-history aesthetic; (2) content drift — if someone manually
	//       writes a different version of the file to default in the gap, the
	//       file rule's existence check stops flagging it, this loop no-ops,
	//       and a squash-merge of the stale branch overwrites the manual
	//       edit. Mitigations to consider: rebase the branch onto current
	//       default before reconcile, close+reopen PRs older than N days,
	//       or recommend operators don't enable auto-merge on
	//       repo-guardian/* branches. See conversation in PR #71.
	now := time.Now().UTC().Format(time.RFC3339)

	if err := e.syncActionableFiles(ctx, log, client, owner, repo, defaultBranch, now, actionable); err != nil {
		return err
	}

	if existingPR == nil {
		return e.createNewPolicyPR(ctx, log, client, owner, repo, defaultBranch, now, actionable)
	}

	// IMPL-0013 Phase 3: existing PR is being updated. Remove orphan
	// files (rules that were in a previous version of the PR but are
	// now satisfied on the default branch) and refresh the PR body.
	orphans := discoverOrphans(ctx, log, client, e.policy.FileRules, actionable, owner, repo)

	var removedOrphans []string

	if len(orphans) > 0 {
		log.Info("cleaning up orphan files", "count", len(orphans))
		removedOrphans = cleanupOrphans(ctx, log, client, orphans, owner, repo)
	}

	// IMPL-0019 Phase 2: inverse orphans — absent rules that stopped being
	// actionable and whose forbidden file the branch deleted must be
	// restored so the PR stops proposing the deletion.
	restoredRules := restoreInverseOrphans(ctx, log, client, e.policy.FileRules, actionable, owner, repo)

	if err := e.refreshPolicyPR(ctx, log, client, owner, repo, defaultBranch, existingPR, actionable, now); err != nil {
		log.Warn("PR body refresh failed; PR text may be stale until next sweep", "err", err)
	}

	// IMPL-0013 Phase 4: sticky reconcile-log comment with per-rule
	// status. Best-effort — failures don't abort the sweep.
	events := buildReconcileLogEvents(e.policy.FileRules, actionable, removedOrphans, restoredRules, gate)
	upsertReconcileLog(ctx, log, client, owner, repo, existingPR, events)

	metrics.PRsUpdatedTotal.WithLabelValues(owner).Inc()
	log.Info("updated existing PR", "pr_number", existingPR.Number)

	return nil
}

// syncActionableFiles renders and commits the template content for
// every actionable rule. Extracted from createOrUpdatePRFromPolicy
// to keep cyclomatic complexity under the gocyclo threshold.
func (e *Engine) syncActionableFiles(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch, now string,
	actionable []policy.FileRuleConfig,
) error {
	for i := range actionable {
		r := &actionable[i]

		if r.CheckMode() == policy.CheckAbsent {
			if err := e.removeForbiddenFiles(ctx, log, client, owner, repo, r); err != nil {
				return err
			}

			continue
		}

		compiled, err := e.templates.Get(r.Template)
		if err != nil {
			return fmt.Errorf("getting template for %s: %w", r.Name, err)
		}

		content, err := compiled.Render(tmpl.FileVars{
			Common: tmpl.Common{
				Owner:         owner,
				Repo:          repo,
				DefaultBranch: defaultBranch,
				Date:          now,
			},
			Rule: tmpl.Rule{Name: r.Name, Target: r.Target},
			Org:  owner,
		})
		if err != nil {
			return fmt.Errorf("rendering template for %s: %w", r.Name, err)
		}

		msg := fmt.Sprintf("chore: add %s", r.Target)

		if err := client.CreateOrUpdateFile(ctx, owner, repo, BranchName, r.Target, content, msg); err != nil {
			return fmt.Errorf("creating file %s: %w", r.Target, err)
		}

		log.Info("added file", "path", r.Target)
	}

	return nil
}

// removeForbiddenFiles deletes every path of an absent-mode rule that is
// present on the reconcile branch, mirroring the INV-0003 three-branch
// idempotency contract: probe the reconcile branch first, delete with the
// blob SHA when present, skip when already absent so a second sweep is a
// no-op. A path that is already gone is not an error — the branch has
// converged for that path (IMPL-0019 Phase 2 task 2.1).
func (*Engine) removeForbiddenFiles(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	r *policy.FileRuleConfig,
) error {
	for _, path := range r.Paths {
		sha, exists, err := client.GetContentsOnBranch(ctx, owner, repo, path, BranchName)
		if err != nil {
			return fmt.Errorf("checking %s on branch for absent rule %q: %w", path, r.Name, err)
		}

		if !exists {
			log.Debug("forbidden file already absent on branch", "path", path, "rule", r.Name)

			continue
		}

		msg := fmt.Sprintf("chore: remove %s (forbidden by rule %q)", path, r.Name)

		if err := client.DeleteFile(ctx, owner, repo, BranchName, path, sha, msg); err != nil {
			return fmt.Errorf("deleting %s for absent rule %q: %w", path, r.Name, err)
		}

		log.Info("removed forbidden file", "path", path, "rule", r.Name)
	}

	return nil
}

// refreshPolicyPR re-renders the title and body for the current
// actionable set and updates the PR if either drifted from the
// in-flight values. No-op when both match — avoids API churn on
// reconcile passes where nothing changed.
func (e *Engine) refreshPolicyPR(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	existingPR *ghclient.PullRequest,
	actionable []policy.FileRuleConfig,
	now string,
) error {
	fallbackBody := buildPRBodyFromPolicy(actionable)
	vars := buildPRVars(owner, repo, defaultBranch, now, actionable)

	var (
		rendered renderedPR
		err      error
	)

	switch len(actionable) {
	case 1:
		rendered, err = e.resolveAndRenderRulePR(log, &actionable[0], &vars, PRTitle, fallbackBody)
	default:
		rendered, err = e.resolveAndRenderBundlePR(log, actionable, &vars, PRTitle, fallbackBody)
	}

	if err != nil {
		return fmt.Errorf("resolving PR template: %w", err)
	}

	if rendered.Title == existingPR.Title {
		// Body is not exposed on the PR struct today; conservatively
		// always issue the patch when the title is stable but the
		// actionable set changed. The cost is one PATCH per sweep on
		// touched repos, which fits inside the per-installation rate
		// budget.
		_ = rendered.Body
	}

	if err := client.UpdatePullRequest(ctx, owner, repo, existingPR.Number, rendered.Title, rendered.Body); err != nil {
		return fmt.Errorf("update PR #%d: %w", existingPR.Number, err)
	}

	return nil
}

// createNewPolicyPR resolves PR templates, renders title/body/labels,
// creates the PR, and applies labels. Extracted from
// createOrUpdatePRFromPolicy to keep cyclomatic complexity in check.
func (e *Engine) createNewPolicyPR(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch, now string,
	actionable []policy.FileRuleConfig,
) error {
	fallbackBody := buildPRBodyFromPolicy(actionable)
	vars := buildPRVars(owner, repo, defaultBranch, now, actionable)

	var (
		rendered renderedPR
		err      error
	)

	switch len(actionable) {
	case 1:
		rendered, err = e.resolveAndRenderRulePR(log, &actionable[0], &vars, PRTitle, fallbackBody)
	default:
		rendered, err = e.resolveAndRenderBundlePR(log, actionable, &vars, PRTitle, fallbackBody)
	}

	if err != nil {
		return fmt.Errorf("resolving PR template: %w", err)
	}

	pr, err := client.CreatePullRequest(ctx, owner, repo, rendered.Title, rendered.Body, BranchName, defaultBranch)
	if err != nil {
		return fmt.Errorf("creating PR: %w", err)
	}

	if err := client.AddLabelsToPR(ctx, owner, repo, pr.Number, rendered.Labels); err != nil {
		log.Warn("adding labels to PR failed; continuing", "pr_number", pr.Number, "err", err)
	}

	metrics.PRsCreatedTotal.WithLabelValues(owner).Inc()
	log.Info("created PR", "pr_number", pr.Number)

	return nil
}

func buildPRBodyFromPolicy(actionable []policy.FileRuleConfig) string {
	var added, removed []policy.FileRuleConfig

	for i := range actionable {
		if actionable[i].CheckMode() == policy.CheckAbsent {
			removed = append(removed, actionable[i])
		} else {
			added = append(added, actionable[i])
		}
	}

	var sb strings.Builder

	sb.WriteString("## Repo Guardian — Configuration Drift\n\n")
	sb.WriteString("This PR was automatically created by **repo-guardian** to bring this\n")
	sb.WriteString("repository into line with the organization's file policy.\n\n")

	if len(added) > 0 {
		sb.WriteString("### Added Files\n\n")

		for i := range added {
			fmt.Fprintf(&sb, "- `%s` — %s\n", added[i].Target, added[i].Name)
		}

		// The CODEOWNERS placeholder note is only relevant when a file is
		// being added; a removal-only PR must not mention it.
		sb.WriteString("\n> **Note:** The CODEOWNERS file contains a placeholder (`@org/CHANGEME`).\n")
		sb.WriteString("> Please replace it with your actual team before merging.\n\n")
	}

	if len(removed) > 0 {
		sb.WriteString("### Removed Files\n\n")

		for i := range removed {
			for _, path := range removed[i].Paths {
				fmt.Fprintf(&sb, "- `%s` — %s\n", path, removed[i].Name)
			}
		}

		sb.WriteString("\n")
	}

	sb.WriteString("### What to do\n\n")
	sb.WriteString("1. Review the changes and adjust for your team's needs.\n")
	sb.WriteString("2. Merge when ready — these are sensible defaults, not one-size-fits-all.\n\n")
	sb.WriteString("---\n")
	sb.WriteString("*Automated by [repo-guardian](https://github.com/apps/repo-guardian). ")
	sb.WriteString("Questions? Reach out in #platform-engineering.*\n")

	return sb.String()
}

// evaluateSettingRules checks all setting rules against the repository.
func (e *Engine) evaluateSettingRules(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
) error {
	if len(e.policy.SettingRules) == 0 {
		return nil
	}

	ownerRepo := owner + "/" + repo
	strict := strictMode(e.policy)

	for i := range e.policy.SettingRules {
		r := &e.policy.SettingRules[i]

		if !r.IsEnabled() {
			continue
		}

		ruleLog := log.With("setting_rule", r.Name, "property", r.Property)

		if !ruleScopeAllows(r.Scope, owner, strict) {
			ruleLog.Info("setting rule out of scope for org, skipping")
			metrics.OutOfScopeTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		if r.Ignore != nil && r.Ignore.Matches(ownerRepo) {
			ruleLog.Info("repository matched per-rule ignore list, skipping setting rule")
			metrics.IgnoredTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		if err := e.evaluateSettingRule(ctx, ruleLog, client, owner, repo, r); err != nil {
			return fmt.Errorf("evaluating setting rule %q: %w", r.Name, err)
		}
	}

	return nil
}

// evaluateSettingRule checks a single setting rule against the repository.
func (e *Engine) evaluateSettingRule(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.SettingRuleConfig,
) error {
	metrics.SettingsCheckedTotal.WithLabelValues(rule.Name, owner).Inc()

	currentValue, err := e.getSettingValue(ctx, client, owner, repo, rule.Property)
	if err != nil {
		return fmt.Errorf("getting current value for %s: %w", rule.Property, err)
	}

	if settingMatches(currentValue, rule.Expected) {
		log.Debug("setting matches expected value", "current", currentValue)
		return nil
	}

	metrics.SettingsMismatchedTotal.WithLabelValues(rule.Name, owner).Inc()
	log.Info("setting mismatch", "current", currentValue, "expected", rule.Expected)

	if !rule.Remediate {
		return nil
	}

	if e.dryRun {
		log.Info("dry run: would remediate setting", "current", currentValue, "expected", rule.Expected)
		return nil
	}

	if err := e.remediateSetting(ctx, log, client, owner, repo, rule); err != nil {
		return fmt.Errorf("remediating %s: %w", rule.Property, err)
	}

	metrics.SettingsRemediatedTotal.WithLabelValues(rule.Name, owner).Inc()
	log.Info("remediated setting", "property", rule.Property)

	return nil
}

// getSettingValue reads the current value of a repository setting.
func (*Engine) getSettingValue(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, property string,
) (any, error) {
	if property == policy.SettingVulnerabilityAlertsEnabled {
		return client.GetVulnerabilityAlertsEnabled(ctx, owner, repo)
	}

	settings, err := client.GetRepoSettings(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	switch property {
	case policy.SettingDefaultBranch:
		return settings.DefaultBranch, nil
	case policy.SettingHasIssues:
		return settings.HasIssues, nil
	case policy.SettingHasWiki:
		return settings.HasWiki, nil
	case policy.SettingDeleteBranchOnMerge:
		return settings.DeleteBranchOnMerge, nil
	case policy.SettingAllowMergeCommit:
		return settings.AllowMergeCommit, nil
	case policy.SettingAllowSquashMerge:
		return settings.AllowSquashMerge, nil
	case policy.SettingAllowRebaseMerge:
		return settings.AllowRebaseMerge, nil
	default:
		return nil, fmt.Errorf("unsupported property: %s", property)
	}
}

// settingMatches compares the current value against the expected value.
func settingMatches(current, expected any) bool {
	return fmt.Sprintf("%v", current) == fmt.Sprintf("%v", expected)
}

// remediateSetting applies the expected setting value via the GitHub API.
func (*Engine) remediateSetting(
	ctx context.Context,
	_ *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.SettingRuleConfig,
) error {
	switch rule.Property {
	case policy.SettingVulnerabilityAlertsEnabled:
		expected, ok := rule.Expected.(bool)
		if !ok {
			return fmt.Errorf("expected bool for %s, got %T", policy.SettingVulnerabilityAlertsEnabled, rule.Expected)
		}

		if expected {
			return client.EnableVulnerabilityAlerts(ctx, owner, repo)
		}

		return client.DisableVulnerabilityAlerts(ctx, owner, repo)

	case policy.SettingDefaultBranch:
		expected, ok := rule.Expected.(string)
		if !ok {
			return fmt.Errorf("expected string for %s, got %T", policy.SettingDefaultBranch, rule.Expected)
		}

		return client.UpdateRepository(ctx, owner, repo, &ghclient.RepoUpdateOpts{
			DefaultBranch: &expected,
		})

	default:
		return remediateBoolSetting(ctx, client, owner, repo, rule)
	}
}

// remediateBoolSetting handles remediation for boolean repository settings.
func remediateBoolSetting(
	ctx context.Context,
	client ghclient.Client,
	owner, repo string,
	rule *policy.SettingRuleConfig,
) error {
	expected, ok := rule.Expected.(bool)
	if !ok {
		return fmt.Errorf("expected bool for %s, got %T", rule.Property, rule.Expected)
	}

	opts := &ghclient.RepoUpdateOpts{}

	switch rule.Property {
	case policy.SettingHasIssues:
		opts.HasIssues = &expected
	case policy.SettingHasWiki:
		opts.HasWiki = &expected
	case policy.SettingDeleteBranchOnMerge:
		opts.DeleteBranchOnMerge = &expected
	case policy.SettingAllowMergeCommit:
		opts.AllowMergeCommit = &expected
	case policy.SettingAllowSquashMerge:
		opts.AllowSquashMerge = &expected
	case policy.SettingAllowRebaseMerge:
		opts.AllowRebaseMerge = &expected
	default:
		return fmt.Errorf("unsupported bool property: %s", rule.Property)
	}

	return client.UpdateRepository(ctx, owner, repo, opts)
}

// evaluateBranchProtectionRules checks all branch protection rules against the repository.
func (e *Engine) evaluateBranchProtectionRules(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
) error {
	if len(e.policy.BranchProtectionRules) == 0 {
		return nil
	}

	ownerRepo := owner + "/" + repo
	strict := strictMode(e.policy)

	for i := range e.policy.BranchProtectionRules {
		r := &e.policy.BranchProtectionRules[i]

		if !r.IsEnabled() {
			continue
		}

		ruleLog := log.With("bp_rule", r.Name, "branch", r.Branch)

		if !ruleScopeAllows(r.Scope, owner, strict) {
			ruleLog.Info("branch protection rule out of scope for org, skipping")
			metrics.OutOfScopeTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		if r.Ignore != nil && r.Ignore.Matches(ownerRepo) {
			ruleLog.Info("repository matched per-rule ignore list, skipping branch protection rule")
			metrics.IgnoredTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		if err := e.evaluateBranchProtectionRule(ctx, ruleLog, client, owner, repo, r); err != nil {
			return fmt.Errorf("evaluating branch protection rule %q: %w", r.Name, err)
		}
	}

	return nil
}

// evaluateBranchProtectionRule checks a single branch protection rule.
func (e *Engine) evaluateBranchProtectionRule(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.BranchProtectionRuleConfig,
) error {
	metrics.BranchProtectionCheckedTotal.WithLabelValues(rule.Name, owner).Inc()

	// Check if the branch exists.
	sha, err := client.GetBranchSHA(ctx, owner, repo, rule.Branch)
	if err != nil {
		return fmt.Errorf("checking branch %s: %w", rule.Branch, err)
	}

	if sha == "" {
		log.Warn("branch does not exist, skipping branch protection rule")
		return nil
	}

	// Fetch existing rulesets.
	rulesets, err := client.ListRepositoryRulesets(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("listing rulesets: %w", err)
	}

	// Find a matching ruleset for this branch.
	existing := findMatchingRuleset(rulesets, rule)

	mismatches := compareBranchProtection(existing, rule)
	if len(mismatches) == 0 {
		log.Debug("branch protection matches expected configuration")
		return nil
	}

	log.Info("branch protection mismatch", "mismatches", mismatches)

	if !rule.Remediate {
		return nil
	}

	if e.dryRun {
		log.Info("dry run: would remediate branch protection", "mismatches", mismatches)
		return nil
	}

	desired := buildDesiredRuleset(rule)

	if existing != nil {
		if _, err := client.UpdateRepositoryRuleset(ctx, owner, repo, existing.ID, desired); err != nil {
			return fmt.Errorf("updating ruleset: %w", err)
		}

		log.Info("updated branch protection ruleset")
	} else {
		if _, err := client.CreateRepositoryRuleset(ctx, owner, repo, desired); err != nil {
			return fmt.Errorf("creating ruleset: %w", err)
		}

		log.Info("created branch protection ruleset")
	}

	metrics.BranchProtectionRemediatedTotal.WithLabelValues(rule.Name, owner).Inc()

	return nil
}

// findMatchingRuleset finds a ruleset that targets the same branch pattern.
func findMatchingRuleset(
	rulesets []*ghclient.Ruleset,
	rule *policy.BranchProtectionRuleConfig,
) *ghclient.Ruleset {
	for _, rs := range rulesets {
		if rs.Conditions == nil {
			continue
		}

		for _, pattern := range rs.Conditions.IncludePatterns {
			if pattern == rule.Branch || pattern == "refs/heads/"+rule.Branch {
				return rs
			}
		}
	}

	return nil
}

// compareBranchProtection compares the existing ruleset against the desired config.
// Returns a list of mismatch descriptions.
func compareBranchProtection(
	existing *ghclient.Ruleset,
	rule *policy.BranchProtectionRuleConfig,
) []string {
	if existing == nil {
		if rule.RequirePR || rule.RequireLinearHistory || len(rule.RequireStatusChecks) > 0 {
			return []string{"no matching ruleset found"}
		}

		return nil
	}

	var mismatches []string

	if rule.RequirePR && existing.RequirePullRequest == nil {
		mismatches = append(mismatches, "pull request required but not configured")
	}

	if rule.RequirePR && existing.RequirePullRequest != nil {
		pr := existing.RequirePullRequest

		if pr.RequiredApprovals != rule.RequiredApprovals {
			mismatches = append(mismatches, fmt.Sprintf(
				"required_approvals: got %d, want %d",
				pr.RequiredApprovals, rule.RequiredApprovals,
			))
		}

		if pr.DismissStaleReviews != rule.DismissStaleReviews {
			mismatches = append(mismatches, fmt.Sprintf(
				"dismiss_stale_reviews: got %v, want %v",
				pr.DismissStaleReviews, rule.DismissStaleReviews,
			))
		}
	}

	if rule.RequireLinearHistory != existing.RequireLinearHistory {
		mismatches = append(mismatches, fmt.Sprintf(
			"require_linear_history: got %v, want %v",
			existing.RequireLinearHistory, rule.RequireLinearHistory,
		))
	}

	return mismatches
}

// buildDesiredRuleset creates a Ruleset from the branch protection config.
func buildDesiredRuleset(rule *policy.BranchProtectionRuleConfig) *ghclient.Ruleset {
	rs := &ghclient.Ruleset{
		Name:        "repo-guardian-" + rule.Name,
		Enforcement: "active",
		Target:      "branch",
		Conditions: &ghclient.RulesetConditions{
			IncludePatterns: []string{"refs/heads/" + rule.Branch},
		},
		RequireLinearHistory: rule.RequireLinearHistory,
	}

	if rule.RequirePR {
		rs.RequirePullRequest = &ghclient.RulesetPullRequest{
			RequiredApprovals:   rule.RequiredApprovals,
			DismissStaleReviews: rule.DismissStaleReviews,
		}
	}

	if len(rule.RequireStatusChecks) > 0 {
		rs.RequireStatusChecks = &ghclient.RulesetStatusChecks{
			RequiredChecks:     rule.RequireStatusChecks,
			StrictStatusChecks: true,
		}
	}

	return rs
}

// policyRuleNames extracts rule names from policy file rules.
func policyRuleNames(rr []policy.FileRuleConfig) []string {
	names := make([]string, len(rr))
	for i := range rr {
		names[i] = rr[i].Name
	}

	return names
}

// plannedDeletions lists the forbidden paths every actionable absent rule
// would delete, so the dry-run log is reviewable before the engine's first
// destructive remediation actually runs (IMPL-0019 Phase 2 task 2.2).
func plannedDeletions(actionable []policy.FileRuleConfig) []string {
	var paths []string

	for i := range actionable {
		if actionable[i].CheckMode() == policy.CheckAbsent {
			paths = append(paths, actionable[i].Paths...)
		}
	}

	return paths
}
