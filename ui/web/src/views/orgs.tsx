import { getRouteApi, Link } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useOrg, useOrgs } from "../api/queries";
import { Loading } from "../components/common";
import { PageHeader } from "../components/page/PageHeader";
import { CoverageText, DecisionBadge, PostureMeter } from "../components/posture";
import { Badge } from "../components/ui/badge";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { Kpi } from "../components/ui/kpi";
import { formatPercent, humanize } from "../lib/format";

// OrgsView lists the caller's orgs with their posture.
export function OrgsView() {
  const orgs = useOrgs();
  return (
    <>
      <PageHeader
        title="Orgs"
        subtitle={
          <>
            Every org you can see · <code>GET /orgs</code>
          </>
        }
      />
      <Card title="Orgs" bodyClassName="p-0">
        {orgs.data ? <DataTable columns={orgListColumns} rows={orgs.data.items} rowKey={(o) => o.org} empty="No org is visible to you." aria-label="Orgs" /> : <Loading />}
      </Card>
    </>
  );
}

const orgListColumns: Column<Schemas["OrgSummary"]>[] = [
  {
    key: "org",
    header: "Org",
    cell: (o) => (
      <Link to="/orgs/$org" params={{ org: o.org }} className="font-mono font-medium hover:text-accent">
        {o.org}
      </Link>
    ),
  },
  { key: "tracked", header: "Repos", cell: (o) => o.tracked.toLocaleString(), numeric: true },
  { key: "parked", header: "Parked", cell: (o) => o.parked.toLocaleString(), numeric: true, hideBelow: "md" },
  { key: "prs", header: "Open PRs", cell: (o) => o.open_prs.toLocaleString(), numeric: true, hideBelow: "md" },
  { key: "compliance", header: "Compliance", cell: (o) => <PostureMeter posture={o.compliance} label={`${o.org} compliance`} />, width: "30%" },
  { key: "coverage", header: "Coverage", cell: (o) => <CoverageText posture={o.compliance} />, numeric: true },
];

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
          <Link to="/repositories" search={{ org: o.org }} className="text-xs text-accent hover:underline">
            Repositories →
          </Link>
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
          <Card title="Controls" subtitle="Baseline, replaced and org-added." bodyClassName="p-0" className="lg:col-span-2">
            <DataTable columns={controlColumns} rows={o.controls} rowKey={(c) => `${c.control_id}/${c.decision}`} empty="No control is assigned here." aria-label="Org controls" />
          </Card>
          <Card title="Worst controls" subtitle="Lowest measured compliance." bodyClassName="p-0">
            <DataTable columns={worstColumns} rows={o.worst_controls} rowKey={(c) => `${c.control_id}/${c.decision}`} empty="Nothing measured yet." aria-label="Worst controls" />
          </Card>
        </div>
        <Card title="Exclusions" subtitle="Repositories or controls the policy leaves out, and why." bodyClassName="p-0">
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
