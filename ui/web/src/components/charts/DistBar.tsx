import { cn } from "../../lib/utils";
import { Legend } from "./ChartCard";

export interface DistSegment {
  label: string;
  value: number;
  color: string;
}

// DistBar is a single distribution bar: up to six segments with 2px
// gaps and a legend carrying each segment's count. Built for the status
// page's fleet distribution (IMPL-0031 7.3).
export function DistBar({ segments, height = 10, "aria-label": ariaLabel, className }: { segments: readonly DistSegment[]; height?: number; "aria-label": string; className?: string }) {
  const total = segments.reduce((n, s) => n + s.value, 0);
  return (
    <div className={className}>
      <div role="img" aria-label={ariaLabel} className={cn("flex gap-0.5 overflow-hidden rounded-[2px]", total === 0 && "bg-panel-2")} style={{ height }}>
        {segments.map((s) =>
          s.value === 0 ? null : <i key={s.label} title={`${s.label} ${s.value.toLocaleString()}`} className="block h-full" style={{ flex: `${s.value} ${s.value} 0`, background: s.color }} />,
        )}
      </div>
      <Legend items={segments.map((s) => ({ color: s.color, label: `${s.label} ${s.value.toLocaleString()}` }))} />
    </div>
  );
}
