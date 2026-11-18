import { Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList as List, BreadcrumbPage, BreadcrumbSeparator } from "@donaldgifford/design-system/components/breadcrumb";
import { useMatches, useNavigate, useRouter } from "@tanstack/react-router";
import { createContext, Fragment, type MouseEvent, type ReactNode, useContext, useEffect, useMemo, useState } from "react";

import type { AppPath } from "../shell/nav";

export interface Crumb {
  label: string;
  to?: AppPath;
}

interface MatchLike {
  routeId: string;
  params?: Record<string, string>;
  search?: Record<string, unknown>;
}

// sections maps a route to its section crumb and, for an item route,
// the param that names the item — a path param, or with `inSearch` the
// search param (the Remediations drawer's `?open=`).
const sections: Record<string, Crumb & { item?: string; inSearch?: boolean }> = {
  "/app/": { label: "Fleet", to: "/" },
  "/app/controls": { label: "Controls", to: "/controls" },
  "/app/controls/$id": { label: "Controls", to: "/controls", item: "id" },
  "/app/findings": { label: "Findings", to: "/findings" },
  "/app/orgs": { label: "Orgs", to: "/orgs" },
  "/app/orgs/$org": { label: "Orgs", to: "/orgs", item: "org" },
  "/app/repositories": { label: "Repositories", to: "/repositories" },
  "/app/repositories/$id": { label: "Repositories", to: "/repositories", item: "id" },
  "/app/remediations": { label: "Remediations", to: "/remediations", item: "open", inSearch: true },
  "/app/history": { label: "History", to: "/history" },
  "/app/policy": { label: "Policy", to: "/policy" },
};

// ItemLabel is a view's name for the item its route param identifies,
// e.g. `acme/widgets` for repository `1` (INV-0023 F2). It applies only
// while the param still matches, so a label never outlives its page.
export interface ItemLabel {
  param: string;
  label: string;
}

// crumbsFor derives `Repo Guardian / <Section> [/ <item>]` from the
// deepest match. The item reads the view's label when one is set for
// this param; until then a name-like param shows as-is and a numeric
// id as `…`, never as a bare number.
export function crumbsFor(match: MatchLike | undefined, itemLabel?: ItemLabel): Crumb[] {
  const out: Crumb[] = [{ label: "Repo Guardian", to: "/" }];
  if (!match) return out;
  const section = sections[match.routeId];
  if (!section) return out;
  out.push({ label: section.label, ...(section.to ? { to: section.to } : {}) });
  const raw = section.item ? (section.inSearch ? match.search?.[section.item] : match.params?.[section.item]) : undefined;
  const item = typeof raw === "string" ? raw : typeof raw === "number" ? String(raw) : undefined;
  if (item) {
    const label = itemLabel?.param === item ? itemLabel.label : /^\d+$/.test(item) ? "…" : item;
    out.push({ label });
  }
  return out;
}

interface ItemLabelState {
  value: ItemLabel | undefined;
  set: (v: ItemLabel | undefined) => void;
}

const ItemLabelContext = createContext<ItemLabelState | undefined>(undefined);

// CrumbLabelProvider holds the item label a view sets for Breadcrumbs.
export function CrumbLabelProvider({ children }: { children: ReactNode }) {
  const [value, set] = useState<ItemLabel | undefined>(undefined);
  const state = useMemo(() => ({ value, set }), [value]);
  return <ItemLabelContext.Provider value={state}>{children}</ItemLabelContext.Provider>;
}

// useCrumbLabel names the current item in the breadcrumbs: param is the
// route param as written in the URL, label what to show once known.
// It clears when the view unmounts or the label goes away.
export function useCrumbLabel(param: string, label: string | undefined) {
  const set = useContext(ItemLabelContext)?.set;
  useEffect(() => {
    if (!set || label === undefined) return;
    set({ param, label });
    return () => set(undefined);
  }, [set, param, label]);
}

export function Breadcrumbs() {
  const matches = useMatches();
  const itemLabel = useContext(ItemLabelContext)?.value;
  const last = matches[matches.length - 1] as MatchLike | undefined;
  return <BreadcrumbList crumbs={crumbsFor(last, itemLabel)} />;
}

// CrumbLink is a section crumb. A router Link would mark itself
// aria-current="page" whenever its route is active, so on a detail page
// the section crumb would claim to be the current page beside the leaf;
// this builds the same href and navigates the same way with no active
// state.
function CrumbLink({ to, children }: { to: AppPath; children: ReactNode }) {
  const router = useRouter();
  const navigate = useNavigate();
  const onClick = (e: MouseEvent<HTMLAnchorElement>) => {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    void navigate({ to });
  };
  return (
    <BreadcrumbLink href={router.buildLocation({ to }).href} onClick={onClick}>
      {children}
    </BreadcrumbLink>
  );
}

// BreadcrumbList is the trail on the package's breadcrumb: earlier
// crumbs link, the leaf is the current page.
export function BreadcrumbList({ crumbs }: { crumbs: Crumb[] }) {
  return (
    <Breadcrumb aria-label="Breadcrumb" className="min-w-0 text-[13px]">
      <List>
        {crumbs.map((c, i) => {
          const leaf = i === crumbs.length - 1;
          return (
            <Fragment key={`${c.label}-${i}`}>
              {i > 0 ? <BreadcrumbSeparator>/</BreadcrumbSeparator> : null}
              <BreadcrumbItem>
                {leaf ? (
                  <BreadcrumbPage className="font-medium">{c.label}</BreadcrumbPage>
                ) : c.to ? (
                  <CrumbLink to={c.to}>{c.label}</CrumbLink>
                ) : (
                  <span>{c.label}</span>
                )}
              </BreadcrumbItem>
            </Fragment>
          );
        })}
      </List>
    </Breadcrumb>
  );
}
