import type { Schemas } from "../../api/client";
import type { IconName } from "../icons";

// The sidebar is one array (DESIGN-0036 §Shell), so IMPL-0029 renames or
// adds an entry by editing data, not markup.

export type NavGroup = "BUSINESS" | "PUBLIC" | "DOCS";

// AppPath is every in-app destination the nav may point at; the router
// types its links against these.
export type AppPath = "/" | "/rules" | "/orgs" | "/findings" | "/status";

export interface NavItem {
  group: NavGroup;
  label: string;
  icon: IconName;
  // to is an in-app route; href an external link (opens a new tab).
  to?: AppPath;
  href?: string;
  // count reads the right-aligned mono count from /summary.
  count?: (summary: Schemas["Summary"]) => number;
}

export const NAV: readonly NavItem[] = [
  { group: "BUSINESS", label: "Fleet", to: "/", icon: "grid" },
  { group: "BUSINESS", label: "Rules", to: "/rules", icon: "list" },
  { group: "BUSINESS", label: "Orgs", to: "/orgs", icon: "boxes" },
  { group: "BUSINESS", label: "Findings", to: "/findings", icon: "activity", count: (s) => s.findings.non_compliant },
  { group: "PUBLIC", label: "Status page", to: "/status", icon: "check" },
];

export const GROUP_LABELS: Record<NavGroup, string> = { BUSINESS: "Business", PUBLIC: "Public", DOCS: "Docs" };

// navItems is the nav for one deployment: the Docs group exists only
// when the BFF's /ui/config carries a docs_url.
export function navItems(docsUrl: string | undefined): NavItem[] {
  const items = [...NAV];
  if (docsUrl) {
    items.push({ group: "DOCS", label: "Documentation", href: docsUrl, icon: "book" });
  }
  return items;
}

// navGroups orders the items by group, in declaration order.
export function navGroups(items: readonly NavItem[]): [NavGroup, NavItem[]][] {
  const order: NavGroup[] = ["BUSINESS", "PUBLIC", "DOCS"];
  return order.map((g) => [g, items.filter((i) => i.group === g)] as [NavGroup, NavItem[]]).filter(([, is]) => is.length > 0);
}
