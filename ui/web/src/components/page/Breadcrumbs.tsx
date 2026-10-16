import { Link, useMatches } from "@tanstack/react-router";
import { Fragment } from "react";

export interface Crumb {
  label: string;
  to?: "/" | "/status";
}

interface MatchLike {
  routeId: string;
}

const sections: Record<string, Crumb> = {
  "/app/": { label: "Overview", to: "/" },
};

// crumbsFor derives `Repo Guardian / <Section>` from the deepest match.
// The controls views add their sections here, with the item crumb
// beside them.
export function crumbsFor(match: MatchLike | undefined): Crumb[] {
  const out: Crumb[] = [{ label: "Repo Guardian", to: "/" }];
  if (!match) return out;
  const section = sections[match.routeId];
  if (section) out.push(section);
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
