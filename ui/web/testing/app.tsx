import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from "@tanstack/react-router";
import type { ReactNode } from "react";

import type { Schemas } from "../src/api/client";
import type { UIConfig } from "../src/api/queries";
import { TimeZoneProvider } from "../src/components/time";

// Test doubles for the shell's data: the query cache is seeded, so no
// request leaves the test, and the router is a memory router whose
// every app path renders the element under test.

export const testMe: Schemas["Me"] = { subject: "user-1", name: "Ada Lovelace", all_orgs: false, orgs: ["acme"] };

export const testConfig: UIConfig = { issuer: "https://idp.example.com", public_url: "http://localhost", session_ttl_seconds: 3600, github_host: "github.com" };

export interface SeededCache {
  config?: UIConfig;
  me?: Schemas["Me"];
  orgs?: Schemas["OrgList"];
}

export function seededClient(seed: SeededCache = {}): QueryClient {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(["ui-config"], seed.config ?? testConfig);
  if (seed.me) client.setQueryData(["me"], seed.me);
  if (seed.orgs) client.setQueryData(["orgs"], seed.orgs);
  return client;
}

const APP_PATHS = ["/", "/controls", "/orgs", "/repositories", "/history", "/policy", "/status"] as const;

// testRouter renders `element` at every app path, with the Outlet-less
// layout the shell pieces expect, starting at `initial`.
export function testRouter(element: ReactNode, initial = "/") {
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const children = APP_PATHS.map((path) =>
    createRoute({
      getParentRoute: () => rootRoute,
      path,
      validateSearch: (s: Record<string, unknown>) => s,
      component: () => <>{element}</>,
    }),
  );
  return createRouter({ routeTree: rootRoute.addChildren(children), history: createMemoryHistory({ initialEntries: [initial] }) });
}

export function AppHarness({ children, seed, initial, zone = "utc" }: { children: ReactNode; seed?: SeededCache; initial?: string; zone?: "local" | "utc" }) {
  const router = testRouter(children, initial);
  return (
    <QueryClientProvider client={seededClient(seed)}>
      <TimeZoneProvider initial={zone}>
        <RouterProvider router={router} />
      </TimeZoneProvider>
    </QueryClientProvider>
  );
}
