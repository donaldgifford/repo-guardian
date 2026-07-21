package policy

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

const (
	blockTypeIgnore = "ignore"
	blockTypeScope  = "scope"
	blockTypeLocals = "locals"
	attrEnabled     = "enabled"
	attrRemediate   = "remediate"
)

// hclConfig is the raw HCL-decoded structure before merging with defaults.
type hclConfig struct {
	Guardian              *GuardianConfig              `hcl:"guardian,block"`
	IgnoreList            *IgnoreConfig                `hcl:"ignore,block"`
	Scope                 *ScopeConfig                 `hcl:"scope,block"`
	Defaults              *DefaultsConfig              `hcl:"defaults,block"`
	FileRules             []FileRuleConfig             `hcl:"rule,block"`
	SettingRules          []SettingRuleConfig          `hcl:"-"`
	BranchProtectionRules []BranchProtectionRuleConfig `hcl:"-"`

	// ScopeBlocks captures every top-level scope block encountered. Strict-mode
	// validation rejects configs with more than one. The singleton Scope above
	// is set to the first block decoded.
	ScopeBlocks []*ScopeConfig `hcl:"-"`
}

// Load reads policy configuration from the given path (file or directory),
// merges with built-in defaults, and applies env var overrides.
// If path is empty or does not exist, built-in defaults are returned.
func Load(path string) (*PolicyConfig, error) {
	defaults := BuiltinDefaults()

	if path == "" {
		applyEnvOverrides(&defaults.Guardian)

		return defaults, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyEnvOverrides(&defaults.Guardian)

			return defaults, nil
		}

		return nil, fmt.Errorf("stat config path %s: %w", path, err)
	}

	var cfg *PolicyConfig

	if info.IsDir() {
		cfg, err = loadDirectory(path)
	} else {
		cfg, err = loadFile(path)
	}

	if err != nil {
		return nil, err
	}

	applyEnvOverrides(&cfg.Guardian)

	if err := parseScheduleInterval(&cfg.Guardian); err != nil {
		return nil, err
	}

	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	if err := validateStrictScope(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	if err := compilePolicyTemplates(cfg); err != nil {
		return nil, err
	}

	warnLegacyPerRuleScope(cfg)

	return cfg, nil
}

// warnLegacyPerRuleScope emits a single warning when running in legacy
// mode (no top-level scope) but at least one rule defines its own scope
// block. The per-rule scope is silently ignored at runtime; the warning
// surfaces the misconfiguration without breaking the load.
func warnLegacyPerRuleScope(cfg *PolicyConfig) {
	if cfg.Scope != nil {
		return
	}

	for i := range cfg.FileRules {
		if cfg.FileRules[i].Scope != nil {
			emitLegacyScopeWarning()

			return
		}
	}

	for i := range cfg.SettingRules {
		if cfg.SettingRules[i].Scope != nil {
			emitLegacyScopeWarning()

			return
		}
	}

	for i := range cfg.BranchProtectionRules {
		if cfg.BranchProtectionRules[i].Scope != nil {
			emitLegacyScopeWarning()

			return
		}
	}
}

func emitLegacyScopeWarning() {
	slog.Warn("per-rule scope ignored: no top-level scope { } block declared. " +
		"Add a top-level scope block to enable strict mode, " +
		"or remove per-rule scope blocks.")
}

func loadFile(path string) (*PolicyConfig, error) {
	parser := hclparse.NewParser()

	f, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() {
		return nil, formatHCLError(path, diags)
	}

	var raw hclConfig

	decodeDiags := decodeBody(f.Body, &raw)
	if decodeDiags.HasErrors() {
		return nil, formatHCLError(path, decodeDiags)
	}

	if err := checkSingleTopLevelScope(&raw); err != nil {
		return nil, err
	}

	return hclConfigToPolicy(&raw), nil
}

