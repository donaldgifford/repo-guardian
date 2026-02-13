// Package catalog parses Backstage catalog-info.yaml files and extracts
// custom property values for GitHub repository metadata.
package catalog

import "gopkg.in/yaml.v3"

// Default values for required custom properties when catalog-info.yaml
// is missing, unparseable, or does not contain the expected fields.
const (
	DefaultOwner     = "Unclassified"
	DefaultComponent = "Unclassified"
)

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
	Owner       string
	Component   string
	JiraProject string
	JiraLabel   string
}

// Parse unmarshals a catalog-info.yaml content string into an Entity
// and extracts custom property values. Returns default Properties
// (Owner and Component set to "Unclassified") if the content cannot
// be parsed or is not a Backstage Component entity.
func Parse(content string) *Properties {
	var entity Entity
	if err := yaml.Unmarshal([]byte(content), &entity); err != nil {
		return defaults()
	}

	if entity.APIVersion != "backstage.io/v1alpha1" || entity.Kind != "Component" {
		return defaults()
	}

	p := &Properties{
		Owner:       entity.Spec.Owner,
		Component:   entity.Metadata.Name,
		JiraProject: entity.Metadata.Annotations["jira/project-key"],
		JiraLabel:   entity.Metadata.Annotations["jira/label"],
	}

	if p.Owner == "" {
		p.Owner = DefaultOwner
	}

	if p.Component == "" {
		p.Component = DefaultComponent
	}

	return p
}

func defaults() *Properties {
	return &Properties{
		Owner:     DefaultOwner,
		Component: DefaultComponent,
	}
}
