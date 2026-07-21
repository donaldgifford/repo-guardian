package catalog

import (
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	jiraAnnotationProps := map[string]string{
		"jira/project-key": "JiraProject",
		"jira/label":       "JiraLabel",
	}

	tests := []struct {
		name            string
		content         string
		annotationProps map[string]string
		want            *Properties
	}{
		{
			name: "mapped annotations present",
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
			annotationProps: jiraAnnotationProps,
			want: &Properties{
				Owner:     "donaldgifford",
				Component: "repo-guardian",
				Extra: map[string]string{
					"JiraProject": "DON",
					"JiraLabel":   "repo-guardian",
				},
			},
		},
		{
			name: "mapped annotations absent",
			content: `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
spec:
  owner: platform-team
`,
			annotationProps: jiraAnnotationProps,
			want: &Properties{
				Owner:     "platform-team",
				Component: "my-service",
			},
		},
		{
			name: "mapped annotation present but empty value is not populated",
			content: `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
  annotations:
    jira/project-key: ""
spec:
  owner: platform-team
`,
			annotationProps: jiraAnnotationProps,
			want: &Properties{
				Owner:     "platform-team",
				Component: "my-service",
			},
		},
		{
			name: "nil annotationProps yields Owner/Component only",
			content: `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
  annotations:
    jira/project-key: "DON"
spec:
  owner: platform-team
`,
			annotationProps: nil,
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
			annotationProps: jiraAnnotationProps,
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
			annotationProps: jiraAnnotationProps,
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
			annotationProps: jiraAnnotationProps,
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
			annotationProps: jiraAnnotationProps,
			want: &Properties{
				Owner:     DefaultOwner,
				Component: DefaultComponent,
			},
		},
		{
			name:            "malformed YAML",
			content:         `{{{`,
			annotationProps: jiraAnnotationProps,
			want: &Properties{
				Owner:     DefaultOwner,
				Component: DefaultComponent,
			},
		},
		{
			name:            "empty string",
			content:         "",
			annotationProps: jiraAnnotationProps,
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
			annotationProps: jiraAnnotationProps,
			want: &Properties{
				Owner:     DefaultOwner,
				Component: DefaultComponent,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Parse(tt.content, tt.annotationProps)

			if got.Owner != tt.want.Owner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.want.Owner)
			}

			if got.Component != tt.want.Component {
				t.Errorf("Component = %q, want %q", got.Component, tt.want.Component)
			}

			if len(got.Extra) != len(tt.want.Extra) {
				t.Errorf("Extra = %v, want %v", got.Extra, tt.want.Extra)
			}

			for k, v := range tt.want.Extra {
				if got.Extra[k] != v {
					t.Errorf("Extra[%q] = %q, want %q", k, got.Extra[k], v)
				}
			}
		})
	}
}

func TestParse_NilAnnotationPropsNeverAllocatesExtra(t *testing.T) {
	t.Parallel()

	content := `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
  annotations:
    jira/project-key: "DON"
spec:
  owner: platform-team
`

	got := Parse(content, nil)

	if got.Extra != nil {
		t.Errorf("Extra = %v, want nil", got.Extra)
	}
}
