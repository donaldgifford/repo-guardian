package api

import (
	"encoding/json"
	"net/http"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

const problemContentType = "application/problem+json"

// problem is an RFC 9457 problem with type about:blank.
func problem(r *http.Request, status int, detail string) gen.Problem {
	p := gen.Problem{Type: "about:blank", Title: http.StatusText(status), Status: status}
	if detail != "" {
		p.Detail = &detail
	}

	if id := requestIDFrom(r.Context()); id != "" {
		p.RequestId = &id
	}

	return p
}

// writeProblem writes an RFC 9457 problem response.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(problem(r, status, detail)); err != nil {
		// The status is already sent; the client sees a short body.
		return
	}
}
