import "./index.css";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { ProblemError } from "./api/client";
import { ErrorBoundary } from "./components/problem";
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
        <RouterProvider router={router} />
      </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>,
);
