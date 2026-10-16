import { DropdownMenu as MenuPrimitive } from "radix-ui";
import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

// DropdownMenu is radix's menu with the popover's look: arrow keys move,
// Enter activates, Escape closes and focus returns to the trigger.
export const DropdownMenu = MenuPrimitive.Root;
export const DropdownMenuTrigger = MenuPrimitive.Trigger;
export const DropdownMenuGroup = MenuPrimitive.Group;

export function DropdownMenuContent({ className, align = "end", sideOffset = 6, ...props }: ComponentProps<typeof MenuPrimitive.Content>) {
  return (
    <MenuPrimitive.Portal>
      <MenuPrimitive.Content
        align={align}
        sideOffset={sideOffset}
        className={cn(
          "z-50 min-w-[200px] rounded-lg border border-border bg-panel p-1.5 text-[13px] text-foreground shadow-[0_12px_36px_rgba(0,0,0,0.45)] outline-none",
          "data-[state=open]:animate-in data-[state=closed]:animate-out",
          className,
        )}
        {...props}
      />
    </MenuPrimitive.Portal>
  );
}

export function DropdownMenuItem({ className, ...props }: ComponentProps<typeof MenuPrimitive.Item>) {
  return (
    <MenuPrimitive.Item
      className={cn(
        "flex w-full cursor-default items-center justify-between gap-3 rounded-lg px-2 py-1.5 outline-none select-none data-[highlighted]:bg-panel-2 data-[disabled]:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

export function DropdownMenuLabel({ className, ...props }: ComponentProps<typeof MenuPrimitive.Label>) {
  return <MenuPrimitive.Label className={cn("px-2 pt-1.5 pb-1 text-xs text-muted-foreground", className)} {...props} />;
}

export function DropdownMenuSeparator({ className, ...props }: ComponentProps<typeof MenuPrimitive.Separator>) {
  return <MenuPrimitive.Separator className={cn("my-1 border-t border-border", className)} {...props} />;
}
