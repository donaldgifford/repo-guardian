import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";

import type { Schemas } from "../api/client";
import { useComplianceHistory, useControls, useOrg, useOrgs, useRemediationActivity, useRemediationEnabled, useSummary } from "../api/queries";
import { BarBuckets, BarList, bucketsTable, ChartCard, DistBar, PairBars, pairTable, Sparkline, TrendChart, trendTable } from "../components/charts";
import { Empty, Loading } from "../components/common";
import { ExportButton } from "../components/ExportButton";
import { gatedContent, NOT_ENABLED } from "../components/GatedPanel";
import { OrgScope, useOrgScope } from "../components/page/OrgScope";
import { PageHeader } from "../components/page/PageHeader";
import { RangeTabs, useRangeDays } from "../components/page/RangeTabs";
import { CoverageText, PostureBar, PostureMeter } from "../components/posture";
import { Badge } from "../components/ui/badge";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { DeltaText } from "../components/ui/kpi";
import { StatStrip } from "../components/ui/stat-strip";
import type { CsvColumns } from "../lib/csv";
import { AGE_COLORS, byControl, DEFAULT_SERIES, FLEET_SERIES, type FleetSeries, pickSeries, rangeDelta, SERIES, STALE_BUCKET, underDays, worstControls } from "../lib/fleet";
import { formatPercent, humanize } from "../lib/format";
import { postureCsvHead, postureCsvRow } from "../lib/posture";
import { patchSearch } from "../lib/search";
import { cn } from "../lib/utils";
import type { SeriesPoint } from "../lib/viewmodel";
import { exclusionColumns, exclusionsCsv } from "./orgs";

const route = getRouteApi("/app/");

