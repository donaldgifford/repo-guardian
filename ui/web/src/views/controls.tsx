import { getRouteApi, Link } from "@tanstack/react-router";
import { useMemo } from "react";

import type { Schemas } from "../api/client";
import { pageItems, useComplianceHistory, useControl, useControls, useResults } from "../api/queries";
import { BarList, type BarListItem, ChartCard, TrendChart, trendTable } from "../components/charts";
import { Empty, Loading } from "../components/common";
import { ExportButton } from "../components/ExportButton";
import { OrgScope, useOrgScope } from "../components/page/OrgScope";
import { PageHeader } from "../components/page/PageHeader";
import { RangeTabs, useRangeDays } from "../components/page/RangeTabs";
import { CoverageText, DecisionBadge, PostureBar, PostureMeter } from "../components/posture";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { type Delta, Kpi } from "../components/ui/kpi";
import type { CsvColumns } from "../lib/csv";
import { ageDays, formatPercent, trend } from "../lib/format";
import { addPosture, emptyPosture, historySeries, postureCsvHead, postureCsvRow } from "../lib/posture";
import type { SeriesPoint } from "../lib/viewmodel";
import { findingKey } from "./findings/drawer";
import { controlColumns, controlsCsv } from "./fleet";

// ControlsView lists every control assigned in the caller's orgs.
export function ControlsView() {
  const org = useOrgScope();
  const controls = useControls(org);
  return (
    <>
      <PageHeader
        title="Controls"
        subtitle={
          <>
            {org ? `Every control assigned in ${org}` : "Every control assigned in the orgs you can see"} · <code>GET /controls</code>
          </>
        }
        controls={<OrgScope />}
      />
      <Card bodyClassName="p-0" title="Controls" right={<ExportButton page="controls" filter={{ org }} columns={controlsCsv} rows={controls.data?.items} />}>
        {controls.data ? <DataTable columns={controlColumns} rows={controls.data.items} rowKey={(c) => c.id} empty="No control is assigned yet." aria-label="Controls" /> : <Loading />}
      </Card>
    </>
  );
}

const route = getRouteApi("/app/controls/$id");

// rangeDelta is the compliance move over the range: last snapshot
// against the first, in points; null until two measured snapshots exist.
export function rangeDelta(series: readonly SeriesPoint[]): Delta | null {
  const measured = series.filter((p) => p.percent !== null);
  const first = measured[0];
  const last = measured[measured.length - 1];
  if (!first || !last || first === last) return null;
  const value = trend(last.percent, first.percent);
  return value === null ? null : { value, goodUp: true, unit: " pts" };
}

// ruleCounts tallies the loaded findings by failing rule, the bar list's
// rows; a finding failing two rules counts under both.
export function ruleCounts(rows: readonly Schemas["ResultRow"][]): BarListItem[] {
  const counts = new Map<string, number>();
  for (const r of rows) {
    for (const rule of r.failing_rules) counts.set(rule, (counts.get(rule) ?? 0) + 1);
  }
  return [...counts.entries()].map(([label, value]) => ({ label, value }));
}

const oldestCount = 10;

