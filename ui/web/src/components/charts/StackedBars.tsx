import type { ReactNode } from "react";

import { cn } from "../../lib/utils";
import { Legend } from "./ChartCard";

export interface Segment {
  key: string;
  label: string;
  color: string;
}

export interface StackRow {
  label: string;
  counts: Readonly<Record<string, number>>;
  // href links the row label, when the category has a page.
  href?: ReactNode;
}

export interface RowShare {
  key: string;
  value: number;
  percent: number;
}

// rowShares is the percent-of-row math: each segment's share of the
// row's own total (not of the largest row), zero when the row is empty.
export function rowShares(row: StackRow, segs: readonly Segment[]): { total: number; shares: RowShare[] } {
  const total = segs.reduce((a, s) => a + (row.counts[s.key] ?? 0), 0);
  const shares = segs.map((s) => {
    const value = row.counts[s.key] ?? 0;
    return { key: s.key, value, percent: total === 0 ? 0 : Math.round((1000 * value) / total) / 10 };
  });
  return { total, shares };
}

// StackedBars is one horizontal stack per category: 10px segments with
// 2px background gaps, the row scaled to the largest total, the total
// after it, and one legend. Status tones only, by contract.
export function StackedBars({ rows, segs, legend = true, "aria-label": ariaLabel, className }: { rows: readonly StackRow[]; segs: readonly Segment[]; legend?: boolean; "aria-label": string; className?: string }) {
  const totals = rows.map((r) => rowShares(r, segs).total);
  const max = Math.max(1, ...totals);
  const single = rows.length === 1;
  return (
    <>
      <div role="img" aria-label={ariaLabel} className={cn("mt-1 grid gap-[9px]", className)}>
        {rows.map((r, ri) => {
          const { total, shares } = rowShares(r, segs);
          return (
            <div key={r.label} className={cn("grid items-center gap-2.5 text-[12.5px]", single ? "grid-cols-[1fr_64px]" : "grid-cols-[150px_1fr_64px]")}>
              {single ? null : <span className="truncate text-right font-mono">{r.href ?? r.label}</span>}
              <div className="flex h-2.5 gap-0.5" style={{ width: `${(100 * total) / max}%` }}>
                {shares.map((s, si) =>
                  s.value === 0 ? null : (
                    <i
                      key={s.key}
                      title={`${segs[si]?.label ?? s.key} ${s.value.toLocaleString()} (${s.percent}%)`}
                      className="block h-full min-w-0 hover:opacity-75"
                      style={{ flex: `${s.value} ${s.value} 0`, background: segs[si]?.color }}
                      data-row={ri}
                      data-seg={s.key}
                    />
                  ),
                )}
              </div>
              <span className="text-right text-muted-foreground tabular-nums">{total.toLocaleString()}</span>
            </div>
          );
        })}
      </div>
      {legend ? <Legend items={segs} /> : null}
    </>
  );
}

export function stackTable(rows: readonly StackRow[], segs: readonly Segment[], first = "Rule") {
  const single = rows.length === 1;
  return {
    head: [...(single ? [] : [first]), ...segs.map((s) => s.label)],
    numeric: segs.map((_, i) => i + (single ? 0 : 1)),
    rows: rows.map((r) => [...(single ? [] : [r.label]), ...segs.map((s) => (r.counts[s.key] ?? 0).toLocaleString())]),
  };
}
