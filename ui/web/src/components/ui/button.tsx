import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

// Button is the mockup's `.btn`: 28px, hairline border on panel, 12px
// text. Outline is the default because the UI is read-only and has no
// primary action to shout about; ghost drops the border for toolbars.
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 rounded-lg text-xs font-medium whitespace-nowrap transition-colors disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        outline: "border border-border bg-panel text-foreground hover:bg-panel-2",
        ghost: "border border-transparent text-muted-foreground hover:bg-panel-2 hover:text-foreground",
        primary: "border border-transparent bg-accent text-background hover:bg-accent/90",
      },
      size: { default: "h-7 px-2.5", sm: "h-[22px] px-[7px] text-[11px]" },
    },
    defaultVariants: { variant: "outline", size: "default" },
  },
);

export type ButtonProps = ComponentProps<"button"> & VariantProps<typeof buttonVariants>;

export function Button({ className, variant, size, type = "button", ...props }: ButtonProps) {
  return <button type={type} className={cn(buttonVariants({ variant, size }), className)} {...props} />;
}
