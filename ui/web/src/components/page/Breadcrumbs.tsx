import { Link, useMatches } from "@tanstack/react-router";
import { createContext, Fragment, type ReactNode, useContext, useEffect, useMemo, useState } from "react";

import type { AppPath } from "../shell/nav";

export interface Crumb {
  label: string;
  to?: AppPath;
}

interface MatchLike {
  routeId: string;
  params?: Record<string, string>;
}

// sections maps a route to its section crumb and, for an item route,
// the param that names the item.
const sections: Record<string, Crumb & { item?: string }> = {
  "/app/": { label: "Fleet", to: "/" },
  "/app/controls": { label: "Controls", to: "/controls" },
  "/app/controls/$id": { label: "Controls", to: "/controls", item: "id" },
  "/app/findings": { label: "Findings", to: "/findings" },
  "/app/orgs": { label: "Orgs", to: "/orgs" },
  "/app/orgs/$org": { label: "Orgs", to: "/orgs", item: "org" },
  "/app/repositories": { label: "Repositories", to: "/repositories" },
  "/app/repositories/$id": { label: "Repositories", to: "/repositories", item: "id" },
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
  const item = section.item ? match.params?.[section.item] : undefined;
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

export function BreadcrumbList({ crumbs }: { crumbs: Crumb[] }) {
  return (
    <nav aria-label="Breadcrumb" className="mb-2 text-xs text-muted-foreground">
      <ol className="flex flex-wrap items-center">
        {crumbs.map((c, i) => {
          const leaf = i === crumbs.length - 1;
          return (
            <Fragment key={`${c.label}-${i}`}>
              {i > 0 ? (
                <li aria-hidden="true" className="mx-1.5 opacity-60">
                  /
                </li>
              ) : null}
              <li>
                {leaf ? (
                  <b className="font-medium text-foreground" aria-current="page">
                    {c.label}
                  </b>
                ) : c.to ? (
                  <Link to={c.to} className="hover:text-foreground">
                    {c.label}
                  </Link>
                ) : (
                  <span>{c.label}</span>
                )}
              </li>
            </Fragment>
          );
        })}
      </ol>
    </nav>
  );
}
