import type { ReactNode } from "react";

// ChartTip is the hover card shared by the SVG charts: a mono heading
// and label/value rows, each with a swatch in the series colour.
export function ChartTip({ heading, rows }: { heading: ReactNode; rows: readonly { label: ReactNode; value: ReactNode; color?: string }[] }) {
  return (
    <div className="min-w-[120px] rounded-lg border border-border bg-panel px-2 py-1.5 text-xs shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
      <div className="mb-0.5 font-mono text-[11px] text-muted-foreground">{heading}</div>
      {rows.map((r, i) => (
        <div key={i} className="flex justify-between gap-3.5">
          <span>
            {r.color !== undefined ? <i aria-hidden="true" className="mr-1.5 inline-block h-0.5 w-2.5 align-middle" style={{ background: r.color }} /> : null}
            {r.label}
          </span>
          <b className="font-semibold tabular-nums">{r.value}</b>
        </div>
      ))}
    </div>
  );
}
