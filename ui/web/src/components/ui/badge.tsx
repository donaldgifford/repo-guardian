import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

// The badge recipe (DESIGN-0036 §Badges): mono 12px, 5px 7px padding,
// no radius, an 18% tint of the tone behind text pulled 65% toward
// white (65%, not the mockup's 75%: the bad tone on a selected row
// fell under AA at 75%). Every tone only sets --tone; the recipe is
// written once.
const tinted = "[background:color-mix(in_srgb,var(--tone)_18%,transparent)] [color:color-mix(in_srgb,var(--tone)_65%,white)]";

const badgeVariants = cva("inline-flex items-center px-[7px] py-[5px] font-mono text-xs leading-none whitespace-nowrap", {
  variants: {
    tone: {
      ok: `[--tone:var(--color-ok)] ${tinted}`,
      warn: `[--tone:var(--color-warn)] ${tinted}`,
      bad: `[--tone:var(--color-bad)] ${tinted}`,
      info: `[--tone:var(--color-info)] ${tinted}`,
      mute: `[--tone:var(--color-muted-foreground)] ${tinted}`,
      outline: "border border-border text-muted-foreground",
    },
  },
  defaultVariants: { tone: "mute" },
});

export type BadgeProps = ComponentProps<"span"> & VariantProps<typeof badgeVariants>;

export function Badge({ className, tone, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />;
}
