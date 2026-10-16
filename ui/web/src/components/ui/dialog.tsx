import { Dialog as DialogPrimitive } from "radix-ui";
import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

// Dialog is radix's modal dialog: focus moves in on open, is trapped,
// and returns to the opener on close; Escape and the scrim dismiss.
export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export const DialogClose = DialogPrimitive.Close;
export const DialogTitle = DialogPrimitive.Title;
export const DialogDescription = DialogPrimitive.Description;

export function DialogOverlay({ className, ...props }: ComponentProps<typeof DialogPrimitive.Overlay>) {
  return <DialogPrimitive.Overlay className={cn("fixed inset-0 z-40 bg-black/40 data-[state=open]:animate-in data-[state=closed]:animate-out", className)} {...props} />;
}

// DialogContent is the centred panel; Drawer builds its own placement
// on the same primitives.
export function DialogContent({ className, children, ...props }: ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogOverlay />
      <DialogPrimitive.Content
        className={cn(
          "fixed top-1/2 left-1/2 z-50 w-[min(92vw,560px)] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-panel p-4 text-foreground shadow-[0_12px_36px_rgba(0,0,0,0.45)] outline-none",
          "data-[state=open]:animate-in data-[state=closed]:animate-out",
          className,
        )}
        {...props}
      >
        {children}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}
