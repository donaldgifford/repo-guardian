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

// repositoriesJoin finds a query (or branch) reading repositories.
var repositoriesJoin = regexp.MustCompile(`(?i)\b(FROM|JOIN) repositories\b`)

// TestAPIQueries_AreScoped fails on any queries_controls/api_*.sql query
// without the scope predicate (IMPL-0025 14.9), so a new endpoint cannot
// read past the caller's orgs by forgetting it.
func TestAPIQueries_AreScoped(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("queries_controls", "api_*.sql"))
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

			// A UNION ALL branch that joins repositories itself must
			// carry its own predicate: one scoped branch beside an
			// unscoped one passes the check above and reads past the
			// caller's orgs (IMPL-0030 7.16's held results).
			for i, branch := range strings.Split(body, "UNION ALL") {
				if repositoriesJoin.MatchString(branch) && !apiScopePredicate.MatchString(branch) {
					t.Errorf("%s: query %s, UNION ALL branch %d reads repositories without the scope predicate", f, name, i)
				}
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
