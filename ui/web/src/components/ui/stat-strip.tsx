import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

export interface StatCell {
  label: ReactNode;
  value: ReactNode;
  subline?: ReactNode;
}

// StatStrip is the row of hairline-separated label/value cells under a
// card header (DESIGN-0036 §Surfaces). Values are proportional here;
// tables are where numbers go tabular.
export function StatStrip({ cells, className }: { cells: StatCell[]; className?: string }) {
  return (
    <dl className={cn("mt-2.5 flex border-y border-border", className)}>
      {cells.map((c, i) => (
        <div key={i} className="min-w-0 flex-1 border-r border-border px-3.5 py-2 last:border-r-0">
          <dt className="text-[11px] font-semibold tracking-[0.06em] text-muted-foreground uppercase">{c.label}</dt>
          <dd className="mt-0.5 text-lg font-semibold whitespace-nowrap">{c.value}</dd>
          {c.subline !== undefined ? <dd className="mt-px text-xs leading-snug text-muted-foreground">{c.subline}</dd> : null}
        </div>
      ))}
    </dl>
  );
}
