// Package catalog parses Backstage catalog-info.yaml files and extracts
// custom property values for GitHub repository metadata.
package catalog

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Default values for required custom properties when no catalog-info
// file exists or a valid Component entity omits the expected fields.
const (
	DefaultOwner     = "Unclassified"
	DefaultComponent = "Unclassified"
)

// ErrNotComponent reports that the input parsed as valid YAML but is
// not a Backstage Component entity. Callers must treat this as "not
// something we manage here" and skip, never as a statement that the
// desired property state is empty (INV-0011 A1, IMPL-0020 Decision 1).
var ErrNotComponent = errors.New("not a Backstage Component entity")

// Entity represents a Backstage catalog entity. Only the fields
// relevant to custom property extraction are included.
type Entity struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

// Metadata holds the metadata section of a Backstage entity.
type Metadata struct {
	Name        string            `yaml:"name"`
	Annotations map[string]string `yaml:"annotations"`
}

// Spec holds the spec section of a Backstage Component entity.
type Spec struct {
	Owner     string `yaml:"owner"`
	Lifecycle string `yaml:"lifecycle"`
	Type      string `yaml:"type"`
	System    string `yaml:"system"`
}

// Properties holds the extracted custom property values destined for
// GitHub repository custom properties.
type Properties struct {
	Owner     string
	Component string

	// Extra holds annotation-sourced property values keyed by GitHub
	// custom property name (the annotationProps argument's values to
	// Parse), not by annotation key. Populated only for annotations
	// that are present and non-empty in the entity's metadata.
	Extra map[string]string
}

// Parse unmarshals a catalog-info.yaml content string into an Entity
// and extracts custom property values. annotationProps maps a catalog
// annotation key (e.g. "jira/project-key") to the GitHub custom
// property name it should populate (e.g. "JiraProject"); a nil or
// empty map yields Owner/Component only.
//
// Parse distinguishes three outcomes:
//   - a valid Backstage Component entity returns its Properties;
//   - unparseable YAML returns a wrapped parse error;
//   - valid YAML that is not a Component entity returns an error
//     matching ErrNotComponent via errors.Is.
//
// Both error outcomes return nil Properties: an error is "I could not
// understand the input", never "the desired property state is empty".
func Parse(content string, annotationProps map[string]string) (*Properties, error) {
	var entity Entity
	if err := yaml.Unmarshal([]byte(content), &entity); err != nil {
		return nil, fmt.Errorf("parsing catalog-info: %w", err)
	}

	if entity.APIVersion != "backstage.io/v1alpha1" || entity.Kind != "Component" {
		return nil, fmt.Errorf("%w: apiVersion=%q kind=%q", ErrNotComponent, entity.APIVersion, entity.Kind)
	}

	p := &Properties{
		Owner:     entity.Spec.Owner,
		Component: entity.Metadata.Name,
	}

	if p.Owner == "" {
		p.Owner = DefaultOwner
	}

	if p.Component == "" {
		p.Component = DefaultComponent
	}

	for annotation, property := range annotationProps {
		value := entity.Metadata.Annotations[annotation]
		if value == "" {
			continue
		}

		if p.Extra == nil {
			p.Extra = make(map[string]string, len(annotationProps))
		}

		p.Extra[property] = value
	}

	return p, nil
}

// Defaults returns the Properties used when a repository has no
// catalog-info file at all: Owner and Component set to "Unclassified"
// and no Extra values. This is a positive desired state (the repo is
// unclassified until a catalog-info.yaml is added), distinct from the
// Parse error outcomes, which must never be synced.
func Defaults() *Properties {
	return &Properties{
		Owner:     DefaultOwner,
		Component: DefaultComponent,
	}
}
