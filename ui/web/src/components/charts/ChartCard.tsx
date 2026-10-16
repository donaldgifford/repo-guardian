import { type ReactNode, useState } from "react";

import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Card } from "../ui/card";

export interface ChartTable {
  head: readonly ReactNode[];
  rows: readonly (readonly ReactNode[])[];
  // numeric marks the columns to right-align in tabular figures.
  numeric?: readonly number[];
  // note is a muted line under the table ("latest 30 of 90 snapshots").
  note?: ReactNode;
}

// ChartCard is the frame every chart renders through: a Card whose
// header carries the `table` toggle that swaps the plot (and its
// legend) for the same data as a dense table — the accessible twin the
// dataviz rules require. `right` sits before the toggle.
export function ChartCard({
  title,
  subtitle,
  right,
  strip,
  table,
  legend,
  children,
  className,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  right?: ReactNode;
  strip?: ReactNode;
  table: ChartTable;
  legend?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const [view, setView] = useState<"plot" | "table">("plot");
  const toggle = (
    <Button variant="ghost" size="sm" aria-pressed={view === "table"} title="Toggle table view" onClick={() => setView(view === "table" ? "plot" : "table")}>
      table
    </Button>
  );
  return (
    <Card
      title={title}
      subtitle={subtitle}
      right={
        <>
          {right}
          {toggle}
        </>
      }
      strip={strip}
      className={className}
    >
      {view === "plot" ? (
        <>
          {children}
          {legend}
        </>
      ) : (
        <DenseTable table={table} />
      )}
    </Card>
  );
}

export function DenseTable({ table }: { table: ChartTable }) {
  const numeric = new Set(table.numeric ?? []);
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-border text-muted-foreground">
            {table.head.map((h, i) => (
              <th key={i} className={cn("px-2 py-1.5 text-left font-medium", numeric.has(i) && "text-right")}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {table.rows.map((r, ri) => (
            <tr key={ri} className="border-b border-border last:border-0">
              {r.map((c, ci) => (
                <td key={ci} className={cn("px-2 py-1", numeric.has(ci) ? "text-right tabular-nums" : "font-mono")}>
                  {c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {table.note !== undefined ? <div className="px-2 py-1.5 text-xs text-muted-foreground">{table.note}</div> : null}
    </div>
  );
}

// Legend is the one legend row under a multi-tone chart.
export function Legend({ items, className }: { items: readonly { label: ReactNode; color: string }[]; className?: string }) {
  return (
    <div className={cn("mt-2 flex flex-wrap gap-3 text-xs text-muted-foreground", className)}>
      {items.map((it, i) => (
        <span key={i} className="inline-flex items-center gap-1.5">
          <i aria-hidden="true" className="inline-block size-2.5 rounded-[2px]" style={{ background: it.color }} />
          {it.label}
        </span>
      ))}
    </div>
  );
}
