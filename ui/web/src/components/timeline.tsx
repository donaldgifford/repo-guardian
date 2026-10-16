import { Link } from "@tanstack/react-router";

import { useRepositoryChecks, useRepositoryEvents } from "../api/queries";
import { buildTimeline, type Timeline as TimelineData, type TimelineInput, type TimelineNode, type TimelineTone } from "../lib/timeline";
import { cn } from "../lib/utils";
import { Loading } from "./common";
import { Time } from "./time";

const dot: Record<TimelineTone, string> = {
  accent: "bg-accent",
  ok: "bg-ok",
  bad: "bg-bad",
  mute: "bg-muted-foreground",
};

// TimelineList draws built nodes oldest first down a hairline, the
// latest node in accent (DESIGN-0036 §Findings). `fullHref` names the
// repository page when the list is capped.
export function TimelineList({ timeline, repositoryId, className, "aria-label": ariaLabel = "Timeline" }: { timeline: TimelineData; repositoryId?: number; className?: string; "aria-label"?: string }) {
  const { nodes, truncated, total } = timeline;
  if (nodes.length === 0) {
    return <p className={cn("text-sm text-muted-foreground", className)}>No events yet.</p>;
  }
  const last = nodes.length - 1;
  return (
    <div className={className}>
      {truncated && repositoryId !== undefined ? (
        <p className="mb-2 text-xs text-muted-foreground">
          Latest {nodes.length} of {total} ·{" "}
          <Link to="/repos/$id" params={{ id: String(repositoryId) }} className="text-foreground hover:underline">
            full timeline →
          </Link>
        </p>
      ) : null}
      <ol aria-label={ariaLabel} className="relative ml-1.5 border-l border-border pl-4">
        {nodes.map((n, i) => (
          <TimelineRow key={n.key} node={n} latest={i === last} />
        ))}
      </ol>
    </div>
  );
}

function TimelineRow({ node, latest }: { node: TimelineNode; latest: boolean }) {
  return (
    <li className="relative pb-3 last:pb-0" data-source={node.source} data-latest={latest || undefined}>
      <span aria-hidden="true" className={cn("absolute top-[5px] -left-[21px] size-2.5 rounded-full border-2 border-panel", latest ? dot.accent : dot[node.tone])} />
      <div className="flex flex-wrap items-baseline gap-x-2 text-[13px]">
        <span className={cn(latest && "font-medium")}>{node.label}</span>
        <Time iso={node.at} style="relative" className="text-xs text-muted-foreground" />
      </div>
      {node.detail !== undefined ? <div className="font-mono text-xs break-all whitespace-pre-wrap text-muted-foreground">{node.detail}</div> : null}
    </li>
  );
}

// Timeline fetches a repository's events and checks and builds the
// list for one finding (with `rule`, `since`, `pr`) or the whole
// repository (without).
export function Timeline({ repositoryId, className, ...input }: Omit<TimelineInput, "events" | "checks"> & { repositoryId: number; className?: string }) {
  const events = useRepositoryEvents(repositoryId);
  const checks = useRepositoryChecks(repositoryId);
  if (!events.data || !checks.data) {
    return <Loading />;
  }
  const timeline = buildTimeline({ ...input, events: events.data.items, checks: checks.data.items });
  return <TimelineList timeline={timeline} repositoryId={repositoryId} {...(className === undefined ? {} : { className })} />;
}
