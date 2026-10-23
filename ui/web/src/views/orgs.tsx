import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";

import type { Schemas } from "../api/client";
import { pageItems, useOpenPullRequests, useOrg, useOrgs, useRemediationEnabled, useRepositories } from "../api/queries";
import { Empty, Loading } from "../components/common";
import { ExportButton } from "../components/ExportButton";
import { GatedPanel, NOT_ENABLED } from "../components/GatedPanel";
import { OrgScope, useOrgScope } from "../components/page/OrgScope";
import { PageHeader } from "../components/page/PageHeader";
import { RangeTabs } from "../components/page/RangeTabs";
import { CoverageText, DecisionBadge, PostureMeter } from "../components/posture";
import { Time } from "../components/time";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { type GridCell, GridChildRows, type GridColumn, GroupGrid } from "../components/ui/group-grid";
import { Kpi } from "../components/ui/kpi";
import { SearchTabs } from "../components/ui/tabs";
import type { CsvColumns } from "../lib/csv";
import { formatPercent, humanize } from "../lib/format";
import { useDebounced } from "../lib/hooks";
import { postureCsvHead, postureCsvRow } from "../lib/posture";
import { patchSearch } from "../lib/search";
import { orgsCsv } from "./fleet";
import { countByRepository, joinOpen, matchesFilter, openOrgs, type OrgPRActivity, ORGS_TABS, type OrgsTab, postureCells, prActivity, staleAfterDays, sumOrgs, toggleOpen } from "./orgs/tree";

const listRoute = getRouteApi("/app/orgs");

const compact = (n: number) => n.toLocaleString();

// gatedCount is an open-PR cell: the count with the Remediation App, a
// dash carrying the not-enabled reason without it (or while /status
// still loads).
function gatedCount(n: number, remediation: boolean | undefined): GridCell {
  return remediation ? n : (
    <span className="text-muted-foreground" title={NOT_ENABLED} data-gated="not_enabled">
      —
    </span>
  );
}

const complianceColumns: GridColumn[] = [
  { key: "tracked", header: "Tracked", tone: "mute" },
  { key: "compliant", header: "Compliant", tone: "ok" },
  { key: "non_compliant", header: "Non-compliant", tone: "bad" },
  { key: "open_prs", header: "Open PRs", tone: "info" },
  { key: "parked", header: "Parked", tone: "warn" },
];
const complianceHeat = ["compliant", "non_compliant"];

const parkedColumns: GridColumn[] = [
  { key: "reason", header: "Reason", tone: "warn", align: "left" },
  { key: "parked_at", header: "Parked", tone: "mute", align: "left" },
];

