package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// apiScopePredicate is the one scoping shape an API query may use
// (DESIGN-0027 § Authorization): filtering happens in SQL, never in Go.
var apiScopePredicate = regexp.MustCompile(
	`\(@scope_all::bool OR lower\((\w+\.)?org\) = ANY \(@scope_orgs::text\[\]\)\)`)

var queryName = regexp.MustCompile(`(?m)^-- name: (\w+)`)

// TestAPIQueries_AreScoped fails on any api_*.sql query without the
// scope predicate (IMPL-0025 14.9), so a new endpoint cannot read past
// the caller's orgs by forgetting it.
func TestAPIQueries_AreScoped(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("queries", "api_*.sql"))
	if err != nil {
		t.Fatal(err)
	}

	if len(files) == 0 {
		t.Fatal("no api_*.sql files; the lint would pass vacuously")
	}

	checked := 0

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		for name, body := range splitQueries(string(b)) {
			checked++

			if !apiScopePredicate.MatchString(body) {
				t.Errorf("%s: query %s is not scoped: it must filter with (@scope_all::bool OR lower(org) = ANY (@scope_orgs::text[]))",
					f, name)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no queries found in api_*.sql")
	}
}

// splitQueries maps each sqlc query name to its text.
func splitQueries(src string) map[string]string {
	out := map[string]string{}
	locs := queryName.FindAllStringSubmatchIndex(src, -1)

	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}

		out[src[loc[2]:loc[3]]] = strings.TrimSpace(src[loc[1]:end])
	}

	return out
}
