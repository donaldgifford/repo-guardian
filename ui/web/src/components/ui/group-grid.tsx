import type { ReactNode } from "react";

import { cn } from "../../lib/utils";
import { heatOpacity } from "../../lib/viewmodel";

export type GridTone = "ok" | "bad" | "warn" | "mute" | "info";

export interface GridColumn {
  key: string;
  header: ReactNode;
  // tone shades a measured cell when the column is in `heat`.
  tone: GridTone;
}

// GridCell is one number with its own denominator (DESIGN-0037 OQ4):
// `of` is what the share and the shade divide by — for a posture, the
// measured assignments (compliant + non_compliant), never the buckets
// that measure nothing. `of` 0 or null is "no data"; `of` undefined is
// a plain count with no share. A ReactNode cell is rendered as given
// (a gated "—").
export type GridCell = { n: number; of?: number | null } | number | ReactNode;

export interface GridRow {
  key: string;
  label: ReactNode;
  // sub is the muted line under the label ("87% compliant · 3 parked").
  sub?: ReactNode;
  badge?: ReactNode;
  cells: Readonly<Record<string, GridCell>>;
}

// GridChildren is what an expanded row shows: its rows in the same
// table body, so columns align, and a footer line ("showing 20 of 134 ·
// Load more") the caller supplies.
export interface GridChildren {
  rows: readonly GridRow[];
  loading?: boolean;
  empty?: ReactNode;
  footer?: ReactNode;
}

const tones: Record<GridTone, string> = {
  ok: "var(--color-ok)",
  bad: "var(--color-bad)",
  warn: "var(--color-warn)",
  mute: "var(--color-muted-foreground)",
  info: "var(--color-info)",
};

// share is the cell's fraction of its denominator, null when unmeasured.
export function share(cell: { n: number; of?: number | null }): number | null {
  if (cell.of === undefined || cell.of === null || cell.of <= 0) return null;
  return cell.n / cell.of;
}

// heatStyle is the cell background: the column tone at heatOpacity of
// the cell's share. Text stays foreground; the number is always read.
export function heatStyle(count: number, total: number, tone: GridTone): { background: string } | undefined {
  const op = heatOpacity(count, total);
  return op === 0 ? undefined : { background: `color-mix(in srgb, ${tones[tone]} ${Math.round(op * 100)}%, transparent)` };
}

const isCount = (c: GridCell): c is { n: number; of?: number | null } => typeof c === "object" && c !== null && "n" in c;

const numeric = "px-[10px] py-[9px] text-right tabular-nums whitespace-nowrap";

function Cell({ cell, col, heat }: { cell: GridCell | undefined; col: GridColumn; heat: boolean }) {
  if (cell === undefined) return <td className={numeric} />;
  if (typeof cell === "number") return <td className={numeric}>{cell.toLocaleString()}</td>;
  if (!isCount(cell)) return <td className={numeric}>{cell}</td>;
  if (cell.of === undefined) return <td className={numeric}>{cell.n.toLocaleString()}</td>;
  const s = share(cell);
  if (s === null) {
    return (
      <td className={cn(numeric, "text-muted-foreground")} data-measured="false">
        no data
      </td>
    );
  }
  const shade = heat ? Math.round(100 * heatOpacity(cell.n, cell.of ?? 0)) : 0;
  return (
    <td className={numeric} style={heat ? heatStyle(cell.n, cell.of ?? 0, col.tone) : undefined} data-heat={heat ? col.key : undefined} data-shade={shade > 0 ? shade : undefined}>
      {cell.n.toLocaleString()}
      {/* The share keeps full foreground on a shaded cell: muted ink on the tint fails AA. */}
      <span className={cn("ml-1.5 text-xs", shade > 0 ? "text-foreground" : "text-muted-foreground")}>{Math.round(100 * s)}%</span>
    </td>
  );
}

function Label({ row, depth }: { row: GridRow; depth: 0 | 1 }) {
  return (
    <span className="min-w-0">
      <span className={cn("flex items-center gap-2 font-mono", depth === 0 && "font-medium")}>
        {row.label}
        {row.badge}
      </span>
      {row.sub !== undefined ? <span className="block text-xs text-muted-foreground">{row.sub}</span> : null}
    </span>
  );
}

