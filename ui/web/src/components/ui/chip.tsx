import type { ComponentProps, ReactNode } from "react";

import { cn } from "../../lib/utils";

// Chip is a `key = value` filter (the reel's `.chip`), mono at 11.5px.
// It is a button: clicking opens that filter's editor. With no value it
// is the dashed "+ filter" chip.
export function Chip({ name, value, className, children, ...props }: ComponentProps<"button"> & { name?: string; value?: ReactNode }) {
  const add = name === undefined;
  return (
    <button
      type="button"
      className={cn(
        "inline-flex items-center gap-1.5 rounded-lg border border-border px-2 py-1 font-mono text-[11.5px] text-muted-foreground hover:bg-panel-2",
        add && "border-dashed",
        className,
      )}
      {...props}
    >
      {add ? (children ?? "+ filter") : (
        <>
          {name} = <b className="font-medium text-foreground">{value}</b>
        </>
      )}
    </button>
  );
}
