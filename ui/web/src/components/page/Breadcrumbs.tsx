import { Link, useMatches } from "@tanstack/react-router";
import { Fragment } from "react";

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
  "/app/orgs": { label: "Orgs", to: "/orgs" },
  "/app/orgs/$org": { label: "Orgs", to: "/orgs", item: "org" },
  "/app/repositories": { label: "Repositories", to: "/repositories" },
  "/app/repositories/$id": { label: "Repositories", to: "/repositories", item: "id" },
  "/app/history": { label: "History", to: "/history" },
  "/app/policy": { label: "Policy", to: "/policy" },
};

// crumbsFor derives `Repo Guardian / <Section> [/ <item>]` from the
// deepest match.
export function crumbsFor(match: MatchLike | undefined): Crumb[] {
  const out: Crumb[] = [{ label: "Repo Guardian", to: "/" }];
  if (!match) return out;
  const section = sections[match.routeId];
  if (!section) return out;
  out.push({ label: section.label, ...(section.to ? { to: section.to } : {}) });
  const item = section.item ? match.params?.[section.item] : undefined;
  if (item) out.push({ label: item });
  return out;
}

export function Breadcrumbs() {
  const matches = useMatches();
  const last = matches[matches.length - 1] as MatchLike | undefined;
  return <BreadcrumbList crumbs={crumbsFor(last)} />;
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
