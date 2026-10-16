import type { ReactNode } from "react";

import type { Schemas } from "../api/client";
import { describeEvidence } from "../lib/evidence";

// ExternalLink opens off-site without handing the opener or referrer
// over. href must come from a builder in lib/pr.ts, never from evidence.
export function ExternalLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-accent">
      {children} ↗
    </a>
  );
}

// Evidence renders a finding's reason and evidence as text. The
// headline and lines are strings, which React escapes: HTML in an
// assertion message shows as the characters it is.
export function Evidence({ finding }: { finding: Pick<Schemas["Finding"], "reason" | "evidence"> }) {
  const view = describeEvidence(finding);
  if (!view) {
    return null;
  }
  return (
    <div className="space-y-0.5">
      <div>
        <span className="font-mono text-xs text-muted-foreground">{view.reason}</span>
        {view.headline !== view.reason ? <span>: {view.headline}</span> : null}
      </div>
      {view.lines.map((line, i) => (
        <div key={i} className="font-mono text-xs break-all whitespace-pre-wrap">
          {line}
        </div>
      ))}
    </div>
  );
}


export function Section({ title, children, actions }: { title: string; children: ReactNode; actions?: ReactNode }) {
  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-semibold">{title}</h2>
        {actions}
      </div>
      {children}
    </section>
  );
}

export function Loading() {
  return <p className="text-sm text-muted-foreground">Loading…</p>;
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}