// GroupGrid is the expandable grid (DESIGN-0036 §Orgs, DESIGN-0037
// OQ4): parent rows with a chevron, child rows the caller supplies
// through `children` (lazily, so expansion can fetch) in the same body,
// heat-shaded measured columns, and a footer row the caller sums. Open
// state is the caller's (Orgs keeps it in `?open=`).
export function GroupGrid({
  label,
  columns,
  rows,
  heat,
  open,
  onToggle,
  children,
  footer,
  "aria-label": ariaLabel,
  className,
}: {
  // label heads the first column ("Org / repository").
  label: ReactNode;
  columns: readonly GridColumn[];
  rows: readonly GridRow[];
  // heat names the columns currently shaded.
  heat: readonly string[];
  open: readonly string[];
  onToggle: (key: string) => void;
  children: (row: GridRow) => GridChildren;
  // footer is the closing row: a label and a cell per column key.
  footer?: { label: ReactNode; cells: Readonly<Record<string, GridCell>> };
  "aria-label": string;
  className?: string;
}) {
  const span = columns.length + 1;
  return (
    <div className={cn("w-full overflow-x-auto", className)}>
      <table className="w-full text-[13px]" aria-label={ariaLabel}>
        <thead>
          <tr className="border-b border-border">
            <th scope="col" className="h-8 px-[10px] text-left text-[11px] font-semibold tracking-[0.04em] text-muted-foreground uppercase">
              {label}
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
            const kids = expanded ? children(row) : undefined;
            return [
              <tr key={row.key} className="border-b border-border hover:bg-panel-2" data-row={row.key}>
                <td className="px-[10px] py-[9px]">
                  {/* The toggle is the chevron alone: the label may be a
                      link, and a control inside a control is unreachable
                      to assistive tech (axe nested-interactive). */}
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      aria-expanded={expanded}
                      aria-label={`${expanded ? "Collapse" : "Expand"} ${row.key}`}
                      onClick={() => onToggle(row.key)}
                      className={cn("inline-block w-3 flex-none text-muted-foreground transition-transform hover:text-foreground", expanded && "rotate-90")}
                    >
                      <span aria-hidden="true">▸</span>
                    </button>
                    <Label row={row} depth={0} />
                  </div>
                </td>
                {columns.map((c) => (
                  <Cell key={c.key} cell={row.cells[c.key]} col={c} heat={heat.includes(c.key)} />
                ))}
              </tr>,
              ...(kids === undefined ? [] : childRows(row.key, kids, columns, heat, span)),
            ];
          })}
          {footer ? (
            <tr className="font-semibold" data-row="__footer">
              <td className="px-[10px] py-[9px]">{footer.label}</td>
              {columns.map((c) => (
                <Cell key={c.key} cell={footer.cells[c.key]} col={c} heat={false} />
              ))}
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}

// childRows is an expanded group's body: a note while loading or
// empty, else one indented row per child and the caller's footer line.
function childRows(parent: string, kids: GridChildren, columns: readonly GridColumn[], heat: readonly string[], span: number): ReactNode[] {
  const note = (text: ReactNode, key: string) => (
    <tr key={`${parent}/${key}`} className="border-b border-border" data-children={parent}>
      <td colSpan={span} className="bg-background py-2 pr-[10px] pl-[34px] text-xs text-muted-foreground">
        {text}
      </td>
    </tr>
  );
  if (kids.loading) return [note("Loading…", "__loading")];
  if (kids.rows.length === 0) return [note(kids.empty ?? "Nothing here.", "__empty")];
  return [
    ...kids.rows.map((r) => (
      <tr key={`${parent}/${r.key}`} className="border-b border-border bg-background hover:bg-panel-2" data-children={parent} data-child={r.key}>
        <td className="py-[7px] pr-[10px] pl-[34px]">
          <Label row={r} depth={1} />
        </td>
        {columns.map((c) => (
          <Cell key={c.key} cell={r.cells[c.key]} col={c} heat={heat.includes(c.key)} />
        ))}
      </tr>
    )),
    ...(kids.footer === undefined ? [] : [note(kids.footer, "__footer")]),
  ];
}