func loadDirectory(dir string) (*PolicyConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading config directory %s: %w", dir, err)
	}

	hclFiles := make([]string, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".hcl") {
			continue
		}

		hclFiles = append(hclFiles, filepath.Join(dir, entry.Name()))
	}

	if len(hclFiles) == 0 {
		defaults := BuiltinDefaults()

		return defaults, nil
	}

	sort.Strings(hclFiles)

	parser := hclparse.NewParser()

	files := make([]*hcl.File, 0, len(hclFiles))

	for _, path := range hclFiles {
		f, diags := parser.ParseHCLFile(path)
		if diags.HasErrors() {
			return nil, formatHCLError(path, diags)
		}

		files = append(files, f)
	}

	body := hcl.MergeFiles(files)

	var raw hclConfig

	decodeDiags := decodeBody(body, &raw)
	if decodeDiags.HasErrors() {
		return nil, fmt.Errorf("decoding merged HCL: %s", decodeDiags.Error())
	}

	if err := checkSingleTopLevelScope(&raw); err != nil {
		return nil, err
	}

	return hclConfigToPolicy(&raw), nil
}

// checkSingleTopLevelScope rejects configs with more than one top-level
// scope { } block. Multiple blocks may appear when a directory load merges
// files that each declare their own top-level scope; the strict-mode model
// requires a single canonical universe declaration.
func checkSingleTopLevelScope(raw *hclConfig) error {
	if len(raw.ScopeBlocks) > 1 {
		return fmt.Errorf("only one top-level scope block allowed, found %d", len(raw.ScopeBlocks))
	}

	return nil
}

func decodeBody(body hcl.Body, raw *hclConfig) hcl.Diagnostics {
	ctx := &hcl.EvalContext{}

	content, diags := body.Content(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: blockTypeLocals},
			{Type: "guardian"},
			{Type: blockTypeIgnore},
			{Type: blockTypeScope},
			{Type: "defaults"},
			{Type: "rule", LabelNames: []string{"type", "name"}},
		},
	})
	if diags.HasErrors() {
		return diags
	}

	diags = append(diags, decodeLocals(content.Blocks, ctx)...)

	for _, block := range content.Blocks {
		diags = append(diags, decodeBlock(block, ctx, raw)...)
	}

	return diags
}

func decodeLocals(blocks hcl.Blocks, ctx *hcl.EvalContext) hcl.Diagnostics {
	var diags hcl.Diagnostics

	locals := map[string]cty.Value{}

	for _, block := range blocks {
		if block.Type != blockTypeLocals {
			continue
		}

		attrs, d := block.Body.JustAttributes()
		diags = append(diags, d...)

		for name, attr := range attrs {
			val, d2 := attr.Expr.Value(ctx)
			diags = append(diags, d2...)

			if !d2.HasErrors() {
				locals[name] = val
			}
		}
	}

	if len(locals) > 0 {
		ctx.Variables = map[string]cty.Value{
			"local": cty.ObjectVal(locals),
		}
	}

	return diags
}

func decodeBlock(block *hcl.Block, ctx *hcl.EvalContext, raw *hclConfig) hcl.Diagnostics {
	var diags hcl.Diagnostics

	switch block.Type {
	case blockTypeLocals:
		// already processed
	case "guardian":
		g, d := decodeGuardianBlock(block, ctx)
		diags = append(diags, d...)

		if g != nil {
			raw.Guardian = g
		}
	case blockTypeIgnore:
		ig, d := decodeIgnoreBlock(block, ctx)
		diags = append(diags, d...)

		if ig != nil {
			raw.IgnoreList = ig
		}
	case blockTypeScope:
		sc, d := decodeScopeBlock(block, ctx)
		diags = append(diags, d...)

		if sc != nil {
			raw.ScopeBlocks = append(raw.ScopeBlocks, sc)

			if raw.Scope == nil {
				raw.Scope = sc
			}
		}
	case "defaults":
		dc, d := decodeDefaultsBlock(block, ctx)
		diags = append(diags, d...)

		if dc != nil {
			raw.Defaults = dc
		}
	case "rule":
		diags = append(diags, decodeRuleOrSettingBlock(block, ctx, raw)...)
	}

	return diags
}

func decodeRuleOrSettingBlock(block *hcl.Block, ctx *hcl.EvalContext, raw *hclConfig) hcl.Diagnostics {
	var diags hcl.Diagnostics

	switch block.Labels[0] {
	case "setting":
		sr, d := decodeSettingRuleBlock(block, ctx)
		diags = append(diags, d...)

		if sr != nil {
			raw.SettingRules = append(raw.SettingRules, *sr)
		}
	case "branch_protection":
		bp, d := decodeBranchProtectionBlock(block, ctx)
		diags = append(diags, d...)

		if bp != nil {
			raw.BranchProtectionRules = append(raw.BranchProtectionRules, *bp)
		}
	default:
		r, d := decodeRuleBlock(block, ctx)
		diags = append(diags, d...)

		if r != nil {
			raw.FileRules = append(raw.FileRules, *r)
		}
	}

	return diags
}

