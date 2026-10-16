import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

// Pill is a legend toggle: a colour dot, a label and the series' current
// value. It is a radio inside a PillGroup, so exactly one series is
// selected and the choice is announced (DESIGN-0036 §Fleet).
export function Pill({
  color,
  label,
  value,
  checked,
  onSelect,
  className,
}: {
  color: string;
  label: ReactNode;
  value?: ReactNode;
  checked: boolean;
  onSelect: () => void;
  className?: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={checked}
      tabIndex={checked ? 0 : -1}
      onClick={onSelect}
      className={cn(
        "inline-flex items-center gap-[7px] rounded-lg border px-[9px] py-1 text-xs",
        checked ? "border-accent/50 bg-panel-2 text-foreground" : "border-border text-muted-foreground hover:text-foreground",
        className,
      )}
    >
      <i aria-hidden="true" className="inline-block size-2 rounded-[2px]" style={{ background: color }} />
      {label}
      {value !== undefined ? <b className="font-medium text-foreground">{value}</b> : null}
    </button>
  );
}

export function PillGroup({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <div role="radiogroup" aria-label={label} className={cn("flex flex-wrap gap-1.5", className)}>
      {children}
    </div>
  );
}