// FleetView is the landing page (DESIGN-0037 §Fleet panels): the
// compliance card with its KPI strip and series pills, finding age,
// worst controls, parked by reason, assignments, compliance by control
// with sparklines, the orgs (or one org's exclusions) and the two
// remediation-gated PR panels. Every history-backed panel follows the
// range and the org scope; the fleet asks for each aggregate once.
export function FleetView() {
  const search = route.useSearch();
  const navigate = useNavigate();
  const org = useOrgScope();
  const days = useRangeDays();
  const scoped = org ? { org } : {};
  const summary = useSummary();
  const controls = useControls(org);
  const orgs = useOrgs();
  const orgDetail = useOrg(org ?? "", org !== undefined);
  const remediation = useRemediationEnabled();
  const fleetHistory = useComplianceHistory({ days, aggregate: "fleet", ...scoped });
  const controlHistory = useComplianceHistory({ days, aggregate: "control", ...scoped });
  const activity = useRemediationActivity(14, remediation === true);
  const series: FleetSeries = search.series ?? DEFAULT_SERIES;
  const points = useMemo(() => pickSeries(fleetHistory.data?.items ?? [], series), [fleetHistory.data, series]);
  const percentSeries = useMemo(() => (series === "percent" ? points : pickSeries(fleetHistory.data?.items ?? [], "percent")), [fleetHistory.data, points, series]);
  const perControl = useMemo(() => byControl(controlHistory.data?.items ?? []), [controlHistory.data]);
  if (!summary.data) {
    return <Loading />;
  }
  const s = summary.data;
  // /summary is the fleet; with one org in scope the strip's tracked,
  // parked and posture come from that org's row of /orgs.
  const orgRow = org ? orgs.data?.items.find((o) => o.org === org) : undefined;
  const posture = orgRow?.compliance ?? s.compliance;
  const tracked = orgRow?.tracked ?? s.tracked;
  const parked = orgRow?.parked ?? s.parked;
  const spec = SERIES[series];
  const delta = fleetHistory.data ? rangeDelta(percentSeries) : undefined;
  const setSeries = (next: FleetSeries) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => patchSearch(prev, { series: next === DEFAULT_SERIES ? undefined : next }) });
  const under30 = underDays(s.finding_age, 3);
  const worst = controls.data ? worstControls(controls.data.items) : undefined;
  return (
    <>
      <PageHeader
        title="Fleet"
        subtitle={
          <>
            Compliance over every assignment in the orgs you can see · <code>GET /summary</code>
          </>
        }
        controls={
          <>
            <OrgScope />
            <RangeTabs />
          </>
        }
      />
      <div className="space-y-4">
        <div className="grid gap-4 lg:grid-cols-3">
          <ChartCard
            className="lg:col-span-2"
            title={`Compliance (${days}d)`}
            subtitle={
              <>
                <code>/compliance/history?aggregate=fleet</code>: compliant ÷ (compliant + non_compliant), summed per day in SQL.
              </>
            }
            right={
              <Link to="/history" className="text-xs text-accent hover:underline">
                full history
              </Link>
            }
            strip={
              <StatStrip
                cells={[
                  { label: "Tracked", value: tracked.toLocaleString(), subline: "active repositories" },
                  {
                    label: "Compliant",
                    value: formatPercent(posture.percent),
                    subline: delta === undefined ? `over ${days}d` : delta === null ? `no delta over ${days}d` : <><DeltaText delta={delta} /> over {days}d</>,
                  },
                  { label: "Non-compliant", value: posture.non_compliant.toLocaleString(), subline: `results · ${s.controls_failing.toLocaleString()} controls with any` },
                  { label: "Parked", value: parked.toLocaleString(), subline: `${s.parked_by_reason.length} ${s.parked_by_reason.length === 1 ? "reason" : "reasons"}` },
                  { label: "Snapshots", value: fleetHistory.data ? points.length.toLocaleString() : "…", subline: `daily · ${days}d` },
                ]}
              />
            }
            table={trendTable(points, spec.label, spec.unit)}
          >
            <div role="radiogroup" aria-label="Series" className="mb-2 flex flex-wrap gap-1.5">
              {FLEET_SERIES.map((k) => (
                <button key={k} type="button" role="radio" aria-checked={series === k} onClick={() => setSeries(k)} className={cn("inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs", series === k ? "border-accent bg-accent/12 text-foreground" : "border-border text-muted-foreground hover:text-foreground")}>
                  <i aria-hidden="true" className="inline-block h-2 w-2 rounded-full" style={{ background: SERIES[k].color }} />
                  {SERIES[k].label}: <b className="font-medium tabular-nums">{k === "percent" ? formatPercent(posture.percent) : posture[k].toLocaleString()}</b>
                </button>
              ))}
            </div>
            {fleetHistory.data ? <TrendChart series={points} unit={spec.unit} color={spec.color} label={spec.label} rangeDays={days} height={200} aria-label={`${spec.label} over the last ${days} days`} /> : <Loading />}
          </ChartCard>
          <Card
            title="Finding age"
            subtitle={
              <>
                How long non-compliant results have been failing (<code>now − last_changed_at</code>){org ? ", every org" : ""}.
              </>
            }
          >
            <div className="mb-2">
              <Badge tone="info">{under30 === null ? "no findings" : `${under30}% under 30d`}</Badge>
            </div>
            <DistBar segments={s.finding_age.map((b, i) => ({ label: b.bucket, value: b.count, color: AGE_COLORS[i] ?? AGE_COLORS[5] }))} aria-label="Finding age distribution" />
            <p className="mt-3 text-xs text-muted-foreground">
              <Link to="/findings" search={{ sort: "since_asc", ...scoped }} className="text-accent hover:underline">
                oldest findings first →
              </Link>
            </p>
          </Card>
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <Card title="Worst controls" subtitle="Lowest measured compliance; delta over the range.">
            {!worst ? (
              <Loading />
            ) : worst.length === 0 ? (
              <Empty>Nothing measured yet.</Empty>
            ) : (
              <ol className="space-y-2.5" aria-label="Worst controls">
                {worst.map((c) => {
                  const d = controlHistory.data ? rangeDelta(perControl.get(c.id) ?? []) : undefined;
                  return (
                    <li key={c.id} className="text-[13px]">
                      <div className="flex items-baseline justify-between gap-3">
                        <span className="min-w-0 truncate">
                          <Link to="/controls/$id" params={{ id: c.id }} search={scoped} className="font-mono font-medium hover:text-accent">
                            {c.id}
                          </Link>
                          <em className="ml-1.5 text-[11px] text-muted-foreground not-italic">{c.type}</em>
                        </span>
                        <b className="font-medium tabular-nums whitespace-nowrap">
                          {formatPercent(c.compliance.percent)} {d ? <span className="ml-1 text-xs font-normal"><DeltaText delta={d} /></span> : null}
                        </b>
                      </div>
                      <PostureMeter posture={c.compliance} label={`${c.id} compliance`} showValue={false} />
                    </li>
                  );
                })}
              </ol>
            )}
          </Card>
          <Card
            title="Parked by reason"
            subtitle={
              <>
                Repositories out of measurement (<code>Summary.parked_by_reason</code>){org ? ", every org" : ""}.
              </>
            }
          >
            {s.parked_by_reason.length === 0 ? <Empty>Nothing parked.</Empty> : <BarList aria-label="Parked repositories by reason" color="var(--color-warn)" items={s.parked_by_reason.map((p) => ({ label: humanize(p.reason), value: p.count }))} />}
          </Card>
          <Card title="Assignments" subtitle="Every assignment lands in exactly one bucket.">
            <PostureBar posture={posture} label="Fleet assignments by bucket" />
          </Card>
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          <Card
            title="Compliance by control"
            subtitle={
              <>
                From <code>/controls</code>; sparkline from <code>aggregate=control</code> over the range.
              </>
            }
            bodyClassName="p-0"
            right={<ExportButton page="controls" filter={{ org }} columns={controlsCsv} rows={controls.data?.items} />}
          >
            {controls.data ? <DataTable columns={byControlColumns(perControl)} rows={controls.data.items} rowKey={(c) => c.id} empty="No control is assigned yet." aria-label="Controls" /> : <Loading />}
          </Card>
          {org ? (
            <Card
              title="Exclusions by reason"
              subtitle={
                <>
                  What the policy leaves out in {org}, and why (<code>OrgDetail.exclusions</code>).
                </>
              }
              bodyClassName="p-0"
              right={<ExportButton page="exclusions" filter={{ org }} columns={exclusionsCsv} rows={orgDetail.data?.exclusions} />}
            >
              {!orgDetail.data ? <Loading /> : orgDetail.data.exclusions.length === 0 ? <Empty>Nothing excluded in {org}.</Empty> : <DataTable columns={exclusionColumns} rows={orgDetail.data.exclusions} rowKey={(e) => `${e.repository}/${e.control_id ?? ""}`} aria-label="Exclusions" />}
            </Card>
          ) : (
            <Card
              title="Orgs"
              subtitle={
                <>
                  From <code>/orgs</code>: tracked, non-compliant and parked per org you can see.
                </>
              }
              bodyClassName="p-0"
              right={<ExportButton page="orgs" columns={orgsCsv} rows={orgs.data?.items} />}
            >
              {orgs.data ? <DataTable columns={orgColumns} rows={[...orgs.data.items].sort((a, b) => b.tracked - a.tracked)} rowKey={(o) => o.org} empty="No org is visible to you." aria-label="Orgs" /> : <Loading />}
            </Card>
          )}
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <ThroughputPanel enabled={remediation} days={activity.data?.items} className="lg:col-span-2" />
          <OpenPRsPanel enabled={remediation} buckets={s.open_prs_by_age} org={org} />
        </div>
      </div>
    </>
  );
}

