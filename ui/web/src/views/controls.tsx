import { getRouteApi, Link } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useControl, useControls } from "../api/queries";
import { Loading } from "../components/common";
import { ExportButton } from "../components/ExportButton";
import { PageHeader } from "../components/page/PageHeader";
import { RangeTabs } from "../components/page/RangeTabs";
import { CoverageText, DecisionBadge, PostureBar, PostureKpis, PostureMeter } from "../components/posture";
import { Badge } from "../components/ui/badge";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import type { CsvColumns } from "../lib/csv";
import { postureCsvHead, postureCsvRow } from "../lib/posture";
import { controlColumns, controlsCsv } from "./fleet";

// ControlsView lists every control assigned in the caller's orgs.
export function ControlsView() {
  const controls = useControls();
  return (
    <>
      <PageHeader
        title="Controls"
        subtitle={
          <>
            Every control assigned in the orgs you can see · <code>GET /controls</code>
          </>
        }
      />
      <Card bodyClassName="p-0" title="Controls" right={<ExportButton page="controls" columns={controlsCsv} rows={controls.data?.items} />}>
        {controls.data ? <DataTable columns={controlColumns} rows={controls.data.items} rowKey={(c) => c.id} empty="No control is assigned yet." aria-label="Controls" /> : <Loading />}
      </Card>
    </>
  );
}

const route = getRouteApi("/app/controls/$id");

// ControlView is one control: its fleet posture and, per org and
// lineage, the versions that org carries.
export function ControlView() {
  const { id } = route.useParams();
  const control = useControl(id);
  if (!control.data) {
    return <Loading />;
  }
  const c = control.data;
  return (
    <>
      <PageHeader
        title={c.id}
        badge={<Badge tone="outline">{c.type}</Badge>}
        subtitle={
          <>
            {c.title ? `${c.title} · ` : ""}
            <code>GET /controls/{c.id}</code>
          </>
        }
        controls={<RangeTabs />}
      />
      <div className="space-y-4">
        <PostureKpis posture={c.compliance} />
        <Card title="Assignments">
          <PostureBar posture={c.compliance} label={`${c.id} assignments by bucket`} />
        </Card>
        <Card title="By org" subtitle="Each org and lineage, with the versions it carries." bodyClassName="p-0" right={<ExportButton page="control orgs" filter={{ control: c.id }} columns={controlOrgsCsv} rows={c.orgs} />}>
          <DataTable columns={orgColumns} rows={c.orgs} rowKey={(o) => `${o.org}/${o.decision}`} empty="No org carries this control." aria-label="Orgs" />
        </Card>
      </div>
    </>
  );
}

const controlOrgsCsv: CsvColumns<Schemas["ControlOrg"]> = {
  head: ["org", "decision", "versions", ...postureCsvHead],
  row: (o) => [o.org, o.decision, o.versions.map((v) => `v${v.version} ${v.revision.slice(0, 12)} (${v.repositories})`).join("; "), ...postureCsvRow(o.compliance)],
};

const orgColumns: Column<Schemas["ControlOrg"]>[] = [
  {
    key: "org",
    header: "Org",
    cell: (o) => (
      <Link to="/orgs/$org" params={{ org: o.org }} className="font-mono font-medium hover:text-accent">
        {o.org}
      </Link>
    ),
  },
  { key: "decision", header: "Decision", cell: (o) => <DecisionBadge decision={o.decision} /> },
  {
    key: "versions",
    header: "Versions",
    cell: (o) =>
      o.versions.length === 0 ? (
        <span className="text-muted-foreground">—</span>
      ) : (
        <ul className="space-y-0.5">
          {o.versions.map((v) => (
            <li key={`${v.version}/${v.revision}`} className="font-mono text-xs">
              v{v.version} <span className="text-muted-foreground">{v.revision.slice(0, 12)}</span> · {v.repositories.toLocaleString()} repos
            </li>
          ))}
        </ul>
      ),
  },
  { key: "compliance", header: "Compliance", cell: (o) => <PostureMeter posture={o.compliance} label={`${o.org} compliance`} />, width: "30%" },
  { key: "coverage", header: "Coverage", cell: (o) => <CoverageText posture={o.compliance} />, numeric: true },
];
