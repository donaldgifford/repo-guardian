package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// VersionV2Prefix marks a v2 policy version. A v1 Version is bare hex,
// so the two can never compare equal.
const VersionV2Prefix = "v2:"

// VersionV2 returns the v2 policy version (DESIGN-0025 § Policy version
// v2): "v2:" + hex(sha256) of an explicit, json-tagged versionInput plus
// every template's name and content.
//
// Only settings that can change an outcome or an action are hashed.
// Operational knobs (log_level, schedule_interval, worker_count,
// queue_size, rate_limit_threshold) are not, so tuning them never
// re-checks the fleet. The input is built by copying fields, never by
// embedding, so renaming a Go field cannot change the version; the
// classification test fails for any policy field not explicitly
// declared hashed or not hashed.
func VersionV2(cfg *PolicyConfig, templates map[string]string) (string, error) {
	if cfg == nil {
		return "", errors.New("policy.VersionV2: nil config")
	}

	b, err := json.Marshal(newVersionInput(cfg, templates))
	if err != nil {
		return "", fmt.Errorf("policy.VersionV2: marshal: %w", err)
	}

	sum := sha256.Sum256(b)

	return VersionV2Prefix + hex.EncodeToString(sum[:]), nil
}

// versionInput is the hashed projection of a policy. Its json tags are
// the hash's stable names: change one only on purpose, with the golden.
type versionInput struct {
	Guardian         guardianInput      `json:"guardian"`
	Ignore           []string           `json:"ignore"`
	Scope            *scopeInput        `json:"scope"`
	DefaultsPR       *prInput           `json:"defaults_pr"`
	FileRules        []fileRuleInput    `json:"file_rules"`
	SettingRules     []settingRuleInput `json:"setting_rules"`
	BranchProtection []branchProtInput  `json:"branch_protection_rules"`
	Templates        map[string]string  `json:"templates"`
}

type guardianInput struct {
	DryRun        bool `json:"dry_run"`
	SkipForks     bool `json:"skip_forks"`
	SkipArchived  bool `json:"skip_archived"`
	AutoClosePR   bool `json:"auto_close_pr"`
	OrphanCleanup bool `json:"orphan_cleanup"`
}

type scopeInput struct {
	Orgs []string `json:"orgs"`
}

type prInput struct {
	SearchTerms []string `json:"search_terms"`
	Title       *string  `json:"title"`
	Body        *string  `json:"body"`
	Labels      []string `json:"labels"`
	LabelsSet   bool     `json:"labels_set"`
	Inherits    *bool    `json:"inherits"`
}

type assertionInput struct {
	Pattern    string `json:"pattern"`
	NotPattern string `json:"not_pattern"`
	YAMLPath   string `json:"yaml_path"`
	Contains   string `json:"contains"`
	Equals     string `json:"equals"`
	NonEmpty   bool   `json:"non_empty"`
	Message    string `json:"message"`
}

type reconcilerInput struct {
	Type                 string            `json:"type"`
	Watch                bool              `json:"watch"`
	Mode                 string            `json:"mode"`
	DeleteExtra          bool              `json:"delete_extra"`
	PR                   *prInput          `json:"pr"`
	AnnotationProperties map[string]string `json:"annotation_properties"`
}

type fileRuleInput struct {
	Type          string            `json:"type"`
	Name          string            `json:"name"`
	Enabled       bool              `json:"enabled"`
	Check         string            `json:"check"`
	Paths         []string          `json:"paths"`
	Target        string            `json:"target"`
	Template      string            `json:"template"`
	PR            *prInput          `json:"pr"`
	Assertions    []assertionInput  `json:"assertions"`
	Ignore        []string          `json:"ignore"`
	Scope         *scopeInput       `json:"scope"`
	Reconcilers   []reconcilerInput `json:"reconcilers"`
	RuleSatisfied string            `json:"rule_satisfied"`
}

type settingRuleInput struct {
	Name      string      `json:"name"`
	Enabled   bool        `json:"enabled"`
	Property  string      `json:"property"`
	Expected  any         `json:"expected"`
	Remediate bool        `json:"remediate"`
	Ignore    []string    `json:"ignore"`
	Scope     *scopeInput `json:"scope"`
}

