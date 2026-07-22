package policy

import (
	"strings"
	"testing"
)

func TestValidate_ValidDefaults(t *testing.T) {
	cfg := BuiltinDefaults()

	if err := Validate(cfg); err != nil {
		t.Errorf("Validate(BuiltinDefaults()) = %v, want nil", err)
	}
}

func TestValidate_GuardianWorkerCount(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.Guardian.WorkerCount = 0

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for WorkerCount = 0")
	}

	if !strings.Contains(err.Error(), "worker_count") {
		t.Errorf("error %q should mention worker_count", err)
	}
}

func TestValidate_GuardianQueueSize(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.Guardian.QueueSize = -1

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for QueueSize = -1")
	}

	if !strings.Contains(err.Error(), "queue_size") {
		t.Errorf("error %q should mention queue_size", err)
	}
}

func TestValidate_GuardianRateLimitThreshold(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"valid zero", 0.0, false},
		{"valid mid", 0.5, false},
		{"valid one", 1.0, false},
		{"negative", -0.1, true},
		{"above one", 1.1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := BuiltinDefaults()
			cfg.Guardian.RateLimitThreshold = tt.value

			err := Validate(cfg)

			if tt.wantErr && err == nil {
				t.Error("expected error")
			}

			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidate_GuardianLogLevel(t *testing.T) {
	tests := []struct {
		level   string
		wantErr bool
	}{
		{"debug", false},
		{"info", false},
		{"warn", false},
		{"error", false},
		{"trace", true},
		{"INFO", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			cfg := BuiltinDefaults()
			cfg.Guardian.LogLevel = tt.level

			err := Validate(cfg)

			if tt.wantErr && err == nil {
				t.Errorf("expected error for log_level %q", tt.level)
			}

			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for log_level %q: %v", tt.level, err)
			}
		})
	}
}

func TestValidate_FileRuleCheckMode(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Check:    "invalid",
		Paths:    []string{"test"},
		Target:   "test",
		Template: "test.tmpl",
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid check mode")
	}

	if !strings.Contains(err.Error(), "check must be one of") {
		t.Errorf("error %q should mention valid check values", err)
	}
}

func TestValidate_FileRuleEmptyPaths(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Paths:    []string{},
		Target:   "test",
		Template: "test.tmpl",
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty paths")
	}

	if !strings.Contains(err.Error(), "paths must be non-empty") {
		t.Errorf("error %q should mention paths", err)
	}
}

func TestValidate_FileRuleEmptyTarget(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Paths:    []string{"test"},
		Target:   "",
		Template: "test.tmpl",
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty target")
	}

	if !strings.Contains(err.Error(), "target must be non-empty") {
		t.Errorf("error %q should mention target", err)
	}
}

func TestValidate_FileRuleEmptyTemplate(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Paths:    []string{"test"},
		Target:   "test",
		Template: "",
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty template")
	}

	if !strings.Contains(err.Error(), "template must be non-empty") {
		t.Errorf("error %q should mention template", err)
	}
}

func TestValidate_AssertionsRequireContainsMode(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Check:    "exists",
		Paths:    []string{"test"},
		Target:   "test",
		Template: "test.tmpl",
		Assertions: []AssertionConfig{
			{Pattern: "foo", Message: "must have foo"},
		},
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for assertions with check=exists")
	}

	if !strings.Contains(err.Error(), "assertions require check") {
		t.Errorf("error %q should mention assertions require contains", err)
	}
}

func TestValidate_AssertionPatternAndYAMLPathMutuallyExclusive(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Check:    "contains",
		Paths:    []string{"test"},
		Target:   "test",
		Template: "test.tmpl",
		Assertions: []AssertionConfig{
			{Pattern: "foo", YAMLPath: "spec.owner", Contains: "bar", Message: "conflict"},
		},
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for pattern + yaml_path")
	}

	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q should mention mutually exclusive", err)
	}
}

func TestValidate_AssertionYAMLPathRequiresContainsOrEquals(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Check:    "contains",
		Paths:    []string{"test"},
		Target:   "test",
		Template: "test.tmpl",
		Assertions: []AssertionConfig{
			{YAMLPath: "spec.owner", Message: "missing check"},
		},
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for yaml_path without contains/equals")
	}

	if !strings.Contains(err.Error(), "requires one of contains, equals, or non_empty") {
		t.Errorf("error %q should mention requires contains, equals, or non_empty", err)
	}
}

