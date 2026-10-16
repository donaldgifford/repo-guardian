// Package controltest is the conformance suite every control type runs
// (DESIGN-0031 Testing Strategy, properties 2–6), with the in-memory
// repository it runs against.
package controltest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Error keys for FakeRepo.Errors, besides "file:<path>" and
// "tree:<path>".
const (
	ErrKeyRepository = "repository"
	ErrKeyRulesets   = "rulesets"
	ErrKeyProperties = "properties"
	ErrKeySchema     = "schema"
	ErrKeyLabels     = "labels"
	// ErrKeyVulnerabilityAlerts fails GetVulnerabilityAlerts.
	ErrKeyVulnerabilityAlerts = control.SettingVulnerabilityAlerts
)

// FakeRepo is a control.Reader over a map of files and API values. The
// zero value is an empty repository.
type FakeRepo struct {
	// Files maps a path to its content.
	Files map[string]string
	// NotRegular are paths that are a symlink or a submodule; reading
	// one returns control.ErrNotRegularFile.
	NotRegular []string

	Settings   control.RepositorySettings
	Rulesets   []control.Ruleset
	Properties map[string]*string
	Schema     []string
	Labels     []control.Label
	// VulnerabilityAlerts is the Dependabot alerts flag.
	VulnerabilityAlerts bool

	// Errors fails a read: "file:<path>", "tree:<path>" or one of the
	// ErrKey constants.
	Errors map[string]error
}

var _ control.Reader = (*FakeRepo)(nil)

// EmptyRepository is a repository as GitHub creates it: no files, no
// labels, rulesets or properties, and GitHub's default settings. It is
// property 5's starting point, so an API type's remediation is proven
// against settings a real repository returns rather than absent ones.
func EmptyRepository() *FakeRepo {
	on, off := true, false
	str := func(s string) *string { return &s }

	return &FakeRepo{Settings: control.RepositorySettings{
		DefaultBranch:            str("main"),
		HasWiki:                  &on,
		HasIssues:                &on,
		HasProjects:              &on,
		AllowMergeCommit:         &on,
		AllowSquashMerge:         &on,
		AllowRebaseMerge:         &on,
		AllowAutoMerge:           &off,
		AllowUpdateBranch:        &off,
		DeleteBranchOnMerge:      &off,
		WebCommitSignoffRequired: &off,
		SquashMergeCommitTitle:   str("COMMIT_OR_PR_TITLE"),
		SquashMergeCommitMessage: str("COMMIT_MESSAGES"),
		MergeCommitTitle:         str("MERGE_MESSAGE"),
		MergeCommitMessage:       str("PR_TITLE"),
	}}
}

// BlobSHA is the blob SHA FakeRepo reports for content: a digest of it,
// so a changed file changes its read.
func BlobSHA(content string) string {
	sum := sha256.Sum256([]byte(content))

	return hex.EncodeToString(sum[:20])
}

// GetContents reads one file.
func (f *FakeRepo) GetContents(_ context.Context, file string) ([]byte, string, bool, error) {
	if err := f.Errors["file:"+file]; err != nil {
		return nil, "", false, err
	}

	if slices.Contains(f.NotRegular, file) {
		return nil, "", false, fmt.Errorf("%s: %w", file, control.ErrNotRegularFile)
	}

	body, ok := f.Files[file]
	if !ok {
		return nil, "", false, nil
	}

	return []byte(body), BlobSHA(body), true, nil
}

