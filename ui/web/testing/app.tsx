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

export const testSummary: Schemas["Summary"] = {
  tracked: 412,
  parked: [{ reason: "archived", count: 7 }],
  compliant_percent: 87.2,
  findings: { compliant: 900, non_compliant: 37, not_applicable: 12, unknown: 3 },
  open_prs: [],
  stale_prs: 0,
  stale_after: "720h",
};

export const testPolicy: Schemas["Policy"] = {
  version: "v2:abcdef0123456789",
  first_seen_at: "2026-10-01T00:00:00Z",
  rollout_state: "complete",
  rules: [],
};

export interface SeededCache {
  config?: UIConfig;
  summary?: Schemas["Summary"];
  policy?: Schemas["Policy"];
  rules?: Schemas["RuleList"];
  orgs?: Schemas["OrgList"];
}

export function seededClient(seed: SeededCache = {}): QueryClient {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(["ui-config"], seed.config ?? testConfig);
  client.setQueryData(["summary"], seed.summary ?? testSummary);
  client.setQueryData(["policy"], seed.policy ?? testPolicy);
  if (seed.rules) client.setQueryData(["rules"], seed.rules);
  if (seed.orgs) client.setQueryData(["orgs"], seed.orgs);
  return client;
}

const APP_PATHS = ["/", "/rules", "/rules/$kind/$name", "/orgs", "/orgs/$org", "/findings", "/repos/$id", "/history", "/policy", "/status"] as const;

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