// guardianBodySchema is the strict attribute schema for the `guardian {}`
// block. Unlike the previous JustAttributes() decode, unknown attributes
// (typos, stale config like the historical `org`) fail load with an
// "Unsupported argument" diagnostic instead of being silently ignored
// (INV-0010).
var guardianBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "dry_run"},
		{Name: "schedule_interval"},
		{Name: "worker_count"},
		{Name: "queue_size"},
		{Name: "log_level"},
		{Name: "skip_forks"},
		{Name: "skip_archived"},
		{Name: "rate_limit_threshold"},
		{Name: "webhook_ip_allowlist"},
		{Name: "webhook_ip_allowlist_fail_open"},
		{Name: "trust_proxy_headers"},
		{Name: "auto_close_pr"},
	},
}

func decodeGuardianBlock(block *hcl.Block, ctx *hcl.EvalContext) (*GuardianConfig, hcl.Diagnostics) {
	g := &GuardianConfig{}

	content, diags := block.Body.Content(guardianBodySchema)
	if diags.HasErrors() {
		return nil, diags
	}

	for name, attr := range content.Attributes {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if d.HasErrors() {
			continue
		}

		setGuardianAttr(g, name, val)
	}

	return g, diags
}

func setGuardianAttr(g *GuardianConfig, name string, val cty.Value) {
	switch name {
	case "dry_run":
		g.DryRun = val.True()
	case "schedule_interval":
		g.ScheduleInterval = val.AsString()
	case "worker_count":
		n, _ := val.AsBigFloat().Int64()
		g.WorkerCount = int(n)
	case "queue_size":
		n, _ := val.AsBigFloat().Int64()
		g.QueueSize = int(n)
	case "log_level":
		g.LogLevel = val.AsString()
	case "skip_forks":
		g.SkipForks = val.True()
	case "skip_archived":
		g.SkipArchived = val.True()
	case "rate_limit_threshold":
		f, _ := val.AsBigFloat().Float64()
		g.RateLimitThreshold = f
	case "webhook_ip_allowlist":
		g.WebhookIPAllowlist = val.True()
	case "webhook_ip_allowlist_fail_open":
		g.WebhookIPAllowlistFailOpen = val.True()
	case "trust_proxy_headers":
		g.TrustProxyHeaders = val.True()
	case "auto_close_pr":
		b := val.True()
		g.AutoClosePR = &b
	}
}

func decodeIgnoreBlock(block *hcl.Block, ctx *hcl.EvalContext) (*IgnoreConfig, hcl.Diagnostics) {
	ig := &IgnoreConfig{}

	attrs, diags := block.Body.JustAttributes()
	if diags.HasErrors() {
		return nil, diags
	}

	if attr, ok := attrs["repos"]; ok {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if !d.HasErrors() {
			for it := val.ElementIterator(); it.Next(); {
				_, v := it.Element()
				ig.Repos = append(ig.Repos, v.AsString())
			}
		}
	}

	return ig, diags
}

func decodeScopeBlock(block *hcl.Block, ctx *hcl.EvalContext) (*ScopeConfig, hcl.Diagnostics) {
	sc := &ScopeConfig{}

	attrs, diags := block.Body.JustAttributes()
	if diags.HasErrors() {
		return nil, diags
	}

	if attr, ok := attrs["orgs"]; ok {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if !d.HasErrors() {
			for it := val.ElementIterator(); it.Next(); {
				_, v := it.Element()
				sc.Orgs = append(sc.Orgs, v.AsString())
			}
		}
	}

	return sc, diags
}

var ruleBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: attrEnabled},
		{Name: "check"},
		{Name: "paths", Required: true},
		{Name: "target", Required: true},
		{Name: "template", Required: true},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "pr"},
		{Type: "assertion"},
		{Type: blockTypeIgnore},
		{Type: blockTypeScope},
		{Type: "reconcile", LabelNames: []string{"type"}},
	},
}

