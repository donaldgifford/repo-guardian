import { Popover as PopoverPrimitive } from "radix-ui";
import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

// Popover is radix's, styled as the mockup's `.pop`: a panel with a
// hairline border and a soft shadow, 200ms fade that the global
// reduced-motion rule switches off. Radix supplies focus, Escape and
// outside-click dismissal.
export const Popover = PopoverPrimitive.Root;
export const PopoverTrigger = PopoverPrimitive.Trigger;
export const PopoverAnchor = PopoverPrimitive.Anchor;

export function PopoverContent({ className, align = "start", sideOffset = 6, ...props }: ComponentProps<typeof PopoverPrimitive.Content>) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        align={align}
        sideOffset={sideOffset}
        className={cn(
          "z-50 min-w-[200px] rounded-lg border border-border bg-panel p-1.5 text-[13px] text-foreground shadow-[0_12px_36px_rgba(0,0,0,0.45)] outline-none",
          "data-[state=open]:animate-in data-[state=closed]:animate-out",
          className,
        )}
        {...props}
      />
    </PopoverPrimitive.Portal>
  );
}

// PopoverHeading is the small uppercase group label inside a popover.
export function PopoverHeading({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("px-2 pt-2 pb-1 text-[10.5px] font-semibold tracking-[0.08em] text-muted-foreground uppercase", className)} {...props} />;
}

// PopoverItem is one selectable row: label left, optional mono detail
// right, current row lifted onto panel-2.
export function PopoverItem({ className, current = false, detail, children, ...props }: ComponentProps<"button"> & { current?: boolean; detail?: string }) {
  return (
    <button
      type="button"
      aria-current={current || undefined}
      className={cn("flex w-full items-center justify-between gap-3 rounded-lg px-2 py-1.5 text-left hover:bg-panel-2", current && "bg-panel-2", className)}
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
