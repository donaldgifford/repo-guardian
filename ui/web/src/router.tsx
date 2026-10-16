import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";

import { ProblemView } from "./components/problem";
import { Shell } from "./components/shell/Shell";
import { StatusView } from "./views/status";

// The findings-era views (Fleet, Rules, Orgs, Findings, Repository,
// History, Policy) went with their endpoints in IMPL-0029 7.6/7.7; the
// controls views arrive with the controls API. Until then the
// authenticated frame opens on the status page.

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
});

const homeRoute = createRoute({ getParentRoute: () => appRoute, path: "/", component: StatusView });

export { appRoute };

const routeTree = rootRoute.addChildren([statusRoute, appRoute.addChildren([homeRoute])]);

export const router = createRouter({ routeTree, defaultErrorComponent: ProblemView });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
