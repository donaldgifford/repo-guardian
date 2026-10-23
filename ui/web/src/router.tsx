import { createRootRoute, createRoute, createRouter, Outlet, retainSearchParams } from "@tanstack/react-router";

import { ProblemView } from "./components/problem";
import { Shell } from "./components/shell/Shell";
import { parseRangeSearch } from "./lib/range";
import { parseScopeSearch } from "./lib/scope";
import { ControlsView, ControlView } from "./views/controls";
import { FindingsView, parseFindingsSearch } from "./views/findings";
import { FleetView } from "./views/fleet";
import { HistoryView, parseHistorySearch } from "./views/history";
import { OrgsView, OrgView } from "./views/orgs";
import { PolicyView } from "./views/policy";
import { parseRepositoriesSearch, parseRepositorySearch, RepositoriesView, RepositoryView } from "./views/repositories";
import { StatusView } from "./views/status";

const rootRoute = createRootRoute({
  component: Outlet,
  errorComponent: ProblemView,
});

// statusRoute is public: it sits outside the layout that asks /me.
const statusRoute = createRoute({ getParentRoute: () => rootRoute, path: "/status", component: StatusView });

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "app",
  component: Shell,
  errorComponent: ProblemView,
  // The org scope and the range ride along from page to page, as the
  // mockup's router keeps them; a view's own filters stay behind
  // (patchSearch's note).
  search: { middlewares: [retainSearchParams(["org", "days"])] },
});

const fleetRoute = createRoute({ getParentRoute: () => appRoute, path: "/", validateSearch: parseScopeSearch, component: FleetView });
const controlsRoute = createRoute({ getParentRoute: () => appRoute, path: "/controls", validateSearch: parseScopeSearch, component: ControlsView });
const controlRoute = createRoute({ getParentRoute: () => appRoute, path: "/controls/$id", validateSearch: parseScopeSearch, component: ControlView });
const findingsRoute = createRoute({ getParentRoute: () => appRoute, path: "/findings", validateSearch: parseFindingsSearch, component: FindingsView });
const orgsRoute = createRoute({ getParentRoute: () => appRoute, path: "/orgs", component: OrgsView });
const orgRoute = createRoute({ getParentRoute: () => appRoute, path: "/orgs/$org", validateSearch: parseRangeSearch, component: OrgView });
const repositoriesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/repositories",
  validateSearch: parseRepositoriesSearch,
  component: RepositoriesView,
});
const repositoryRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/repositories/$id",
  validateSearch: parseRepositorySearch,
  component: RepositoryView,
});
const historyRoute = createRoute({ getParentRoute: () => appRoute, path: "/history", validateSearch: parseHistorySearch, component: HistoryView });
const policyRoute = createRoute({ getParentRoute: () => appRoute, path: "/policy", component: PolicyView });

export { appRoute };

const routeTree = rootRoute.addChildren([
  statusRoute,
  appRoute.addChildren([fleetRoute, controlsRoute, controlRoute, findingsRoute, orgsRoute, orgRoute, repositoriesRoute, repositoryRoute, historyRoute, policyRoute]),
]);

export const router = createRouter({ routeTree, defaultErrorComponent: ProblemView });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
