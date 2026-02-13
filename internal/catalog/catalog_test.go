package catalog

import (
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    *Properties
	}{
		{
			name: "all fields present",
			content: `---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: repo-guardian
  title: Repo Guardian
  description: "Github App to automate repo onboarding and settings"
  annotations:
    backstage.io/code-coverage: enabled
    backstage.io/source-location: url:https://github.com/donaldgifford/repo-guardian
    backstage.io/techdocs-ref: "dir:."
    github.com/project-slug: "donaldgifford/repo-guardian"
    jira/project-key: "DON"
    jira/label: "repo-guardian"
  tags:
    - go
    - github
  namespace: default
spec:
  lifecycle: production
  type: service
  owner: donaldgifford
  system: infrastructure
`,
			want: &Properties{
				Owner:       "donaldgifford",
				Component:   "repo-guardian",
				JiraProject: "DON",
				JiraLabel:   "repo-guardian",
			},
		},
		{
			name: "missing jira annotations",
			content: `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
spec:
  owner: platform-team
`,
			want: &Properties{
				Owner:     "platform-team",
				Component: "my-service",
			},
		},
		{
			name: "empty spec.owner",
			content: `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
spec:
  owner: ""
`,
			want: &Properties{
				Owner:     DefaultOwner,
				Component: "my-service",
			},
		},
		{
			name: "empty metadata.name",
			content: `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: ""
spec:
  owner: some-team
`,
			want: &Properties{
				Owner:     "some-team",
				Component: DefaultComponent,
			},
		},
		{
			name: "wrong kind",
			content: `apiVersion: backstage.io/v1alpha1
kind: API
metadata:
  name: my-api
spec:
  owner: api-team
`,
			want: &Properties{
				Owner:     DefaultOwner,
				Component: DefaultComponent,
			},
		},
		{
			name: "wrong apiVersion",
			content: `apiVersion: v2
kind: Component
metadata:
  name: my-service
spec:
  owner: some-team
`,
			want: &Properties{
				Owner:     DefaultOwner,
				Component: DefaultComponent,
			},
		},
		{
			name:    "malformed YAML",
			content: `{{{`,
			want: &Properties{
				Owner:     DefaultOwner,
				Component: DefaultComponent,
			},
		},
		{
			name:    "empty string",
			content: "",
			want: &Properties{
				Owner:     DefaultOwner,
				Component: DefaultComponent,
			},
		},
		{
			name: "valid YAML but not backstage entity",
			content: `name: foo
version: 1.0
description: some random config
`,
			want: &Properties{
				Owner:     DefaultOwner,
				Component: DefaultComponent,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Parse(tt.content)

			if got.Owner != tt.want.Owner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.want.Owner)
			}
			if got.Component != tt.want.Component {
				t.Errorf("Component = %q, want %q", got.Component, tt.want.Component)
			}
			if got.JiraProject != tt.want.JiraProject {
				t.Errorf("JiraProject = %q, want %q", got.JiraProject, tt.want.JiraProject)
			}
			if got.JiraLabel != tt.want.JiraLabel {
				t.Errorf("JiraLabel = %q, want %q", got.JiraLabel, tt.want.JiraLabel)
			}
		})
	}
}