func decodeRuleBlock(block *hcl.Block, ctx *hcl.EvalContext) (*FileRuleConfig, hcl.Diagnostics) {
	r := &FileRuleConfig{
		Type: block.Labels[0],
		Name: block.Labels[1],
	}

	content, diags := block.Body.Content(ruleBodySchema)
	if diags.HasErrors() {
		return nil, diags
	}

	d := decodeRuleAttributes(r, content.Attributes, ctx)
	diags = append(diags, d...)

	d = decodeRuleSubBlocks(r, content.Blocks, ctx)
	diags = append(diags, d...)

	return r, diags
}

func decodeRuleAttributes(
	r *FileRuleConfig,
	attrs hcl.Attributes,
	ctx *hcl.EvalContext,
) hcl.Diagnostics {
	var diags hcl.Diagnostics

	for name, attr := range attrs {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if d.HasErrors() {
			continue
		}

		switch name {
		case attrEnabled:
			b := val.True()
			r.Enabled = &b
		case "check":
			r.Check = val.AsString()
		case "paths":
			for it := val.ElementIterator(); it.Next(); {
				_, v := it.Element()
				r.Paths = append(r.Paths, v.AsString())
			}
		case "target":
			r.Target = val.AsString()
		case "template":
			r.Template = val.AsString()
		}
	}

	return diags
}

func decodeRuleSubBlocks(
	r *FileRuleConfig,
	blocks hcl.Blocks,
	ctx *hcl.EvalContext,
) hcl.Diagnostics {
	var diags hcl.Diagnostics

	for _, sub := range blocks {
		switch sub.Type {
		case "pr":
			pr, d := decodePRBlock(sub, ctx)
			diags = append(diags, d...)
			r.PR = pr
		case "assertion":
			a, d := decodeAssertionBlock(sub, ctx)
			diags = append(diags, d...)

			if a != nil {
				r.Assertions = append(r.Assertions, *a)
			}
		case blockTypeIgnore:
			ig, d := decodeIgnoreBlock(sub, ctx)
			diags = append(diags, d...)
			r.Ignore = ig
		case blockTypeScope:
			sc, d := decodeScopeBlock(sub, ctx)
			diags = append(diags, d...)
			r.Scope = sc
		case "reconcile":
			rec, d := decodeReconcileBlock(sub, ctx)
			diags = append(diags, d...)

			if rec != nil {
				r.Reconcilers = append(r.Reconcilers, *rec)
			}
		}
	}

	return diags
}

var reconcileBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "watch"},
		{Name: "mode"},
		{Name: "delete_extra"},
		{Name: "annotation_properties"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "pr"},
	},
}

func decodeReconcileBlock(block *hcl.Block, ctx *hcl.EvalContext) (*ReconcilerConfig, hcl.Diagnostics) {
	rec := &ReconcilerConfig{Type: block.Labels[0]}

	content, diags := block.Body.Content(reconcileBodySchema)
	if diags.HasErrors() {
		return nil, diags
	}

	for name, attr := range content.Attributes {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if d.HasErrors() {
			continue
		}

		switch name {
		case "watch":
			rec.Watch = val.True()
		case "mode":
			rec.Mode = val.AsString()
		case "delete_extra":
			rec.DeleteExtra = val.True()
		case "annotation_properties":
			props, d := decodeAnnotationProperties(val, attr.Range)
			diags = append(diags, d...)
			rec.AnnotationProperties = props
		}
	}

	for _, sub := range content.Blocks {
		if sub.Type != "pr" {
			continue
		}

		pr, d := decodePRBlock(sub, ctx)
		diags = append(diags, d...)
		rec.PR = pr
	}

	return rec, diags
}

