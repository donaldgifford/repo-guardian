import type { ReactNode } from "react";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { Schemas } from "../api/client";
import { useUIConfig } from "../api/queries";
import { describeEvidence } from "../lib/evidence";
import { humanize, statusTone } from "../lib/format";
import { prLabel, prLink } from "../lib/pr";
import type { SeriesPoint } from "../lib/viewmodel";
import { Badge } from "./ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";

export function StatusBadge({ status }: { status: string }) {
  return <Badge tone={statusTone(status)}>{status === "not_applicable" ? "n/a" : humanize(status)}</Badge>;
}

// ExternalLink opens off-site without handing the opener or referrer
// over. href must come from a builder in lib/pr.ts, never from evidence.
export function ExternalLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-primary">
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

// Remediation shows the remediation and a link to its PR, built from
// the host, org, name and number.
export function Remediation({ finding }: { finding: Schemas["Finding"] }) {
  const config = useUIConfig();
  const label = prLabel(finding);
  const href = finding.pr && config.data ? prLink(config.data.github_host, finding.org, finding.repository, finding.pr.number) : undefined;
  return (
    <div className="space-y-0.5">
      {finding.remediation !== "none" ? <div className="text-muted-foreground">{humanize(finding.remediation)}</div> : null}
      {label ? href ? <ExternalLink href={href}>{label}</ExternalLink> : <span>{label}</span> : null}
      {finding.pr_stale ? <Badge tone="unknown">stale</Badge> : null}
    </div>
  );
}

export function Tile({ title, value, hint }: { title: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="text-2xl font-semibold tabular-nums">{value}</div>
        {hint ? <div className="mt-1 text-xs text-muted-foreground">{hint}</div> : null}
      </CardContent>
    </Card>
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

// TrendChart plots compliance over time. Unmeasured points are gaps,
// never zero.
export function TrendChart({ series, height = 180 }: { series: SeriesPoint[]; height?: number }) {
  if (series.length === 0) {
    return <Empty>No snapshots yet.</Empty>;
  }
  const data = series.map((p) => ({ at: p.at.slice(0, 10), percent: p.percent }));
  return (
    <ResponsiveContainer width="100%" height={height}>
      <LineChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: -16 }}>
        <CartesianGrid strokeDasharray="3 3" vertical={false} />
        <XAxis dataKey="at" tick={{ fontSize: 11 }} minTickGap={24} />
        <YAxis domain={[0, 100]} tick={{ fontSize: 11 }} />
        <Tooltip formatter={(v) => (typeof v === "number" ? `${v.toFixed(1)}%` : "unmeasured")} />
        <Line type="monotone" dataKey="percent" stroke="var(--color-compliant)" dot={false} connectNulls={false} isAnimationActive={false} />
      </LineChart>
    </ResponsiveContainer>
  );
}
