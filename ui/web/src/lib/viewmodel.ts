import type { Schemas } from "../api/client";
import { compliantPercent } from "./format";

type RuleCompliance = Schemas["RuleCompliance"];

// worstRules orders rules by compliance, lowest first; unmeasured rules
// sort last because they have nothing failing to show.
export function worstRules(rules: RuleCompliance[], n: number): RuleCompliance[] {
  return [...rules]
    .sort((a, b) => {
      if (a.compliant_percent === b.compliant_percent) return a.name.localeCompare(b.name);
      if (a.compliant_percent === null) return 1;
      if (b.compliant_percent === null) return -1;
      return a.compliant_percent - b.compliant_percent;
    })
    .slice(0, n);
}

export interface SeriesPoint {
  at: string;
  percent: number | null;
}

// complianceSeries folds (org, rule) snapshot points into one series:
// counts are summed per snapshot time and the percentage recomputed
// with the API's floor, never averaged, so a small org cannot skew it.
export function complianceSeries(points: Schemas["HistoryPoint"][]): SeriesPoint[] {
  const byTime = new Map<string, { compliant: number; non_compliant: number }>();
  for (const p of points) {
    const t = byTime.get(p.snapshot_at) ?? { compliant: 0, non_compliant: 0 };
    t.compliant += p.counts.compliant;
    t.non_compliant += p.counts.non_compliant;
    byTime.set(p.snapshot_at, t);
  }
  return [...byTime.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([at, c]) => ({ at, percent: compliantPercent(c) }));
}

// seriesByOrg splits snapshot points into one series per org.
export function seriesByOrg(points: Schemas["HistoryPoint"][]): Map<string, SeriesPoint[]> {
  const orgs = new Map<string, Schemas["HistoryPoint"][]>();
  for (const p of points) {
    orgs.set(p.org, [...(orgs.get(p.org) ?? []), p]);
  }
  return new Map([...orgs.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([org, ps]) => [org, complianceSeries(ps)]));
}

// parkedTotal sums parked repositories across reasons.
export function parkedTotal(parked: Schemas["ParkedCount"][]): number {
  return parked.reduce((n, p) => n + p.count, 0);
}

// visibleOrgs is the org list for the filter: /me's orgs, or every org
// /orgs returned when the caller sees all of them.
export function visibleOrgs(me: Schemas["Me"], all: Schemas["OrgSummary"][] | undefined): string[] {
  const orgs = me.all_orgs ? (all ?? []).map((o) => o.org) : me.orgs;
  return [...new Set(orgs)].sort((a, b) => a.localeCompare(b));
}

// hasNoAccess is true when the caller may see nothing: the "ask for
// access" page instead of a wall of 403s.
export function hasNoAccess(me: Schemas["Me"]): boolean {
  return !me.all_orgs && me.orgs.length === 0;
}
