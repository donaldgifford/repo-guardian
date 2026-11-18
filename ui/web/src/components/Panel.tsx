import type { ComponentProps, ReactNode } from "react";

import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@donaldgifford/design-system/components/card";
import { cn } from "@donaldgifford/design-system/lib/utils";

// Local, not in the design system: Panel is this app's surface
// composed from the package's Card (header, title, action, body), with
// a stats strip and flush-table body that are the mockup's, not a
// general card's. The package keeps Card unopinionated (DESIGN-0039 D§1).

interface PanelProps extends Omit<ComponentProps<"section">, "title"> {
  title?: ReactNode;
  subtitle?: ReactNode;
  // right is the header's right-hand slot: tabs, a toggle, an action.
  right?: ReactNode;
  // strip is a StatStrip rendered between the header and the body.
  strip?: ReactNode;
  // bodyClassName styles the body; use "p-0" for a flush table.
  bodyClassName?: string;
}

// Panel is the app's one surface (DESIGN-0036 §Surfaces) on the design
// system's Card, always a section. The card's own gap and padding are
// zeroed so the header and body pad themselves, which keeps a flush
// table flush. Without a title it is the bare surface: the reset also
// undoes the card's flex column, so a caller's own layout classes win.
export function Panel({ title, subtitle, right, strip, bodyClassName, className, children, ...props }: PanelProps) {
  const surface = (extra: string) => (p: ComponentProps<"section">) => <section {...p} {...props} className={cn(p.className, extra, className)} />;
  if (title === undefined) {
    return <Card render={surface("block flex-row gap-0 py-0")}>{children}</Card>;
  }
  return (
    <Card render={surface("gap-0 py-0")}>
      <CardHeader className="gap-x-3 gap-y-0 px-3.5 pt-2.5">
        <CardTitle className="min-w-0 text-[13px]" render={(p) => <h3 {...p} />}>
          {title}
        </CardTitle>
        {subtitle !== undefined ? <CardDescription className="text-xs">{subtitle}</CardDescription> : null}
        {right !== undefined ? <CardAction className="flex flex-none items-center gap-1.5">{right}</CardAction> : null}
      </CardHeader>
      {strip}
      <CardContent className={cn("py-2.5", bodyClassName)}>{children}</CardContent>
    </Card>
  );
}

// PanelBody is a bare panel's padded body.
export function PanelBody({ className, ...props }: ComponentProps<"div">) {
  return <CardContent className={cn("py-2.5", className)} {...props} />;
}