// Count is a table count; zero is muted so the non-zero ones stand out.
function Count({ n }: { n: number }) {
  return n === 0 ? <span className="text-muted-foreground">0</span> : <>{n.toLocaleString()}</>;
}

// byControlColumns is the Compliance-by-control table: counts, N/A, the
// percentage and a sparkline from the one aggregate=control history.
export function byControlColumns(perControl: ReadonlyMap<string, SeriesPoint[]>): Column<Schemas["ControlSummary"]>[] {
  return [
    {
      key: "id",
      header: "Control",
      cell: (c) => (
        <Link to="/controls/$id" params={{ id: c.id }} className="font-mono font-medium hover:text-accent">
          {c.id}
        </Link>
      ),
    },
    // Type and N/A drop below xl: at lg the card is half the page, and
    // seven columns scrolled sideways and clipped the trend.
    { key: "type", header: "Type", cell: (c) => <Badge tone="outline">{c.type}</Badge>, hideBelow: "xl" },
    { key: "compliant", header: "Compliant", cell: (c) => <Count n={c.compliance.compliant} />, numeric: true },
    { key: "non_compliant", header: "Non-compliant", cell: (c) => <Count n={c.compliance.non_compliant} />, numeric: true },
    { key: "na", header: "N/A", cell: (c) => <span className="text-muted-foreground">{c.compliance.not_applicable.toLocaleString()}</span>, numeric: true, hideBelow: "xl" },
    // The percentage is the row's answer: set off from the counts by a
    // rule and in the heaviest weight, so it is not one more number in a
    // run of numbers.
    { key: "percent", header: "%", cell: (c) => <span className="text-[14px] font-semibold">{formatPercent(c.compliance.percent)}</span>, numeric: true, className: "border-l border-border" },
    { key: "trend", header: "Trend", cell: (c) => <Sparkline values={(perControl.get(c.id) ?? []).map((p) => p.percent)} /> },
  ];
}

