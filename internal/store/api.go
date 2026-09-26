package store

import (
	"slices"
	"strings"
	"time"
)

// APIScope is the set of orgs an API caller may see (DESIGN-0027 §
// Authorization). Unlike Scope, where no orgs means every org, the zero
// APIScope sees nothing: a principal that lost its groups must never
// read the fleet. Build one with ScopeAll or ScopeOrgs.
type APIScope struct {
	all  bool
	orgs []string
}

// ScopeAll sees every org.
func ScopeAll() APIScope {
	return APIScope{all: true, orgs: []string{}}
}

// ScopeOrgs sees exactly orgs, compared case-insensitively.
func ScopeOrgs(orgs ...string) APIScope {
	out := make([]string, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, strings.ToLower(o))
	}

	slices.Sort(out)

	return APIScope{orgs: slices.Compact(out)}
}

// All reports whether the scope sees every org.
func (s APIScope) All() bool { return s.all }

// Orgs returns the lower-cased orgs, never nil. It is empty for ScopeAll.
func (s APIScope) Orgs() []string {
	if s.orgs == nil {
		return []string{}
	}

	return slices.Clone(s.orgs)
}

// Empty reports whether the scope sees no org at all.
func (s APIScope) Empty() bool { return !s.all && len(s.orgs) == 0 }

// StatusCounts counts findings by status.
type StatusCounts struct {
	Compliant     int
	NonCompliant  int
	NotApplicable int
	Unknown       int
}

// ParkedCount is the number of inactive repositories with one reason.
type ParkedCount struct {
	Reason ParkReason
	Count  int
}

// Summary is the fleet summary over an APIScope.
type Summary struct {
	// Tracked counts active repositories.
	Tracked int
	Parked  []ParkedCount
	// Findings counts findings on active repositories.
	Findings StatusCounts
	// CompliantPercent is the shared compliance math; nil when nothing
	// is measured.
	CompliantPercent *float64
	// OpenPRs holds each open repo-guardian PR's created_at.
	OpenPRs []time.Time
}
