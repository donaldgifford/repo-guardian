import { parseRangeSearch, type RangeSearch } from "./range";

// OrgSearch is the `?org=` part of a route's search: the header org
// scope (DESIGN-0037 OQ3), one org or every visible one.
export interface OrgSearch {
  org?: string;
}

export function parseOrgSearch(s: Record<string, unknown>): OrgSearch {
  return typeof s.org === "string" && s.org !== "" ? { org: s.org } : {};
}

// ScopeSearch is what every page with the header scope and range
// validates: `?org=` and `?days=`, both retained across pages.
export type ScopeSearch = OrgSearch & RangeSearch;

export function parseScopeSearch(s: Record<string, unknown>): ScopeSearch {
  return { ...parseOrgSearch(s), ...parseRangeSearch(s) };
}
