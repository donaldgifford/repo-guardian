import type { Schemas } from "../api/client";

type Counts = Schemas["Posture"];

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
// "no data", never 0% or 100%.
export function formatPercent(p: number | null | undefined): string {
  return p === null || p === undefined ? "no data" : `${p.toFixed(1)}%`;
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

// humanizeDuration shortens a Go duration from the API ("720h") to days
// when it is whole days, else hours; anything it cannot read is returned
// as-is.
export function humanizeDuration(d: string): string {
  const m = /^(\d+)h(?:0m)?(?:0s)?$/.exec(d.trim());
  if (!m) return d;
  const hours = Number(m[1]);
  return hours % 24 === 0 && hours > 0 ? `${hours / 24}d` : `${hours}h`;
}
// Tone is a badge colour by meaning, never by rank (DESIGN-0036 §Badges).
export type Tone = "ok" | "warn" | "bad" | "info" | "mute" | "outline";

// ToneField names a column of the DESIGN-0036 tone table. For `reason`
// the value is the finding's *status*: a reason is warn under
// non_compliant and mute otherwise.
export type ToneField = "status" | "remediation" | "reason" | "state" | "kind";

const tones: Record<Exclude<ToneField, "reason" | "kind">, Record<string, Tone>> = {
  status: { compliant: "ok", non_compliant: "bad", not_applicable: "mute", unknown: "warn" },
  remediation: { pr_open: "info", applied: "ok", foreign_pr: "warn", disabled: "mute", dry_run: "mute", none: "mute" },
  state: { operational: "ok", degraded: "warn" },
};

// toneFor is the one tone table. Both API enums are open, so any value
// the table does not know is mute; a component state it does not know
// is bad, because an unknown state of a service is not good news.
export function toneFor(field: ToneField, value: string): Tone {
  switch (field) {
    case "kind":
      return "outline";
    case "reason":
      return value === "non_compliant" ? "warn" : "mute";
    case "state":
      return tones.state[value] ?? "bad";
    default:
      return tones[field][value] ?? "mute";
  }
}
