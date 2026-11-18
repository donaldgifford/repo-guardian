import type { ReactNode } from "react";

import { cn } from "@donaldgifford/design-system/lib/utils";

// DetailKV is the key/value block: a two-column list, keys muted.
export function DetailKV({ rows, className }: { rows: readonly { key: string; value: ReactNode }[]; className?: string }) {
  return (
    <dl className={cn("grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[13px]", className)}>
      {rows.map((r) => (
        <div key={r.key} className="contents">
          <dt className="text-muted-foreground">{r.key}</dt>
          <dd className="min-w-0">{r.value}</dd>
        </div>
      ))}
    </dl>
  );
}
