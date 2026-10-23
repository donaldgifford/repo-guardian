import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import type { Schemas } from "../api/client";
import { pageItems, useEvaluate, useEvaluateUnsupported, useRepositories, useRepository, useRepositoryControls, useRepositoryEvaluations, useRepositoryEvents, useUIConfig } from "../api/queries";
import { RuleStatusBadge, StatusBadge } from "../components/badges";
import { Loading } from "../components/common";
import { ExportButton } from "../components/ExportButton";
import { LoadMore } from "../components/LoadMore";
import { useCrumbLabel } from "../components/page/Breadcrumbs";
import { PageHeader } from "../components/page/PageHeader";
import { BucketBadge, DecisionBadge, EvidenceText, ExternalLink } from "../components/posture";
import { Time } from "../components/time";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { DetailKV } from "../components/ui/detail-drawer";
import { SearchTabs } from "../components/ui/tabs";
import type { CsvColumns } from "../lib/csv";
import { useDebounced } from "../lib/hooks";
import { humanize } from "../lib/format";
import { patchSearch } from "../lib/search";

// RepositoriesSearch is the list's filter, kept in the URL.
export interface RepositoriesSearch {
  org?: string;
  q?: string;
  policy_state?: string;
}

const policyStates = ["managed", "unmanaged", "excluded", "parked"] as const;

export function parseRepositoriesSearch(s: Record<string, unknown>): RepositoriesSearch {
  const str = (v: unknown) => (typeof v === "string" && v !== "" ? v : undefined);
  const out: RepositoriesSearch = {};
  const org = str(s.org);
  const q = str(s.q);
  const state = str(s.policy_state);
  if (org) out.org = org;
  if (q) out.q = q;
  if (state) out.policy_state = state;
  return out;
}

const listRoute = getRouteApi("/app/repositories");