// OrgsView is the orgs tree (DESIGN-0037 OQ4): each org a row that
// expands to its repositories in the same grid, with Compliance, Parked
// and PR activity tabs, a repository filter over the loaded children,
// KPI chips and Export. Counts are assignments (repository × control).
export function OrgsView() {
  const search = listRoute.useSearch();
  const navigate = useNavigate();
  const scope = useOrgScope();
  const remediation = useRemediationEnabled();
  const orgs = useOrgs();
  const tab: OrgsTab = search.tab ?? "compliance";
  const [q, setQ] = useState(search.q ?? "");
  const settled = useDebounced(q, 250);
  const patch = (p: Record<string, string | undefined>) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => patchSearch(prev, p) });
  useEffect(() => {
    if ((settled || undefined) !== search.q) patch({ q: settled || undefined });
    // The URL follows the settled input; the input never follows the URL
    // back, so typing is not interrupted by a round trip.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settled]);
  const visible = useMemo(() => (orgs.data?.items ?? []).filter((o) => !scope || o.org === scope), [orgs.data, scope]);
  const open = openOrgs(search.open);
  const allOpen = visible.length > 0 && visible.every((o) => open.includes(o.org));
  const total = sumOrgs(visible);
  const filter = search.q;
  return (
    <>
      <PageHeader
        title="Orgs"
        subtitle={
          <>
            Each org expands to its repositories. Counts are assignments (repository × control); cells shade by share of measured and the footer sums the rows · <code>GET /orgs</code>
          </>
        }
        controls={
          <>
            <OrgScope />
            <ExportButton page="orgs" filter={{ org: scope }} columns={orgsCsv} rows={orgs.data ? visible : undefined} />
          </>
        }
      />
      <Card bodyClassName="p-0">
        <div className="flex flex-wrap items-center gap-2 px-3.5 py-2.5" aria-label="Orgs toolbar" role="toolbar">
          <span className="text-xs text-muted-foreground">Tracked</span>
          <Badge tone="mute">{compact(total.tracked)}</Badge>
          <span className="text-xs text-muted-foreground">Non-compliant</span>
          <Badge tone="bad">{compact(total.compliance.non_compliant)}</Badge>
          <span className="text-xs text-muted-foreground">Parked</span>
          <Badge tone="warn">{compact(total.parked)}</Badge>
          <span className="flex-1" />
          {tab !== "pr" ? <input aria-label="Filter repositories" placeholder="Filter repositories…" value={q} onChange={(e) => setQ(e.target.value)} className="h-7 rounded-lg border border-border bg-panel px-2.5 text-xs" /> : null}
          <SearchTabs param="tab" defaultValue="compliance" options={ORGS_TABS} label="Orgs tabs" />
          {tab !== "pr" ? (
            <Button variant="outline" size="sm" onClick={() => patch({ open: joinOpen(allOpen ? [] : visible.map((o) => o.org)) })} disabled={visible.length === 0}>
              {allOpen ? "Collapse all" : "Expand all"}
            </Button>
          ) : null}
        </div>
        {!orgs.data ? (
          <Loading />
        ) : tab === "pr" ? (
          <PRActivity orgs={visible} />
        ) : tab === "parked" ? (
          <GroupGrid
            label="Org / repository"
            columns={parkedColumns}
            rows={visible.map((o) => ({ key: o.org, label: orgLink(o.org), sub: `${compact(o.parked)} parked`, cells: {} }))}
            heat={[]}
            open={open}
            onToggle={(k) => patch({ open: joinOpen(toggleOpen(open, k)) })}
            footer={{ label: `${visible.length} orgs`, cells: { reason: `${compact(total.parked)} parked` } }}
            aria-label="Parked repositories by org"
          >
            {(row) => <ParkedChildren key={`${row.key}/children`} org={row.key} total={visible.find((o) => o.org === row.key)?.parked ?? 0} filter={filter} />}
          </GroupGrid>
        ) : (
          <GroupGrid
            label="Org / repository"
            columns={complianceColumns}
            rows={visible.map((o) => ({
              key: o.org,
              label: orgLink(o.org),
              sub: `${formatPercent(o.compliance.percent)} compliant · ${compact(o.parked)} parked`,
              cells: { tracked: o.tracked, ...postureCells(o.compliance), open_prs: gatedCount(o.open_prs, remediation), parked: o.parked },
            }))}
            heat={complianceHeat}
            open={open}
            onToggle={(k) => patch({ open: joinOpen(toggleOpen(open, k)) })}
            footer={{
              label: `${visible.length} orgs`,
              cells: { tracked: total.tracked, ...postureCells(total.compliance), open_prs: gatedCount(total.open_prs, remediation), parked: total.parked },
            }}
            aria-label="Orgs"
          >
            {(row) => <ComplianceChildren key={`${row.key}/children`} org={row.key} total={visible.find((o) => o.org === row.key)?.tracked ?? 0} filter={filter} remediation={remediation} />}
          </GroupGrid>
        )}
      </Card>
    </>
  );
}

const orgLink = (org: string) => (
  <Link to="/orgs/$org" params={{ org }} className="hover:text-accent">
    {org}
  </Link>
);

const repoLink = (r: Schemas["Repository"]) => (
  <Link to="/repositories/$id" params={{ id: String(r.id) }} className="hover:text-accent">
    {r.name}
  </Link>
);

// childFooter is the "showing N of M · Load more" line under an
// expanded org; with a filter it counts the loaded rows that match.
function childFooter(shown: number, loaded: number, total: number, filter: string | undefined, repos: ReturnType<typeof useRepositories>, noun: string) {
  const more = repos.hasNextPage ? (
    <Button variant="ghost" size="sm" className="ml-2 h-6" onClick={() => void repos.fetchNextPage()} disabled={repos.isFetchingNextPage}>
      {repos.isFetchingNextPage ? "Loading…" : "Load more"}
    </Button>
  ) : null;
  if (filter) {
    return (
      <>
        {compact(shown)} of {compact(loaded)} loaded {noun} match
        {more}
      </>
    );
  }
  if (!repos.hasNextPage && loaded >= total) return undefined;
  return (
    <>
      showing {compact(loaded)} of {compact(total)}
      {more}
    </>
  );
}

// ComplianceChildren is an expanded org on the Compliance tab: its
// active repositories by page, each with its own posture, and its open
// pull requests counted per repository with the Remediation App.
function ComplianceChildren({ org, total, filter, remediation }: { org: string; total: number; filter: string | undefined; remediation: boolean | undefined }) {
  const repos = useRepositories({ org, active: true });
  const prs = useOpenPullRequests(org, remediation === true);
  const loaded = pageItems(repos.data) ?? [];
  const byRepo = countByRepository(prs.data?.rows ?? []);
  const shown = loaded.filter((r) => matchesFilter(r.name, filter));
  return (
    <GridChildRows
      parent={org}
      loading={!repos.data}
      empty={filter ? "No loaded repository matches." : "No active repository."}
      rows={shown.map((r) => ({
        key: `${org}/${r.name}`,
        label: repoLink(r),
        badge: r.policy_state && r.policy_state !== "managed" ? <Badge tone="mute">{humanize(r.policy_state)}</Badge> : undefined,
        cells: { tracked: { n: r.compliance.assigned }, ...postureCells(r.compliance), open_prs: remediation ? (prs.data ? byRepo.get(r.id) ?? 0 : "…") : gatedCount(0, remediation), parked: "" },
      }))}
      {...(repos.data ? { footer: childFooter(shown.length, loaded.length, total, filter, repos, "repositories") } : {})}
    />
  );
}

// ParkedChildren is an expanded org on the Parked tab: its parked
// repositories with the reason and when.
function ParkedChildren({ org, total, filter }: { org: string; total: number; filter: string | undefined }) {
  const repos = useRepositories({ org, active: false });
  const loaded = pageItems(repos.data) ?? [];
  const shown = loaded.filter((r) => matchesFilter(r.name, filter));
  return (
    <GridChildRows
      parent={org}
      loading={!repos.data}
      empty={filter ? "No loaded repository matches." : "Nothing parked."}
      rows={shown.map((r) => ({
        key: `${org}/${r.name}`,
        label: repoLink(r),
        cells: { reason: <Badge tone="warn">{humanize(r.park_reason ?? "unknown")}</Badge>, parked_at: <Time iso={r.parked_at} style="relative" /> },
      }))}
      {...(repos.data ? { footer: childFooter(shown.length, loaded.length, total, filter, repos, "repositories") } : {})}
    />
  );
}

const prColumns: Column<OrgPRActivity>[] = [
  {
    key: "org",
    header: "Org",
    cell: (r) => (
      <Link to="/findings" search={{ org: r.org, pr_stale: true }} className="font-mono font-medium hover:text-accent">
        {r.org}
      </Link>
    ),
  },
  { key: "open", header: "Open PRs", cell: (r) => compact(r.open), numeric: true },
  { key: "stale", header: `Stale (${staleAfterDays}d+)`, cell: (r) => compact(r.stale), numeric: true },
  { key: "oldest", header: "Oldest", cell: (r) => (r.oldestDays === null ? <span className="text-muted-foreground">—</span> : `${r.oldestDays}d`), numeric: true },
];

// PRActivity is the PR activity tab: open pull requests per org with
// how many are stale and the oldest's age, gated on the Remediation
// App. A row opens the org's stale findings.
function PRActivity({ orgs }: { orgs: readonly Schemas["OrgSummary"][] }) {
  const remediation = useRemediationEnabled();
  const prs = useOpenPullRequests(undefined, remediation === true);
  const rows = prActivity(orgs, prs.data?.rows ?? []);
  return (
    <GatedPanel isEmpty={prs.data !== undefined && prs.data.rows.length === 0} empty={<Empty>No open pull requests.</Empty>}>
      {prs.data ? <DataTable columns={prColumns} rows={rows} rowKey={(r) => r.org} empty="No org is visible to you." aria-label="PR activity" /> : <Loading />}
    </GatedPanel>
  );
}

const route = getRouteApi("/app/orgs/$org");

// OrgView is one org: the enterprise baseline against what the org
// added, its controls with the worst first, and its exclusions.
export function OrgView() {
  const { org } = route.useParams();
  const detail = useOrg(org);
  if (!detail.data) {
    return <Loading />;
  }
  const o = detail.data;
  return (
    <>
      <PageHeader
        title={o.org}
        subtitle={
          <>
            {o.tracked.toLocaleString()} repositories · {o.parked.toLocaleString()} parked · <code>GET /orgs/{o.org}</code>
          </>
        }
        controls={
          <>
            <RangeTabs />
            <Link to="/repositories" search={{ org: o.org }} className="text-xs text-accent hover:underline">
              Repositories →
            </Link>
          </>
        }
      />
      <div className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Kpi label="Baseline compliance" value={formatPercent(o.baseline.percent)} subline={`coverage ${formatPercent(o.baseline.coverage)}`} />
          <Kpi label="Org-added compliance" value={formatPercent(o.org_added.percent)} subline={`coverage ${formatPercent(o.org_added.coverage)}`} />
          <Kpi label="Baseline assignments" value={o.baseline.assigned.toLocaleString()} />
          <Kpi label="Org-added assignments" value={o.org_added.assigned.toLocaleString()} />
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <Card
            title="Controls"
            subtitle="Baseline, replaced and org-added."
            bodyClassName="p-0"
            className="lg:col-span-2"
            right={<ExportButton page="org controls" filter={{ org: o.org }} columns={orgControlsCsv} rows={o.controls} />}
          >
            <DataTable columns={controlColumns} rows={o.controls} rowKey={(c) => `${c.control_id}/${c.decision}`} empty="No control is assigned here." aria-label="Org controls" />
          </Card>
          <Card title="Worst controls" subtitle="Lowest measured compliance." bodyClassName="p-0" right={<ExportButton page="worst controls" filter={{ org: o.org }} columns={orgControlsCsv} rows={o.worst_controls} />}>
            <DataTable columns={worstColumns} rows={o.worst_controls} rowKey={(c) => `${c.control_id}/${c.decision}`} empty="Nothing measured yet." aria-label="Worst controls" />
          </Card>
        </div>
        <Card
          title="Exclusions"
          subtitle="Repositories or controls the policy leaves out, and why."
          bodyClassName="p-0"
          right={<ExportButton page="exclusions" filter={{ org: o.org }} columns={exclusionsCsv} rows={o.exclusions} />}
        >
          <DataTable columns={exclusionColumns} rows={o.exclusions} rowKey={(e) => `${e.repository}/${e.control_id ?? ""}`} empty="No exclusions." aria-label="Exclusions" />
        </Card>
      </div>
    </>
  );
}

const controlLink = (c: Schemas["OrgControl"]) => (
  <Link to="/controls/$id" params={{ id: c.control_id }} className="font-mono font-medium hover:text-accent">
    {c.control_id}
  </Link>
);

const orgControlsCsv: CsvColumns<Schemas["OrgControl"]> = {
  head: ["control", "decision", ...postureCsvHead],
  row: (c) => [c.control_id, c.decision, ...postureCsvRow(c.compliance)],
};

const exclusionsCsv: CsvColumns<Schemas["Exclusion"]> = {
  head: ["repository", "control", "state", "reason", "excluded_by"],
  row: (e) => [e.repository, e.control_id ?? "", e.state, e.reason, e.excluded_by ?? ""],
};

const controlColumns: Column<Schemas["OrgControl"]>[] = [
  { key: "control", header: "Control", cell: controlLink },
  { key: "decision", header: "Decision", cell: (c) => <DecisionBadge decision={c.decision} /> },
  { key: "compliance", header: "Compliance", cell: (c) => <PostureMeter posture={c.compliance} label={`${c.control_id} compliance`} />, width: "35%" },
  { key: "coverage", header: "Coverage", cell: (c) => <CoverageText posture={c.compliance} />, numeric: true },
];

const worstColumns: Column<Schemas["OrgControl"]>[] = [
  { key: "control", header: "Control", cell: controlLink },
  { key: "compliance", header: "Compliance", cell: (c) => <PostureMeter posture={c.compliance} label={`${c.control_id} compliance`} />, width: "55%" },
];

const exclusionColumns: Column<Schemas["Exclusion"]>[] = [
  { key: "repository", header: "Repository", cell: (e) => <span className="font-mono">{e.repository}</span> },
  { key: "control", header: "Control", cell: (e) => (e.control_id ? <span className="font-mono">{e.control_id}</span> : <span className="text-muted-foreground">every control</span>) },
  { key: "state", header: "State", cell: (e) => <Badge tone="mute">{humanize(e.state)}</Badge> },
  { key: "reason", header: "Reason", cell: (e) => e.reason },
  { key: "by", header: "Excluded by", cell: (e) => <span className="font-mono text-xs">{e.excluded_by ?? "—"}</span>, hideBelow: "md" },
];
