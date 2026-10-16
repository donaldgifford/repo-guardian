package policy

import "testing"

// testCatalogue defines the controls the policy tests reference. The
// codeowners versions own the same file; legacy_codeowners owns it too,
// under another slug, for ownership conflicts.
const testCatalogue = `
control "codeowners" {
  version = 1
  type    = "testfile"
  paths   = ["CODEOWNERS"]
  rule "exists" {}
}

control "codeowners" {
  version = 2
  type    = "testfile"
  paths   = ["CODEOWNERS"]
  rule "exists" {}
}

control "legacy_codeowners" {
  version = 1
  type    = "testfile"
  paths   = ["CODEOWNERS"]
  rule "exists" {}
}

control "deps" {
  version = 1
  type    = "testfile"
  paths   = ["renovate.json"]
  rule "exists" {}
}

control "catalog" {
  version = 1
  type    = "testfile"
  paths   = ["catalog-info.yaml"]
  rule "exists" {}
}

control "branch" {
  version = 1
  type    = "testsetting"
  rule "protected" {
    kind     = "equals"
    property = "branch_protection"
    value    = true
  }
}

control "wiki" {
  version = 1
  type    = "testsetting"
  rule "off" {
    kind     = "equals"
    property = "has_wiki"
    value    = false
  }
}
`

// withCatalogue adds the test catalogue to files.
func withCatalogue(files map[string]string) map[string]string {
	out := map[string]string{"catalogue/test.hcl": testCatalogue}
	for k, v := range files {
		out[k] = v
	}

	return out
}

// mustLoad loads files plus the test catalogue.
func mustLoad(t *testing.T, files map[string]string) *Snapshot {
	t.Helper()

	snap, err := Load(writeRoot(t, withCatalogue(files)), testRegistry(), discard())
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	return snap
}
