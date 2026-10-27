package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAuthzConfig_RejectsUnknownFieldsAndUndefinedGroups(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"unknown field":   "groups: {}\nrogue: 1\n",
		"undefined group": "groups: {a: [x]}\nclients: {bot: [b]}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "authz.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := LoadAuthzConfig(path); err == nil {
				t.Error("LoadAuthzConfig = nil error")
			}
		})
	}
}

func TestAuthzConfig_Scope(t *testing.T) {
	t.Parallel()

	cfg := &AuthzConfig{
		Groups:      map[string][]string{"web": {"Acme-Web"}, "docs": {"acme-docs", "acme-web"}, "admin": {"*"}},
		DefaultOrgs: []string{"acme-public"},
	}

	tests := []struct {
		groups  []string
		wantAll bool
		want    string
	}{
		{[]string{"web", "docs"}, false, "acme-docs,acme-web"},
		{[]string{"admin", "web"}, true, ""},
		{[]string{"nobody"}, false, "acme-public"},
		{nil, false, "acme-public"},
	}

	for _, tt := range tests {
		s := cfg.Scope(&Claims{Groups: tt.groups})
		if s.All() != tt.wantAll || strings.Join(s.Orgs(), ",") != tt.want {
			t.Errorf("Scope(%v) = all %v orgs %v, want all %v orgs %s", tt.groups, s.All(), s.Orgs(), tt.wantAll, tt.want)
		}
	}
}
