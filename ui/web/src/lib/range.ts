import { trendFrom } from "./viewmodel";

// The time ranges every page with a time dimension offers (DESIGN-0036
// §Page anatomy), kept in `?range=` with 90d as the unwritten default.
export const RANGES = ["1d", "7d", "30d", "90d"] as const;
export type Range = (typeof RANGES)[number];
export const DEFAULT_RANGE: Range = "90d";

export function isRange(v: unknown): v is Range {
  return typeof v === "string" && (RANGES as readonly string[]).includes(v);
}

// parseRange reads a search value, falling back to the default so an
// unknown value never leaves a page without a range.
export function parseRange(v: unknown): Range {
  return isRange(v) ? v : DEFAULT_RANGE;
}

export function rangeDays(range: Range): number {
  return Number(range.slice(0, -1));
}

// rangeFrom is the `from` timestamp for a range. Ranges of a week and
// more start at midnight UTC through trendFrom, so the history query
// key is stable across renders (#207); a day rounds to the hour for
// the same reason.
export function rangeFrom(range: Range, now: number = Date.now()): string {
  const days = rangeDays(range);
  if (days >= 7) return trendFrom(days, now);
  const d = new Date(now - days * 86_400_000);
  d.setUTCMinutes(0, 0, 0);
  return d.toISOString();
}
