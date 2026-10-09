// Package control is the controls framework's dependency leaf
// (DESIGN-0031 D17). It holds the consumer-side interfaces a control,
// the evaluation and the remediation read and write GitHub through —
// Reader, PRObserver and Writer — and their value types.
//
// It imports nothing of ours, so every package on either side of the
// evaluator/remediator split can depend on it. internal/github
// implements Reader and PRObserver; internal/github/write implements
// Writer, and depguard keeps that package away from the evaluator side.
// IMPL-0029 adds the definitions, rules, the registry and the rest of
// the framework to this package.
package control
