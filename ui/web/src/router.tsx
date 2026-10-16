import { createRootRoute, createRoute, createRouter, Outlet, retainSearchParams } from "@tanstack/react-router";

import { ProblemView } from "./components/problem";
import { Shell } from "./components/shell/Shell";
import { FindingsView, parseFindingsSearch } from "./views/findings";
import { FleetView, parseFleetSearch } from "./views/fleet";
import { HistoryView, parseHistorySearch } from "./views/history";
import { OrgView, OrgsView, parseOrgsSearch } from "./views/orgs";
import { PolicyView } from "./views/policy";
import { RepositoryView } from "./views/repository";
import { parseRulesSearch, RuleView, RulesView } from "./views/rules";
import { StatusView } from "./views/status";
import { isRange, type Range } from "./lib/range";

// OrgSearch is the org scope and time range every authenticated view
// shares. Both live in the URL, so a scoped view is a link that can be
// shared, and both are retained across navigation.
export interface OrgSearch {
  org?: string;
  range?: Range;
}

export function parseOrgSearch(s: Record<string, unknown>): OrgSearch {
  return {
    ...(typeof s.org === "string" && s.org !== "" ? { org: s.org } : {}),
    ...(isRange(s.range) ? { range: s.range } : {}),
  };
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
  search: { middlewares: [retainSearchParams(["org", "range"])] },
  component: Shell,
  errorComponent: ProblemView,
});

const fleetRoute = createRoute({ getParentRoute: () => appRoute, path: "/", validateSearch: parseFleetSearch, component: FleetView });
const rulesRoute = createRoute({ getParentRoute: () => appRoute, path: "/rules", validateSearch: parseRulesSearch, component: RulesView });
export const ruleRoute = createRoute({ getParentRoute: () => appRoute, path: "/rules/$kind/$name", component: RuleView });
const orgsRoute = createRoute({ getParentRoute: () => appRoute, path: "/orgs", validateSearch: parseOrgsSearch, component: OrgsView });
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
