import { Dialog as DialogPrimitive } from "radix-ui";
import type { ComponentProps, ReactNode } from "react";

import { cn } from "../../lib/utils";
import { DialogOverlay } from "./dialog";

// Drawer is a radix Dialog placed as a 470px panel on the chosen side
// (DESIGN-0036 §Drawer): scrim, slide at 200ms (off under reduced
// motion through the global rule), focus trap, focus return, Escape.
// Open state is the caller's: Findings keeps it in `?open=`.
export const Drawer = DialogPrimitive.Root;
export const DrawerTrigger = DialogPrimitive.Trigger;
export const DrawerClose = DialogPrimitive.Close;
export const DrawerTitle = DialogPrimitive.Title;
export const DrawerDescription = DialogPrimitive.Description;

export function DrawerContent({
  side = "right",
  className,
  children,
  ...props
}: ComponentProps<typeof DialogPrimitive.Content> & { side?: "left" | "right"; children: ReactNode }) {
  return (
    <DialogPrimitive.Portal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-side={side}
        className={cn(
          "fixed inset-y-0 z-50 flex w-[470px] max-w-[92vw] flex-col overflow-y-auto bg-panel px-[18px] py-4 text-foreground shadow-[-12px_0_32px_rgba(0,0,0,0.45)] outline-none",
          side === "right" ? "right-0 border-l border-border animate-drawer-right" : "left-0 border-r border-border animate-drawer-left",
          className,
        )}
        {...props}
      >
        {children}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}

// DrawerHeader is the title row with the close button at its right.
export function DrawerHeader({ title, subtitle, className }: { title: ReactNode; subtitle?: ReactNode; className?: string }) {
  return (
    <header className={cn("mb-3.5", className)}>
      <div className="flex items-center justify-between gap-2">
        <DialogPrimitive.Title className="text-[15px] font-semibold">{title}</DialogPrimitive.Title>
        <DialogPrimitive.Close
          aria-label="Close"
          className="flex size-6 flex-none items-center justify-center rounded-lg text-muted-foreground hover:bg-panel-2 hover:text-foreground"
        >
          ×
        </DialogPrimitive.Close>
      </div>
      {subtitle !== undefined ? <DialogPrimitive.Description className="mt-[3px] font-mono text-xs text-muted-foreground">{subtitle}</DialogPrimitive.Description> : null}
    </header>
  );
}
