// Axis arithmetic shared by the charts (DESIGN-0036 §Charts). Pure, so
// the bounds and tick fitting are table-tested without rendering.

export interface Bounds {
  min: number;
  max: number;
  ticks: number[];
}

// niceBounds brackets the data with a floor and ceiling on a step so a
// series living in a narrow band (97–99%) is legible rather than a flat
// line at the top of a 0–100 axis. Percent axes never leave [0, 100];
// count axes never go below zero. Empty data gives the full range.
export function niceBounds(values: readonly (number | null | undefined)[], { percent = false, step = 10, pad = 2 }: { percent?: boolean; step?: number; pad?: number } = {}): Bounds {
  const vals = values.filter((v): v is number => typeof v === "number" && Number.isFinite(v));
  if (vals.length === 0) {
    return withTicks(0, percent ? 100 : step);
  }
  let min = Math.floor((Math.min(...vals) - pad) / step) * step;
  let max = Math.ceil((Math.max(...vals) + pad) / step) * step;
  if (percent) {
    min = Math.max(0, min);
    max = Math.min(100, max);
  } else {
    min = Math.max(0, min);
  }
  if (max <= min) {
    if (percent && min >= 100) min = 100 - step;
    else max = min + step;
  }
  return withTicks(min, max);
}

function withTicks(min: number, max: number): Bounds {
  const stepSize = (max - min) / 4;
  const ticks = [0, 1, 2, 3, 4].map((g) => Number((min + g * stepSize).toFixed(1)));
  return { min, max, ticks };
}

// fitTicks picks at most `want` indices from n points, one constant
// step apart and anchored on the last (newest) point, never repeating
// one. A constant step matters: rounding evenly spread positions to
// indices skipped days unevenly (8 daily points labelled 18, 19, 21,
// 22, 24, 25), which read as missing snapshots.
export function fitTicks(n: number, want: number): number[] {
  if (n <= 0) return [];
  const step = Math.max(1, Math.ceil((n - 1) / Math.max(1, want - 1)));
  const out: number[] = [];
  for (let i = n - 1; i >= 0; i -= step) out.unshift(i);
  return out;
}

export type TickStyle = "time" | "date";

// xTicksFor says how many x labels a range gets and whether they are
// clock times (a day of snapshots) or dates.
export function xTicksFor(rangeDays: number): { want: number; style: TickStyle } {
  if (rangeDays <= 1) return { want: 6, style: "time" };
  if (rangeDays <= 7) return { want: 7, style: "date" };
  return { want: 6, style: "date" };
}

// tickLabel formats one x label. Dates are UTC calendar days because a
// snapshot is a daily point, not a moment the reader's zone matters for.
export function tickLabel(iso: string, style: TickStyle): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  if (style === "time") {
    return `${String(d.getUTCHours()).padStart(2, "0")}:${String(d.getUTCMinutes()).padStart(2, "0")}`;
  }
  return `${MONTHS[d.getUTCMonth()] ?? ""} ${d.getUTCDate()}`;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

// formatValue renders a plotted value with its unit; null is a gap.
export function formatValue(v: number | null | undefined, unit: "%" | "count"): string {
  if (v === null || v === undefined) return "—";
  return unit === "%" ? `${v.toFixed(1)}%` : v.toLocaleString();
}
