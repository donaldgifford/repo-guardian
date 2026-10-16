package control

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// countingReader is a Reader that counts its calls and serves fixed
// answers; any method it is not given answers with errNotSeeded.
type countingReader struct {
	calls atomic.Int32

	files    map[string]string
	fileErr  map[string]error
	labelErr error
	props    map[string]*string
	settings RepositorySettings
}

var errNotSeeded = errors.New("not seeded")

func (c *countingReader) GetContents(_ context.Context, path string) ([]byte, string, bool, error) {
	c.calls.Add(1)

	if err := c.fileErr[path]; err != nil {
		return nil, "", false, err
	}

	body, ok := c.files[path]
	if !ok {
		return nil, "", false, nil
	}

	return []byte(body), "sha-" + path, true, nil
}

func (c *countingReader) ListDirectory(context.Context, string) ([]string, error) {
	c.calls.Add(1)

	return []string{"b", "a"}, nil
}

func (c *countingReader) GetRepository(context.Context) (RepositorySettings, error) {
	c.calls.Add(1)

	return c.settings, nil
}

func (c *countingReader) ListRulesets(context.Context) ([]Ruleset, error) {
	c.calls.Add(1)

	return nil, errNotSeeded
}

func (c *countingReader) GetCustomProperties(context.Context) (map[string]*string, error) {
	c.calls.Add(1)

	return c.props, nil
}

func (c *countingReader) OrgPropertySchema(context.Context) ([]string, error) {
	c.calls.Add(1)

	return nil, errNotSeeded
}

func (c *countingReader) ListLabels(context.Context) ([]Label, error) {
	c.calls.Add(1)

	if c.labelErr != nil {
		return nil, c.labelErr
	}

	return []Label{{Name: "Bug", Color: "d73a4a"}}, nil
}

var fixedNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func newEval(base Reader) *EvalReader {
	return NewEvalReader(base, func() time.Time { return fixedNow })
}

func TestEvalReader_SharedFileIsOneCall(t *testing.T) {
	t.Parallel()

	base := &countingReader{files: map[string]string{"CODEOWNERS": "* @team\n"}}
	r := newEval(base)

	// Two controls read one file.
	for _, p := range []string{"CODEOWNERS", "CODEOWNERS"} {
		got, sha, found, err := r.GetContents(t.Context(), p)
		if err != nil || !found || sha != "sha-CODEOWNERS" || string(got) != "* @team\n" {
			t.Fatalf("GetContents(%q) = %q, %q, %v, %v; want the file", p, got, sha, found, err)
		}
	}

	if n := base.calls.Load(); n != 1 {
		t.Errorf("underlying calls = %d, want 1", n)
	}

	want := ResourceRead{Resource: MustCanonical(KindFile, "CODEOWNERS"), State: Present, Digest: "sha-CODEOWNERS"}
	if got := r.Reads(); len(got) != 1 || got[0] != want {
		t.Errorf("Reads() = %+v, want [%+v]", got, want)
	}
}

func TestEvalReader_AbsentAndErrorReadsAreRecorded(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	base := &countingReader{fileErr: map[string]error{
		"broken":  boom,
		"symlink": fmt.Errorf("reading symlink: %w", ErrNotRegularFile),
	}}
	r := newEval(base)

	if _, _, found, err := r.GetContents(t.Context(), "missing"); found || err != nil {
		t.Fatalf("GetContents(missing) = %v, %v; want absent", found, err)
	}

	for range 2 {
		if _, _, _, err := r.GetContents(t.Context(), "broken"); !errors.Is(err, boom) {
			t.Fatalf("GetContents(broken) err = %v, want boom", err)
		}
	}

	if _, _, _, err := r.GetContents(t.Context(), "symlink"); !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("GetContents(symlink) err = %v, want ErrNotRegularFile", err)
	}

	if n := base.calls.Load(); n != 3 {
		t.Errorf("underlying calls = %d, want 3 (the failed read is cached too)", n)
	}

	want := []ResourceRead{
		{Resource: MustCanonical(KindFile, "broken"), State: ReadError, Err: "boom"},
		{Resource: MustCanonical(KindFile, "missing"), State: Absent},
		{Resource: MustCanonical(KindFile, "symlink"), State: ReadError, Err: NotRegularFile},
	}

	got := r.Reads()
	if len(got) != len(want) {
		t.Fatalf("Reads() = %+v, want %+v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Reads()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEvalReader_PermissionSurfaces(t *testing.T) {
	t.Parallel()

	denied := fmt.Errorf("%w: 403 Resource not accessible by integration", ErrPermission)
	r := newEval(&countingReader{labelErr: denied})

	if _, err := r.ListLabels(t.Context()); !errors.Is(err, ErrPermission) {
		t.Fatalf("ListLabels() err = %v, want ErrPermission", err)
	}

	got := r.Reads()
	if len(got) != 1 || got[0].Resource != (Resource{Kind: KindLabel, Key: listWildcard}) ||
		got[0].State != ReadError || !got[0].ObservedAt.Equal(fixedNow) {
		t.Errorf("Reads() = %+v, want one label:* error read observed now", got)
	}
}

func TestEvalReader_APIReads(t *testing.T) {
	t.Parallel()

	wiki := true
	r := newEval(&countingReader{
		settings: RepositorySettings{HasWiki: &wiki},
		props:    map[string]*string{"Owner": nil},
	})

	if _, err := r.GetRepository(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := r.GetCustomProperties(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := r.ListLabels(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := r.ListDirectory(t.Context(), ""); err != nil {
		t.Fatal(err)
	}

	byKey := make(map[string]ResourceRead)
	for _, rr := range r.Reads() {
		byKey[rr.Resource.String()] = rr
	}

	if n := len(byKey); n != len(settingNames)+3 {
		t.Errorf("recorded %d reads, want %d", n, len(settingNames)+3)
	}

	checks := []struct {
		key   string
		state ReadState
	}{
		{"setting:has_wiki", Present},
		{"setting:has_issues", Absent},
		{"setting:security_and_analysis", Absent},
		{"property:Owner", Present},
		{"label:bug", Present},
		{"tree:.", Present},
	}

	for _, c := range checks {
		rr, ok := byKey[c.key]
		if !ok || rr.State != c.state {
			t.Errorf("read %s = %+v (recorded %v), want state %s", c.key, rr, ok, c.state)
		}
	}

	if d := byKey["property:Owner"].Digest; d == "" {
		t.Error("property:Owner (null) has no digest; a null value is a present read")
	}

	if !byKey["setting:has_wiki"].ObservedAt.Equal(fixedNow) {
		t.Error("setting:has_wiki has no ObservedAt")
	}

	if !byKey["tree:."].ObservedAt.IsZero() {
		t.Error("tree:. has an ObservedAt; a tree read is commit-pinned")
	}
}
