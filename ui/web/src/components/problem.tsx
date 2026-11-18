import { ErrorState } from "@donaldgifford/design-system/blocks/error-state";
import { ApiError } from "@donaldgifford/design-system/lib/errors";
import { Component, type ErrorInfo, type ReactNode } from "react";

import { ProblemError } from "../api/client";

// Local, not in the design system: the bridge from this app's
// ProblemError (the API client's RFC 9457 error) to the package's
// ApiError and ErrorState, and the app-level ErrorBoundary. The package
// renders errors; it does not know this API client.

// apiErrorOf turns a ProblemError into the design system's ApiError, so
// ErrorState heads it "<status> · <title>", says its detail and shows
// its request id. Anything else passes through and reads as "Something
// went wrong" with its message.
export function apiErrorOf(error: unknown): unknown {
  if (!(error instanceof ProblemError)) return error;
  const p = error.problem;
  const problem = { type: p.type, title: p.title, status: p.status, detail: p.detail, request_id: p.request_id };
  return new ApiError(p.status, undefined, p.detail ?? p.title, p.request_id, problem);
}

// ProblemView renders an RFC 9457 problem, or any other error as one,
// on ErrorState. Every field is text: a problem's detail can echo
// request input.
export function ProblemView({ error }: { error: unknown }) {
  return <ErrorState error={apiErrorOf(error)} className="mx-auto mt-8 max-w-xl" />;
}

// ErrorBoundary catches render errors outside the router's own boundaries.
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: unknown }> {
  override state: { error: unknown } = { error: undefined };

  static getDerivedStateFromError(error: unknown) {
    return { error };
  }

  override componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error(error, info.componentStack);
  }

  override render() {
    return this.state.error === undefined ? this.props.children : <ProblemView error={this.state.error} />;
  }
}
