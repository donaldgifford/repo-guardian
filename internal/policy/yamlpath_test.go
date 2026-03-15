package policy

import (
	"testing"
)

func TestEvaluateYAMLPath(t *testing.T) {
	catalogInfo := `
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: my-service
  annotations:
    jira/project-key: PROJ
    jira/label: backend
spec:
  type: service
  owner: team-platform
  lifecycle: production
`

	dependabotYAML := `
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
  - package-ecosystem: docker
    directory: /
    schedule:
      interval: monthly
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
`

	tests := []struct {
		name    string
		content string
		path    string
		want    []string
		wantErr bool
	}{
		{
			name:    "simple dot path",
			content: catalogInfo,
			path:    "spec.owner",
			want:    []string{"team-platform"},
		},
		{
			name:    "nested dot path",
			content: catalogInfo,
			path:    "metadata.name",
			want:    []string{"my-service"},
		},
		{
			name:    "key with slash",
			content: catalogInfo,
			path:    "metadata.annotations.jira/project-key",
			want:    []string{"PROJ"},
		},
		{
			name:    "another key with slash",
			content: catalogInfo,
			path:    "metadata.annotations.jira/label",
			want:    []string{"backend"},
		},
		{
			name:    "array wildcard",
			content: dependabotYAML,
			path:    "updates[*].package-ecosystem",
			want:    []string{"gomod", "docker", "github-actions"},
		},
		{
			name:    "top-level key",
			content: catalogInfo,
			path:    "kind",
			want:    []string{"Component"},
		},
		{
			name:    "non-existent path returns empty",
			content: catalogInfo,
			path:    "spec.nonexistent",
			want:    nil,
		},
		{
			name:    "non-existent nested path returns empty",
			content: catalogInfo,
			path:    "a.b.c.d",
			want:    nil,
		},
		{
			name:    "invalid YAML",
			content: "key: [invalid\n  broken: yaml",
			path:    "spec.owner",
			wantErr: true,
		},
		{
			name:    "empty path",
			content: catalogInfo,
			path:    "",
			wantErr: true,
		},
		{
			name:    "empty segment in path",
			content: catalogInfo,
			path:    "spec..owner",
			wantErr: true,
		},
		{
			name:    "wildcard without key",
			content: dependabotYAML,
			path:    "[*].foo",
			wantErr: true,
		},
		{
			name:    "empty content",
			content: "",
			path:    "spec.owner",
			want:    nil,
		},
		{
			name:    "scalar at leaf of array wildcard path",
			content: dependabotYAML,
			path:    "updates[*].directory",
			want:    []string{"/", "/", "/"},
		},
		{
			name:    "version as string",
			content: dependabotYAML,
			path:    "version",
			want:    []string{"2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateYAMLPath(tt.content, tt.path)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !stringSliceEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}

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