type branchProtInput struct {
	Name                 string      `json:"name"`
	Enabled              bool        `json:"enabled"`
	Branch               string      `json:"branch"`
	RequirePR            bool        `json:"require_pr"`
	RequiredApprovals    int         `json:"required_approvals"`
	DismissStaleReviews  bool        `json:"dismiss_stale_reviews"`
	RequireStatusChecks  []string    `json:"require_status_checks"`
	EnforceAdmins        bool        `json:"enforce_admins"`
	RequireLinearHistory bool        `json:"require_linear_history"`
	Remediate            bool        `json:"remediate"`
	Ignore               []string    `json:"ignore"`
	Scope                *scopeInput `json:"scope"`
}

func newVersionInput(cfg *PolicyConfig, templates map[string]string) versionInput {
	in := versionInput{
		Guardian: guardianInput{
			DryRun:        cfg.Guardian.DryRun,
			SkipForks:     cfg.Guardian.SkipForks,
			SkipArchived:  cfg.Guardian.SkipArchived,
			AutoClosePR:   cfg.Guardian.AutoClosePREnabled(),
			OrphanCleanup: cfg.Guardian.OrphanCleanupEnabled(),
		},
		Ignore:           cfg.IgnoreList.Repos,
		Scope:            scopeOf(cfg.Scope),
		FileRules:        make([]fileRuleInput, 0, len(cfg.FileRules)),
		SettingRules:     make([]settingRuleInput, 0, len(cfg.SettingRules)),
		BranchProtection: make([]branchProtInput, 0, len(cfg.BranchProtectionRules)),
		Templates:        templates,
	}

	if cfg.Defaults != nil {
		in.DefaultsPR = prOf(cfg.Defaults.PR)
	}

	for i := range cfg.FileRules {
		in.FileRules = append(in.FileRules, fileRuleOf(&cfg.FileRules[i]))
	}

	for i := range cfg.SettingRules {
		r := &cfg.SettingRules[i]
		in.SettingRules = append(in.SettingRules, settingRuleInput{
			Name: r.Name, Enabled: r.IsEnabled(), Property: r.Property, Expected: r.Expected,
			Remediate: r.Remediate, Ignore: ignoreOf(r.Ignore), Scope: scopeOf(r.Scope),
		})
	}

	for i := range cfg.BranchProtectionRules {
		r := &cfg.BranchProtectionRules[i]
		in.BranchProtection = append(in.BranchProtection, branchProtInput{
			Name: r.Name, Enabled: r.IsEnabled(), Branch: r.Branch, RequirePR: r.RequirePR,
			RequiredApprovals: r.RequiredApprovals, DismissStaleReviews: r.DismissStaleReviews,
			RequireStatusChecks: r.RequireStatusChecks, EnforceAdmins: r.EnforceAdmins,
			RequireLinearHistory: r.RequireLinearHistory, Remediate: r.Remediate,
			Ignore: ignoreOf(r.Ignore), Scope: scopeOf(r.Scope),
		})
	}

	return in
}

func fileRuleOf(r *FileRuleConfig) fileRuleInput {
	out := fileRuleInput{
		Type: r.Type, Name: r.Name, Enabled: r.IsEnabled(), Check: r.Check, Paths: r.Paths,
		Target: r.Target, Template: r.Template, PR: prOf(r.PR),
		Ignore: ignoreOf(r.Ignore), Scope: scopeOf(r.Scope),
	}

	if r.When != nil {
		out.RuleSatisfied = r.When.RuleSatisfied
	}

	for i := range r.Assertions {
		a := &r.Assertions[i]
		out.Assertions = append(out.Assertions, assertionInput{
			Pattern: a.Pattern, NotPattern: a.NotPattern, YAMLPath: a.YAMLPath, Contains: a.Contains,
			Equals: a.Equals, NonEmpty: a.NonEmpty, Message: a.Message,
		})
	}

	for i := range r.Reconcilers {
		rc := &r.Reconcilers[i]
		out.Reconcilers = append(out.Reconcilers, reconcilerInput{
			Type: rc.Type, Watch: rc.Watch, Mode: rc.Mode, DeleteExtra: rc.DeleteExtra,
			PR: prOf(rc.PR), AnnotationProperties: rc.AnnotationProperties,
		})
	}

	return out
}

func prOf(p *PRConfig) *prInput {
	if p == nil {
		return nil
	}

	return &prInput{
		SearchTerms: p.SearchTerms, Title: p.Title, Body: p.Body,
		Labels: p.Labels, LabelsSet: p.LabelsSet, Inherits: p.Inherits,
	}
}

func scopeOf(s *ScopeConfig) *scopeInput {
	if s == nil {
		return nil
	}

	return &scopeInput{Orgs: s.Orgs}
}

func ignoreOf(i *IgnoreConfig) []string {
	if i == nil {
		return nil
	}

	return i.Repos
}