// decodeAnnotationProperties decodes the `annotation_properties` map
// attribute on a `reconcile {}` block. rng anchors diagnostics to the
// attribute's source location. Non-map values and non-string map values
// each produce a diagnostic rather than panicking and are skipped; the
// remaining entries still decode. The type guard checks IsObjectType/
// IsMapType specifically rather than CanIterateElements: the latter is
// also true for lists/sets/tuples, whose AsValueMap panics internally
// (it calls AsString on a numeric index key). Reserved-name,
// duplicate-target, and charset validation happen later at
// policy.Validate time (validateAnnotationProperties), where file/rule
// context is available for a clearer error prefix.
func decodeAnnotationProperties(val cty.Value, rng hcl.Range) (map[string]string, hcl.Diagnostics) {
	if !val.Type().IsObjectType() && !val.Type().IsMapType() {
		return nil, hcl.Diagnostics{{
			Severity: hcl.DiagError,
			Summary:  "Invalid annotation_properties",
			Detail:   "annotation_properties must be a map of string to string.",
			Subject:  rng.Ptr(),
		}}
	}

	raw := val.AsValueMap()

	var diags hcl.Diagnostics

	props := make(map[string]string, len(raw))

	for k, v := range raw {
		if v.Type() != cty.String {
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid annotation_properties value",
				Detail: fmt.Sprintf(
					"annotation_properties[%q] must be a string, got %s.",
					k, v.Type().FriendlyName(),
				),
				Subject: rng.Ptr(),
			})

			continue
		}

		props[k] = v.AsString()
	}

	return props, diags
}

func decodePRBlock(block *hcl.Block, ctx *hcl.EvalContext) (*PRConfig, hcl.Diagnostics) {
	pr := &PRConfig{}

	attrs, diags := block.Body.JustAttributes()
	if diags.HasErrors() {
		return nil, diags
	}

	if attr, ok := attrs["search_terms"]; ok {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if !d.HasErrors() {
			for it := val.ElementIterator(); it.Next(); {
				_, v := it.Element()
				pr.SearchTerms = append(pr.SearchTerms, v.AsString())
			}
		}
	}

	if attr, ok := attrs["title"]; ok {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if !d.HasErrors() {
			s := val.AsString()
			pr.Title = &s
		}
	}

	if attr, ok := attrs["body"]; ok {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if !d.HasErrors() {
			s := val.AsString()
			pr.Body = &s
		}
	}

	if attr, ok := attrs["labels"]; ok {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if !d.HasErrors() {
			pr.LabelsSet = true
			pr.Labels = make([]string, 0)

			for it := val.ElementIterator(); it.Next(); {
				_, v := it.Element()
				pr.Labels = append(pr.Labels, v.AsString())
			}
		}
	}

	if attr, ok := attrs["inherits"]; ok {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if !d.HasErrors() {
			b := val.True()
			pr.Inherits = &b
		}
	}

	return pr, diags
}

func decodeDefaultsBlock(block *hcl.Block, ctx *hcl.EvalContext) (*DefaultsConfig, hcl.Diagnostics) {
	dc := &DefaultsConfig{}

	content, diags := block.Body.Content(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "pr"},
		},
	})
	if diags.HasErrors() {
		return nil, diags
	}

	for _, sub := range content.Blocks {
		if sub.Type != "pr" {
			continue
		}

		pr, d := decodePRBlock(sub, ctx)
		diags = append(diags, d...)
		dc.PR = pr
	}

	return dc, diags
}

func decodeAssertionBlock(block *hcl.Block, ctx *hcl.EvalContext) (*AssertionConfig, hcl.Diagnostics) {
	a := &AssertionConfig{}

	attrs, diags := block.Body.JustAttributes()
	if diags.HasErrors() {
		return nil, diags
	}

	for name, attr := range attrs {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if d.HasErrors() {
			continue
		}

		switch name {
		case "pattern":
			a.Pattern = val.AsString()
		case "not_pattern":
			a.NotPattern = val.AsString()
		case "yaml_path":
			a.YAMLPath = val.AsString()
		case string(CheckContains):
			a.Contains = val.AsString()
		case "equals":
			a.Equals = val.AsString()
		case "non_empty":
			a.NonEmpty = val.True()
		case "message":
			a.Message = val.AsString()
		}
	}

	return a, diags
}

var settingRuleBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: attrEnabled},
		{Name: "property", Required: true},
		{Name: "expected", Required: true},
		{Name: attrRemediate},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: blockTypeIgnore},
		{Type: blockTypeScope},
	},
}