// ListDirectory returns the sorted entry names directly under dir, files
// and subdirectories alike.
func (f *FakeRepo) ListDirectory(_ context.Context, dir string) ([]string, error) {
	if err := f.Errors["tree:"+dir]; err != nil {
		return nil, err
	}

	prefix := ""
	if dir != "" && dir != "." {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}

	seen := make(map[string]bool)

	for file := range f.Files {
		rest, ok := strings.CutPrefix(file, prefix)
		if !ok {
			continue
		}

		name, _, _ := strings.Cut(rest, "/")
		seen[name] = true
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// GetRepository returns Settings.
func (f *FakeRepo) GetRepository(context.Context) (control.RepositorySettings, error) {
	if err := f.Errors[ErrKeyRepository]; err != nil {
		return control.RepositorySettings{}, err
	}

	return f.Settings, nil
}

// ListRulesets returns Rulesets.
func (f *FakeRepo) ListRulesets(context.Context) ([]control.Ruleset, error) {
	if err := f.Errors[ErrKeyRulesets]; err != nil {
		return nil, err
	}

	return slices.Clone(f.Rulesets), nil
}

// GetCustomProperties returns Properties.
func (f *FakeRepo) GetCustomProperties(context.Context) (map[string]*string, error) {
	if err := f.Errors[ErrKeyProperties]; err != nil {
		return nil, err
	}

	return maps.Clone(f.Properties), nil
}

// OrgPropertySchema returns Schema.
func (f *FakeRepo) OrgPropertySchema(context.Context) ([]string, error) {
	if err := f.Errors[ErrKeySchema]; err != nil {
		return nil, err
	}

	return slices.Clone(f.Schema), nil
}

// ListLabels returns Labels.
func (f *FakeRepo) ListLabels(context.Context) ([]control.Label, error) {
	if err := f.Errors[ErrKeyLabels]; err != nil {
		return nil, err
	}

	return slices.Clone(f.Labels), nil
}

// GetVulnerabilityAlerts returns VulnerabilityAlerts.
func (f *FakeRepo) GetVulnerabilityAlerts(context.Context) (bool, error) {
	if err := f.Errors[ErrKeyVulnerabilityAlerts]; err != nil {
		return false, err
	}

	return f.VulnerabilityAlerts, nil
}

// Clone returns a deep enough copy of f that Apply never mutates f.
func (f *FakeRepo) Clone() *FakeRepo {
	out := *f
	out.Files = maps.Clone(f.Files)
	out.NotRegular = slices.Clone(f.NotRegular)
	out.Rulesets = slices.Clone(f.Rulesets)
	out.Properties = maps.Clone(f.Properties)
	out.Schema = slices.Clone(f.Schema)
	out.Labels = slices.Clone(f.Labels)
	out.Errors = maps.Clone(f.Errors)
	out.Settings.SecurityAndAnalysis = maps.Clone(f.Settings.SecurityAndAnalysis)

	return &out
}

// Apply returns repo with cs applied: file writes and deletes, then the
// API changes as an overlay. repo is not modified.
//
// API values by resource kind: setting takes the field's JSON value (a
// bool or a string) and nil clears it; property takes a *string or a
// string, and delete removes it; label takes a control.Label; ruleset
// takes a control.Ruleset, keyed by its name.
func Apply(repo *FakeRepo, cs control.ChangeSet) (*FakeRepo, error) { //nolint:gocritic // hugeParam: test harness
	out := repo.Clone()
	if out.Files == nil {
		out.Files = make(map[string]string)
	}

	for _, fc := range cs.Files {
		if fc.Delete {
			delete(out.Files, fc.Path)

			continue
		}

		out.Files[path.Clean(fc.Path)] = string(fc.Content)
	}

	for _, ch := range cs.API {
		if err := applyAPI(out, &ch); err != nil {
			return nil, fmt.Errorf("applying %s: %w", ch.Resource, err)
		}
	}

	return out, nil
}

func applyAPI(f *FakeRepo, ch *control.APIChange) error {
	switch ch.Resource.Kind {
	case control.KindSetting:
		return applySetting(f, ch)
	case control.KindProperty:
		return applyProperty(f, ch)
	case control.KindLabel:
		f.Labels = slices.DeleteFunc(f.Labels, func(l control.Label) bool { return strings.EqualFold(l.Name, ch.Resource.Key) })

		if ch.Op == control.OpDelete {
			return nil
		}

		l, ok := ch.Value.(control.Label)
		if !ok {
			return fmt.Errorf("label value is %T, want control.Label", ch.Value)
		}

		f.Labels = append(f.Labels, l)

		return nil
	case control.KindRuleset:
		f.Rulesets = slices.DeleteFunc(f.Rulesets, func(r control.Ruleset) bool { return r.Name == ch.Resource.Key })

		if ch.Op == control.OpDelete {
			return nil
		}

		rs, ok := ch.Value.(control.Ruleset)
		if !ok {
			return fmt.Errorf("ruleset value is %T, want control.Ruleset", ch.Value)
		}

		f.Rulesets = append(f.Rulesets, rs)

		return nil
	default:
		return fmt.Errorf("unsupported resource kind %q", ch.Resource.Kind)
	}
}

// applySetting sets one field of Settings by its JSON name, through a
// JSON round trip so the overlay needs no per-field code.
func applySetting(f *FakeRepo, ch *control.APIChange) error {
	if ch.Resource.Key == control.SettingVulnerabilityAlerts {
		enabled, ok := ch.Value.(bool)
		if !ok && ch.Op != control.OpDelete {
			return fmt.Errorf("vulnerability_alerts value is %T, want bool", ch.Value)
		}

		f.VulnerabilityAlerts = enabled

		return nil
	}

	b, err := json.Marshal(f.Settings)
	if err != nil {
		return err
	}

	fields := make(map[string]any)
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}

	if ch.Op == control.OpDelete || ch.Value == nil {
		delete(fields, ch.Resource.Key)
	} else {
		fields[ch.Resource.Key] = ch.Value
	}

	if b, err = json.Marshal(fields); err != nil {
		return err
	}

	var s control.RepositorySettings
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}

	f.Settings = s

	return nil
}

func applyProperty(f *FakeRepo, ch *control.APIChange) error {
	if f.Properties == nil {
		f.Properties = make(map[string]*string)
	}

	if ch.Op == control.OpDelete {
		delete(f.Properties, ch.Resource.Key)

		return nil
	}

	switch v := ch.Value.(type) {
	case nil:
		f.Properties[ch.Resource.Key] = nil
	case *string:
		f.Properties[ch.Resource.Key] = v
	case string:
		f.Properties[ch.Resource.Key] = &v
	default:
		return fmt.Errorf("property value is %T, want *string or string", ch.Value)
	}

	return nil
}
