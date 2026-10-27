// Fonts are bundled (DESIGN-0036 § Fonts under the CSP): Vite emits the
// woff2 files as hashed assets, so the CSP's implicit font-src 'self'
// holds and no request leaves the origin. Latin subsets only.
import "@fontsource/ibm-plex-sans/latin-400.css";
import "@fontsource/ibm-plex-sans/latin-500.css";
import "@fontsource/ibm-plex-sans/latin-600.css";
import "@fontsource/jetbrains-mono/latin-400.css";
import "@fontsource/jetbrains-mono/latin-500.css";
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
