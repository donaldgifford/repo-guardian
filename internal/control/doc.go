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
//
// The framework (IMPL-0029 Phase 1) runs on those types:
//
//   - Canonical / MustCanonical: resource keys, canonicalized here and
//     never by a type, so two spellings of one file collide on ownership.
//   - Validate and StatusOf: one result per declared rule, bounded flat
//     evidence, then the control status (fail > error/unknown > all
//     not_applicable > compliant).
//   - ValidateChangeSet: every write owned, canonical, unique, regular and
//     within MaxChangeSetFiles / MaxFileBytes; every failing rule in
//     exactly one of Fixed, Manual or Blocked.
//   - Revision and Fingerprint: the content digest results bind to (D19)
//     and the digest of rule statuses plus read digests (D13).
//   - EvalReader: the Reader every control in one evaluation shares; it
//     caches every read and records a ResourceRead per resource.
//   - LocateFile, FileType and NewFileControl: a file control type writes
//     Parse, Check, Fix and Render; the adapter supplies Evaluate and
//     Remediate per DESIGN-0031's file-control flowchart.
//   - Registry: Register, Type and Build, which validates a Definition
//     against its type's rule kinds and proves its template passes the
//     remediable rules.
//
// Package controltest is the conformance suite (properties 2–6) every
// type in internal/controls runs.
package control
