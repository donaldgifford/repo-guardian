import type { Schemas } from "../api/client";
import type { Delta } from "../components/ui/kpi";
import { trend } from "./format";
import { historySeries, type Posture } from "./posture";
import type { ScopeSearch } from "./scope";
import type { SeriesPoint } from "./viewmodel";

// The fleet compliance card's series pills (DESIGN-0037 §Fleet panels
// 1): what the area chart draws from aggregate=fleet history.
export type FleetSeries = "percent" | "non_compliant" | "not_applicable" | "unknown";

export const FLEET_SERIES: readonly FleetSeries[] = ["percent", "non_compliant", "not_applicable", "unknown"];

export const DEFAULT_SERIES: FleetSeries = "percent";

export interface SeriesSpec {
  label: string;
  unit: "%" | "count";
  color: string;
  pick: (p: Posture) => number | null;
}

export const SERIES: Record<FleetSeries, SeriesSpec> = {
  percent: { label: "compliance %", unit: "%", color: "var(--color-accent)", pick: (p) => p.percent },
  non_compliant: { label: "findings", unit: "count", color: "var(--color-bad)", pick: (p) => p.non_compliant },
  not_applicable: { label: "not applicable", unit: "count", color: "var(--color-muted-foreground)", pick: (p) => p.not_applicable },
  unknown: { label: "unknown", unit: "count", color: "var(--color-warn)", pick: (p) => p.unknown },
};

export function isFleetSeries(v: unknown): v is FleetSeries {
  return typeof v === "string" && (FLEET_SERIES as readonly string[]).includes(v);
}

export interface FleetSearch extends ScopeSearch {
  series?: FleetSeries;
}

// parseFleetSearch keeps `?series=` beside the scope; the default pill
// is not written.
export function parseFleetSearch(s: Record<string, unknown>): FleetSearch {
  const out: FleetSearch = {};
  if (typeof s.org === "string" && s.org !== "") out.org = s.org;
  if (typeof s.days === "number" && [1, 7, 30, 90].includes(s.days)) out.days = s.days as 1 | 7 | 30 | 90;
  if (isFleetSeries(s.series) && s.series !== DEFAULT_SERIES) out.series = s.series;
  return out;
}

// rangeDelta is the move over the range: the last measured snapshot
// against the first, in points; null with fewer than two measured
// points, which renders no delta.
export function rangeDelta(series: readonly SeriesPoint[]): Delta | null {
  const measured = series.filter((p) => p.percent !== null);
  const first = measured[0];
  const last = measured[measured.length - 1];
  if (!first || !last || first === last) return null;
  const value = trend(last.percent, first.percent);
  return value === null ? null : { value, goodUp: true, unit: " pts" };
}

// pickSeries draws one of the pills from the fleet history: each day's
// rows summed into a posture, then the pill's field.
export function pickSeries(points: readonly Schemas["HistoryPoint"][], series: FleetSeries): SeriesPoint[] {
  if (series === "percent") return historySeries(points);
  const spec = SERIES[series];
  const byAt = new Map<string, number>();
  for (const p of points) byAt.set(p.snapshot_at, (byAt.get(p.snapshot_at) ?? 0) + (spec.pick(p.compliance) ?? 0));
  return [...byAt.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([at, v]) => ({ at, percent: v }));
}

// byControl splits one aggregate=control history into a series per
// control, so the fleet asks for the history once, not once per row.
export function byControl(points: readonly Schemas["HistoryPoint"][]): Map<string, SeriesPoint[]> {
  const groups = new Map<string, Schemas["HistoryPoint"][]>();
  for (const p of points) {
    const id = p.control_id ?? "";
    const g = groups.get(id);
    if (g) g.push(p);
    else groups.set(id, [p]);
  }
  return new Map([...groups.entries()].map(([id, ps]) => [id, historySeries(ps)]));
}

// worstControls is the five lowest measured percentages; an unmeasured
// control (null percent) is not "worst", it is unknown.
export function worstControls<T extends { compliance: Posture }>(controls: readonly T[], n = 5): T[] {
  return controls
    .filter((c) => c.compliance.percent !== null)
    .sort((a, b) => (a.compliance.percent ?? 0) - (b.compliance.percent ?? 0))
    .slice(0, n);
}

// underDays is the share of an age distribution in the buckets under a
// threshold — "N% under 30d" over the first three of the six finding-age
// buckets (<1d, 1-7d, 7-30d). Null when nothing is counted.
export function underDays(buckets: readonly Schemas["AgeCount"][], first: number): number | null {
  const total = buckets.reduce((n, b) => n + b.count, 0);
  if (total === 0) return null;
  const under = buckets.slice(0, first).reduce((n, b) => n + b.count, 0);
  return Math.round((100 * under) / total);
}

// AGE_COLORS tone the six finding-age buckets from fresh to old.
export const AGE_COLORS = ["#7aa7ff", "#5fa8f5", "#d9a020", "#e0823a", "#f2554d", "#b8403a"] as const;

// staleIndex is the open-PR age bucket past the stale threshold: the
// last of the four (30d+).
export const STALE_BUCKET = "30d+";
