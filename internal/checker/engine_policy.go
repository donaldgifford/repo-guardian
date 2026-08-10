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
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
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
	// Yield to a PR someone else already opened for this rule. Our own
	// reconcile PR never matches here — it is handled by the converge
	// path (see foreignPRForRule).
	if pr, term := foreignPRForRule(openPRs, rule); pr != nil {
		log.Info("existing PR found, skipping rule",
			"pr_number", pr.Number,
			"pr_head", pr.Head,
			"matched_term", term,
		)

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

// policyRuleNames extracts rule names from policy file rules.
func policyRuleNames(rr []policy.FileRuleConfig) []string {
	names := make([]string, len(rr))
	for i := range rr {
		names[i] = rr[i].Name
	}

	return names
}

// plannedWrites returns the paths syncActionableFiles commits on this
// sweep: the Target of every actionable rule that is not in absent mode.
// Mirror of plannedDeletions.
func plannedWrites(actionable []policy.FileRuleConfig) []string {
	var paths []string

	for i := range actionable {
		if actionable[i].CheckMode() != policy.CheckAbsent && actionable[i].Target != "" {
			paths = append(paths, actionable[i].Target)
		}
	}

	return paths
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
