import type { Schemas } from "../api/client";

type Counts = Schemas["StatusCounts"];

// compliantPercent is the API's compliance math (store.CompliantPercent):
// compliant / (compliant + non_compliant), floored to one decimal in
// integer arithmetic, and null when nothing is measured — never 100.
export function compliantPercent(c: Pick<Counts, "compliant" | "non_compliant">): number | null {
  const measured = c.compliant + c.non_compliant;
  if (measured === 0) {
    return null;
  }
  return Math.floor((c.compliant * 1000) / measured) / 10;
}

// formatPercent renders a percentage the API computed; null is
// "unmeasured", never 0% or 100%.
export function formatPercent(p: number | null | undefined): string {
  return p === null || p === undefined ? "unmeasured" : `${p.toFixed(1)}%`;
}

// trend is the change against the previous snapshot, when both exist.
export function trend(current: number | null, previous: number | null | undefined): number | null {
  if (current === null || previous === null || previous === undefined) {
    return null;
  }
  return Math.round((current - previous) * 10) / 10;
}

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ["day", 86_400],
  ["hour", 3_600],
  ["minute", 60],
];

// relativeTime renders an RFC 3339 time as "3 hours ago".
export function relativeTime(iso: string | undefined, now: Date = new Date()): string {
  if (!iso) {
    return "never";
  }
  const seconds = Math.round((new Date(iso).getTime() - now.getTime()) / 1000);
  const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) {
      return rtf.format(Math.trunc(seconds / size), unit);
    }
  }
  return rtf.format(seconds, "second");
}

// ageDays is the whole days from iso to now.
export function ageDays(iso: string, now: Date = new Date()): number {
  return Math.floor((now.getTime() - new Date(iso).getTime()) / 86_400_000);
}

// humanize turns an enum value into words: "non_compliant" → "non compliant".
export function humanize(v: string): string {
  return v.replaceAll("_", " ");
}

// statusTone maps a finding status onto a badge tone; an unknown status
// (the enum is open) is neutral.
export function statusTone(status: string): "compliant" | "non_compliant" | "not_applicable" | "unknown" | "neutral" {
  switch (status) {
    case "compliant":
    case "non_compliant":
    case "not_applicable":
    case "unknown":
      return status;
    default:
      return "neutral";
  }
}
