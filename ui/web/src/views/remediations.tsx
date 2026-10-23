import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useRef } from "react";

import type { Schemas } from "../api/client";
import { exportCap, fetchAllRemediations, openPullRequestsCap, pageItems, useControls, useOrgs, useRemediationEnabled, useRemediationOverview, useRemediations } from "../api/queries";
import { HoldBadge, RemediationStateBadge } from "../components/badges";
import { Empty, Loading } from "../components/common";
import { ExportButton } from "../components/ExportButton";
import { gatedContent, NOT_ENABLED } from "../components/GatedPanel";
import { LoadMore } from "../components/LoadMore";
import { OrgScope, useOrgScope } from "../components/page/OrgScope";
import { PageHeader } from "../components/page/PageHeader";
import { ExternalLink } from "../components/posture";
import { Time } from "../components/time";
import { Badge } from "../components/ui/badge";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { Kpi } from "../components/ui/kpi";
import { SearchTabs } from "../components/ui/tabs";
import { humanize } from "../lib/format";
import { HOLDS, holdSentence } from "../lib/holds";
import { patchSearch } from "../lib/search";
import { SIDE_BY_SIDE, useMediaQuery } from "../lib/use-media-query";
import { cn } from "../lib/utils";
import { RemediationDrawer } from "./remediations/drawer";
import { isStale, KINDS, kpis, openSearch, remediationAge, remediationFilter, remediationKey, remediationsCsv, STATES, VIEWS } from "./remediations/model";

const route = getRouteApi("/app/remediations");

