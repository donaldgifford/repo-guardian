import { relativeTime } from "./format";

// One timestamp helper for the whole UI (DESIGN-0036 §Time zone): every
// timestamp renders through formatTime in the zone the user picked, and
// the other zone sits in the element's title.

export type Zone = "local" | "utc";
export type TimeStyle = "datetime" | "date" | "relative";

export const ZONE_STORAGE_KEY = "rg.tz";

export function isZone(v: unknown): v is Zone {
  return v === "local" || v === "utc";
}

export function otherZone(zone: Zone): Zone {
  return zone === "utc" ? "local" : "utc";
}

// readZone is the persisted choice, local when nothing is stored or
// storage is unavailable (private mode, tests, server render).
export function readZone(storage: Pick<Storage, "getItem"> | undefined = safeStorage()): Zone {
  try {
    const v = storage?.getItem(ZONE_STORAGE_KEY);
    return isZone(v) ? v : "local";
  } catch {
    return "local";
  }
}

export function writeZone(zone: Zone, storage: Pick<Storage, "setItem"> | undefined = safeStorage()): void {
  try {
    storage?.setItem(ZONE_STORAGE_KEY, zone);
  } catch {
    // Storage full or denied: the choice lives for this page only.
  }
}

function safeStorage(): Storage | undefined {
  try {
    return typeof localStorage === "undefined" ? undefined : localStorage;
  } catch {
    return undefined;
  }
}

// parseTime reads an RFC 3339 time. A value with no zone designator is
// taken as UTC, which is what every API timestamp is, rather than as
// the browser's zone, which is what `new Date` would assume.
export function parseTime(iso: string): Date {
  return new Date(/(Z|[+-]\d\d:\d\d)$/i.test(iso) ? iso : `${iso}Z`);
}

export interface FormatTimeOptions {
  zone: Zone;
  style?: TimeStyle;
  now?: Date;
}

// formatTime renders iso as `YYYY-MM-DD HH:mm` (datetime), `YYYY-MM-DD`
// (date) or "3 hours ago" (relative, zone-independent). An undefined
// or unparsable value renders as "—" so a missing field never shows
// "Invalid Date".
export function formatTime(iso: string | undefined, { zone, style = "datetime", now }: FormatTimeOptions): string {
  if (!iso) return "—";
  const d = parseTime(iso);
  if (Number.isNaN(d.getTime())) return "—";
  if (style === "relative") return relativeTime(d.toISOString(), now);
  const p = zone === "utc" ? utcParts(d) : localParts(d);
  const date = `${p.year}-${p.month}-${p.day}`;
  return style === "date" ? date : `${date} ${p.hour}:${p.minute}`;
}

interface Parts {
  year: string;
  month: string;
  day: string;
  hour: string;
  minute: string;
}

const two = (n: number) => String(n).padStart(2, "0");

function utcParts(d: Date): Parts {
  return {
    year: String(d.getUTCFullYear()),
    month: two(d.getUTCMonth() + 1),
    day: two(d.getUTCDate()),
    hour: two(d.getUTCHours()),
    minute: two(d.getUTCMinutes()),
  };
}

function localParts(d: Date): Parts {
  return {
    year: String(d.getFullYear()),
    month: two(d.getMonth() + 1),
    day: two(d.getDate()),
    hour: two(d.getHours()),
    minute: two(d.getMinutes()),
  };
}
