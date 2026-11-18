import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";

import { cn } from "@donaldgifford/design-system/lib/utils";

// Button is the mockup's `.btn`: 28px, hairline border on panel, 12px
// text. Outline is the default because the UI is read-only and has no
// primary action to shout about; ghost drops the border for toolbars.
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 rounded-lg text-xs font-medium whitespace-nowrap transition-colors disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        outline: "border border-border bg-card text-foreground hover:bg-muted",
        ghost: "border border-transparent text-muted-foreground hover:bg-muted hover:text-foreground",
        primary: "border border-transparent bg-primary text-background hover:bg-primary/90",
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
