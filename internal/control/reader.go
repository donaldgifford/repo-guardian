package control

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// listWildcard is the key a failed list read is recorded under: the
// names the list would have returned are unknown, so the whole
// collection is the resource that errored.
const listWildcard = "*"

// rootTree is the tree key for a listing of the repository root.
const rootTree = "."

// EvalReader is the Reader every control in one evaluation shares
// (DESIGN-0031 D13). It wraps the per-repository adapter, which is
// already pinned to one commit, caches every read for the evaluation
// (failures included, so every control sees the same answer), and
// records a ResourceRead per resource for the fingerprint.
//
// Build one per evaluation with NewEvalReader; it is safe for
// concurrent use.
type EvalReader struct {
	base Reader
	now  func() time.Time

	mu    sync.Mutex
	calls map[string]*evalCall
	reads map[Resource]ResourceRead
}

var _ Reader = (*EvalReader)(nil)

// evalCall is one cached base read; once guards the fetch so concurrent
// callers for the same key make one underlying call.
type evalCall struct {
	once sync.Once
	val  any
	err  error
}

// NewEvalReader returns an EvalReader over base. now stamps ObservedAt
// on API reads; file reads are commit-pinned and carry none.
func NewEvalReader(base Reader, now func() time.Time) *EvalReader {
	return &EvalReader{
		base:  base,
		now:   now,
		calls: make(map[string]*evalCall),
		reads: make(map[Resource]ResourceRead),
	}
}

// Reads returns every recorded read, sorted by resource.
func (r *EvalReader) Reads() []ResourceRead {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]ResourceRead, 0, len(r.reads))
	for _, rr := range r.reads {
		out = append(out, rr)
	}

	slices.SortFunc(out, func(a, b ResourceRead) int {
		return strings.Compare(a.Resource.String(), b.Resource.String())
	})

	return out
}

// cached runs fetch once per key for the reader's lifetime and returns
// its result to every caller. record runs inside the same once, so a
// resource is recorded exactly when it is fetched.
func cached[T any](r *EvalReader, key string, fetch func() (T, error)) (T, error) {
	r.mu.Lock()
	c, ok := r.calls[key]
	if !ok {
		c = &evalCall{}
		r.calls[key] = c
	}
	r.mu.Unlock()

	c.once.Do(func() { c.val, c.err = fetch() })

	v, ok := c.val.(T)
	if !ok {
		var zero T

		return zero, c.err
	}

	return v, c.err
}

func (r *EvalReader) record(rr *ResourceRead) {
	r.mu.Lock()
	r.reads[rr.Resource] = *rr
	r.mu.Unlock()
}

// recordAPI records an API read observed now: present with the digest
// of v's canonical JSON, or error.
func (r *EvalReader) recordAPI(res Resource, v any, err error) {
	rr := ResourceRead{Resource: res, ObservedAt: r.now()}

	switch {
	case err != nil:
		rr.State, rr.Err = ReadError, readErr(err)
	default:
		d, derr := digest(v)
		if derr != nil {
			rr.State, rr.Err = ReadError, derr.Error()
		} else {
			rr.State, rr.Digest = Present, d
		}
	}

	r.record(&rr)
}

// readErr is ResourceRead.Err for err: NotRegularFile for a symlink or
// submodule, so ValidateChangeSet can refuse a write to it.
func readErr(err error) string {
	if errors.Is(err, ErrNotRegularFile) {
		return NotRegularFile
	}

	return err.Error()
}

// fileResult is one cached GetContents result.
type fileResult struct {
	content []byte
	sha     string
	found   bool
}

// GetContents reads one file once per evaluation and records it as
// present (digest = blob SHA), absent, or error.
func (r *EvalReader) GetContents(ctx context.Context, path string) ([]byte, string, bool, error) {
	res, err := Canonical(KindFile, path)
	if err != nil {
		return nil, "", false, err
	}

	f, err := cached(r, res.String(), func() (fileResult, error) {
		content, sha, found, err := r.base.GetContents(ctx, res.Key)

		rr := ResourceRead{Resource: res}

		switch {
		case err != nil:
			rr.State, rr.Err = ReadError, readErr(err)
		case found:
			rr.State, rr.Digest = Present, sha
		default:
			rr.State = Absent
		}

		r.record(&rr)

		return fileResult{content: content, sha: sha, found: found}, err
	})
	if err != nil {
		return nil, "", false, err
	}

	return slices.Clone(f.content), f.sha, f.found, nil
}

// ListDirectory lists path once per evaluation and records it as
// tree:<path> (tree:. for the root): present with the digest of the
// sorted names, absent when empty, or error.
func (r *EvalReader) ListDirectory(ctx context.Context, path string) ([]string, error) {
	key := path
	if key == "" {
		key = rootTree
	}

	res := Resource{Kind: KindTree, Key: key}

	names, err := cached(r, res.String(), func() ([]string, error) {
		names, err := r.base.ListDirectory(ctx, path)

		rr := ResourceRead{Resource: res}

		switch {
		case err != nil:
			rr.State, rr.Err = ReadError, readErr(err)
		case len(names) == 0:
			rr.State = Absent
		default:
			sorted := slices.Sorted(slices.Values(names))
			if d, derr := digest(sorted); derr != nil {
				rr.State, rr.Err = ReadError, derr.Error()
			} else {
				rr.State, rr.Digest = Present, d
			}
		}

		r.record(&rr)

		return names, err
	})

	return slices.Clone(names), err
}

