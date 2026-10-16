package policy

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// EvaluateYAMLPath evaluates a dot-separated path expression against YAML
// content and returns all matching values as strings.
//
// Supported path syntax:
//   - "spec.owner" — nested map access
//   - "metadata.annotations.jira/project-key" — keys with slashes
//   - "updates[*].package-ecosystem" — array wildcard
//
// Returns an empty slice (not an error) for paths that don't match.
func EvaluateYAMLPath(content, path string) ([]string, error) {
	var root yaml.Node

	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	if root.Kind == 0 {
		return nil, nil
	}

	segments, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	// The root node is a document node wrapping the actual content.
	var startNode *yaml.Node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		startNode = root.Content[0]
	} else {
		startNode = &root
	}

	return evaluateSegments(startNode, segments), nil
}

// pathSegment represents one part of a YAML path expression.
type pathSegment struct {
	key      string // map key to access
	wildcard bool   // true if this segment is [*]
}

// parsePath splits a path expression into segments.
// Dots separate segments, but slashes within keys are preserved.
// "[*]" suffix on a segment indicates array wildcard.
func parsePath(path string) ([]pathSegment, error) {
	if path == "" {
		return nil, fmt.Errorf("empty YAML path")
	}

	parts := splitPath(path)
	segments := make([]pathSegment, 0, len(parts))

	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("invalid YAML path %q: empty segment", path)
		}

		if key, found := strings.CutSuffix(part, "[*]"); found {
			if key == "" {
				return nil, fmt.Errorf("invalid YAML path %q: [*] without key", path)
			}

			segments = append(segments, pathSegment{key: key}, pathSegment{wildcard: true})
		} else {
			segments = append(segments, pathSegment{key: part})
		}
	}

	return segments, nil
}

// splitPath splits a path by dots, but preserves segments that contain
// slashes (e.g., "jira/project-key" stays together).
func splitPath(path string) []string {
	return strings.Split(path, ".")
}

// evaluateSegments recursively walks the YAML node tree following the
// path segments and collects matching leaf values.
func evaluateSegments(node *yaml.Node, segments []pathSegment) []string {
	if node == nil || len(segments) == 0 {
		return nil
	}

	seg := segments[0]
	rest := segments[1:]

	if seg.wildcard {
		return evaluateWildcard(node, rest)
	}

	child := findMapValue(node, seg.key)
	if child == nil {
		return nil
	}

	if len(rest) == 0 {
		return nodeToStrings(child)
	}

	return evaluateSegments(child, rest)
}

// evaluateWildcard iterates over a sequence node and evaluates remaining
// segments against each element.
func evaluateWildcard(node *yaml.Node, rest []pathSegment) []string {
	if node.Kind != yaml.SequenceNode {
		return nil
	}

	var results []string

	for _, item := range node.Content {
		if len(rest) == 0 {
			results = append(results, nodeToStrings(item)...)
		} else {
			results = append(results, evaluateSegments(item, rest)...)
		}
	}

	return results
}

// findMapValue looks up a key in a mapping node.
func findMapValue(node *yaml.Node, key string) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

// nodeToStrings converts a YAML node to string values.
// Scalar nodes return their value. Sequence nodes return all scalar values.
func nodeToStrings(node *yaml.Node) []string {
	switch node.Kind {
	case yaml.ScalarNode:
		return []string{node.Value}
	case yaml.SequenceNode:
		var results []string

		for _, item := range node.Content {
			if item.Kind == yaml.ScalarNode {
				results = append(results, item.Value)
			}
		}

		return results
	default:
		return nil
	}
}
