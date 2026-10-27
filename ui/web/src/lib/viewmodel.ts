import type { Schemas } from "../api/client";

export interface SeriesPoint {
  at: string;
  percent: number | null;
}

// heatOpacity shades a grid cell by its share of the row's tracked
// count: nothing for zero, then 8% to 50% so the column reads as a heat
// map without drowning the number (DESIGN-0036 §Orgs).
export function heatOpacity(count: number, total: number): number {
  if (count <= 0 || total <= 0) return 0;
  return Math.round(8 + 42 * Math.min(1, count / total)) / 100;
}

// initials is the avatar text: first letters of the first two words of
// the name, else the first two characters of the subject.
export function initials(name: string, subject: string): string {
  const fromName = name
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0] ?? "")
    .join("");
  return (fromName || subject.slice(0, 2)).toUpperCase();
}

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
