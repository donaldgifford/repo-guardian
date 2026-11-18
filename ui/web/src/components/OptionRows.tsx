import type { ComponentProps } from "react";

import { cn } from "@donaldgifford/design-system/lib/utils";

// Local, not in the design system: rows for this app's picker
// popovers (the org scope and filter chips), with a mono detail column
// and a "current" row. The package's Popover is the container; these
// are the app's content inside it.

// PopoverHeading is the small uppercase group label inside a popover.
export function PopoverHeading({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("px-2 pt-2 pb-1 text-[10.5px] font-semibold tracking-[0.08em] text-muted-foreground uppercase", className)} {...props} />;
}

// PopoverItem is one selectable row: label left, optional mono detail
// right, current row lifted onto the muted surface.
export function PopoverItem({ className, current = false, detail, children, ...props }: ComponentProps<"button"> & { current?: boolean; detail?: string }) {
  return (
    <button
      type="button"
      aria-current={current || undefined}
      className={cn("flex w-full items-center justify-between gap-3 rounded-lg px-2 py-1.5 text-left hover:bg-muted", current && "bg-muted", className)}
      {...props}
    >
      <span className="min-w-0 truncate">{children}</span>
      {detail !== undefined ? <small className="font-mono text-[11px] text-muted-foreground">{detail}</small> : null}
    </button>
  );
}

export function PopoverSeparator({ className, ...props }: ComponentProps<"hr">) {
  return <hr className={cn("my-1 border-0 border-t border-border", className)} {...props} />;
}
