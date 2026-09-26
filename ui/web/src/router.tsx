import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  retainSearchParams,
  useNavigate,
  useSearch,
} from "@tanstack/react-router";

import { useMe, useOrgs } from "./api/queries";
import { Loading } from "./components/common";
import { ProblemView } from "./components/problem";
import { patchSearch } from "./lib/search";
import { hasNoAccess, visibleOrgs } from "./lib/viewmodel";
import { AccessView } from "./views/access";
import { FindingsView, parseFindingsSearch } from "./views/findings";
import { FleetView } from "./views/fleet";
import { HistoryView, parseHistorySearch } from "./views/history";
import { OrgView, OrgsView } from "./views/orgs";
import { PolicyView } from "./views/policy";
import { RepositoryView } from "./views/repository";
import { RuleView, RulesView } from "./views/rules";
import { StatusView } from "./views/status";

// OrgSearch is the org filter every authenticated view shares. It lives
// in the URL, so a filtered view is a link that can be shared.
export interface OrgSearch {
  org?: string;
}

export function parseOrgSearch(s: Record<string, unknown>): OrgSearch {
  return typeof s.org === "string" && s.org !== "" ? { org: s.org } : {};
}

const rootRoute = createRootRoute({
  component: Outlet,
  errorComponent: ProblemView,
});

// statusRoute is public: it sits outside the layout that asks /me.
const statusRoute = createRoute({ getParentRoute: () => rootRoute, path: "/status", component: StatusView });

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "app",
  validateSearch: parseOrgSearch,
  search: { middlewares: [retainSearchParams(["org"])] },
  component: AppLayout,
  errorComponent: ProblemView,
});

const nav = [
  { to: "/", label: "Fleet" },
  { to: "/rules", label: "Rules" },
  { to: "/orgs", label: "Orgs" },
  { to: "/findings", label: "Findings" },
  { to: "/history", label: "History" },
  { to: "/policy", label: "Policy" },
] as const;

function AppLayout() {
  const me = useMe();
  if (!me.data) {
    return <Loading />;
  }
  if (hasNoAccess(me.data)) {
    return <AccessView me={me.data} />;
  }
  return (
    <div className="min-h-screen">
      <header className="border-b border-border">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center gap-4 px-4 py-3">
          <span className="font-semibold">repo-guardian</span>
          <nav className="flex flex-wrap gap-3 text-sm">
            {nav.map((n) => (
              <Link key={n.to} to={n.to} className="text-muted-foreground hover:text-foreground" activeProps={{ className: "text-foreground font-medium" }} activeOptions={{ exact: n.to === "/" }}>
                {n.label}
              </Link>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3 text-sm">
            <OrgFilter />
            <span className="text-muted-foreground">{me.data.name || me.data.subject}</span>
            <form method="post" action="/auth/logout">
              <button type="submit" className="underline underline-offset-2">
                Sign out
              </button>
            </form>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-7xl space-y-6 px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}

function OrgFilter() {
  const me = useMe();
  const orgs = useOrgs();
  const { org } = useSearch({ from: appRoute.id });
  const navigate = useNavigate();
  if (!me.data) {
    return null;
  }
  const options = visibleOrgs(me.data, orgs.data?.items);
  return (
    <label className="flex items-center gap-2">
      <span className="text-muted-foreground">Org</span>
      <select
        className="h-8 rounded-md border border-border bg-background px-2"
        value={org ?? ""}
        onChange={(e) => {
          const next = e.target.value;
          void navigate({ to: ".", search: (prev) => patchSearch(prev, { org: next }) });
        }}
      >
        <option value="">All orgs</option>
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </label>
  );
}

const fleetRoute = createRoute({ getParentRoute: () => appRoute, path: "/", component: FleetView });
const rulesRoute = createRoute({ getParentRoute: () => appRoute, path: "/rules", component: RulesView });
export const ruleRoute = createRoute({ getParentRoute: () => appRoute, path: "/rules/$kind/$name", component: RuleView });
const orgsRoute = createRoute({ getParentRoute: () => appRoute, path: "/orgs", component: OrgsView });
export const orgRoute = createRoute({ getParentRoute: () => appRoute, path: "/orgs/$org", component: OrgView });
export const findingsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/findings",
  validateSearch: parseFindingsSearch,
  component: FindingsView,
});
export const repositoryRoute = createRoute({ getParentRoute: () => appRoute, path: "/repos/$id", component: RepositoryView });
export const historyRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/history",
  validateSearch: parseHistorySearch,
  component: HistoryView,
});
const policyRoute = createRoute({ getParentRoute: () => appRoute, path: "/policy", component: PolicyView });

export { appRoute };

const routeTree = rootRoute.addChildren([
  statusRoute,
  appRoute.addChildren([fleetRoute, rulesRoute, ruleRoute, orgsRoute, orgRoute, findingsRoute, repositoryRoute, historyRoute, policyRoute]),
]);

export const router = createRouter({ routeTree, defaultErrorComponent: ProblemView });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
