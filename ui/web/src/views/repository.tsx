import { getRouteApi } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useRepository, useRepositoryChecks, useRepositoryEvents, useUIConfig } from "../api/queries";
import { Empty, Evidence, ExternalLink, Loading, Remediation, Section, StatusBadge } from "../components/common";
import { Badge } from "../components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { humanize, relativeTime } from "../lib/format";
import { repoLink } from "../lib/pr";

const route = getRouteApi("/app/repos/$id");

// RepositoryView (DESIGN-0027 sketch): findings with evidence and PR
// links, park state, timeline and recent checks.
export function RepositoryView() {
  const { id } = route.useParams();
  const repoId = Number(id);
  const detail = useRepository(repoId);
  const config = useUIConfig();
  if (!detail.data) {
    return <Loading />;
  }
  const { repository: r, findings, last_check } = detail.data;
  const href = config.data ? repoLink(config.data.github_host, r.org, r.name) : undefined;
  return (
    <>
      <div className="flex flex-wrap items-baseline gap-3">
        <h1 className="text-xl font-semibold">
          {r.org}/{r.name}
        </h1>
        {href ? <ExternalLink href={href}>on GitHub</ExternalLink> : null}
        <span className="ml-auto text-sm text-muted-foreground">
          {r.active ? "active" : `parked (${humanize(r.park_reason ?? "unknown")}) ${relativeTime(r.parked_at)}`} · checked {relativeTime(r.last_checked_at)}
          {last_check ? ` · ${last_check.outcome}` : ""}
        </span>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Rule</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Reason / evidence</TableHead>
            <TableHead>Remediation</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {findings.map((f) => (
            <TableRow key={`${f.rule_kind}/${f.rule_name}`}>
              <TableCell>
                <div className="font-medium">{f.rule_name}</div>
                <div className="text-xs text-muted-foreground">{humanize(f.rule_kind)}</div>
              </TableCell>
              <TableCell>
                <StatusBadge status={f.status} />
              </TableCell>
              <TableCell>
                <Evidence finding={f} />
              </TableCell>
              <TableCell>
                <Remediation finding={f} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <div className="grid gap-6 md:grid-cols-2">
        <Timeline id={repoId} />
        <Checks id={repoId} />
      </div>
    </>
  );
}

// describeEvent renders one timeline entry as text.
export function describeEvent(e: Schemas["Event"]): string {
  if (e.source === "finding") {
    const reason = e.to_reason ? ` (${e.to_reason})` : "";
    const from = e.from_status ? `${e.from_status} → ` : "";
    return `${e.rule_name ?? "rule"} ${from}${e.to_status ?? "?"}${reason}`;
  }
  return humanize(e.kind ?? e.source);
}

function Timeline({ id }: { id: number }) {
  const events = useRepositoryEvents(id);
  return (
    <Section title="Timeline">
      {!events.data ? (
        <Loading />
      ) : events.data.items.length === 0 ? (
        <Empty>No events.</Empty>
      ) : (
        <ul className="space-y-1 text-sm">
          {events.data.items.map((e) => (
            <li key={`${e.source}/${e.id}`} className="flex gap-3">
              <span className="w-24 shrink-0 text-muted-foreground tabular-nums">{e.occurred_at.slice(0, 10)}</span>
              <span>{describeEvent(e)}</span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

function Checks({ id }: { id: number }) {
  const checks = useRepositoryChecks(id);
  return (
    <Section title="Recent checks">
      {!checks.data ? (
        <Loading />
      ) : checks.data.items.length === 0 ? (
        <Empty>No checks yet.</Empty>
      ) : (
        <Table>
          <TableBody>
            {checks.data.items.map((c) => (
              <TableRow key={c.id}>
                <TableCell className="whitespace-nowrap">{relativeTime(c.started_at)}</TableCell>
                <TableCell>{humanize(c.trigger)}</TableCell>
                <TableCell>
                  <Badge tone={c.outcome === "error" ? "non_compliant" : c.outcome === "success" ? "compliant" : "neutral"}>{c.outcome}</Badge>
                  {c.error ? <div className="mt-1 font-mono text-xs break-all">{c.error}</div> : null}
                </TableCell>
                <TableCell className="text-right tabular-nums">{c.duration_seconds === undefined ? "" : `${c.duration_seconds.toFixed(1)}s`}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Section>
  );
}
