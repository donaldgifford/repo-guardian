// index.css is the design system's stylesheet, which also bundles the
// fonts (latin subsets): Vite emits the woff2 files as hashed assets, so
// the CSP's implicit font-src 'self' holds and no request leaves the origin.
import "./index.css";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { TimeZoneProvider } from "@donaldgifford/design-system/blocks/time";
import { createRoot } from "react-dom/client";

import { ProblemError } from "./api/client";
import { ErrorBoundary } from "./components/problem";
import { ZONE_STORAGE_KEY } from "./lib/zone";
import { router } from "./router";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      // A problem is an answer, not a flake: retry only what might be
      // transient (5xx and network errors), and never a 4xx.
      retry: (count, error) => count < 2 && !(error instanceof ProblemError && error.problem.status < 500),
      // Errors surface in the route's error boundary as a problem.
      throwOnError: true,
    },
  },
});

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing #root");
}

createRoot(root).render(
  <StrictMode>
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <TimeZoneProvider storageKey={ZONE_STORAGE_KEY}>
          <RouterProvider router={router} />
        </TimeZoneProvider>
      </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>,
);
