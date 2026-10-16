import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

// Avatar is a 20px square in the avatar colour with initials; there is
// no image path (DESIGN-0036 §Shell).
export function Avatar({ initials, className, ...props }: ComponentProps<"span"> & { initials: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn("inline-block size-5 rounded-lg bg-avatar text-center font-sans text-[10px] leading-5 font-semibold text-white select-none", className)}
      {...props}
    >
      {initials}
    </span>
  );
}
