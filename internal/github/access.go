package github

import (
	"errors"
	"net/http"

	gh "github.com/google/go-github/v68/github"
)

// IsAccessDenied reports whether err means the App cannot reach a
// repository at all, as opposed to a transient failure worth retrying.
//
// GitHub answers "you may not see this" with 404 rather than 403, so a
// repository outside the installation is indistinguishable from one that
// does not exist — and both are equally permanent from our side. 403
// covers the cases GitHub does name, such as an org IP allowlist.
//
// This is the terminal counterpart to AsThrottled: throttling means try
// again later, access denial means do not try again until discovery says
// otherwise. Callers MUST test AsThrottled first, because a secondary
// rate limit also surfaces as 403 and is emphatically not permanent.
func IsAccessDenied(err error) bool {
	if err == nil {
		return false
	}

	// A throttle is never an access denial, whatever its status code.
	if thr, throttled := AsThrottled(err); throttled && thr != nil {
		return false
	}

	var resp *gh.ErrorResponse
	if !errors.As(err, &resp) || resp.Response == nil {
		return false
	}

	switch resp.Response.StatusCode {
	case http.StatusNotFound, http.StatusForbidden:
		return true
	default:
		return false
	}
}
