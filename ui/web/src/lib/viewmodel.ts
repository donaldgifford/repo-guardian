import type { Schemas } from "../api/client";

// hasNoAccess is true when the caller may see nothing: the "ask for
// access" page instead of a wall of 403s.
export function hasNoAccess(me: Schemas["Me"]): boolean {
  return !me.all_orgs && me.orgs.length === 0;
}

// orgCountLabel is the org-scope button's "All orgs (N)": N is every org
// the API returned for a caller who sees all of them, else /me's list.
export function orgCountLabel(me: Schemas["Me"], orgs: Schemas["OrgSummary"][] | undefined): string {
  const n = me.all_orgs ? (orgs ?? []).length : me.orgs.length;
  return `All orgs (${n})`;
}

// visibleOrgs is the org list for the scope: /me's orgs, or every org
// /orgs returned when the caller sees all of them.
export function visibleOrgs(me: Schemas["Me"], all: Schemas["OrgSummary"][] | undefined): string[] {
  const orgs = me.all_orgs ? (all ?? []).map((o) => o.org) : me.orgs;
  return [...new Set(orgs)].sort((a, b) => a.localeCompare(b));
}
