import { useQueryClient } from "@tanstack/react-query";
import { Link, useMatches } from "@tanstack/react-router";
import { Fragment } from "react";

import type { Schemas } from "../../api/client";

export interface Crumb {
  label: string;
  to?: "/" | "/rules" | "/orgs" | "/findings" | "/history" | "/policy";
}

interface MatchLike {
  routeId: string;
  params: Record<string, unknown>;
  search: Record<string, unknown>;
}

const sections: Record<string, Crumb> = {
  "/app/": { label: "Fleet", to: "/" },
  "/app/rules": { label: "Rules", to: "/rules" },
  "/app/rules/$kind/$name": { label: "Rules", to: "/rules" },
  "/app/orgs": { label: "Orgs", to: "/orgs" },
  "/app/orgs/$org": { label: "Orgs", to: "/orgs" },
  "/app/findings": { label: "Findings", to: "/findings" },
  "/app/repos/$id": { label: "Repositories" },
  "/app/history": { label: "History", to: "/history" },
  "/app/policy": { label: "Policy", to: "/policy" },
};

// crumbsFor derives `Repo Guardian / <Section> [/ <item>]` from the
// deepest match: the item is the route param, the Findings drawer's
// `?open=` rule, or a repository's name when its query is cached.
export function crumbsFor(match: MatchLike | undefined, repositoryName?: string): Crumb[] {
  const out: Crumb[] = [{ label: "Repo Guardian", to: "/" }];
  if (!match) return out;
  const section = sections[match.routeId];
  if (!section) return out;
  out.push(section);
  const p = match.params;
  switch (match.routeId) {
    case "/app/rules/$kind/$name":
      out.push({ label: String(p.name ?? "") });
      break;
    case "/app/orgs/$org":
      out.push({ label: String(p.org ?? "") });
      break;
    case "/app/repos/$id":
      out.push({ label: repositoryName ?? `#${String(p.id ?? "")}` });
      break;
    case "/app/findings": {
      const open = match.search.open;
      if (typeof open === "string" && open !== "") {
        const [, kind, ...name] = open.split("/");
        if (name.length > 0) out.push({ label: `rule ${name.join("/")} (${kind ?? ""})` });
      }
      break;
    }
    default:
      break;
  }
  // The section alone is the leaf on list pages: Fleet is both section
  // and item, so it is not repeated.
  return out;
}

export function Breadcrumbs() {
  const matches = useMatches();
  const queryClient = useQueryClient();
  const last = matches[matches.length - 1] as MatchLike | undefined;
  const repoId = last?.routeId === "/app/repos/$id" ? Number(last.params.id) : undefined;
  const repo = repoId !== undefined ? queryClient.getQueryData<Schemas["RepositoryDetail"]>(["repository", repoId]) : undefined;
  const crumbs = crumbsFor(last, repo ? `${repo.repository.org}/${repo.repository.name}` : undefined);
  return <BreadcrumbList crumbs={crumbs} />;
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