// OpenPRsPanel is Open PRs by age (DESIGN-0037 §Remediation-gated
// panels): the four buckets from Summary.open_prs_by_age and the count
// over the stale threshold linking to the stale findings. Without the
// Remediation App it reads the one not-enabled sentence.
export function OpenPRsPanel({ enabled, buckets, org, className }: { enabled: boolean | undefined; buckets: readonly Schemas["AgeCount"][]; org: string | undefined; className?: string }) {
  const total = buckets.reduce((n, b) => n + b.count, 0);
  const stale = buckets.find((b) => b.bucket === STALE_BUCKET)?.count ?? 0;
  const state = gatedContent(enabled, total === 0);
  const content = (
    <>
      <BarBuckets buckets={buckets.map((b) => ({ label: b.bucket, value: b.count }))} label="open PRs" height={160} aria-label="Open pull requests by age" />
      <p className="mt-2 text-xs text-muted-foreground">
        {stale.toLocaleString()} over threshold →{" "}
        <Link to="/findings" search={{ pr_stale: true, ...(org ? { org } : {}) }} className="text-accent hover:underline">
          findings?pr_stale=true
        </Link>
      </p>
    </>
  );
  return (
    <ChartCard
      title="Open PRs by age"
      subtitle={
        <>
          Open pull-request remediations by <code>created_at</code>; {STALE_BUCKET} is stale.
        </>
      }
      table={bucketsTable(buckets.map((b) => ({ label: b.bucket, value: b.count })), "open PRs")}
      {...(className === undefined ? {} : { className })}
    >
      <div data-gated={state}>{state === "content" ? content : state === "loading" ? <Loading /> : <Empty>{state === "not_enabled" ? NOT_ENABLED : "No open pull requests."}</Empty>}</div>
    </ChartCard>
  );
}

// ThroughputPanel is PR throughput (14d): pull requests opened and
// closed per UTC day from /remediations/activity, gated the same way.
export function ThroughputPanel({ enabled, days, className }: { enabled: boolean | undefined; days: readonly Schemas["ActivityDay"][] | undefined; className?: string }) {
  const pairs = (days ?? []).map((d) => ({ day: d.day, a: d.opened, b: d.closed }));
  const labels = ["opened", "closed"] as const;
  const state = gatedContent(enabled, days !== undefined && pairs.every((d) => d.a === 0 && d.b === 0));
  return (
    <ChartCard
      title="PR throughput (14d)"
      subtitle={
        <>
          Pull-request remediations opened vs closed per day (<code>/remediations/activity</code>).
        </>
      }
      table={pairTable(pairs, labels)}
      {...(className === undefined ? {} : { className })}
    >
      <div data-gated={state}>
        {state === "content" ? days ? <PairBars days={pairs} labels={labels} height={160} aria-label="Pull requests opened and closed per day" /> : <Loading /> : state === "loading" ? <Loading /> : <Empty>{state === "not_enabled" ? NOT_ENABLED : "No pull request activity in 14 days."}</Empty>}
      </div>
    </ChartCard>
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
  { key: "compliance", header: "Compliance", cell: (c) => <PostureMeter posture={c.compliance} label={`${c.id} compliance`} showValue={false} />, width: "40%" },
  { key: "coverage", header: "Coverage", cell: (c) => <CoverageText posture={c.compliance} />, numeric: true },
];

export const controlsCsv: CsvColumns<Schemas["ControlSummary"]> = {
  head: ["control", "type", ...postureCsvHead],
  row: (c) => [c.id, c.type, ...postureCsvRow(c.compliance)],
};

export const orgsCsv: CsvColumns<Schemas["OrgSummary"]> = {
  head: ["org", "tracked", "parked", "open_prs", ...postureCsvHead],
  row: (o) => [o.org, o.tracked, o.parked, o.open_prs, ...postureCsvRow(o.compliance)],
};

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
