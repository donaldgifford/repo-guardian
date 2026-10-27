# Adding a control type

Most new checks need no code: a catalogue entry of an existing type
(`file` above all) is enough, and is covered by the
[policy reference](usage/policy-reference.md). This page is for the
rarer case where no built-in type can express the check and repo-guardian
itself needs a new **control type**. The design is
[DESIGN-0031](design/0031-control-framework-controls-control-rules-and-file-controls.md).

<!--toc:start-->
- [Where a type lives](#where-a-type-lives)
- [The two interfaces](#the-two-interfaces)
- [Owns and Reads](#owns-and-reads)
- [API types](#api-types)
- [Semantics and the revision golden](#semantics-and-the-revision-golden)
- [Tests](#tests)
- [The generated reference](#the-generated-reference)
- [Checklist](#checklist)
<!--toc:end-->

## Where a type lives

Each type is one package under `internal/controls/<name>/`. A type may
import `internal/control`, `internal/template`, `internal/catalog` and
`internal/metrics` and nothing else of ours; depguard (`controls-types`)
fails the lint otherwise. Only `internal/activities`, `cmd/repo-guardian`
and `examples/*_test.go` import the types (`controls-consumers`).

`internal/controls/builtin` is the one place a type is registered: add
it to `Types()` in name order. `Registry()` builds the registry the
policy loader, the evaluator, the remediator and `repo-guardian
controls docs` all use, so a type registered there is available
everywhere at once.

## The two interfaces

A `control.Type` is the kind (`codeowners`); a `control.Control` is one
catalogue definition of it (`codeowners@1`), built at policy load:

- `Name()` is the name a catalogue entry's `type = "..."` refers to.
- `Semantics()` is the version of the type's evaluation meaning (see
  below).
- `RuleKinds()` declares the rules a definition may contain and their
  parameters. The loader refuses an undeclared rule.
- `Build(def, env)` validates the definition's parameters and returns
  the `Control`. Every parameter error belongs here, so a bad policy
  fails at load rather than at evaluation.

The `Control` then:

- `Evaluate` reads through the `control.Reader` it is given, pinned to
  one commit, and returns one result per declared rule. It never
  writes. The workflows, not the type, run `control.Validate` and
  `control.StatusOf` on the result; evidence is flat and carries
  `evidence_kind` and `evidence_version`.
- `Remediate` returns the `ChangeSet` that makes the failing remediable
  rules pass, computed against the same pinned state, and names the
  rules it could not fix. It writes nothing either: the remediator owns
  every write.

## Owns and Reads

`Owns()` is what the control may write. Every `ChangeSet` is checked
against it (`control.ValidateChangeSet`), and two controls owning the
same resource is a load-time warning.

`Reads()` is what invalidates a result: files read without owning them,
tree prefixes, and every alternate location of a file control. A push
re-evaluates a control when it touches a file or tree in
`Owns() ∪ Reads()`; API resources are re-evaluated on the schedule only.
Get `Reads()` wrong and a relevant push re-evaluates nothing.

## API types

A type that changes repository settings through the API rather than a
commit implements the `control.APIType` marker (`RemediatesByAPI()`);
only such a type may take the `apply` attribute. One that also
implements `control.WorkflowApply` may be applied with
`apply = "workflow"`, where the remediation pull request carries a
workflow that applies the change on merge. The Remediation App's
permissions are derived from the policy (`control.PermissionResource`
for each resource kind a `direct` control writes), so a new resource
kind needs an entry there.

## Semantics and the revision golden

`control.Revision` is a content digest over the definition: id,
version, type name, `Semantics()`, rules, parameters, apply and every
template byte it references. When a revision changes, every result of
that control is stale fleet-wide and re-evaluates.

Each type pins one revision in `TestRevisionGolden`. If a change alters
what the type's evaluation **means**, bump `Semantics()` and update the
golden in the same change. If a refactor moves the golden without
changing meaning, the refactor is wrong: never edit the golden to make
a refactor pass. Titles are excluded from the digest, so a title edit
moves nothing.

## Tests

- Run the conformance suite: `controltest.Run(t, Type{}, fixtures)`
  checks remediation, idempotence, byte-exact minimal edits, that the
  template passes its own rules, and two remediable rules failing at
  once. `controltest.FakeRepo` is the repository state.
- Add `TestRevisionGolden` with the type's pinned digest.
- Cover every `Build` error with a table test.

`make test` runs them; `internal/controls/labels` is a short example to
copy.

## The generated reference

[The control-type reference](usage/controls-reference.md) is generated
from the registry by `repo-guardian controls docs`. After adding or
changing a type, run `make controls-docs` and commit the result.
`make lint-controls-docs` (part of `make lint`) fails when the
committed page is stale.

## Checklist

1. `internal/controls/<name>/` with the `Type` and its `Control`.
2. Registered in `internal/controls/builtin.Types()`.
3. `controltest.Run` fixtures, `TestRevisionGolden`, `Build` errors.
4. For an API type: `APIType`, and `control.PermissionResource` for any
   new resource kind.
5. `make controls-docs`, then `make lint` and `make test`.
6. A catalogue example in `examples/policy/` if operators should copy
   it.