// RepositoriesView lists the caller's repositories, filtered by org,
// name and policy state.
export function RepositoriesView() {
  const search = listRoute.useSearch();
  const navigate = useNavigate();
  const [q, setQ] = useState(search.q ?? "");
  const settled = useDebounced(q, 250);
  const { q: _urlQ, ...rest } = search;
  const repos = useRepositories(settled ? { ...rest, q: settled } : rest);
  const items = pageItems(repos.data);
  const setFilter = (patch: Record<string, string | undefined>) => void navigate({ to: ".", search: (prev: RepositoriesSearch) => patchSearch(prev, patch) });
  return (
    <>
      <PageHeader
        title="Repositories"
        subtitle={
          <>
            {search.org ? `In ${search.org} · ` : ""}
            <code>GET /repositories</code>
          </>
        }
        controls={
          <>
            <input
              aria-label="Search repositories"
              placeholder="Search by name"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onBlur={() => setFilter({ q: q || undefined })}
              className="h-7 rounded-lg border border-border bg-panel px-2.5 text-xs"
            />
            <select
              aria-label="Policy state"
              value={search.policy_state ?? ""}
              onChange={(e) => setFilter({ policy_state: e.target.value || undefined })}
              className="h-7 rounded-lg border border-border bg-panel px-2 text-xs"
            >
              <option value="">any policy state</option>
              {policyStates.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </>
        }
      />
      <Card title="Repositories" bodyClassName="p-0" right={<ExportButton page="repositories" filter={{ ...rest, q: settled }} columns={repositoriesCsv} rows={items} />}>
        {items ? (
          <>
            <DataTable columns={repositoryColumns} rows={items} rowKey={(r) => String(r.id)} empty="No repository matches." aria-label="Repositories" />
            <LoadMore loaded={items.length} hasMore={repos.hasNextPage} loading={repos.isFetchingNextPage} onMore={() => void repos.fetchNextPage()} noun="repositories" />
          </>
        ) : (
          <Loading />
        )}
      </Card>
    </>
  );
}

function policyStateBadge(r: Schemas["Repository"]) {
  if (!r.active) return <Badge tone="warn">parked</Badge>;
  return <Badge tone={r.policy_state === "managed" ? "ok" : "mute"}>{r.policy_state ?? "unresolved"}</Badge>;
}

const repositoriesCsv: CsvColumns<Schemas["Repository"]> = {
  head: ["org", "repository", "active", "park_reason", "policy_state", "last_evaluated_at"],
  row: (r) => [r.org, r.name, r.active, r.park_reason ?? "", r.policy_state ?? "", r.last_evaluated_at ?? ""],
};

const repositoryColumns: Column<Schemas["Repository"]>[] = [
  {
    key: "name",
    header: "Repository",
    cell: (r) => (
      <Link to="/repositories/$id" params={{ id: String(r.id) }} className="font-mono font-medium hover:text-accent">
        {r.org}/{r.name}
      </Link>
    ),
  },
  { key: "state", header: "Policy", cell: policyStateBadge },
  { key: "evaluated", header: "Last evaluated", cell: (r) => (r.last_evaluated_at ? <Time iso={r.last_evaluated_at} style="relative" /> : <span className="text-muted-foreground">never</span>), hideBelow: "md" },
];

const route = getRouteApi("/app/repositories/$id");

type Tab = "controls" | "evaluations" | "events";

const tabs = [
  { value: "controls", label: "Controls" },
  { value: "evaluations", label: "Evaluation log" },
  { value: "events", label: "Events" },
] as const;

// RepositoryView is one repository: its facts, its controls with rule
// results and evidence, its evaluation log and its events.
export function RepositoryView() {
  const { id } = route.useParams();
  const { tab } = route.useSearch();
  const repoId = Number(id);
  const detail = useRepository(repoId);
  const config = useUIConfig();
  const named = detail.data?.repository;
  useCrumbLabel(id, named ? `${named.org}/${named.name}` : undefined);
  if (!detail.data) {
    return <Loading />;
  }
  const r = detail.data.repository;
  const href = config.data ? `https://${config.data.github_host}/${r.org}/${r.name}` : undefined;
  const current: Tab = tab ?? "controls";
  return (
    <>
      <PageHeader
        title={`${r.org}/${r.name}`}
        badge={policyStateBadge(r)}
        subtitle={
          <>
            evaluated {r.last_evaluated_at ? <Time iso={r.last_evaluated_at} style="relative" /> : "never"} · <code>GET /repositories/{r.id}</code>
          </>
        }
        controls={
          <>
            {r.active ? <EvaluateButton id={r.id} /> : null}
            {href ? <ExternalLink href={href}>Open on GitHub</ExternalLink> : null}
          </>
        }
      />
      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-3 lg:col-span-2">
          <SearchTabs param="tab" defaultValue="controls" options={tabs} label="Repository sections" />
          {current === "controls" ? <Controls id={repoId} /> : current === "evaluations" ? <Evaluations id={repoId} /> : <Events id={repoId} />}
        </div>
        <Card title="Repository">
          <Facts repository={r} />
        </Card>
      </div>
    </>
  );
}

export function parseRepositorySearch(s: Record<string, unknown>): { tab?: Tab } {
  return s.tab === "evaluations" || s.tab === "events" ? { tab: s.tab } : {};
}

// EvaluateButton asks for an evaluation now. It is hidden once the API
// answers 501 (no Temporal client on this replica).
export function EvaluateButton({ id }: { id: number }) {
  const unsupported = useEvaluateUnsupported();
  const evaluate = useEvaluate(id);
  if (unsupported) return null;
  return (
    <span className="inline-flex items-center gap-2">
      <Button onClick={() => evaluate.mutate()} disabled={evaluate.isPending}>
        Re-evaluate
      </Button>
      {evaluate.data === "accepted" ? <span className="text-xs text-muted-foreground">requested</span> : null}
      {evaluate.error ? <span className="text-xs text-bad">{evaluate.error.message}</span> : null}
    </span>
  );
}

function Facts({ repository: r }: { repository: Schemas["Repository"] }) {
  const rows = [
    { key: "state", value: r.active ? <Badge tone="ok">active</Badge> : <Badge tone="warn">parked · {humanize(r.park_reason ?? "unknown")}</Badge> },
    { key: "policy state", value: <span className="font-mono">{r.policy_state ?? "unresolved"}</span> },
    ...(r.policy_reason ? [{ key: "policy reason", value: r.policy_reason }] : []),
    { key: "evaluable", value: r.evaluable ? "yes" : "no" },
    {
      key: "remediable",
      // INV-0023 F4: "no" here is about App access, while a rule's
      // "Remediable rule" is about the rule, so say which one this is.
      value: r.remediable ? (
        "yes"
      ) : (
        <span>
          no<span className="block text-xs text-muted-foreground">Remediation App has no access to this repository</span>
        </span>
      ),
    },
    { key: "discovered", value: <Time iso={r.discovered_at} style="date" /> },
    { key: "next due", value: r.next_due_at ? <Time iso={r.next_due_at} style="relative" /> : "—" },
  ];
  return <DetailKV rows={rows} />;
}

function Controls({ id }: { id: number }) {
  const controls = useRepositoryControls(id);
  if (!controls.data) return <Loading />;
  if (controls.data.items.length === 0) {
    return <p className="text-sm text-muted-foreground">No control is assigned to this repository.</p>;
  }
  return (
    <div className="space-y-3">
      {controls.data.items.map((c) => (
        <ControlCard key={c.control_id} control={c} />
      ))}
    </div>
  );
}

function ControlCard({ control: c }: { control: Schemas["RepositoryControl"] }) {
  return (
    <Card
      title={
        <Link to="/controls/$id" params={{ id: c.control_id }} className="font-mono hover:text-accent">
          {c.control_id}
        </Link>
      }
      subtitle={`v${c.version} · ${c.effective_mode} (${c.mode_source}) · from ${c.source}${c.source_block ? ` ${c.source_block}` : ""}`}
      right={
        <span className="inline-flex items-center gap-2">
          <DecisionBadge decision={c.decision} />
          <BucketBadge bucket={c.bucket} />
        </span>
      }
      bodyClassName="p-0"
      data-control={c.control_id}
    >
      {c.reason || c.error || c.hold ? (
        <p className="border-b border-border px-3.5 py-2 text-xs text-muted-foreground">
          {[c.reason, c.error, c.hold ? `hold: ${c.hold}` : undefined].filter(Boolean).join(" · ")}
        </p>
      ) : null}
      <DataTable columns={ruleColumns} rows={c.rules} rowKey={(r) => r.rule_id} empty="Not evaluated yet." aria-label={`${c.control_id} rules`} />
    </Card>
  );
}

const ruleColumns: Column<Schemas["RuleResult"]>[] = [
  { key: "rule", header: "Rule", cell: (r) => <span className="font-mono">{r.rule_id}</span> },
  { key: "status", header: "Status", cell: (r) => <RuleStatusBadge status={r.status} /> },
  { key: "evidence", header: "Evidence", cell: (r) => <EvidenceText evidence={r.evidence} /> },
  { key: "remediate", header: "Remediable rule", cell: (r) => (r.remediate ? "yes" : "no"), hideBelow: "md" },
];

function outcomeTone(outcome: string): "ok" | "bad" | "mute" {
  if (outcome === "error") return "bad";
  if (outcome === "success") return "ok";
  return "mute";
}

const evaluationColumns: Column<Schemas["Evaluation"]>[] = [
  { key: "time", header: "Time", cell: (e) => <Time iso={e.started_at} style="relative" />, className: "whitespace-nowrap" },
  { key: "trigger", header: "Trigger", cell: (e) => humanize(e.trigger) },
  { key: "outcome", header: "Outcome", cell: (e) => <Badge tone={outcomeTone(e.outcome)}>{e.outcome}</Badge> },
  { key: "duration", header: "Duration", cell: (e) => (e.duration_seconds === undefined ? "" : `${e.duration_seconds.toFixed(1)}s`), numeric: true },
  { key: "error", header: "Error", cell: (e) => (e.error ? <span className="font-mono text-xs break-all">{e.error}</span> : <span className="text-muted-foreground">—</span>), hideBelow: "md" },
];

const evaluationsCsv: CsvColumns<Schemas["Evaluation"]> = {
  head: ["started_at", "trigger", "outcome", "duration_seconds", "error"],
  row: (e) => [e.started_at, e.trigger, e.outcome, e.duration_seconds, e.error ?? ""],
};

function Evaluations({ id }: { id: number }) {
  const evaluations = useRepositoryEvaluations(id);
  return (
    <Card title="Evaluation log" subtitle="The last 20 evaluations, newest first." bodyClassName="p-0" right={<ExportButton page="evaluations" filter={{ repository: id }} columns={evaluationsCsv} rows={evaluations.data?.items} />}>
      {evaluations.data ? <DataTable columns={evaluationColumns} rows={evaluations.data.items} rowKey={(e) => String(e.id)} empty="No evaluation yet." aria-label="Evaluation log" /> : <Loading />}
    </Card>
  );
}

const eventColumns: Column<Schemas["Event"]>[] = [
  { key: "time", header: "Time", cell: (e) => <Time iso={e.occurred_at} style="relative" />, className: "whitespace-nowrap" },
  {
    key: "what",
    header: "Event",
    cell: (e) =>
      e.kind ? (
        <span className="font-mono">{humanize(e.kind)}</span>
      ) : (
        <span className="font-mono">
          {e.control_id}
          {e.rule_id ? `/${e.rule_id}` : ""}
        </span>
      ),
  },
  {
    key: "change",
    header: "Change",
    cell: (e) =>
      e.to_status || e.from_status ? (
        <span className="inline-flex items-center gap-1.5">
          {e.from_status ? <EventStatus event={e} status={e.from_status} /> : <span className="text-muted-foreground">new</span>}→
          {e.to_status ? <EventStatus event={e} status={e.to_status} /> : <span className="text-muted-foreground">cleared</span>}
        </span>
      ) : null,
  },
];

// EventStatus tones a rule-level event (one with a rule_id) by the rule
// row and a control-level event by the control status row.
function EventStatus({ event, status }: { event: Schemas["Event"]; status: string }) {
  return event.rule_id ? <RuleStatusBadge status={status} /> : <StatusBadge status={status} />;
}

const eventsCsv: CsvColumns<Schemas["Event"]> = {
  head: ["occurred_at", "source", "kind", "control", "rule", "from_status", "to_status"],
  row: (e) => [e.occurred_at, e.source, e.kind ?? "", e.control_id ?? "", e.rule_id ?? "", e.from_status ?? "", e.to_status ?? ""],
};

function Events({ id }: { id: number }) {
  const events = useRepositoryEvents(id);
  return (
    <Card title="Events" subtitle="Result changes and repository events, newest first." bodyClassName="p-0" right={<ExportButton page="events" filter={{ repository: id }} columns={eventsCsv} rows={events.data?.items} />}>
      {events.data ? <DataTable columns={eventColumns} rows={events.data.items} rowKey={(e) => `${e.source}/${e.id}`} empty="No events yet." aria-label="Events" /> : <Loading />}
    </Card>
  );
}
