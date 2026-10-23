import type { IconName } from "../icons";

// The sidebar is one array (DESIGN-0036 §Shell), so the controls views
// add an entry by editing data, not markup.

export type NavGroup = "BUSINESS" | "PUBLIC" | "DOCS";

// AppPath is every routed in-app destination; the router types its
// links against these. PendingPath is a destination declared in the nav
// before its route lands (IMPL-0032 2.7); it moves into AppPath with
// the route.
export type AppPath = "/" | "/controls" | "/findings" | "/orgs" | "/repositories" | "/remediations" | "/history" | "/policy" | "/status";
// PendingPath is empty now that every declared entry has its route.
export type PendingPath = never;

// NavBadge names the count the Sidebar renders beside an entry
// (DESIGN-0037 §Shell): findings is Summary.compliance.non_compliant;
// remediations is the outstanding count, blank without the App.
export type NavBadge = "findings" | "remediations";

export interface NavItem {
  group: NavGroup;
  label: string;
  icon: IconName;
  // to is an in-app route; href an external link (opens a new tab).
  to?: AppPath | PendingPath;
  href?: string;
  badge?: NavBadge;
  // hidden keeps an entry out of the sidebar until its route lands
  // (IMPL-0032 2.7): the data is declared once, the page un-hides it.
  hidden?: boolean;
}

// RoutedNavItem is an entry the sidebar may link: every un-hidden
// entry's `to` is a routed path.
export type RoutedNavItem = Omit<NavItem, "to"> & { to?: AppPath };

const routed = (n: NavItem): n is RoutedNavItem => !n.hidden;

export const NAV: readonly NavItem[] = [
  { group: "BUSINESS", label: "Fleet", to: "/", icon: "grid" },
  { group: "BUSINESS", label: "Findings", to: "/findings", icon: "alert", badge: "findings" },
  { group: "BUSINESS", label: "Controls", to: "/controls", icon: "shield" },
  { group: "BUSINESS", label: "Orgs", to: "/orgs", icon: "boxes" },
  { group: "BUSINESS", label: "Repositories", to: "/repositories", icon: "list" },
  { group: "BUSINESS", label: "Remediations", to: "/remediations", icon: "pr", badge: "remediations" },
  { group: "BUSINESS", label: "History", to: "/history", icon: "activity" },
  { group: "BUSINESS", label: "Policy", to: "/policy", icon: "clock" },
  { group: "PUBLIC", label: "Status page", to: "/status", icon: "check" },
];

export const GROUP_LABELS: Record<NavGroup, string> = { BUSINESS: "Business", PUBLIC: "Public", DOCS: "Docs" };

// navItems is the nav for one deployment: hidden entries are left out,
// and the Docs group exists only when the BFF's /ui/config carries a
// docs_url.
export function navItems(docsUrl: string | undefined): RoutedNavItem[] {
  const items: RoutedNavItem[] = NAV.filter(routed);
  if (docsUrl) {
    items.push({ group: "DOCS", label: "Documentation", href: docsUrl, icon: "book" });
  }
  return items;
}

// navGroups orders the items by group, in declaration order.
export function navGroups<T extends NavItem | RoutedNavItem>(items: readonly T[]): [NavGroup, T[]][] {
  const order: NavGroup[] = ["BUSINESS", "PUBLIC", "DOCS"];
  return order.map((g) => [g, items.filter((i) => i.group === g)] as [NavGroup, T[]]).filter(([, is]) => is.length > 0);
}
