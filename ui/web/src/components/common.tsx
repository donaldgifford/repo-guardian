import type { ReactNode } from "react";

// Local, not in the design system: one-line, in-panel loading and empty
// text. The package's EmptyState is a page-level block (icon, heading,
// dashed border) and its skeletons stand in for layout, neither of
// which suits a line inside a panel or a table.

export function Loading() {
  return <p className="text-sm text-muted-foreground">Loading…</p>;
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}

