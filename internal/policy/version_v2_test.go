package policy

import (
	"path/filepath"
	"strings"
	"testing"
)

// goldenVersionV2 pins the v2 version of testdata/version_v2. Changing
// it re-checks every repository in every fleet: update it only for a
// deliberate change to the hash input.
const goldenVersionV2 = "v2:14ae2e64b32cfd8e5f43f42b5d8d8f1b9f9a82563e6b07b30d10e47d87af97d4"

// The fixture's template names, spelled as the fixture spells them.
const (
	fixtureCodeowners = "codeowners"
	fixtureRenovate   = "renovate"
)

var versionV2Templates = map[string]string{
	fixtureCodeowners: "* @acme/platform\n",
	fixtureRenovate:   "{\"extends\": [\"config:recommended\"]}\n",
}

func loadVersionFixture(t *testing.T, name string) *PolicyConfig {
	t.Helper()

	cfg, err := Load(filepath.Join("testdata", "version_v2", name))
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}

	return cfg
}

func mustVersionV2(t *testing.T, cfg *PolicyConfig, templates map[string]string) string {
	t.Helper()

	v, err := VersionV2(cfg, templates)
	if err != nil {
		t.Fatalf("VersionV2: %v", err)
	}

	return v
}

func TestVersionV2_Golden(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"guardian.hcl", "guardian_reordered.hcl"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := mustVersionV2(t, loadVersionFixture(t, name), versionV2Templates)
			if got != goldenVersionV2 {
				t.Errorf("VersionV2 = %s, want golden %s", got, goldenVersionV2)
			}
		})
	}
}

func TestVersionV2_NeverEqualsV1(t *testing.T) {
	t.Parallel()

	cfg := loadVersionFixture(t, "guardian.hcl")

	v1, err := Version(cfg, versionV2Templates)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}

	if v2 := mustVersionV2(t, cfg, versionV2Templates); v1 == v2 || !strings.HasPrefix(v2, VersionV2Prefix) {
		t.Errorf("v1 %s, v2 %s: want distinct, v2-prefixed", v1, v2)
	}
}

func TestVersionV2_NilConfig(t *testing.T) {
	t.Parallel()

	if _, err := VersionV2(nil, nil); err == nil {
		t.Error("VersionV2(nil) succeeded")
	}
}
