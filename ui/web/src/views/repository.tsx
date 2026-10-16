import { getRouteApi } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useRepository, useRepositoryChecks, useUIConfig } from "../api/queries";
import { KindBadge, StateBadge, StatusBadge } from "../components/badges";
import { Evidence, ExternalLink, Loading } from "../components/common";
import { PageHeader } from "../components/page";
import { Time } from "../components/time";
import { Timeline } from "../components/timeline";
import { Badge } from "../components/ui/badge";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { DetailKV } from "../components/ui/detail-drawer";
import { humanize } from "../lib/format";
import { prLabel, prLink, repoLink } from "../lib/pr";

const route = getRouteApi("/app/repos/$id");

// Remediation shows the remediation and a link to its PR, built from
// the host, org, name and number.
function Remediation({ finding }: { finding: Schemas["Finding"] }) {
  const config = useUIConfig();
  const label = prLabel(finding);
  const href = finding.pr && config.data ? prLink(config.data.github_host, finding.org, finding.repository, finding.pr.number) : undefined;
  return (
    <div className="space-y-0.5">
      {finding.remediation !== "none" ? <div className="text-muted-foreground">{humanize(finding.remediation)}</div> : null}
      {label ? href ? <ExternalLink href={href}>{label}</ExternalLink> : <span>{label}</span> : null}
      {finding.pr_stale ? <Badge tone="warn">stale</Badge> : null}
    </div>
  );
}

// RepositoryView (DESIGN-0036 §Repository): header with the park state,
// two-thirds findings and recent checks, one-third repository facts and
// the shared timeline over every event.
export function RepositoryView() {
  const { id } = route.useParams();
  const repoId = Number(id);
  const detail = useRepository(repoId);
  const config = useUIConfig();
  if (!detail.data) {
    return <Loading />;
  }
  const { repository: r, installation, findings, last_check } = detail.data;
  const href = config.data ? repoLink(config.data.github_host, r.org, r.name) : undefined;
  return (
    <>
      <PageHeader
        title={`${r.org}/${r.name}`}
        badge={<StateBadge state={r.active ? "active" : "parked"} />}
        subtitle={
          <>
            checked <Time iso={r.last_checked_at} style="relative" />
            {last_check ? ` · ${last_check.outcome}` : ""} · installation <code>{r.installation_id}</code> ({installation.account})
            {r.active ? null : (
              <>
                {" · parked "}
                {humanize(r.park_reason ?? "unknown")} <Time iso={r.parked_at} style="relative" />
              </>
            )}
          </>
        }
        controls={href ? <ExternalLink href={href}>Open on GitHub</ExternalLink> : null}
      />
      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <Card title="Findings" subtitle="One row per rule, with the evidence behind its status." bodyClassName="p-0">
            <DataTable columns={findingColumns} rows={findings} rowKey={(f) => `${f.rule_kind}/${f.rule_name}`} empty="No rules apply to this repository." aria-label="Findings" />
          </Card>
          <Checks id={repoId} />
        </div>
        <div className="space-y-4">
          <Card title="Repository" subtitle="What repo-guardian knows about it.">
            <Facts repository={r} installation={installation} />
          </Card>
          <Card title="Timeline" subtitle="Every finding change, repository event and check.">
            <Timeline repositoryId={repoId} />
          </Card>
        </div>
      </div>
    </>
  );
}

const findingColumns: Column<Schemas["Finding"]>[] = [
  {
    key: "rule",
    header: "Rule",
    cell: (f) => (
      <span className="inline-flex items-center gap-2">
        <span className="font-mono font-medium">{f.rule_name}</span>
        <KindBadge kind={f.rule_kind} />
      </span>
    ),
  },
  { key: "status", header: "Status", cell: (f) => <StatusBadge status={f.status} /> },
  { key: "evidence", header: "Reason / evidence", cell: (f) => <Evidence finding={f} /> },
  { key: "remediation", header: "Remediation", cell: (f) => <Remediation finding={f} /> },
];

// Facts is the repository's key/value block. The API carries no default
// branch or catalog parse state yet (IMPL-0031 Phase 7 follow-ups), so
// the block lists what /repositories/{id} returns.
function Facts({ repository: r, installation }: { repository: Schemas["Repository"]; installation: Schemas["Installation"] }) {
  const rows = [
    { key: "state", value: <StateBadge state={r.active ? "active" : "parked"} /> },
    ...(r.active ? [] : [{ key: "park reason", value: <Badge tone="warn">{humanize(r.park_reason ?? "unknown")}</Badge> }]),
    { key: "installation", value: <span className="font-mono">{installation.account}</span> },
    { key: "discovered", value: <Time iso={r.discovered_at} style="date" /> },
    { key: "last check", value: <span className="inline-flex items-center gap-2"><Time iso={r.last_checked_at} style="relative" /><Badge tone={outcomeTone(r.last_check_outcome)}>{r.last_check_outcome}</Badge></span> },
    { key: "next due", value: <Time iso={r.next_due_at} style="relative" /> },
    { key: "policy", value: <span className="font-mono text-xs break-all">{r.policy_version}</span> },
    ...(r.provider_repo_id === undefined ? [] : [{ key: "provider id", value: <span className="font-mono">{r.provider_repo_id}</span> }]),
  ];
  return <DetailKV rows={rows} />;
}

function outcomeTone(outcome: string): "ok" | "bad" | "mute" {
  if (outcome === "error") return "bad";
  if (outcome === "success") return "ok";
  return "mute";
}

const checkColumns: Column<Schemas["Check"]>[] = [
  { key: "time", header: "Time", cell: (c) => <Time iso={c.started_at} style="relative" />, className: "whitespace-nowrap" },
  { key: "trigger", header: "Trigger", cell: (c) => humanize(c.trigger) },
  { key: "outcome", header: "Outcome", cell: (c) => <Badge tone={outcomeTone(c.outcome)}>{c.outcome}</Badge> },
  { key: "duration", header: "Duration", cell: (c) => (c.duration_seconds === undefined ? "" : `${c.duration_seconds.toFixed(1)}s`), numeric: true },
  { key: "error", header: "Error", cell: (c) => (c.error ? <span className="font-mono text-xs break-all">{c.error}</span> : <span className="text-muted-foreground">—</span>), hideBelow: "md" },
];

function Checks({ id }: { id: number }) {
  const checks = useRepositoryChecks(id);
  return (
    <Card title="Recent checks" subtitle="The last 20 checks, newest first." bodyClassName="p-0">
      {!checks.data ? <Loading /> : <DataTable columns={checkColumns} rows={checks.data.items} rowKey={(c) => String(c.id)} empty="No checks yet." aria-label="Recent checks" />}
    </Card>
  );
}
