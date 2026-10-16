import { Link } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useControls, useOrgs, useSummary } from "../api/queries";
import { Loading } from "../components/common";
import { PageHeader } from "../components/page/PageHeader";
import { CoverageText, PostureBar, PostureKpis, PostureMeter } from "../components/posture";
import { Time } from "../components/time";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { Kpi } from "../components/ui/kpi";

// FleetView is the landing page: fleet totals over the caller's orgs,
// every control linking to its breakdown, and the orgs.
export function FleetView() {
  const summary = useSummary();
  const controls = useControls();
  const orgs = useOrgs();
  if (!summary.data) {
    return <Loading />;
  }
  const s = summary.data;
  return (
    <>
      <PageHeader
        title="Fleet"
        subtitle={
          <>
            Compliance over every assignment in the orgs you can see · <code>GET /summary</code>
          </>
        }
      />
      <div className="space-y-4">
        <PostureKpis posture={s.compliance}>
          <Kpi label="Repositories" value={s.tracked.toLocaleString()} subline={`${s.parked.toLocaleString()} parked · ${s.open_prs.toLocaleString()} open PRs`} />
          <Kpi
            label="Evaluated in 24h"
            value={s.evaluated_24h.toLocaleString()}
            subline={s.last_evaluated_at ? <>last <Time iso={s.last_evaluated_at} style="relative" /></> : "no evaluation yet"}
          />
        </PostureKpis>
        <Card title="Assignments" subtitle="Every assignment lands in exactly one bucket.">
          <PostureBar posture={s.compliance} label="Fleet assignments by bucket" />
        </Card>
        <div className="grid gap-4 lg:grid-cols-2">
          <Card title="Controls" subtitle="Each control's compliance across the fleet." bodyClassName="p-0">
            {controls.data ? <DataTable columns={controlColumns} rows={controls.data.items} rowKey={(c) => c.id} empty="No control is assigned yet." aria-label="Controls" /> : <Loading />}
          </Card>
          <Card title="Orgs" bodyClassName="p-0">
            {orgs.data ? <DataTable columns={orgColumns} rows={orgs.data.items} rowKey={(o) => o.org} empty="No org is visible to you." aria-label="Orgs" /> : <Loading />}
          </Card>
        </div>
      </div>
    </>
  );
}

export const controlColumns: Column<Schemas["ControlSummary"]>[] = [
  {
    key: "id",
    header: "Control",
    cell: (c) => (
      <Link to="/controls/$id" params={{ id: c.id }} className="font-mono font-medium hover:text-accent">
        {c.id}
      </Link>
    ),
  },
  { key: "type", header: "Type", cell: (c) => <span className="font-mono text-xs text-muted-foreground">{c.type}</span>, hideBelow: "md" },
  { key: "compliance", header: "Compliance", cell: (c) => <PostureMeter posture={c.compliance} label={`${c.id} compliance`} />, width: "40%" },
  { key: "coverage", header: "Coverage", cell: (c) => <CoverageText posture={c.compliance} />, numeric: true },
];

const orgColumns: Column<Schemas["OrgSummary"]>[] = [
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
  { key: "compliance", header: "Compliance", cell: (o) => <PostureMeter posture={o.compliance} label={`${o.org} compliance`} />, width: "45%" },
];