func decodeSettingRuleBlock(block *hcl.Block, ctx *hcl.EvalContext) (*SettingRuleConfig, hcl.Diagnostics) {
	sr := &SettingRuleConfig{
		Name: block.Labels[1],
	}

	content, diags := block.Body.Content(settingRuleBodySchema)
	if diags.HasErrors() {
		return nil, diags
	}

	for name, attr := range content.Attributes {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if d.HasErrors() {
			continue
		}

		switch name {
		case attrEnabled:
			b := val.True()
			sr.Enabled = &b
		case "property":
			sr.Property = val.AsString()
		case "expected":
			switch val.Type() {
			case cty.Bool:
				sr.Expected = val.True()
			case cty.String:
				sr.Expected = val.AsString()
			default:
				sr.Expected = val.AsString()
			}
		case attrRemediate:
			sr.Remediate = val.True()
		}
	}

	for _, sub := range content.Blocks {
		switch sub.Type {
		case blockTypeIgnore:
			ig, d := decodeIgnoreBlock(sub, ctx)
			diags = append(diags, d...)
			sr.Ignore = ig
		case blockTypeScope:
			sc, d := decodeScopeBlock(sub, ctx)
			diags = append(diags, d...)
			sr.Scope = sc
		}
	}

	return sr, diags
}

var branchProtectionBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: attrEnabled},
		{Name: "branch", Required: true},
		{Name: "require_pr"},
		{Name: "required_approvals"},
		{Name: "dismiss_stale_reviews"},
		{Name: "require_status_checks"},
		{Name: "enforce_admins"},
		{Name: "require_linear_history"},
		{Name: attrRemediate},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: blockTypeIgnore},
		{Type: blockTypeScope},
	},
}

func decodeBranchProtectionBlock(
	block *hcl.Block,
	ctx *hcl.EvalContext,
) (*BranchProtectionRuleConfig, hcl.Diagnostics) {
	bp := &BranchProtectionRuleConfig{
		Name: block.Labels[1],
	}

	content, diags := block.Body.Content(branchProtectionBodySchema)
	if diags.HasErrors() {
		return nil, diags
	}

	for name, attr := range content.Attributes {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		if d.HasErrors() {
			continue
		}

		decodeBranchProtectionAttr(bp, name, val)
	}

	for _, sub := range content.Blocks {
		switch sub.Type {
		case blockTypeIgnore:
			ig, d := decodeIgnoreBlock(sub, ctx)
			diags = append(diags, d...)
			bp.Ignore = ig
		case blockTypeScope:
			sc, d := decodeScopeBlock(sub, ctx)
			diags = append(diags, d...)
			bp.Scope = sc
		}
	}

	return bp, diags
}

func decodeBranchProtectionAttr(bp *BranchProtectionRuleConfig, name string, val cty.Value) {
	switch name {
	case attrEnabled:
		b := val.True()
		bp.Enabled = &b
	case "branch":
		bp.Branch = val.AsString()
	case "require_pr":
		bp.RequirePR = val.True()
	case "required_approvals":
		n, _ := val.AsBigFloat().Int64()
		bp.RequiredApprovals = int(n)
	case "dismiss_stale_reviews":
		bp.DismissStaleReviews = val.True()
	case "require_status_checks":
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			bp.RequireStatusChecks = append(bp.RequireStatusChecks, v.AsString())
		}
	case "enforce_admins":
		bp.EnforceAdmins = val.True()
	case "require_linear_history":
		bp.RequireLinearHistory = val.True()
	case attrRemediate:
		bp.Remediate = val.True()
	}
}

func hclConfigToPolicy(raw *hclConfig) *PolicyConfig {
	defaults := BuiltinDefaults()

	cfg := &PolicyConfig{
		Guardian: defaults.Guardian,
	}

	if raw.Guardian != nil {
		mergeGuardianConfig(&cfg.Guardian, raw.Guardian)
	}

	if raw.IgnoreList != nil {
		cfg.IgnoreList = *raw.IgnoreList
	}

	if raw.Scope != nil {
		cfg.Scope = raw.Scope
	}

	if raw.Defaults != nil {
		cfg.Defaults = raw.Defaults
	}

	// If HCL defines file rules, use those instead of defaults.
	// In strict mode (top-level scope present), never fall back to defaults:
	// the user has opted in to declaring every rule with its own scope.
	switch {
	case len(raw.FileRules) > 0:
		cfg.FileRules = raw.FileRules
	case raw.Scope != nil:
		cfg.FileRules = nil
	default:
		cfg.FileRules = defaults.FileRules
	}

	cfg.SettingRules = raw.SettingRules
	cfg.BranchProtectionRules = raw.BranchProtectionRules

	return cfg
}

