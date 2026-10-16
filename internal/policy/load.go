package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// MaxFileBytes caps every file under the policy root (DESIGN-0030; the
// whole root also has to fit one 1 MiB ConfigMap).
const MaxFileBytes = 1 << 20

// The policy root layout (DESIGN-0030 § Files).
const (
	enterpriseFile = "enterprise.hcl"
	catalogueDir   = "catalogue"
	orgsDir        = "orgs"
	templatesDir   = "templates"
	kubeletData    = "..data"
	hclExt         = ".hcl"
)

// Load reads the policy root in one pass, parses and validates every
// file, builds every catalogue definition through reg, and returns the
// snapshot. Warnings go to logger once. There is no hot reload: a
// change to the root takes effect on the next Load.
//
// Expressions may only be literals and file("<path>"), a path relative
// to the root. Every file is resolved with filepath.EvalSymlinks and must
// lie under the resolved root, which admits a ConfigMap projection's
// symlinks and rejects links that escape it; each is capped at
// MaxFileBytes. Errors carry file:line.
func Load(root string, reg *control.Registry, logger *slog.Logger) (*Snapshot, error) {
	files, err := readRoot(root)
	if err != nil {
		return nil, err
	}

	l := &loader{files: files, reg: reg, parser: hclparse.NewParser()}
	l.ctx = &hcl.EvalContext{Functions: map[string]function.Function{"file": l.fileFunc()}}

	snap, err := l.load()
	if err != nil {
		return nil, err
	}

	for _, w := range snap.warnings {
		logger.Warn("policy", "warning", w.String())
	}

	return snap, nil
}

// loader is one Load in progress.
type loader struct {
	files  map[string][]byte
	reg    *control.Registry
	parser *hclparse.Parser
	ctx    *hcl.EvalContext
}

// load parses the layout and hands each part to its decoder.
func (l *loader) load() (*Snapshot, error) {
	layout, err := classify(l.files)
	if err != nil {
		return nil, err
	}

	bodies := make(map[string]hcl.Body, len(layout.hcl))

	for _, name := range layout.hcl {
		f, diags := l.parser.ParseHCL(l.files[name], name)
		if diags.HasErrors() {
			return nil, diags
		}

		bodies[name] = f.Body
	}

	return l.build(layout, bodies)
}

// rootLayout is the policy root's files sorted into their roles.
type rootLayout struct {
	catalogue []string
	orgs      []string
	// hcl is every HCL file, sorted.
	hcl []string
}

// classify checks the layout: exactly one enterprise.hcl at the root,
// catalogue/*.hcl and orgs/*.hcl one level deep, and no other HCL file.
func classify(files map[string][]byte) (rootLayout, error) {
	var out rootLayout

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	slices.Sort(names)

	enterprise := false

	for _, name := range names {
		if path.Ext(name) != hclExt {
			continue
		}

		dir := path.Dir(name)

		switch {
		case name == enterpriseFile:
			enterprise = true
		case dir == catalogueDir:
			out.catalogue = append(out.catalogue, name)
		case dir == orgsDir:
			out.orgs = append(out.orgs, name)
		default:
			return rootLayout{}, fmt.Errorf(
				"%s: unexpected HCL file; policy files are %s, %s/*.hcl and %s/*.hcl",
				name,
				enterpriseFile,
				catalogueDir,
				orgsDir,
			)
		}

		out.hcl = append(out.hcl, name)
	}

	if !enterprise {
		return rootLayout{}, fmt.Errorf("policy root has no %s", enterpriseFile)
	}

	return out, nil
}

// fileFunc is file("<path>"): the content of a file under the root, as
// read in the same pass, so it is hashed with everything else.
func (l *loader) fileFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			rel, err := rootRelative(args[0].AsString())
			if err != nil {
				return cty.NilVal, err
			}

			b, ok := l.files[rel]
			if !ok {
				return cty.NilVal, fmt.Errorf("%s: no such file under the policy root", rel)
			}

			return cty.StringVal(string(b)), nil
		},
	})
}

// rootRelative validates a path a policy names: relative, inside the
// root lexically, and clean.
func rootRelative(p string) (string, error) {
	if p == "" || path.IsAbs(p) || strings.HasPrefix(p, "../") || p == ".." {
		return "", fmt.Errorf("%q: a path must be relative to the policy root and stay inside it", p)
	}

	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q: a path must stay inside the policy root", p)
	}

	return clean, nil
}

// readRoot reads every file under root once, keyed by its slash path
// relative to the root. When root holds kubelet's ..data link it walks
// that, the projection's one consistent snapshot; names starting with
// ".." are skipped either way (INV-0022 spike 7).
func readRoot(root string) (map[string][]byte, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("policy root %s: %w", root, err)
	}

	base := resolvedRoot
	if info, err := os.Stat(filepath.Join(resolvedRoot, kubeletData)); err == nil && info.IsDir() {
		if base, err = filepath.EvalSymlinks(filepath.Join(resolvedRoot, kubeletData)); err != nil {
			return nil, fmt.Errorf("policy root %s: %w", root, err)
		}
	}

	files := make(map[string][]byte)

	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if p == base {
			return nil
		}

		if strings.HasPrefix(d.Name(), "..") {
			if d.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}

		b, err := readContained(resolvedRoot, p, filepath.ToSlash(rel))
		if err != nil {
			return err
		}

		files[filepath.ToSlash(rel)] = b

		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// errOutsideRoot is a file that resolves outside the policy root.
var errOutsideRoot = errors.New("resolves outside the policy root")

// readContained resolves p, requires it to lie under root and to be a
// regular file of at most MaxFileBytes, and reads it.
func readContained(root, p, rel string) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}

	within, err := filepath.Rel(root, resolved)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) || filepath.IsAbs(within) {
		return nil, fmt.Errorf("%s: %w", rel, errOutsideRoot)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", rel)
	}

	if info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("%s: %d bytes, over the %d-byte limit", rel, info.Size(), MaxFileBytes)
	}

	b, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}

	return b, nil
}
