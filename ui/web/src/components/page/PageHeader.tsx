import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

// PageHeader is the block under the breadcrumbs on every authenticated
// page (DESIGN-0036 §Page anatomy): a 20px title with an optional
// outline badge, a one-line muted subtitle (endpoint names in <code>),
// and right-aligned controls.
export function PageHeader({ title, badge, subtitle, controls, className }: { title: ReactNode; badge?: ReactNode; subtitle?: ReactNode; controls?: ReactNode; className?: string }) {
  return (
    <div className={cn("mb-3.5 flex flex-wrap items-end justify-between gap-4", className)}>
      <div className="min-w-0">
        <h1 className="flex items-center gap-2.5 text-xl font-semibold">
          <span className="truncate">{title}</span>
          {badge}
        </h1>
        {subtitle !== undefined ? <p className="mt-[3px] text-[13px] text-muted-foreground [&_code]:font-mono [&_code]:text-xs">{subtitle}</p> : null}
      </div>
      {controls !== undefined ? <div className="flex flex-wrap items-center gap-2">{controls}</div> : null}
    </div>
  );
}