func mergeGuardianConfig(dst, src *GuardianConfig) {
	if src.DryRun {
		dst.DryRun = true
	}

	if src.ScheduleInterval != "" {
		dst.ScheduleInterval = src.ScheduleInterval
	}

	if src.WorkerCount != 0 {
		dst.WorkerCount = src.WorkerCount
	}

	if src.QueueSize != 0 {
		dst.QueueSize = src.QueueSize
	}

	if src.LogLevel != "" {
		dst.LogLevel = src.LogLevel
	}

	if src.SkipForks {
		dst.SkipForks = true
	}

	if src.SkipArchived {
		dst.SkipArchived = true
	}

	if src.RateLimitThreshold != 0 {
		dst.RateLimitThreshold = src.RateLimitThreshold
	}

	if src.WebhookIPAllowlist {
		dst.WebhookIPAllowlist = true
	}

	if src.WebhookIPAllowlistFailOpen {
		dst.WebhookIPAllowlistFailOpen = true
	}

	if src.TrustProxyHeaders {
		dst.TrustProxyHeaders = true
	}

	if src.AutoClosePR != nil {
		dst.AutoClosePR = src.AutoClosePR
	}
}

func applyEnvOverrides(g *GuardianConfig) {
	applyEnvBool("DRY_RUN", &g.DryRun)
	applyEnvString("SCHEDULE_INTERVAL", &g.ScheduleInterval)
	applyEnvInt("WORKER_COUNT", &g.WorkerCount)
	applyEnvInt("QUEUE_SIZE", &g.QueueSize)
	applyEnvString("LOG_LEVEL", &g.LogLevel)
	applyEnvBool("SKIP_FORKS", &g.SkipForks)
	applyEnvBool("SKIP_ARCHIVED", &g.SkipArchived)
	applyEnvFloat("RATE_LIMIT_THRESHOLD", &g.RateLimitThreshold)
	applyEnvBool("WEBHOOK_IP_ALLOWLIST", &g.WebhookIPAllowlist)
	applyEnvBool("WEBHOOK_IP_ALLOWLIST_FAIL_OPEN", &g.WebhookIPAllowlistFailOpen)
	applyEnvBool("TRUST_PROXY_HEADERS", &g.TrustProxyHeaders)
	applyEnvBoolPtr("AUTO_CLOSE_PR", &g.AutoClosePR)
}

func applyEnvBool(key string, dst *bool) {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

// applyEnvBoolPtr is the *bool variant for fields whose HCL block
// uses a pointer to distinguish "unset" from "explicitly false".
// When the env var is set, dst is reassigned to a new *bool so the
// caller observes the value via the same Set/Get path as an HCL
// assignment.
func applyEnvBoolPtr(key string, dst **bool) {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = &b
		}
	}
}

func applyEnvString(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func applyEnvInt(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func applyEnvFloat(key string, dst *float64) {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			*dst = f
		}
	}
}

func parseScheduleInterval(g *GuardianConfig) error {
	if g.ScheduleInterval == "" {
		g.ParsedScheduleInterval = defaultScheduleInterval

		return nil
	}

	d, err := time.ParseDuration(g.ScheduleInterval)
	if err != nil {
		return fmt.Errorf("parsing schedule_interval %q: %w", g.ScheduleInterval, err)
	}

	g.ParsedScheduleInterval = d

	return nil
}

func formatHCLError(path string, err error) error {
	var diags hcl.Diagnostics
	if errors.As(err, &diags) {
		var msgs []string

		for _, d := range diags {
			if d.Subject != nil {
				msgs = append(msgs, fmt.Sprintf("%s:%d:%d: %s",
					d.Subject.Filename, d.Subject.Start.Line, d.Subject.Start.Column, d.Detail))
			} else {
				msgs = append(msgs, d.Detail)
			}
		}

		return fmt.Errorf("parsing %s: %s", path, strings.Join(msgs, "; "))
	}

	return fmt.Errorf("parsing %s: %w", path, err)
}
