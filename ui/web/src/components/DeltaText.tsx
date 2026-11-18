import { cn } from "@donaldgifford/design-system/lib/utils";

// Local, not in the design system: upstream asked for the delta to stay
// in the app and be passed to Kpi as its hint (design-system#11).

export interface Delta {
  // value is the change against the comparison point, already rounded.
  value: number;
  // goodUp says which direction is good: up for compliance, down for
  // failing counts.
  goodUp: boolean;
  unit?: string;
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
    <span className={cn("tabular-nums", good ? "text-success" : "text-destructive")} data-direction={up ? "up" : "down"}>
      {up ? "▲" : "▼"} {Math.abs(delta.value)}
      {unit}
    </span>
  );
}
