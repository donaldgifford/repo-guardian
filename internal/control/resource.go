package control

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// errEmptyKey is a resource with no key.
var errEmptyKey = errors.New("empty key")

// Canonical returns the canonical resource for kind and key (DESIGN-0030
// AR-0030-02). Keys are canonicalized here, never by a type, so two
// controls naming one file the same way in different spellings collide on
// ownership instead of both owning it:
//
//   - file: rejected when absolute, when a segment is "..", when a segment
//     is empty, or when the key starts with "./"; otherwise path.Clean,
//     then compared byte for byte as GitHub does;
//   - label: lower-cased, because GitHub compares labels
//     case-insensitively;
//   - setting, ruleset, property, tree: compared exactly.
//
// An unknown kind is an error.
func Canonical(kind, key string) (Resource, error) {
	if key == "" {
		return Resource{}, fmt.Errorf("%s resource: %w", kind, errEmptyKey)
	}

	switch kind {
	case KindFile:
		clean, err := canonicalFile(key)
		if err != nil {
			return Resource{}, fmt.Errorf("file resource %q: %w", key, err)
		}

		return Resource{Kind: kind, Key: clean}, nil
	case KindLabel:
		return Resource{Kind: kind, Key: strings.ToLower(key)}, nil
	case KindSetting, KindRuleset, KindProperty, KindTree:
		return Resource{Kind: kind, Key: key}, nil
	default:
		return Resource{}, fmt.Errorf("resource %q: unknown kind %q", key, kind)
	}
}

// MustCanonical is Canonical for keys fixed in code; it panics on an
// invalid one.
func MustCanonical(kind, key string) Resource {
	r, err := Canonical(kind, key)
	if err != nil {
		panic(err)
	}

	return r
}

func canonicalFile(key string) (string, error) {
	switch {
	case strings.HasPrefix(key, "/"):
		return "", errors.New("absolute path")
	case strings.HasPrefix(key, "./"):
		return "", errors.New(`starts with "./"`)
	}

	for seg := range strings.SplitSeq(key, "/") {
		switch seg {
		case "":
			return "", errors.New("empty path segment")
		case "..":
			return "", errors.New(`".." segment`)
		}
	}

	clean := path.Clean(key)
	if clean == "." {
		return "", errors.New("names the repository root")
	}

	return clean, nil
}
