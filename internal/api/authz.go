package api

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// allOrgs is the authz wildcard: a group mapped to it sees every org.
const allOrgs = "*"

// AuthzConfig maps identities to visible orgs (DESIGN-0027 §
// Authorization). It is the API_AUTHZ_CONFIG file, read once at
// startup; a checksum annotation rolls the pods on change (OQ26).
//
//	groups:
//	  platform-engineering: ["*"]
//	  team-web: ["acme-web", "acme-docs"]
//	defaultOrgs: []
//	clients:
//	  ci-dashboard: ["platform-engineering"]
type AuthzConfig struct {
	// Groups maps a group claim value to orgs; "*" is every org.
	Groups map[string][]string `yaml:"groups"`
	// DefaultOrgs applies when none of the caller's groups match.
	DefaultOrgs []string `yaml:"defaultOrgs"`
	// Clients maps a machine client's azp to groups, for IdPs that put
	// no groups on client-credentials tokens (OQ22).
	Clients map[string][]string `yaml:"clients"`
}

// LoadAuthzConfig reads and validates the authz file at path.
func LoadAuthzConfig(path string) (*AuthzConfig, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the path is operator configuration (API_AUTHZ_CONFIG)
	if err != nil {
		return nil, fmt.Errorf("read authz config: %w", err)
	}

	var cfg AuthzConfig

	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)

	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse authz config %s: %w", path, err)
	}

	for azp, groups := range cfg.Clients {
		for _, g := range groups {
			if _, ok := cfg.Groups[g]; !ok {
				return nil, fmt.Errorf("authz config %s: client %q maps to undefined group %q", path, azp, g)
			}
		}
	}

	return &cfg, nil
}

// Scope resolves the orgs claims may see: the union over the caller's
// groups (plus its client's mapped groups), or DefaultOrgs when none
// match.
func (c *AuthzConfig) Scope(claims *Claims) store.APIScope {
	groups := append([]string{}, claims.Groups...)
	if claims.Client != "" {
		groups = append(groups, c.Clients[claims.Client]...)
	}

	var (
		orgs    []string
		matched bool
	)

	for _, g := range groups {
		mapped, ok := c.Groups[g]
		if !ok {
			continue
		}

		matched = true

		for _, o := range mapped {
			if o == allOrgs {
				return store.ScopeAll()
			}

			orgs = append(orgs, o)
		}
	}

	if !matched {
		for _, o := range c.DefaultOrgs {
			if o == allOrgs {
				return store.ScopeAll()
			}
		}

		return store.ScopeOrgs(c.DefaultOrgs...)
	}

	return store.ScopeOrgs(orgs...)
}
