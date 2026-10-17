// Package builtin lists the control types repo-guardian ships
// (DESIGN-0031 § Built-in types) and builds a registry holding them. It
// is the one package that imports every type, so a type is added here and
// nowhere else.
package builtin

import (
	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/controls/branch_ruleset"
	"github.com/donaldgifford/repo-guardian/internal/controls/catalog_info"
	"github.com/donaldgifford/repo-guardian/internal/controls/codeowners"
	"github.com/donaldgifford/repo-guardian/internal/controls/custom_properties"
	"github.com/donaldgifford/repo-guardian/internal/controls/dependency_updates"
	"github.com/donaldgifford/repo-guardian/internal/controls/file"
	"github.com/donaldgifford/repo-guardian/internal/controls/labels"
	"github.com/donaldgifford/repo-guardian/internal/controls/repo_settings"
)

// Types returns every built-in control type, in name order.
func Types() []control.Type {
	return []control.Type{
		branch_ruleset.Type{},
		catalog_info.Type{},
		codeowners.Type{},
		custom_properties.Type{},
		dependency_updates.Type{},
		file.Type{},
		labels.Type{},
		repo_settings.Type{},
	}
}

// Registry returns a registry holding every built-in type.
func Registry() *control.Registry {
	reg := control.NewRegistry()

	for _, t := range Types() {
		if err := reg.Register(t); err != nil {
			panic(err) // two built-ins share a name: a programming error
		}
	}

	return reg
}