// settingNames are the JSON names of RepositorySettings, one setting
// resource each.
var settingNames = func() []string {
	t := reflect.TypeFor[RepositorySettings]()

	names := make([]string, 0, t.NumField())
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		names = append(names, name)
	}

	return names
}()

// GetRepository reads the settings once per evaluation and records one
// setting:<json name> per field: present with its value's digest,
// absent when the response lacked it, or every field as error.
func (r *EvalReader) GetRepository(ctx context.Context) (RepositorySettings, error) {
	s, err := cached(r, "repository", func() (RepositorySettings, error) {
		s, err := r.base.GetRepository(ctx)

		v := reflect.ValueOf(s)

		for i, name := range settingNames {
			res := Resource{Kind: KindSetting, Key: name}

			if err == nil && v.Field(i).IsNil() {
				r.record(&ResourceRead{Resource: res, State: Absent, ObservedAt: r.now()})

				continue
			}

			r.recordAPI(res, v.Field(i).Interface(), err)
		}

		return s, err
	})

	s.SecurityAndAnalysis = maps.Clone(s.SecurityAndAnalysis)

	return s, err
}

// ListRulesets reads the rulesets once per evaluation and records each
// as ruleset:<name>; a failed list is recorded as ruleset:*.
func (r *EvalReader) ListRulesets(ctx context.Context) ([]Ruleset, error) {
	rulesets, err := cached(r, "rulesets", func() ([]Ruleset, error) {
		rulesets, err := r.base.ListRulesets(ctx)
		if err != nil {
			r.recordAPI(Resource{Kind: KindRuleset, Key: listWildcard}, nil, err)

			return nil, err
		}

		for i := range rulesets {
			r.recordAPI(Resource{Kind: KindRuleset, Key: rulesets[i].Name}, rulesets[i], nil)
		}

		return rulesets, nil
	})

	return slices.Clone(rulesets), err
}

// GetCustomProperties reads the property values once per evaluation
// and records each as property:<name>; a null value is present with the
// digest of null, distinct from an absent property. A failed read is
// recorded as property:*.
func (r *EvalReader) GetCustomProperties(ctx context.Context) (map[string]*string, error) {
	props, err := cached(r, "properties", func() (map[string]*string, error) {
		props, err := r.base.GetCustomProperties(ctx)
		if err != nil {
			r.recordAPI(Resource{Kind: KindProperty, Key: listWildcard}, nil, err)

			return nil, err
		}

		for name, v := range props {
			r.recordAPI(Resource{Kind: KindProperty, Key: name}, v, nil)
		}

		return props, nil
	})
	if err != nil {
		return nil, err
	}

	return maps.Clone(props), nil
}

// SettingVulnerabilityAlerts is the setting key of Dependabot alerts,
// which RepositorySettings does not carry because it is read from its
// own endpoint.
const SettingVulnerabilityAlerts = "vulnerability_alerts"

// GetVulnerabilityAlerts reads the alerts flag once per evaluation and
// records it as setting:vulnerability_alerts.
func (r *EvalReader) GetVulnerabilityAlerts(ctx context.Context) (bool, error) {
	return cached(r, SettingVulnerabilityAlerts, func() (bool, error) {
		enabled, err := r.base.GetVulnerabilityAlerts(ctx)
		r.recordAPI(Resource{Kind: KindSetting, Key: SettingVulnerabilityAlerts}, enabled, err)

		return enabled, err
	})
}

// OrgPropertySchema reads the org schema once per evaluation. It is an
// org-level read, not a repository resource, so it is not recorded.
func (r *EvalReader) OrgPropertySchema(ctx context.Context) ([]string, error) {
	names, err := cached(r, "schema", func() ([]string, error) {
		return r.base.OrgPropertySchema(ctx)
	})

	return slices.Clone(names), err
}

// ListLabels reads the labels once per evaluation and records each as
// label:<lower-cased name>; a failed list is recorded as label:*.
func (r *EvalReader) ListLabels(ctx context.Context) ([]Label, error) {
	labels, err := cached(r, "labels", func() ([]Label, error) {
		labels, err := r.base.ListLabels(ctx)
		if err != nil {
			r.recordAPI(Resource{Kind: KindLabel, Key: listWildcard}, nil, err)

			return nil, err
		}

		for _, l := range labels {
			r.recordAPI(Resource{Kind: KindLabel, Key: strings.ToLower(l.Name)}, l, nil)
		}

		return labels, nil
	})

	return slices.Clone(labels), err
}