// ControlView is one control (DESIGN-0037 §Control detail): its posture
// in scope with the move over the range, compliance over time at
// aggregate=control, each org and lineage with the versions it carries,
// its findings by failing rule and the ten longest-failing findings.
export function ControlView() {
  const { id } = route.useParams();
  const org = useOrgScope();
  const days = useRangeDays();
  const control = useControl(id);
  const scoped = org ? { org } : {};
  const history = useComplianceHistory({ days, aggregate: "control", control: id, ...scoped });
  const findings = useResults({ bucket: "non_compliant", control: id, sort: "since_asc", ...scoped });
  const series = useMemo(() => historySeries(history.data?.items ?? []), [history.data]);
  if (!control.data) {
    return <Loading />;
  }
  const c = control.data;
  // GET /controls/{id} takes no org: the by-org table and the posture
  // are narrowed here, summing the org's lineages.
  const orgRows = org ? c.orgs.filter((o) => o.org === org) : c.orgs;
  const posture = org ? orgRows.reduce((p, o) => addPosture(p, o.compliance), emptyPosture()) : c.compliance;
  const rows = pageItems(findings.data) ?? [];
  const total = findings.data?.pages[0]?.total ?? rows.length;
  const oldest = rows.slice(0, oldestCount);
  const first = oldest[0];
  const findingsSearch = { control: c.id, ...scoped };
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
        controls={
          <>
            <OrgScope />
            <RangeTabs />
            <Link to="/findings" search={findingsSearch}>
              <Button variant="outline" size="sm" tabIndex={-1}>
                Findings for this control
              </Button>
            </Link>
          </>
        }
      />
      <div className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Kpi label="Compliance" value={formatPercent(posture.percent)} {...(history.data ? { delta: rangeDelta(series), subline: `over ${days}d` } : {})} />
          <Kpi label="Compliant" value={posture.compliant.toLocaleString()} subline="assignments" />
          <Kpi label="Non-compliant" value={posture.non_compliant.toLocaleString()} {...(first ? { subline: `oldest: ${ageDays(first.last_changed_at)} days` } : findings.data ? { subline: "no findings in scope" } : {})} />
          <Kpi label="Coverage" value={formatPercent(posture.coverage)} subline={`${posture.not_applicable.toLocaleString()} n/a · ${posture.unknown.toLocaleString()} unknown`} />
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <div className="space-y-4 lg:col-span-2">
            <ChartCard
              title={`Compliance (${days}d)`}
              subtitle={
                <>
                  <code>aggregate=control</code> over the orgs in scope.
                </>
              }
              table={trendTable(series, "compliance %")}
            >
              {history.data ? <TrendChart series={series} rangeDays={days} height={180} aria-label={`${c.id} compliance over time`} /> : <Loading />}
            </ChartCard>
            <Card
              title="By org"
              subtitle="Each org and lineage, with the versions it carries."
              bodyClassName="p-0"
              right={<ExportButton page="control orgs" filter={{ control: c.id, org }} columns={controlOrgsCsv} rows={orgRows} />}
            >
              <DataTable columns={orgColumns} rows={orgRows} rowKey={(o) => `${o.org}/${o.decision}`} empty="No org carries this control." aria-label="Orgs" />
            </Card>
            <Card title="Assignments" subtitle="By bucket, now.">
              <PostureBar posture={posture} label={`${c.id} assignments by bucket`} />
            </Card>
          </div>
          <div className="space-y-4">
            <Card title="Top failing rules" subtitle={total > rows.length ? `Findings by failing rule over the ${rows.length} oldest of ${total.toLocaleString()}. A row filters the findings page.` : "Findings by failing rule. A row filters the findings page."}>
              {!findings.data ? (
                <Loading />
              ) : rows.length === 0 ? (
                <Empty>No findings in scope.</Empty>
              ) : (
                <BarList
                  aria-label={`${c.id} findings by failing rule`}
                  items={ruleCounts(rows).map((it) => ({
                    ...it,
                    render: (label) => (
                      <Link to="/findings" search={{ ...findingsSearch, rule: label }} className="hover:text-accent">
                        {label}
                      </Link>
                    ),
                  }))}
                />
              )}
            </Card>
            <Card title="Oldest findings" subtitle="Longest failing first; a row opens the finding." bodyClassName="p-0">
              {!findings.data ? (
                <Loading />
              ) : oldest.length === 0 ? (
                <Empty>No findings in scope.</Empty>
              ) : (
                <ul className="divide-y divide-border text-[13px]" aria-label="Oldest findings">
                  {oldest.map((f) => (
                    <li key={findingKey(f)}>
                      <Link to="/findings" search={{ ...findingsSearch, open: findingKey(f) }} className="flex items-baseline justify-between gap-3 px-3.5 py-1.5 hover:bg-panel-2">
                        <span className="min-w-0">
                          <span className="block truncate font-mono">
                            {f.org}/{f.repository}
                          </span>
                          <span className="block truncate text-xs text-muted-foreground">{f.failing_rules.join(", ")}</span>
                        </span>
                        <span className="flex-none font-mono text-xs text-muted-foreground tabular-nums">{ageDays(f.last_changed_at)}d</span>
                      </Link>
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          </div>
        </div>
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
