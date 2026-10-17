import type { IconName } from "../icons";

// The sidebar is one array (DESIGN-0036 §Shell), so the controls views
// add an entry by editing data, not markup.

export type NavGroup = "BUSINESS" | "PUBLIC" | "DOCS";

// AppPath is every in-app destination the nav may point at; the router
// types its links against these.
export type AppPath = "/" | "/controls" | "/orgs" | "/repositories" | "/history" | "/policy" | "/status";

export interface NavItem {
  group: NavGroup;
  label: string;
  icon: IconName;
  // to is an in-app route; href an external link (opens a new tab).
  to?: AppPath;
  href?: string;
}

export const NAV: readonly NavItem[] = [
  { group: "BUSINESS", label: "Fleet", to: "/", icon: "grid" },
  { group: "BUSINESS", label: "Controls", to: "/controls", icon: "shield" },
  { group: "BUSINESS", label: "Orgs", to: "/orgs", icon: "boxes" },
  { group: "BUSINESS", label: "Repositories", to: "/repositories", icon: "list" },
  { group: "BUSINESS", label: "History", to: "/history", icon: "activity" },
  { group: "BUSINESS", label: "Policy", to: "/policy", icon: "clock" },
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
