// Package ghapi is rgctl's only route to GitHub. It builds a client from
// either GitHub App credentials (a JWT client for the App plus one
// installation client per org) or an operator token, and exposes one method
// per endpoint rgctl uses: search, pull request reads, commits, comments,
// close and branch deletion, and repository listing.
//
// Search calls are paced at least two seconds apart (30 per minute). A
// primary or secondary rate-limit error is retried once after the reset or
// Retry-After the API names, then returned.
package ghapi
