import { Component, type ErrorInfo, type ReactNode } from "react";

import { type Problem, ProblemError } from "../api/client";
import { Card, CardContent, CardHeader } from "./ui/card";

// ProblemView renders an RFC 9457 problem, or any other error as one.
// Every field is text: a problem's detail can echo request input.
export function ProblemView({ error }: { error: unknown }) {
  const p: Problem =
    error instanceof ProblemError
      ? error.problem
      : { type: "about:blank", title: "Something went wrong", status: 500, detail: error instanceof Error ? error.message : String(error) };

  return (
    <Card className="mx-auto mt-8 max-w-xl" role="alert">
      <CardHeader>
        <h2 className="text-lg font-semibold">
          {p.status} · {p.title}
        </h2>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {p.detail ? <p>{p.detail}</p> : null}
        {p.request_id ? <p className="text-muted-foreground">Request ID: {p.request_id}</p> : null}
      </CardContent>
    </Card>
  );
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
