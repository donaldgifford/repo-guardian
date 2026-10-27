// The time range every page with a time dimension offers (DESIGN-0037
// §Page anatomy), kept in `?days=` as a number so the URL reads
// `?days=7`, never the router's JSON-quoted string. 30 is the API's
// own default (IMPL-0032 OQ5) and is not written.
export const DAYS = [1, 7, 30, 90] as const;
export type Days = (typeof DAYS)[number];
export const DEFAULT_DAYS: Days = 30;

export function isDays(v: unknown): v is Days {
  return typeof v === "number" && (DAYS as readonly number[]).includes(v);
}

// parseDays reads a search value (a number, or the string the URL
// carries) and falls back to the default, so an unknown value never
// leaves a page without a range.
export function parseDays(v: unknown): Days {
  const n = typeof v === "string" && v !== "" ? Number(v) : v;
  return isDays(n) ? n : DEFAULT_DAYS;
}

// RangeSearch is the `?days=` part of a route's search; routes with a
// range spread parseRangeSearch into their validator.
export interface RangeSearch {
  days?: Days | undefined;
}

// parseRangeSearch keeps only a non-default range, so the default is
// never written to the URL.
export function parseRangeSearch(s: Record<string, unknown>): RangeSearch {
  const days = parseDays(s.days);
  return days === DEFAULT_DAYS ? {} : { days };
}
