package activities

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// overlayReader reads a commit as if a change set were applied on top
// of it, for the remediation self-check (IMPL-0030 4.8). Only file reads
// change; every other read passes through.
type overlayReader struct {
	control.Reader

	files map[string]control.FileChange
}

var _ control.Reader = (*overlayReader)(nil)

func newOverlayReader(base control.Reader, files []control.FileChange) *overlayReader {
	m := make(map[string]control.FileChange, len(files))
	for _, f := range files {
		m[f.Path] = f
	}

	return &overlayReader{Reader: base, files: m}
}

func (o *overlayReader) GetContents(ctx context.Context, p string) (content []byte, blobSHA string, found bool, err error) {
	f, ok := o.files[p]
	if !ok {
		return o.Reader.GetContents(ctx, p)
	}

	if f.Delete {
		return nil, "", false, nil
	}

	return f.Content, gitBlobSHA(f.Content), true, nil
}

func (o *overlayReader) ListDirectory(ctx context.Context, dir string) ([]string, error) {
	names, err := o.Reader.ListDirectory(ctx, dir)
	if err != nil {
		return nil, err
	}

	for p, f := range o.files {
		parent, name := path.Split(p)
		if strings.TrimSuffix(parent, "/") != strings.Trim(dir, "/") {
			continue
		}

		i := slices.Index(names, name)

		switch {
		case f.Delete && i >= 0:
			names = slices.Delete(names, i, i+1)
		case !f.Delete && i < 0:
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names, nil
}
