import type { ReactNode } from "react";

import { useRemediationEnabled } from "../api/queries";
import { Loading } from "./common";

// NOT_ENABLED is the one sentence every gated panel shows without the
// Remediation App (DESIGN-0037 §Gated panels).
export const NOT_ENABLED = "Remediation is not enabled on this deployment";

export type GatedContent = "loading" | "not_enabled" | "empty" | "content";

// gatedContent is the decision table: no answer from /status yet is
// loading; no Remediation App is "not enabled" whatever the data says;
// with the App, no rows is the caller's empty state ("No open pull
// requests"), never "not enabled".
export function gatedContent(enabled: boolean | undefined, isEmpty: boolean): GatedContent {
  if (enabled === undefined) return "loading";
  if (!enabled) return "not_enabled";
  return isEmpty ? "empty" : "content";
}

// GatedPanel wraps a remediation-backed panel body. `isEmpty` is the
// caller's judgement of its own data (false while that data loads, so
// children render their own loading state).
export function GatedPanel({ isEmpty, empty, children, className }: { isEmpty: boolean; empty: ReactNode; children: ReactNode; className?: string }) {
  const state = gatedContent(useRemediationEnabled(), isEmpty);
  switch (state) {
    case "loading":
      return <Loading />;
    case "not_enabled":
      return (
        <p className={className ?? "text-sm text-muted-foreground"} data-gated="not_enabled">
          {NOT_ENABLED}
        </p>
      );
    case "empty":
      return (
        <p className={className ?? "text-sm text-muted-foreground"} data-gated="empty">
          {empty}
        </p>
      );
    default:
      return <>{children}</>;
  }
}
