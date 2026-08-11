package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dirPerm is the mode for the output directory. Reports name internal
// repositories and their weaknesses, so they are not world-readable.
const dirPerm = 0o750

// filePerm matches: owner-writable, group-readable, no world access.
const filePerm = 0o640

// WriteAll renders every org into dir and returns the paths written.
//
// One file per org rather than one combined document: the audience is
// per-org, and a single file invites sending the whole fleet's
// weaknesses to a team that should only see their own.
func (r *Renderer) WriteAll(dir string, orgs []Org) ([]string, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("report: create %s: %w", dir, err)
	}

	paths := make([]string, 0, len(orgs))

	for _, o := range orgs {
		name, err := filename(o.Name)
		if err != nil {
			return nil, err
		}

		body, err := r.Render(o)
		if err != nil {
			return nil, err
		}

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), filePerm); err != nil {
			return nil, fmt.Errorf("report: write %s: %w", path, err)
		}

		paths = append(paths, path)
	}

	return paths, nil
}

// filename returns the report filename for an org.
//
// Rejects rather than sanitizes. The org name reaches here from a
// database column, and silently rewriting a bad one is worse than
// refusing it in two ways: a name containing a separator would escape
// the output directory, and any sanitizing rule can map two distinct
// orgs onto one filename, so one org's report would overwrite
// another's and nothing would say so.
func filename(org string) (string, error) {
	switch {
	case org == "":
		return "", fmt.Errorf("report: empty org name")
	case org == "." || org == "..":
		return "", fmt.Errorf("report: refusing to write a report for org %q", org)
	case strings.ContainsAny(org, `/\`):
		return "", fmt.Errorf("report: refusing org name %q: contains a path separator", org)
	case strings.Contains(org, ".."):
		return "", fmt.Errorf("report: refusing org name %q: contains a parent-directory reference", org)
	}

	return org + ".md", nil
}
