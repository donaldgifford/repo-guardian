package policy

import (
	"errors"
	"fmt"
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

// hclConfig is the raw HCL-decoded structure before merging with defaults.
type hclConfig struct {
	Guardian   *GuardianConfig  `hcl:"guardian,block"`
	IgnoreList *IgnoreConfig    `hcl:"ignore,block"`
	FileRules  []FileRuleConfig `hcl:"rule,block"`
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

	return cfg, nil
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

	return hclConfigToPolicy(&raw), nil
}

func decodeBody(body hcl.Body, raw *hclConfig) hcl.Diagnostics {
	ctx := &hcl.EvalContext{}

	content, diags := body.Content(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "locals"},
			{Type: "guardian"},
			{Type: "ignore"},
			{Type: "rule", LabelNames: []string{"type", "name"}},
		},
	})
	if diags.HasErrors() {
		return diags
	}

	// Process locals blocks first to build eval context.
	locals := map[string]cty.Value{}

	for _, block := range content.Blocks {
		if block.Type != "locals" {
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

	for _, block := range content.Blocks {
		switch block.Type {
		case "locals":
			continue // already processed
		case "guardian":
			g, d := decodeGuardianBlock(block, ctx)
			diags = append(diags, d...)

			if g != nil {
				raw.Guardian = g
			}
		case "ignore":
			ig, d := decodeIgnoreBlock(block, ctx)
			diags = append(diags, d...)

			if ig != nil {
				raw.IgnoreList = ig
			}
		case "rule":
			r, d := decodeRuleBlock(block, ctx)
			diags = append(diags, d...)

			if r != nil {
				raw.FileRules = append(raw.FileRules, *r)
			}
		}
	}

	return diags
}

func decodeGuardianBlock(block *hcl.Block, ctx *hcl.EvalContext) (*GuardianConfig, hcl.Diagnostics) {
	g := &GuardianConfig{}

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
		}
	}

	return g, diags
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

var ruleBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "enabled"},
		{Name: "check"},
		{Name: "paths", Required: true},
		{Name: "target", Required: true},
		{Name: "template", Required: true},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "pr"},
		{Type: "assertion"},
		{Type: "ignore"},
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
		case "enabled":
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
		case "ignore":
			ig, d := decodeIgnoreBlock(sub, ctx)
			diags = append(diags, d...)
			r.Ignore = ig
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

func decodeReconcileBlock(block *hcl.Block, ctx *hcl.EvalContext) (*ReconcilerConfig, hcl.Diagnostics) {
	rec := &ReconcilerConfig{Type: block.Labels[0]}

	attrs, diags := block.Body.JustAttributes()
	if diags.HasErrors() {
		return nil, diags
	}

	for name, attr := range attrs {
		val, d := attr.Expr.Value(ctx)
		diags = append(diags, d...)

		switch name {
		case "watch":
			rec.Watch = val.True()
		case "mode":
			rec.Mode = val.AsString()
		}
	}

	return rec, diags
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

	return pr, diags
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
		case "contains":
			a.Contains = val.AsString()
		case "equals":
			a.Equals = val.AsString()
		case "message":
			a.Message = val.AsString()
		}
	}

	return a, diags
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

	// If HCL defines file rules, use those instead of defaults.
	if len(raw.FileRules) > 0 {
		cfg.FileRules = raw.FileRules
	} else {
		cfg.FileRules = defaults.FileRules
	}

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
}

func applyEnvBool(key string, dst *bool) {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
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
