package policy

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

const testOrgPolicy = `org "test-org" {
  mode = "remediate"

  controls = ["branch@1"]

  replace "codeowners@1" {
    with   = "codeowners@2"
    reason = "trial"
  }

  exclude "deps@1" {
    reason = "in-house updater"
  }

  control "codeowners@2" {
    mode = "evaluate"
  }

  exclude_repos {
    match  = ["sandbox-*"]
    reason = "throwaway"
  }

  repos "legacy" {
    match = ["legacy-api", "legacy-web"]
    exclude "deps@1" {
      reason = "decommissioning"
    }
    mode = "evaluate"
  }

  remediation {
    max_open_prs = 5
  }
}
`

const testEnterprise = `enterprise {
  orgs     = ["test-org", "other"]
  mode     = "evaluate"
  controls = ["codeowners@1", "deps@1"]

  remediation {
    max_open_prs = 3
    reopen_after = "336h"
  }
}
`

func TestDecode_EnterpriseAndOrg(t *testing.T) {
	t.Parallel()

	snap := mustLoad(t, map[string]string{"enterprise.hcl": testEnterprise, "orgs/test-org.hcl": testOrgPolicy})

	ent := snap.enterprise
	if len(ent.orgs) != 2 || ent.mode.mode != ModeEvaluate || len(ent.controls) != 2 || *ent.remed.maxOpenPRs != 3 ||
		*ent.remed.reopenAfter != "336h" {
		t.Errorf("enterprise = %+v", ent)
	}

	if len(snap.orgs) != 1 {
		t.Fatalf("orgs = %d, want 1", len(snap.orgs))
	}

	org := snap.orgs[0]
	if org.name != "test-org" || org.layer.mode.mode != ModeRemediate || *org.remed.maxOpenPRs != 5 || org.remed.reopenAfter != nil {
		t.Errorf("org = %+v", org)
	}

	kinds := make([]opKind, 0, len(org.layer.ops))
	for _, o := range org.layer.ops {
		kinds = append(kinds, o.kind)
	}

	if want := []opKind{opAdd, opReplace, opExclude}; !equalKinds(kinds, want) {
		t.Errorf("op kinds = %v, want %v (file order)", kinds, want)
	}

	rep := org.layer.ops[1]
	if rep.id != (control.ID{Slug: "codeowners", Version: 1}) || rep.with != (control.ID{Slug: "codeowners", Version: 2}) || rep.reason != "trial" {
		t.Errorf("replace = %+v", rep)
	}

	if len(org.layer.overrides) != 1 || org.layer.overrides[0].mode.mode != ModeEvaluate {
		t.Errorf("overrides = %+v", org.layer.overrides)
	}

	if len(org.excludeRepos) != 1 || org.excludeRepos[0].reason != "throwaway" {
		t.Errorf("exclude_repos = %+v", org.excludeRepos)
	}

	if len(org.repos) != 1 || org.repos[0].label != "legacy" || len(org.repos[0].match) != 2 ||
		org.repos[0].layer.mode.mode != ModeEvaluate || len(org.repos[0].layer.ops) != 1 {
		t.Errorf("repos = %+v", org.repos)
	}
}

func equalKinds(a, b []opKind) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestDecode_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			"enterprise exclusion",
			map[string]string{"enterprise.hcl": "enterprise {\n  orgs = []\n  exclude \"x@1\" {\n    reason = \"r\"\n  }\n}\n"},
			"enterprise.hcl:3: the enterprise policy is the baseline and has no exclusions or variations: exclude block not allowed",
		},
		{
			"two enterprise blocks",
			map[string]string{"enterprise.hcl": minimalEnterprise + minimalEnterprise},
			"want exactly one enterprise block, found 2",
		},
		{
			"org not onboarded",
			map[string]string{"enterprise.hcl": minimalEnterprise, "orgs/x.hcl": "org \"stray\" {}\n"},
			`orgs/x.hcl:1: org "stray" is not in enterprise.orgs (enterprise.hcl:2)`,
		},
		{
			"duplicate org",
			map[string]string{"enterprise.hcl": minimalEnterprise, "orgs/a.hcl": "org \"acme\" {}\n", "orgs/b.hcl": "org \"ACME\" {}\n"},
			`orgs/b.hcl:1: a second policy for org "ACME" (first at orgs/a.hcl:1)`,
		},
		{
			"repos without label",
			map[string]string{"enterprise.hcl": minimalEnterprise, "orgs/a.hcl": "org \"acme\" {\n  repos {\n    match = [\"x\"]\n  }\n}\n"},
			"orgs/a.hcl:2",
		},
		{
			"bad reference",
			map[string]string{"enterprise.hcl": "enterprise {\n  orgs = []\n  controls = [\"codeowners\"]\n}\n"},
			`enterprise.hcl:3: control id "codeowners": want slug@version`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(writeRoot(t, tt.files), testRegistry(), discard())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
