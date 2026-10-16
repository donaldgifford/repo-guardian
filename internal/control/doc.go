// Package control is the controls framework's dependency leaf
// (DESIGN-0031 D17). It holds the value types a control produces or
// consumes (Definition, Rule, RuleResult, Resource, ResourceRead,
// Evaluation, ChangeSet), the Type and Control interfaces, and the
// consumer-side interfaces a control, the evaluation and the remediation
// read and write GitHub through: Reader, PRObserver and Writer.
//
// It imports internal/template and nothing else of ours, so every package
// on either side of the evaluator/remediator split can depend on it; the
// depguard rule control-leaf enforces that. internal/github implements
// Reader and PRObserver; internal/github/write implements Writer, and
// depguard keeps that package away from the evaluator side.
package control