func TestValidate_AssertionMessageRequired(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Check:    "contains",
		Paths:    []string{"test"},
		Target:   "test",
		Template: "test.tmpl",
		Assertions: []AssertionConfig{
			{Pattern: "foo", Message: ""},
		},
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty message")
	}

	if !strings.Contains(err.Error(), "message is required") {
		t.Errorf("error %q should mention message required", err)
	}
}

func TestValidate_DuplicateRuleNames(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{
		{Type: "file", Name: "test", Paths: []string{"a"}, Target: "a", Template: "a.tmpl"},
		{Type: "file", Name: "test", Paths: []string{"b"}, Target: "b", Template: "b.tmpl"},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for duplicate rule names")
	}

	if !strings.Contains(err.Error(), "duplicate rule") {
		t.Errorf("error %q should mention duplicate rule", err)
	}
}

func TestValidate_ValidContainsWithAssertions(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{{
		Type:     "file",
		Name:     "test",
		Check:    "contains",
		Paths:    []string{"test"},
		Target:   "test",
		Template: "test.tmpl",
		Assertions: []AssertionConfig{
			{Pattern: "foo", Message: "must have foo"},
			{YAMLPath: "spec.owner", Contains: "team", Message: "must have owner"},
		},
	}}

	if err := Validate(cfg); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_SettingRule_Valid(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.SettingRules = []SettingRuleConfig{
		{Name: "enable_issues", Property: "has_issues", Expected: true},
		{Name: "default_branch", Property: "default_branch", Expected: "main"},
	}

	if err := Validate(cfg); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_SettingRule_UnsupportedProperty(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.SettingRules = []SettingRuleConfig{
		{Name: "bad_prop", Property: "nonexistent_thing", Expected: true},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported property")
	}

	if !strings.Contains(err.Error(), "unsupported property") {
		t.Errorf("error %q should mention unsupported property", err)
	}
}

func TestValidate_SettingRule_WrongExpectedType_Bool(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.SettingRules = []SettingRuleConfig{
		{Name: "enable_issues", Property: "has_issues", Expected: "yes"},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for wrong expected type")
	}

	if !strings.Contains(err.Error(), "must be a bool") {
		t.Errorf("error %q should mention must be a bool", err)
	}
}

func TestValidate_SettingRule_WrongExpectedType_String(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.SettingRules = []SettingRuleConfig{
		{Name: "default_branch", Property: "default_branch", Expected: true},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for wrong expected type")
	}

	if !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("error %q should mention must be a string", err)
	}
}

func TestValidate_SettingRule_DuplicateNames(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.SettingRules = []SettingRuleConfig{
		{Name: "dup", Property: "has_issues", Expected: true},
		{Name: "dup", Property: "has_wiki", Expected: true},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for duplicate setting rule names")
	}

	if !strings.Contains(err.Error(), "duplicate setting rule") {
		t.Errorf("error %q should mention duplicate setting rule", err)
	}
}

func TestValidate_SettingRule_NilExpected(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.SettingRules = []SettingRuleConfig{
		{Name: "test", Property: "has_issues", Expected: nil},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for nil expected")
	}

	if !strings.Contains(err.Error(), "expected must be set") {
		t.Errorf("error %q should mention expected must be set", err)
	}
}

func TestValidate_BranchProtection_Valid(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.BranchProtectionRules = []BranchProtectionRuleConfig{
		{Name: "main_protection", Branch: "main", RequirePR: true, RequiredApprovals: 1},
	}

	if err := Validate(cfg); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_BranchProtection_EmptyBranch(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.BranchProtectionRules = []BranchProtectionRuleConfig{
		{Name: "bad", Branch: ""},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty branch")
	}

	if !strings.Contains(err.Error(), "branch must be non-empty") {
		t.Errorf("error %q should mention branch must be non-empty", err)
	}
}

func TestValidate_BranchProtection_NegativeApprovals(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.BranchProtectionRules = []BranchProtectionRuleConfig{
		{Name: "bad", Branch: "main", RequiredApprovals: -1},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative required_approvals")
	}

	if !strings.Contains(err.Error(), "required_approvals must be >= 0") {
		t.Errorf("error %q should mention required_approvals", err)
	}
}

func TestValidate_BranchProtection_DuplicateNames(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.BranchProtectionRules = []BranchProtectionRuleConfig{
		{Name: "dup", Branch: "main"},
		{Name: "dup", Branch: "develop"},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for duplicate names")
	}

	if !strings.Contains(err.Error(), "duplicate branch_protection rule") {
		t.Errorf("error %q should mention duplicate", err)
	}
}

func TestValidate_ErrorMessageClarity(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.Guardian.WorkerCount = 0
	cfg.Guardian.LogLevel = "invalid"

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected errors")
	}

	errStr := err.Error()

	if !strings.Contains(errStr, "guardian.worker_count") {
		t.Error("error should include full field path guardian.worker_count")
	}

	if !strings.Contains(errStr, "guardian.log_level") {
		t.Error("error should include full field path guardian.log_level")
	}
}

func fileRuleWithReconciler(annotationProperties map[string]string) FileRuleConfig {
	return FileRuleConfig{
		Type:     "file",
		Name:     "catalog_info",
		Check:    "exists",
		Paths:    []string{"catalog-info.yaml"},
		Target:   "catalog-info.yaml",
		Template: "test.tmpl",
		Reconcilers: []ReconcilerConfig{{
			Type:                 "custom_properties",
			Mode:                 "api",
			AnnotationProperties: annotationProperties,
		}},
	}
}

func TestValidate_AnnotationProperties_Valid(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{fileRuleWithReconciler(map[string]string{
		"jira/project-key": "JiraProject",
		"jira/label":       "JiraLabel",
	})}

	if err := Validate(cfg); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestValidate_AnnotationProperties_EmptyOrNilIsValid(t *testing.T) {
	for _, props := range []map[string]string{nil, {}} {
		cfg := BuiltinDefaults()
		cfg.FileRules = []FileRuleConfig{fileRuleWithReconciler(props)}

		if err := Validate(cfg); err != nil {
			t.Errorf("Validate() with AnnotationProperties=%v = %v, want nil", props, err)
		}
	}
}

func TestValidate_AnnotationProperties_ReservedName(t *testing.T) {
	for _, reserved := range []string{"Owner", "owner", "OWNER", "Component", "component"} {
		cfg := BuiltinDefaults()
		cfg.FileRules = []FileRuleConfig{fileRuleWithReconciler(map[string]string{
			"some/annotation": reserved,
		})}

		err := Validate(cfg)
		if err == nil {
			t.Fatalf("expected error for reserved property name %q", reserved)
		}

		if !strings.Contains(err.Error(), "reserved property name") {
			t.Errorf("error %q should mention reserved property name for %q", err, reserved)
		}
	}
}

func TestValidate_AnnotationProperties_EmptyAnnotationKey(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{fileRuleWithReconciler(map[string]string{
		"": "SomeProperty",
	})}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty annotation key")
	}

	if !strings.Contains(err.Error(), "empty annotation key") {
		t.Errorf("error %q should mention empty annotation key", err)
	}
}

func TestValidate_AnnotationProperties_EmptyPropertyName(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{fileRuleWithReconciler(map[string]string{
		"jira/project-key": "",
	})}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty property name")
	}

	if !strings.Contains(err.Error(), "empty property name") {
		t.Errorf("error %q should mention empty property name", err)
	}
}

func TestValidate_AnnotationProperties_DuplicateTarget(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{fileRuleWithReconciler(map[string]string{
		"jira/project-key": "JiraProject",
		"other/annotation": "jiraproject",
	})}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for duplicate property target (case-insensitive)")
	}

	if !strings.Contains(err.Error(), "duplicate property name") {
		t.Errorf("error %q should mention duplicate property name", err)
	}
}

func TestValidate_AnnotationProperties_InvalidCharset(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{fileRuleWithReconciler(map[string]string{
		"jira/project-key": "Jira Project!",
	})}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid property name charset")
	}

	if !strings.Contains(err.Error(), "must match") {
		t.Errorf("error %q should mention charset constraint", err)
	}
}

func TestValidate_AnnotationProperties_TooLong(t *testing.T) {
	cfg := BuiltinDefaults()
	cfg.FileRules = []FileRuleConfig{fileRuleWithReconciler(map[string]string{
		"jira/project-key": strings.Repeat("a", 76),
	})}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for property name > 75 characters")
	}

	if !strings.Contains(err.Error(), "must match") {
		t.Errorf("error %q should mention charset constraint", err)
	}
}
