import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

export interface BarListItem {
  label: string;
  value: number;
  // sub is muted text after the label (a kind, a reason family).
  sub?: string;
  // render replaces the label text, for a link.
  render?: (label: string) => ReactNode;
}

export const BAR_LIST_MAX = 8;

// foldBarList sorts descending and folds everything past `max` rows into
// one "other" row, so a bar list never asks the eye to rank more than
// eight things (DESIGN-0036 §Charts).
export function foldBarList<T extends BarListItem>(items: readonly T[], max = BAR_LIST_MAX): BarListItem[] {
  const sorted = [...items].sort((a, b) => b.value - a.value || a.label.localeCompare(b.label));
  if (sorted.length <= max) return sorted;
  const kept = sorted.slice(0, max);
  const other = sorted.slice(max).reduce((n, it) => n + it.value, 0);
  return [...kept, { label: "other", value: other, sub: `${sorted.length - max} more` }];
}

// BarList is the ranked list that replaces donuts: label (mono), count,
// share of the total, and a single-colour bar scaled to the largest.
export function BarList({
  items,
  color = "var(--color-accent)",
  share = true,
  max = BAR_LIST_MAX,
  "aria-label": ariaLabel,
  className,
}: {
  items: readonly BarListItem[];
  color?: string;
  share?: boolean;
  max?: number;
  "aria-label": string;
  className?: string;
}) {
  const rows = foldBarList(items, max);
  const total = rows.reduce((n, r) => n + r.value, 0);
  const largest = Math.max(1, ...rows.map((r) => r.value));
  return (
    <div role="list" aria-label={ariaLabel} className={cn("mt-1 grid gap-2", className)}>
      {rows.map((r) => (
        <div key={r.label} role="listitem" className="grid gap-1 text-[12.5px]">
          <div className="flex items-baseline justify-between">
            <span className="min-w-0 truncate font-mono">
              {r.render ? r.render(r.label) : r.label}
              {r.sub !== undefined ? <em className="ml-1.5 text-[11px] text-muted-foreground not-italic">{r.sub}</em> : null}
            </span>
            <b className="font-medium tabular-nums">
              {r.value.toLocaleString()}
              {share && total > 0 ? <span className="text-muted-foreground"> · {Math.round((100 * r.value) / total)}%</span> : null}
            </b>
          </div>
          <div className="h-2">
            <div className="h-full min-w-0.5 rounded-r-[4px]" style={{ width: `${(100 * r.value) / largest}%`, background: color }} />
          </div>
        </div>
      ))}
    </div>
  );
}

export function barListTable(items: readonly BarListItem[], first = "Item", max = BAR_LIST_MAX) {
  const rows = foldBarList(items, max);
  const total = rows.reduce((n, r) => n + r.value, 0);
  return {
    head: [first, "Count", "Share"],
    numeric: [1, 2],
    rows: rows.map((r) => [r.label, r.value.toLocaleString(), total > 0 ? `${Math.round((100 * r.value) / total)}%` : "—"]),
  };
}
