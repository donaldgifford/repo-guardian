import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

export interface Delta {
  // value is the change against the comparison point, already rounded.
  value: number;
  // goodUp says which direction is good: up for compliance, down for
  // failing counts.
  goodUp: boolean;
  unit?: string;
}

// Kpi is a label, a 22px value and a subline, optionally led by a delta
// arrow (DESIGN-0036 §Component inventory).
export function Kpi({ label, value, subline, delta, className }: { label: ReactNode; value: ReactNode; subline?: ReactNode; delta?: Delta | null; className?: string }) {
  return (
    <div className={cn("rounded-lg border border-border bg-panel px-3.5 py-2.5", className)}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-0.5 text-[22px] leading-tight font-semibold tabular-nums">{value}</div>
      {subline !== undefined || delta !== undefined ? (
        <div className="mt-0.5 text-xs text-muted-foreground">
          {delta !== undefined ? <DeltaText delta={delta} /> : null}
          {delta !== undefined && subline !== undefined ? " " : null}
          {subline}
        </div>
      ) : null}
    </div>
  );
}

// DeltaText is "▲ 1.2 pts" toned by whether the move is good; null is
// "no previous snapshot" and zero is a neutral dot.
export function DeltaText({ delta }: { delta: Delta | null }) {
  if (delta === null) {
    return <span>no previous snapshot</span>;
  }
  const unit = delta.unit ?? "";
  if (delta.value === 0) {
    return <span>• 0{unit}</span>;
  }
  const up = delta.value > 0;
  const good = delta.goodUp ? up : !up;
  return (
    <span className={cn("tabular-nums", good ? "text-ok" : "text-bad")} data-direction={up ? "up" : "down"}>
      {up ? "▲" : "▼"} {Math.abs(delta.value)}
      {unit}
    </span>
  );
}
