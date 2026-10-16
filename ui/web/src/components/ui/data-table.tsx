import type { KeyboardEvent, MouseEvent, ReactNode } from "react";

import { cn } from "../../lib/utils";

export interface Column<T> {
  key: string;
  header: ReactNode;
  cell: (row: T) => ReactNode;
  align?: "left" | "right";
  // numeric right-aligns in tabular figures.
  numeric?: boolean;
  // hideBelow drops the column at narrow widths; "compact" drops it
  // whenever the table is told it is sharing the row (a drawer open
  // side-by-side), independent of the viewport.
  hideBelow?: "md" | "lg" | "xl" | "compact";
  width?: string;
  className?: string;
}

const hideClass: Record<NonNullable<Column<unknown>["hideBelow"]>, string> = {
  md: "hidden md:table-cell",
  lg: "hidden lg:table-cell",
  xl: "hidden xl:table-cell",
  compact: "",
};

// DataTable is the one table every list page renders through
// (DESIGN-0036 §Component inventory): 13px cells at 9px 10px, hover on
// panel-2, the selected row tinted accent at 12%, and rows that are
// keyboard focusable and activatable when the page gives them an
// action. Links and buttons inside a row keep their own behaviour.
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  selectedKey,
  onActivate,
  compact = false,
  empty,
  className,
  "aria-label": ariaLabel,
}: {
  columns: readonly Column<T>[];
  rows: readonly T[];
  rowKey: (row: T) => string;
  selectedKey?: string;
  onActivate?: (row: T) => void;
  compact?: boolean;
  empty?: ReactNode;
  className?: string;
  "aria-label"?: string;
}) {
  const shown = compact ? columns.filter((c) => c.hideBelow !== "compact") : columns;
  const interactive = onActivate !== undefined;

  const activate = (row: T) => (e: MouseEvent<HTMLTableRowElement>) => {
    if (!onActivate) return;
    if ((e.target as HTMLElement).closest("a, button, input, select, [role=menuitem]")) return;
    onActivate(row);
  };
  const keyActivate = (row: T) => (e: KeyboardEvent<HTMLTableRowElement>) => {
    if (!onActivate || e.target !== e.currentTarget) return;
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onActivate(row);
    }
  };

  return (
    <div className={cn("w-full overflow-x-auto", className)}>
      <table className="w-full text-[13px]" aria-label={ariaLabel}>
        <thead>
          <tr className="border-b border-border">
            {shown.map((c) => (
              <th
                key={c.key}
                scope="col"
                style={c.width ? { width: c.width } : undefined}
                className={cn(
                  "h-8 px-[10px] text-left text-[11px] font-semibold tracking-[0.04em] whitespace-nowrap text-muted-foreground uppercase",
                  (c.numeric || c.align === "right") && "text-right",
                  c.hideBelow ? hideClass[c.hideBelow] : undefined,
                  c.className,
                )}
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && empty !== undefined ? (
            <tr>
              <td colSpan={shown.length} className="px-[10px] py-3 text-sm text-muted-foreground">
                {empty}
              </td>
            </tr>
          ) : null}
          {rows.map((row) => {
            const key = rowKey(row);
            const selected = selectedKey !== undefined && key === selectedKey;
            return (
              <tr
                key={key}
                data-key={key}
                data-selected={selected || undefined}
                aria-selected={interactive ? selected : undefined}
                tabIndex={interactive ? 0 : undefined}
                onClick={interactive ? activate(row) : undefined}
                onKeyDown={interactive ? keyActivate(row) : undefined}
                className={cn(
                  "border-b border-border align-middle last:border-0",
                  interactive && "cursor-pointer outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset",
                  selected ? "bg-accent/12" : "hover:bg-panel-2",
                )}
              >
                {shown.map((c) => (
                  <td
                    key={c.key}
                    className={cn("px-[10px] py-[9px]", (c.numeric || c.align === "right") && "text-right", c.numeric && "tabular-nums", c.hideBelow ? hideClass[c.hideBelow] : undefined, c.className)}
                  >
                    {c.cell(row)}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
