package policy

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// CompiledAssertion is a pre-compiled version of AssertionConfig.
// Regexes are compiled at config load time so that compile errors are
// caught early and evaluation is efficient.
type CompiledAssertion struct {
	pattern    *regexp.Regexp
	notPattern *regexp.Regexp
	yamlPath   string
	contains   string
	equals     string
	message    string
}

// CompileAssertions compiles all assertions in a FileRuleConfig.
// Returns an error if any regex pattern is invalid.
func CompileAssertions(assertions []AssertionConfig) ([]CompiledAssertion, error) {
	compiled := make([]CompiledAssertion, 0, len(assertions))

	for i, a := range assertions {
		ca, err := compileAssertion(&a)
		if err != nil {
			return nil, fmt.Errorf("assertion[%d]: %w", i, err)
		}

		compiled = append(compiled, ca)
	}

	return compiled, nil
}

func compileAssertion(a *AssertionConfig) (CompiledAssertion, error) {
	ca := CompiledAssertion{
		yamlPath: a.YAMLPath,
		contains: a.Contains,
		equals:   a.Equals,
		message:  a.Message,
	}

	if a.Pattern != "" {
		re, err := regexp.Compile(a.Pattern)
		if err != nil {
			return CompiledAssertion{}, fmt.Errorf("invalid pattern %q: %w", a.Pattern, err)
		}

		ca.pattern = re
	}

	if a.NotPattern != "" {
		re, err := regexp.Compile(a.NotPattern)
		if err != nil {
			return CompiledAssertion{}, fmt.Errorf("invalid not_pattern %q: %w", a.NotPattern, err)
		}

		ca.notPattern = re
	}

	return ca, nil
}

// Evaluate runs the compiled assertion against file content.
// Returns an error with the assertion's message if the check fails.
func (ca *CompiledAssertion) Evaluate(content string) error {
	if ca.pattern != nil {
		if !ca.pattern.MatchString(content) {
			return fmt.Errorf("%s", ca.message)
		}
	}

	if ca.notPattern != nil {
		if ca.notPattern.MatchString(content) {
			return fmt.Errorf("%s", ca.message)
		}
	}

	if ca.yamlPath != "" {
		return ca.evaluateYAMLPath(content)
	}

	return nil
}

func (ca *CompiledAssertion) evaluateYAMLPath(content string) error {
	values, err := EvaluateYAMLPath(content, ca.yamlPath)
	if err != nil {
		return fmt.Errorf("evaluating yaml_path %q: %w", ca.yamlPath, err)
	}

	if ca.contains != "" {
		for _, v := range values {
			if strings.Contains(v, ca.contains) {
				return nil
			}
		}

		return fmt.Errorf("%s", ca.message)
	}

	if ca.equals != "" {
		if !slices.Contains(values, ca.equals) {
			return fmt.Errorf("%s", ca.message)
		}
	}

	return nil
}

// EvaluateAssertions runs all compiled assertions against content.
// Returns the first assertion failure, or nil if all pass.
func EvaluateAssertions(assertions []CompiledAssertion, content string) error {
	for i := range assertions {
		if err := assertions[i].Evaluate(content); err != nil {
			return err
		}
	}

	return nil
}
