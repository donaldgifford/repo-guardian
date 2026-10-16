import type { ComponentProps, ReactNode } from "react";

import { cn } from "../../lib/utils";

interface CardProps extends Omit<ComponentProps<"section">, "title"> {
  title?: ReactNode;
  subtitle?: ReactNode;
  // right is the header's right-hand slot: tabs, a toggle, an action.
  right?: ReactNode;
  // strip is a StatStrip rendered between the header and the body.
  strip?: ReactNode;
  // bodyClassName styles the body wrapper; use "p-0" for a flush table.
  bodyClassName?: string;
}

// Card is the one surface (DESIGN-0036 §Surfaces): panel, hairline
// border, 3px radius, no shadow. With a title it renders the header,
// the optional strip and a padded body; without one it is the bare
// surface and children lay themselves out.
export function Card({ title, subtitle, right, strip, bodyClassName, className, children, ...props }: CardProps) {
  const surface = cn("rounded-lg border border-border bg-panel", className);
  if (title === undefined) {
    return (
      <section className={surface} {...props}>
        {children}
      </section>
    );
  }
  return (
    <section className={surface} {...props}>
      <CardHeader title={title} subtitle={subtitle} right={right} />
      {strip}
      <CardBody className={bodyClassName}>{children}</CardBody>
    </section>
  );
}

export function CardHeader({ title, subtitle, right, className }: { title: ReactNode; subtitle?: ReactNode; right?: ReactNode; className?: string }) {
  return (
    <header className={cn("flex items-start justify-between gap-3 px-3.5 pt-2.5", className)}>
      <div className="min-w-0">
        <h3 className="text-[13px] font-semibold">{title}</h3>
        {subtitle !== undefined ? <p className="mt-px text-xs text-muted-foreground">{subtitle}</p> : null}
      </div>
      {right !== undefined ? <div className="flex flex-none items-center gap-1.5">{right}</div> : null}
    </header>
  );
}

export function CardBody({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("px-3.5 py-2.5", className)} {...props} />;
}

// CardContent is kept for views not yet converted (IMPL-0031 6.6).
export const CardContent = CardBody;
