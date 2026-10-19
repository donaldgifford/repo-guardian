import type { ReactNode } from "react";

import type { Schemas } from "../api/client";
import { describeEvidence } from "../lib/evidence";
import { formatPercent, humanize, toneFor } from "../lib/format";
import { postureSegments, type Posture } from "../lib/posture";
import { DistBar } from "./charts";
import { Badge } from "./ui/badge";
import { Kpi } from "./ui/kpi";
import { Meter } from "./ui/meter";

// The controls views' shared pieces: a posture as a meter, a KPI or a
// distribution, and evidence as text. A null percent is "no data".

export function PostureMeter({ posture, label = "Compliance" }: { posture: Posture; label?: string }) {
  return <Meter percent={posture.percent} aria-label={label} />;
}

export function CoverageText({ posture }: { posture: Posture }) {
  return <span className="tabular-nums text-muted-foreground">{formatPercent(posture.coverage)}</span>;
}

export function PostureKpis({ posture, children }: { posture: Posture; children?: ReactNode }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <Kpi label="Compliance" value={formatPercent(posture.percent)} subline="compliant ÷ (compliant + non-compliant)" />
      <Kpi label="Coverage" value={formatPercent(posture.coverage)} subline={`measured of ${posture.assigned.toLocaleString()} assigned`} />
      {children}
    </div>
  );
}

export function PostureBar({ posture, label }: { posture: Posture; label: string }) {
  return <DistBar segments={postureSegments(posture)} aria-label={label} />;
}

// BucketBadge is an assignment's posture bucket; the enum is open, so a
// bucket the table does not know is mute.
export function BucketBadge({ bucket }: { bucket: string }) {
  return <Badge tone={toneFor("bucket", bucket)}>{humanize(bucket)}</Badge>;
}

export function DecisionBadge({ decision }: { decision: string }) {
  return <Badge tone={toneFor("decision", decision)}>{humanize(decision)}</Badge>;
}

// EvidenceText is a rule result's evidence as text, never markup.
export function EvidenceText({ evidence }: { evidence: Schemas["Evidence"] }) {
  const v = describeEvidence(evidence);
  return (
    <div className="space-y-0.5">
      <div className="font-mono text-xs">{v.headline}</div>
      {v.lines.map((l) => (
        <div key={l} className="font-mono text-[11px] break-all text-muted-foreground">
          {l}
        </div>
      ))}
    </div>
  );
}

export function ExternalLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
      {children} ↗
    </a>
  );
}
