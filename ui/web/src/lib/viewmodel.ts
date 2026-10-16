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

// FleetPoint is one snapshot time with the fleet's summed status counts
// and the percent derived from them.
export interface FleetPoint extends SeriesPoint {
  counts: Schemas["StatusCounts"];
}

// trendFrom is the start of a trend window of days ending now, at
// midnight UTC. It must not change between renders: the history query is
// keyed on it, and a fresh millisecond timestamp per render made every
// render a new query that fetched and re-rendered, a loop of about 400
// requests a minute (#207). Rounding to the day also lets the cache hit.
export function trendFrom(days: number, now: number = Date.now()): string {
  const d = new Date(now - days * 86_400_000);
  d.setUTCHours(0, 0, 0, 0);
  return d.toISOString();
}

// fleetSeries sums every (org, rule) point per snapshot_at into one
// fleet point, with the percent floored exactly as compliantPercent
// does — never an average of percentages, so a small org cannot skew
// it. complianceSeries is its percent-only projection.
export function fleetSeries(points: Schemas["HistoryPoint"][]): FleetPoint[] {
  const byTime = new Map<string, Schemas["StatusCounts"]>();
  for (const p of points) {
    const t = byTime.get(p.snapshot_at) ?? { compliant: 0, non_compliant: 0, not_applicable: 0, unknown: 0 };
    t.compliant += p.counts.compliant;
    t.non_compliant += p.counts.non_compliant;
    t.not_applicable += p.counts.not_applicable;
    t.unknown += p.counts.unknown;
    byTime.set(p.snapshot_at, t);
  }
  return [...byTime.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([at, counts]) => ({ at, counts, percent: compliantPercent(counts) }));
}

export function complianceSeries(points: Schemas["HistoryPoint"][]): SeriesPoint[] {
  return fleetSeries(points).map(({ at, percent }) => ({ at, percent }));
}

// seriesByOrg splits snapshot points into one series per org.
export function seriesByOrg(points: Schemas["HistoryPoint"][]): Map<string, SeriesPoint[]> {
  const orgs = new Map<string, Schemas["HistoryPoint"][]>();
  for (const p of points) {
    orgs.set(p.org, [...(orgs.get(p.org) ?? []), p]);
  }
  return new Map([...orgs.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([org, ps]) => [org, complianceSeries(ps)]));
}

// deltaFrom is the change between the last two measured points of a
// series, to one decimal; a gap at the range edge is skipped rather
// than read as a change to nothing. Fewer than two measured points is
// null ("no previous snapshot").
export function deltaFrom(series: readonly SeriesPoint[]): number | null {
  const measured = series.filter((p): p is SeriesPoint & { percent: number } => p.percent !== null);
  const last = measured[measured.length - 1];
  const prev = measured[measured.length - 2];
  if (!last || !prev) return null;
  return Math.round((last.percent - prev.percent) * 10) / 10;
}

// cadenceWord names the snapshot rhythm from the median gap between
// points: "daily" at a day or less, "every Nd" beyond, "—" with fewer
// than two points.
export function cadenceWord(series: readonly SeriesPoint[]): string {
  if (series.length < 2) return "—";
  const gaps: number[] = [];
  for (let i = 1; i < series.length; i++) {
    const a = series[i - 1]?.at;
    const b = series[i]?.at;
    if (a === undefined || b === undefined) continue;
    gaps.push((new Date(b).getTime() - new Date(a).getTime()) / 86_400_000);
  }
  gaps.sort((x, y) => x - y);
  const mid = Math.floor(gaps.length / 2);
  const upper = gaps[mid] ?? 0;
  const lower = gaps[mid - 1];
  const median = gaps.length % 2 === 1 || lower === undefined ? upper : (upper + lower) / 2;
  const days = Math.round(median);
  return days <= 1 ? "daily" : `every ${days}d`;
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

// orgCountLabel is the org-scope button's "All orgs (N)": N is every org
// the API returned for a caller who sees all of them, else /me's list.
export function orgCountLabel(me: Schemas["Me"], orgs: Schemas["OrgSummary"][] | undefined): string {
  const n = me.all_orgs ? (orgs ?? []).length : me.orgs.length;
  return `All orgs (${n})`;
}

// failingRuleCount is how many rules have any failing repository.
export function failingRuleCount(rules: readonly Pick<RuleCompliance, "counts">[]): number {
  return rules.filter((r) => r.counts.non_compliant > 0).length;
}

// ruleKinds is the sorted set of kinds present in a rule list: the
// Rules page's kind tabs come from data, not from a hard-coded list.
export function ruleKinds(rules: readonly Pick<RuleCompliance, "kind">[]): string[] {
  return [...new Set(rules.map((r) => r.kind))].sort((a, b) => a.localeCompare(b));
}

// FleetSeriesKey names the four series a fleet point carries; the
// compliance card draws exactly one at a time (DESIGN-0036 §Fleet).
export type FleetSeriesKey = "compliant" | "failing" | "not_applicable" | "unknown";

export interface FleetSeries {
  key: FleetSeriesKey;
  label: string;
  unit: "%" | "count";
  color: string;
  // goodUp says whether a rise is good, for the delta's tone.
  goodUp: boolean;
}

export const FLEET_SERIES: readonly FleetSeries[] = [
  { key: "compliant", label: "compliant %", unit: "%", color: "var(--color-accent)", goodUp: true },
  { key: "failing", label: "failing findings", unit: "count", color: "var(--color-bad)", goodUp: false },
  { key: "not_applicable", label: "not applicable", unit: "count", color: "var(--color-muted-foreground)", goodUp: false },
  { key: "unknown", label: "unknown", unit: "count", color: "var(--color-warn)", goodUp: false },
];

export function isFleetSeriesKey(v: unknown): v is FleetSeriesKey {
  return FLEET_SERIES.some((s) => s.key === v);
}

// fleetSeriesFor projects fleet points onto one series: the percent, or
// one of the summed counts, as SeriesPoints the chart can draw.
export function fleetSeriesFor(points: readonly FleetPoint[], key: FleetSeriesKey): SeriesPoint[] {
  return points.map((p) => {
    switch (key) {
      case "compliant":
        return { at: p.at, percent: p.percent };
      case "failing":
        return { at: p.at, percent: p.counts.non_compliant };
      case "not_applicable":
        return { at: p.at, percent: p.counts.not_applicable };
      case "unknown":
        return { at: p.at, percent: p.counts.unknown };
    }
  });
}

// latestValue is the last measured value of a series, null when none.
export function latestValue(series: readonly SeriesPoint[]): number | null {
  for (let i = series.length - 1; i >= 0; i--) {
    const v = series[i]?.percent;
    if (v !== null && v !== undefined) return v;
  }
  return null;
}

// deltaOver is the last measured value minus the newest measured value
// at least `days` before it: the "7d delta". Null without both.
export function deltaOver(series: readonly SeriesPoint[], days: number): number | null {
  const measured = series.filter((p): p is SeriesPoint & { percent: number } => p.percent !== null);
  const last = measured[measured.length - 1];
  if (!last) return null;
  const cutoff = new Date(last.at).getTime() - days * 86_400_000;
  const base = [...measured].reverse().find((p) => new Date(p.at).getTime() <= cutoff);
  if (!base) return null;
  return Math.round((last.percent - base.percent) * 10) / 10;
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