// remediationColumns is the table (DESIGN-0037 §Remediations view):
// Repository, Control, Kind, State (a pull request links its number),
// Hold with its sentence, Age with the stale badge, Updated.
export const remediationColumns: Column<Schemas["Remediation"]>[] = [
  {
    key: "repository",
    header: "Repository",
    cell: (r) => (
      <>
        <Link to="/repositories/$id" params={{ id: String(r.repository_id) }} className="font-mono font-medium hover:text-accent">
          {r.repository}
        </Link>
        <span className="block text-xs text-muted-foreground">{r.org}</span>
      </>
    ),
  },
  { key: "control", header: "Control", cell: (r) => <span className="font-mono">{r.control_id}</span> },
  { key: "kind", header: "Kind", cell: (r) => <Badge tone="outline">{humanize(r.kind)}</Badge>, hideBelow: "md" },
  {
    key: "state",
    header: "State",
    cell: (r) => (
      <span className="inline-flex items-center gap-1.5">
        {r.state ? <RemediationStateBadge state={r.state} /> : <Badge tone="mute">held</Badge>}
        {r.pr_number !== undefined ? r.pr_url ? <ExternalLink href={r.pr_url}>#{r.pr_number}</ExternalLink> : <span className="font-mono text-xs">#{r.pr_number}</span> : null}
      </span>
    ),
  },
  {
    key: "hold",
    header: "Hold",
    cell: (r) =>
      r.hold ? (
        <span className="block max-w-[320px] whitespace-normal">
          <HoldBadge hold={r.hold} />
          <span className="mt-0.5 block text-xs text-muted-foreground">{holdSentence(r.hold)}</span>
        </span>
      ) : (
        <span className="text-muted-foreground">—</span>
      ),
  },
  {
    key: "age",
    header: "Age",
    cell: (r) => (
      <span className="inline-flex items-center gap-1.5 font-mono">
        {remediationAge(r)}d{isStale(r) ? <Badge tone="warn">stale</Badge> : null}
      </span>
    ),
    numeric: true,
  },
  { key: "updated", header: "Updated", cell: (r) => <Time iso={r.last_run_at ?? r.created_at} style="relative" />, hideBelow: "compact", className: "whitespace-nowrap" },
];

// A chip is a select bound to one /remediations parameter.
function Chip({ label, value, options, onChange }: { label: string; value: string; options: readonly string[]; onChange: (v: string) => void }) {
  return (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} className="h-7 rounded-lg border border-border bg-panel px-2 font-mono text-xs">
      <option value="">{label}: any</option>
      {options.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
    </select>
  );
}

// RemediationsView lists what repo-guardian is doing about findings
// (DESIGN-0037 §Remediations view): KPIs, the segmented views, filter
// chips mapping 1:1 to /remediations, the table, Export and the drawer.
// Without the Remediation App the page is the one not-enabled state.
export function RemediationsView() {
  const search = route.useSearch();
  const open = search.open === undefined ? undefined : String(search.open);
  const navigate = useNavigate();
  const scopeOrg = useOrgScope();
  const remediation = useRemediationEnabled();
  const filter = remediationFilter(search, scopeOrg);
  const rows = useRemediations(filter, remediation === true);
  const overview = useRemediationOverview(scopeOrg, remediation === true);
  const controls = useControls(filter.org);
  const orgs = useOrgs();
  const items = pageItems(rows.data);
  const patch = (p: Record<string, string | undefined>) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => patchSearch(prev, p) });
  const openRow = (r: Schemas["Remediation"] | undefined) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...patchSearch(prev, { open: undefined }), ...(r === undefined ? {} : { open: openSearch(r) }) }) });
  const sideBySide = useMediaQuery(SIDE_BY_SIDE);
  const tableRef = useRef<HTMLDivElement>(null);
  const inline = sideBySide && open !== undefined;
  const rowFor = (key: string | undefined) => (key === undefined ? null : tableRef.current?.querySelector<HTMLElement>(`tr[data-key="${CSS.escape(key)}"]`));
  const state = gatedContent(remediation, false);
  const k = overview.data ? kpis(overview.data.rows, overview.data.held) : undefined;
  return (
    <>
      <PageHeader
        title="Remediations"
        subtitle={
          <>
            Pull requests and API changes repo-guardian makes to bring results to compliant. Filters map 1:1 to <code>GET /remediations</code>.
          </>
        }
        controls={
          <>
            <OrgScope />
            {state === "content" ? <ExportButton page="remediations" filter={{ view: search.view, org: filter.org, control: filter.control, state: filter.state }} columns={remediationsCsv} rows={items} all={{ fetch: () => fetchAllRemediations(filter, exportCap), cap: exportCap }} /> : null}
          </>
        }
      />
      {state !== "content" ? (
        <Card>
          <div data-gated={state}>{state === "loading" ? <Loading /> : <Empty>{NOT_ENABLED}</Empty>}</div>
        </Card>
      ) : (
        <div className="space-y-4" data-gated="content">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Kpi label="Open PRs" value={k ? k.open.toLocaleString() : "…"} subline={k ? `${k.stale.toLocaleString()} stale (30d+)` : undefined} />
            <Kpi label="Recommendations" value={k ? k.recommended.toLocaleString() : "…"} subline="outstanding, waiting for a human" />
            <Kpi label="Held" value={k ? k.held.toLocaleString() : "…"} subline={k && k.heldByReason.length > 0 ? k.heldByReason.map((h) => `${humanize(h.hold)} ${h.count}`).join(" · ") : "nothing held"} />
            <Kpi label="Failed applies" value={k ? k.failed.toLocaleString() : "…"} subline="direct apply stopped partway" />
          </div>
          <div className={cn("grid gap-4", inline && "grid-cols-[minmax(0,1fr)_470px]")} data-layout={inline ? "side-by-side" : "stacked"}>
            <div ref={tableRef} className="min-w-0 space-y-3">
              <Card className="flex flex-wrap items-center gap-2 px-3.5 py-2.5">
                <SearchTabs param="view" defaultValue="all" options={VIEWS} label="Remediation views" />
                <Chip label="kind" value={search.kind ?? ""} options={KINDS} onChange={(v) => patch({ kind: v || undefined })} />
                <Chip label="state" value={search.state ?? ""} options={STATES} onChange={(v) => patch({ state: v || undefined })} />
                <Chip label="org" value={search.org ?? ""} options={(orgs.data?.items ?? []).map((o) => o.org)} onChange={(v) => patch({ org: v || undefined })} />
                <Chip label="control" value={search.control ?? ""} options={(controls.data?.items ?? []).map((c) => c.id)} onChange={(v) => patch({ control: v || undefined })} />
                <Chip label="hold" value={search.hold ?? ""} options={HOLDS} onChange={(v) => patch({ hold: v || undefined })} />
                <label className="inline-flex items-center gap-1 font-mono text-xs text-muted-foreground">
                  older than
                  <input aria-label="older than days" type="number" min={1} value={search.older_than ?? ""} onChange={(e) => patch({ older_than: e.target.value || undefined })} className="h-7 w-16 rounded-lg border border-border bg-panel px-2 text-xs text-foreground" />
                  d
                </label>
                <span className="flex-1" />
                {overview.data?.truncated ? <span className="text-xs text-muted-foreground">KPIs count the first {openPullRequestsCap.toLocaleString()} rows</span> : null}
              </Card>
              <Card bodyClassName="p-0" title="Remediations">
                {!items ? (
                  <Loading />
                ) : items.length === 0 ? (
                  <Empty>No remediations match.</Empty>
                ) : (
                  <>
                    <DataTable columns={remediationColumns} rows={items} rowKey={remediationKey} {...(open === undefined ? {} : { selectedKey: open })} onActivate={(r) => openRow(r)} compact={inline} aria-label="Remediations" />
                    <LoadMore loaded={items.length} hasMore={rows.hasNextPage} loading={rows.isFetchingNextPage} onMore={() => void rows.fetchNextPage()} noun="remediations" />
                  </>
                )}
              </Card>
            </div>
            {open !== undefined ? <RemediationDrawer open={open} loaded={items ?? []} inline={inline} onClose={() => openRow(undefined)} returnFocusTo={() => rowFor(open)} /> : null}
          </div>
        </div>
      )}
    </>
  );
}
