package api

import (
	"encoding/json"
	"net/http"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

const problemContentType = "application/problem+json"

// problem is an RFC 9457 problem of type typ, about:blank when empty.
func problem(r *http.Request, status int, typ, detail string) gen.Problem {
	if typ == "" {
		typ = "about:blank"
	}

	p := gen.Problem{Type: typ, Title: http.StatusText(status), Status: status}
	if detail != "" {
		p.Detail = &detail
	}

	if id := requestIDFrom(r.Context()); id != "" {
		p.RequestId = &id
	}

	return p
}

// writeProblem writes an RFC 9457 problem response of type about:blank.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	writeTypedProblem(w, r, status, "", detail)
}

// writeTypedProblem writes an RFC 9457 problem response of type typ.
func writeTypedProblem(w http.ResponseWriter, r *http.Request, status int, typ, detail string) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(problem(r, status, typ, detail)); err != nil {
		// The status is already sent; the client sees a short body.
		return
	}
}
