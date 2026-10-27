package control

import (
	"errors"
	"fmt"
	"slices"
)

// Change-set bounds (D18). They are repo-guardian's own: the commit
// mutation accepted 101 files and a 1 MiB + 1 byte file in the IMPL-0028
// spike (INV-0022 Phase-0 results, spike 1), and no control needs more.
const (
	MaxChangeSetFiles = 100
	MaxFileBytes      = 1 << 20
)

// ErrNotRegularFile is returned by a Reader for a path that is a symlink
// or a submodule. The evaluation reader records it as a read error with
// Err set to NotRegularFile; a change set may not write that path, and the
// rule that needed it is Blocked.
var ErrNotRegularFile = errors.New("not a regular file")

// NotRegularFile is the ResourceRead.Err of a symlink or submodule read.
const NotRegularFile = "not_regular_file"

// ErrInvalidChangeSet is wrapped by every ValidateChangeSet failure.
var ErrInvalidChangeSet = errors.New("invalid change set")

// ValidateChangeSet checks a ChangeSet before anything leaves Remediate
// (D18): file paths are canonical and unique; every file and API resource
// is within c.Owns(); at most MaxChangeSetFiles files and MaxFileBytes per
// file; no write to a path ev read as a symlink or submodule; every rule
// named is declared and named once; every failing rule of ev is in exactly
// one of Fixed, Manual or Blocked; and there are at least as many notes as
// Manual and Blocked rules, so each is explained.
func ValidateChangeSet(c Control, ev Evaluation, cs ChangeSet) error { //nolint:gocritic // hugeParam: DESIGN-0031's signature
	if err := validateWrites(c, ev, cs); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalidChangeSet, c.ID(), err)
	}

	if err := validateDisposition(c, ev, cs); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalidChangeSet, c.ID(), err)
	}

	return nil
}

func validateWrites(c Control, ev Evaluation, cs ChangeSet) error { //nolint:gocritic // hugeParam: passed through
	if len(cs.Files) > MaxChangeSetFiles {
		return fmt.Errorf("%d files, over %d", len(cs.Files), MaxChangeSetFiles)
	}

	owned := make(map[Resource]bool)
	for _, r := range c.Owns() {
		owned[r] = true
	}

	irregular := make(map[Resource]bool)

	for _, r := range ev.Reads {
		if r.State == ReadError && r.Err == NotRegularFile {
			irregular[r.Resource] = true
		}
	}

	seen := make(map[Resource]bool, len(cs.Files))

	for _, f := range cs.Files {
		r, err := Canonical(KindFile, f.Path)
		if err != nil {
			return err
		}

		switch {
		case seen[r]:
			return fmt.Errorf("%s changed twice", r)
		case !owned[r]:
			return fmt.Errorf("%s is not owned by the control", r)
		case irregular[r]:
			return fmt.Errorf("%s is a symlink or submodule; its rule belongs in Blocked", r)
		case !f.Delete && len(f.Content) > MaxFileBytes:
			return fmt.Errorf("%s is %d bytes, over %d", r, len(f.Content), MaxFileBytes)
		}

		seen[r] = true
	}

	for _, a := range cs.API {
		if !owned[a.Resource] {
			return fmt.Errorf("%s is not owned by the control", a.Resource)
		}
	}

	return nil
}

func validateDisposition(c Control, ev Evaluation, cs ChangeSet) error { //nolint:gocritic // hugeParam: passed through
	declared := make(map[RuleID]bool)
	for _, r := range c.Rules() {
		declared[r.ID] = true
	}

	where := make(map[RuleID]string)

	for name, ids := range map[string][]RuleID{"Fixed": cs.Fixed, "Manual": cs.Manual, "Blocked": cs.Blocked} {
		for _, id := range ids {
			if !declared[id] {
				return fmt.Errorf("%s names undeclared rule %q", name, id)
			}

			if prev, dup := where[id]; dup {
				return fmt.Errorf("rule %q is in both %s and %s", id, prev, name)
			}

			where[id] = name
		}
	}

	for _, r := range ev.Results {
		if r.Status == Fail && where[r.RuleID] == "" {
			return fmt.Errorf("failing rule %q is in none of Fixed, Manual or Blocked", r.RuleID)
		}
	}

	for _, id := range cs.Fixed {
		if !slices.ContainsFunc(ev.Results, func(r RuleResult) bool { return r.RuleID == id && r.Status == Fail }) {
			return fmt.Errorf("rule %q is in Fixed but not failing", id)
		}
	}

	if want := len(cs.Manual) + len(cs.Blocked); len(cs.Notes) < want {
		return fmt.Errorf("%d Manual and Blocked rules but %d notes", want, len(cs.Notes))
	}

	return nil
}
