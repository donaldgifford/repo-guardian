import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

const badgeVariants = cva("inline-flex items-center rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap", {
  variants: {
    tone: {
      neutral: "border-border bg-muted text-foreground",
      compliant: "border-transparent bg-compliant/15 text-compliant",
      non_compliant: "border-transparent bg-non-compliant/15 text-non-compliant",
      not_applicable: "border-transparent bg-not-applicable/20 text-muted-foreground",
      unknown: "border-transparent bg-unknown/20 text-foreground",
    },
  },
  defaultVariants: { tone: "neutral" },
});

// Badge is shadcn/ui's badge with a tone per finding status.
export function Badge({ className, tone, ...props }: ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />;
}
