import type { ReactNode } from "react";

import { cn } from "../../lib/utils";
import { heatOpacity } from "../../lib/viewmodel";

export type GridTone = "ok" | "bad" | "warn" | "mute";

export interface GridColumn {
  key: string;
  header: ReactNode;
  // tone shades the cell when the column is in `heat`.
  tone: GridTone;
  // percent prints the cell's share of the row's total beside the count.
  percent?: boolean;
}

export interface GridRow {
  key: string;
  label: ReactNode;
  // sub is the muted line under the label ("87% compliant · 3 parked").
  sub?: ReactNode;
  badge?: ReactNode;
  cells: Readonly<Record<string, number>>;
  // total is the row's denominator for shares and heat (tracked repos).
  total: number;
}

const tones: Record<GridTone, string> = {
  ok: "var(--color-ok)",
  bad: "var(--color-bad)",
  warn: "var(--color-warn)",
  mute: "var(--color-muted-foreground)",
};

// gridTotals sums every column and every total across rows: the
// closing totals row and the CSV footer share this arithmetic.
export function gridTotals(rows: readonly GridRow[], columns: readonly GridColumn[]): GridRow {
  const cells: Record<string, number> = {};
  for (const c of columns) cells[c.key] = rows.reduce((n, r) => n + (r.cells[c.key] ?? 0), 0);
  return { key: "__total", label: "Total", cells, total: rows.reduce((n, r) => n + r.total, 0) };
}

// heatStyle is the cell background: the column tone at heatOpacity of
// the cell's share. Text stays foreground; the number is always read.
export function heatStyle(count: number, total: number, tone: GridTone): { background: string } | undefined {
  const op = heatOpacity(count, total);
  return op === 0 ? undefined : { background: `color-mix(in srgb, ${tones[tone]} ${Math.round(op * 100)}%, transparent)` };
}

function Cell({ row, col, heat }: { row: GridRow; col: GridColumn; heat: boolean }) {
  const n = row.cells[col.key] ?? 0;
  const pct = col.percent && row.total > 0 ? Math.round((100 * n) / row.total) : null;
  const shade = heat ? Math.round(100 * heatOpacity(n, row.total)) : 0;
  return (
    <td className="px-[10px] py-[9px] text-right tabular-nums" style={heat ? heatStyle(n, row.total, col.tone) : undefined} data-heat={heat ? col.key : undefined} data-shade={shade > 0 ? shade : undefined}>
      {n.toLocaleString()}
      {pct !== null ? <span className="ml-1.5 text-xs text-muted-foreground">{pct}%</span> : null}
    </td>
  );
}

// GroupGrid is the expandable grid (DESIGN-0036 §Orgs): parent rows
// with a chevron, child rows the caller renders through `children`
// (lazily, so expansion can fetch), heat-shaded numeric columns, and a
// totals row. Open state is the caller's (Orgs keeps it in `?open=`).
export function GroupGrid({
  columns,
  rows,
  heat,
  open,
  onToggle,
  children,
  "aria-label": ariaLabel,
  className,
}: {
  columns: readonly GridColumn[];
  rows: readonly GridRow[];
  // heat names the columns currently shaded.
  heat: readonly string[];
  open: readonly string[];
  onToggle: (key: string) => void;
  children: (row: GridRow) => ReactNode;
  "aria-label": string;
  className?: string;
}) {
  const total = gridTotals(rows, columns);
  return (
    <div className={cn("w-full overflow-x-auto", className)}>
      <table className="w-full text-[13px]" aria-label={ariaLabel}>
        <thead>
          <tr className="border-b border-border">
            <th scope="col" className="h-8 px-[10px] text-left text-[11px] font-semibold tracking-[0.04em] text-muted-foreground uppercase">
              Org
            </th>
            {columns.map((c) => (
              <th key={c.key} scope="col" className="h-8 px-[10px] text-right text-[11px] font-semibold tracking-[0.04em] whitespace-nowrap text-muted-foreground uppercase">
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const expanded = open.includes(row.key);
            return [
              <tr key={row.key} className="border-b border-border hover:bg-panel-2" data-row={row.key}>
                <td className="px-[10px] py-[9px]">
                  <button type="button" aria-expanded={expanded} aria-controls={`grid-children-${row.key}`} onClick={() => onToggle(row.key)} className="flex w-full items-center gap-2 text-left">
                    <span aria-hidden="true" className={cn("inline-block w-3 text-muted-foreground transition-transform", expanded && "rotate-90")}>
                      ▸
                    </span>
                    <span className="min-w-0">
                      <span className="flex items-center gap-2 font-mono font-medium">
                        {row.label}
                        {row.badge}
                      </span>
                      {row.sub !== undefined ? <span className="block text-xs text-muted-foreground">{row.sub}</span> : null}
                    </span>
                  </button>
                </td>
                {columns.map((c) => (
                  <Cell key={c.key} row={row} col={c} heat={heat.includes(c.key)} />
                ))}
              </tr>,
              expanded ? (
                <tr key={`${row.key}-children`} id={`grid-children-${row.key}`} className="border-b border-border" data-children={row.key}>
                  <td colSpan={columns.length + 1} className="bg-background p-0">
                    {children(row)}
                  </td>
                </tr>
              ) : null,
            ];
          })}
          <tr className="font-semibold" data-row="__total">
            <td className="px-[10px] py-[9px]">Total</td>
            {columns.map((c) => (
              <Cell key={c.key} row={total} col={c} heat={false} />
            ))}
          </tr>
        </tbody>
      </table>
    </div>
  );
}

// GridChildRows renders child rows with the parent's columns inside an
// expanded row; the caller's render prop feeds it loaded data.
export function GridChildRows({ rows, columns, heat, loading = false, empty = "Nothing here." }: { rows: readonly GridRow[]; columns: readonly GridColumn[]; heat: readonly string[]; loading?: boolean; empty?: ReactNode }) {
  if (loading) {
    return <p className="px-[10px] py-2 text-xs text-muted-foreground">Loading…</p>;
  }
  if (rows.length === 0) {
    return <p className="px-[10px] py-2 text-xs text-muted-foreground">{empty}</p>;
  }
  return (
    <table className="w-full text-[13px]">
      <tbody>
        {rows.map((r) => (
          <tr key={r.key} className="border-b border-border last:border-0 hover:bg-panel-2" data-child={r.key}>
            <td className="py-[7px] pr-[10px] pl-[34px]">
              <span className="flex items-center gap-2">
                {r.label}
                {r.badge}
              </span>
              {r.sub !== undefined ? <span className="block text-xs text-muted-foreground">{r.sub}</span> : null}
            </td>
            {columns.map((c) => (
              <Cell key={c.key} row={r} col={c} heat={heat.includes(c.key)} />
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}
