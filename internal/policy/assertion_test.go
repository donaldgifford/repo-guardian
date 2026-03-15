package policy

import (
	"strings"
	"testing"
)

func TestCompileAssertions_ValidPatterns(t *testing.T) {
	assertions := []AssertionConfig{
		{Pattern: `^version:\s+\d+`, Message: "must have version"},
		{NotPattern: `TODO`, Message: "must not have TODOs"},
	}

	compiled, err := CompileAssertions(assertions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(compiled) != 2 {
		t.Fatalf("got %d compiled assertions, want 2", len(compiled))
	}
}

func TestCompileAssertions_InvalidPattern(t *testing.T) {
	assertions := []AssertionConfig{
		{Pattern: `[invalid`, Message: "bad regex"},
	}

	_, err := CompileAssertions(assertions)
	if err == nil {
		t.Fatal("expected error for invalid pattern")
	}

	if !strings.Contains(err.Error(), "invalid pattern") {
		t.Errorf("error %q should mention invalid pattern", err)
	}
}

func TestCompileAssertions_InvalidNotPattern(t *testing.T) {
	assertions := []AssertionConfig{
		{NotPattern: `[invalid`, Message: "bad regex"},
	}

	_, err := CompileAssertions(assertions)
	if err == nil {
		t.Fatal("expected error for invalid not_pattern")
	}

	if !strings.Contains(err.Error(), "invalid not_pattern") {
		t.Errorf("error %q should mention invalid not_pattern", err)
	}
}

func TestEvaluate_PatternMatch(t *testing.T) {
	compiled, err := CompileAssertions([]AssertionConfig{
		{Pattern: `CODEOWNERS`, Message: "must contain CODEOWNERS"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := compiled[0].Evaluate("# CODEOWNERS file\n* @team"); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_PatternNoMatch(t *testing.T) {
	compiled, err := CompileAssertions([]AssertionConfig{
		{Pattern: `CODEOWNERS`, Message: "must contain CODEOWNERS"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = compiled[0].Evaluate("some other content")
	if err == nil {
		t.Fatal("expected failure for non-matching pattern")
	}

	if err.Error() != "must contain CODEOWNERS" {
		t.Errorf("got %q, want %q", err, "must contain CODEOWNERS")
	}
}

func TestEvaluate_NotPatternPass(t *testing.T) {
	compiled, err := CompileAssertions([]AssertionConfig{
		{NotPattern: `TODO`, Message: "must not have TODOs"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := compiled[0].Evaluate("clean content"); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_NotPatternFail(t *testing.T) {
	compiled, err := CompileAssertions([]AssertionConfig{
		{NotPattern: `TODO`, Message: "must not have TODOs"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = compiled[0].Evaluate("some TODO item")
	if err == nil {
		t.Fatal("expected failure for matching not_pattern")
	}

	if err.Error() != "must not have TODOs" {
		t.Errorf("got %q, want %q", err, "must not have TODOs")
	}
}

func TestEvaluate_YAMLPathContainsPass(t *testing.T) {
	content := `
spec:
  owner: team-platform
`
	compiled, err := CompileAssertions([]AssertionConfig{
		{YAMLPath: "spec.owner", Contains: "team", Message: "owner must contain team"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := compiled[0].Evaluate(content); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_YAMLPathContainsFail(t *testing.T) {
	content := `
spec:
  owner: individual-dev
`
	compiled, err := CompileAssertions([]AssertionConfig{
		{YAMLPath: "spec.owner", Contains: "team", Message: "owner must contain team"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = compiled[0].Evaluate(content)
	if err == nil {
		t.Fatal("expected failure")
	}

	if err.Error() != "owner must contain team" {
		t.Errorf("got %q, want %q", err, "owner must contain team")
	}
}

func TestEvaluate_YAMLPathEqualsPass(t *testing.T) {
	content := `
spec:
  lifecycle: production
`
	compiled, err := CompileAssertions([]AssertionConfig{
		{YAMLPath: "spec.lifecycle", Equals: "production", Message: "must be production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := compiled[0].Evaluate(content); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_YAMLPathEqualsFail(t *testing.T) {
	content := `
spec:
  lifecycle: experimental
`
	compiled, err := CompileAssertions([]AssertionConfig{
		{YAMLPath: "spec.lifecycle", Equals: "production", Message: "must be production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = compiled[0].Evaluate(content)
	if err == nil {
		t.Fatal("expected failure")
	}

	if err.Error() != "must be production" {
		t.Errorf("got %q, want %q", err, "must be production")
	}
}

func TestEvaluateAssertions_AllPass(t *testing.T) {
	content := `
spec:
  owner: team-platform
  lifecycle: production
`
	compiled, err := CompileAssertions([]AssertionConfig{
		{Pattern: `owner:`, Message: "must have owner field"},
		{YAMLPath: "spec.owner", Contains: "team", Message: "owner must be a team"},
		{YAMLPath: "spec.lifecycle", Equals: "production", Message: "must be production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := EvaluateAssertions(compiled, content); err != nil {
		t.Errorf("expected all pass, got: %v", err)
	}
}

func TestEvaluateAssertions_OneFailure(t *testing.T) {
	content := `
spec:
  owner: individual-dev
  lifecycle: production
`
	compiled, err := CompileAssertions([]AssertionConfig{
		{Pattern: `owner:`, Message: "must have owner field"},
		{YAMLPath: "spec.owner", Contains: "team", Message: "owner must be a team"},
		{YAMLPath: "spec.lifecycle", Equals: "production", Message: "must be production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = EvaluateAssertions(compiled, content)
	if err == nil {
		t.Fatal("expected failure")
	}

	if err.Error() != "owner must be a team" {
		t.Errorf("got %q, want %q", err, "owner must be a team")
	}
}

func TestEvaluate_YAMLPathArrayWildcard(t *testing.T) {
	content := `
updates:
  - package-ecosystem: gomod
    directory: /
  - package-ecosystem: docker
    directory: /
`
	compiled, err := CompileAssertions([]AssertionConfig{
		{YAMLPath: "updates[*].package-ecosystem", Contains: "gomod", Message: "must have gomod"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := compiled[0].Evaluate(content); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_YAMLPathNonExistentPath(t *testing.T) {
	content := `
spec:
  owner: team-platform
`
	compiled, err := CompileAssertions([]AssertionConfig{
		{YAMLPath: "spec.nonexistent", Contains: "value", Message: "field missing"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = compiled[0].Evaluate(content)
	if err == nil {
		t.Fatal("expected failure for non-existent path")
	}

	if err.Error() != "field missing" {
		t.Errorf("got %q, want %q", err, "field missing")
	}
}

func TestEvaluate_ErrorMessageIncludesAssertionMessage(t *testing.T) {
	compiled, err := CompileAssertions([]AssertionConfig{
		{Pattern: `required-text`, Message: "File must include required-text marker"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = compiled[0].Evaluate("no match here")
	if err == nil {
		t.Fatal("expected failure")
	}

	if !strings.Contains(err.Error(), "File must include required-text marker") {
		t.Errorf("error %q should contain the assertion message", err)
	}
}
