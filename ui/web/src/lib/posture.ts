import type { Schemas } from "../api/client";
import type { DistSegment } from "../components/charts";

export type Posture = Schemas["Posture"];

// postureSegments is a posture's buckets as a distribution bar, worst
// news after the good: every bucket the API counts, none merged, so the
// legend adds up to the assignments.
export function postureSegments(p: Posture): DistSegment[] {
  return [
    { label: "compliant", value: p.compliant, color: "var(--color-ok)" },
    { label: "non-compliant", value: p.non_compliant, color: "var(--color-bad)" },
    { label: "pending", value: p.pending, color: "var(--color-info)" },
    { label: "stale", value: p.stale, color: "var(--color-warn)" },
    { label: "unmeasurable", value: p.unmeasurable_access_denied + p.unmeasurable_suspended + p.unmeasurable_conflict, color: "var(--color-muted-foreground)" },
    { label: "n/a", value: p.not_applicable + p.unknown + p.excluded, color: "var(--color-border)" },
  ];
}

// emptyPosture is the zero posture: no assignments, no percentages.
export function emptyPosture(): Posture {
  return {
    compliant: 0,
    non_compliant: 0,
    pending: 0,
    stale: 0,
    unmeasurable_access_denied: 0,
    unmeasurable_suspended: 0,
    unmeasurable_conflict: 0,
    not_applicable: 0,
    unknown: 0,
    excluded: 0,
    assigned: 0,
    percent: null,
    coverage: null,
  };
}

// floorPercent is the floor_percent SQL function: num / den floored to
// one decimal in integer arithmetic, null on an empty denominator.
export function floorPercent(num: number, den: number): number | null {
  return den <= 0 ? null : Math.floor((num * 1000) / den) / 10;
}

// addPosture sums two postures and recomputes both percentages from the
// sums, as store.ControlPosture.Plus does.
export function addPosture(a: Posture, b: Posture): Posture {
  const out: Posture = {
    compliant: a.compliant + b.compliant,
    non_compliant: a.non_compliant + b.non_compliant,
    pending: a.pending + b.pending,
    stale: a.stale + b.stale,
    unmeasurable_access_denied: a.unmeasurable_access_denied + b.unmeasurable_access_denied,
    unmeasurable_suspended: a.unmeasurable_suspended + b.unmeasurable_suspended,
    unmeasurable_conflict: a.unmeasurable_conflict + b.unmeasurable_conflict,
    not_applicable: a.not_applicable + b.not_applicable,
    unknown: a.unknown + b.unknown,
    excluded: a.excluded + b.excluded,
    assigned: a.assigned + b.assigned,
    percent: null,
    coverage: null,
  };
  out.percent = floorPercent(out.compliant, out.compliant + out.non_compliant);
  out.coverage = floorPercent(out.compliant + out.non_compliant + out.not_applicable, out.assigned);
  return out;
}

// historySeries sums history points per snapshot into one fleet line,
// oldest first.
export function historySeries(points: readonly Schemas["HistoryPoint"][]): { at: string; percent: number | null }[] {
  const byAt = new Map<string, Posture>();
  for (const p of points) {
    byAt.set(p.snapshot_at, addPosture(byAt.get(p.snapshot_at) ?? emptyPosture(), p.compliance));
  }
  return [...byAt.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([at, posture]) => ({ at, percent: posture.percent }));
}

// postureCsvHead and postureCsvRow are a posture's CSV cells, shared by
// every table export that carries one: the API's percentages as-is
// (null stays empty, never 0 or 100) and the counts behind them.
export const postureCsvHead = ["compliance_percent", "coverage_percent", "assigned", "compliant", "non_compliant"] as const;

export function postureCsvRow(p: Posture): (number | null)[] {
  return [p.percent, p.coverage, p.assigned, p.compliant, p.non_compliant];
}
