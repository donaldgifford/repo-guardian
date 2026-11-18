import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";

import type { Schemas } from "../api/client";
import { useComplianceHistory, useControls, useOrg, useOrgs, useRemediationActivity, useRemediationEnabled, useSummary } from "../api/queries";
import { DistBar } from "@donaldgifford/design-system/blocks/dist-bar";
import { BarBuckets, bucketsTable } from "@donaldgifford/design-system/blocks/bar-buckets";
import { BarList } from "@donaldgifford/design-system/blocks/bar-list";
import { PairBars, pairTable } from "@donaldgifford/design-system/blocks/pair-bars";
import { Sparkline } from "@donaldgifford/design-system/blocks/sparkline";
import { TrendChart, trendTable } from "@donaldgifford/design-system/blocks/trend-chart";
import { ChartCard } from "@donaldgifford/design-system/blocks/chart-card";
import { Empty, Loading } from "../components/common";
import { ExportButton } from "@donaldgifford/design-system/blocks/export-button";
import { gatedContent, NOT_ENABLED } from "../components/GatedPanel";
import { OrgScope, useOrgScope } from "../components/page/OrgScope";
import { PageHeader } from "../components/page/PageHeader";
import { RangeTabs, useRangeDays } from "../components/page/RangeTabs";
import { CoverageText, PostureBar, PostureMeter } from "../components/posture";
import { Badge } from "@donaldgifford/design-system/components/badge";
import { Panel } from "../components/Panel";
import { DataTable, type DataTableColumn } from "@donaldgifford/design-system/blocks/data-table";
import { DeltaText } from "../components/DeltaText";
import { StatStrip } from "@donaldgifford/design-system/blocks/stat-strip";
import type { CsvColumns } from "@donaldgifford/design-system/lib/csv";
import { AGE_COLORS, byControl, DEFAULT_SERIES, FLEET_SERIES, type FleetSeries, isFleetSeries, pickSeries, rangeDelta, SERIES, STALE_BUCKET, underDays, worstControls } from "../lib/fleet";
import { humanize } from "../lib/format";
import { postureCsvHead, postureCsvRow } from "../lib/posture";
import { patchSearch } from "../lib/search";
import type { SeriesPoint } from "../lib/viewmodel";
import { exclusionColumns, exclusionsCsv } from "./orgs";
import { SeriesPills } from "@donaldgifford/design-system/blocks/series-pills";
import { formatPercent } from "@donaldgifford/design-system/lib/percent";

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
        description={
          <>
            Compliance over every assignment in the orgs you can see · <code>GET /summary</code>
          </>
        }
        actions={
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
            description={
              <>
                <code>/compliance/history?aggregate=fleet</code>: compliant ÷ (compliant + non_compliant), summed per day in SQL.
              </>
            }
            action={
              <Link to="/history" className="text-xs text-primary hover:underline">
                full history
              </Link>
            }
            strip={
              <StatStrip
                items={[
                  { label: "Tracked", value: tracked.toLocaleString(), sub: "active repositories" },
                  {
                    label: "Compliant",
                    value: formatPercent(posture.percent),
                    sub: delta === undefined ? `over ${days}d` : delta === null ? `no delta over ${days}d` : <><DeltaText delta={delta} /> over {days}d</>,
                  },
                  { label: "Non-compliant", value: posture.non_compliant.toLocaleString(), sub: `results · ${s.controls_failing.toLocaleString()} controls with any` },
                  { label: "Parked", value: parked.toLocaleString(), sub: `${s.parked_by_reason.length} ${s.parked_by_reason.length === 1 ? "reason" : "reasons"}` },
                  { label: "Snapshots", value: fleetHistory.data ? points.length.toLocaleString() : "…", sub: `daily · ${days}d` },
                ]}
              />
            }
            table={trendTable(points, spec.label, { unit: spec.unit, head: "Snapshot", noun: "snapshots" })}
          >
            <SeriesPills
              label="Series"
              className="mb-2"
              value={series}
              onValueChange={(k) => {
                if (isFleetSeries(k)) setSeries(k);
              }}
              items={FLEET_SERIES.map((k) => ({
                key: k,
                label: SERIES[k].label,
                color: SERIES[k].color,
                value: k === "percent" ? formatPercent(posture.percent) : posture[k].toLocaleString(),
              }))}
            />
            {fleetHistory.data ? <TrendChart points={points} unit={spec.unit} color={spec.color} series={spec.label} rangeDays={days} height={200} label={`${spec.label} over the last ${days} days`} /> : <Loading />}
          </ChartCard>
          <Panel
            title="Finding age"
            subtitle={
              <>
                How long non-compliant results have been failing (<code>now − last_changed_at</code>){org ? ", every org" : ""}.
              </>
            }
          >
            <div className="mb-2">
              <Badge variant="info">{under30 === null ? "no findings" : `${under30}% under 30d`}</Badge>
            </div>
            <DistBar segments={s.finding_age.map((b, i) => ({ label: b.bucket, value: b.count, color: AGE_COLORS[i] ?? AGE_COLORS[5] }))} size="sm" label="Finding age distribution" />
            <p className="mt-3 text-xs text-muted-foreground">
              <Link to="/findings" search={{ sort: "since_asc", ...scoped }} className="text-primary hover:underline">
                oldest findings first →
              </Link>
            </p>
          </Panel>
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <Panel title="Worst controls" subtitle="Lowest measured compliance; delta over the range.">
            {!worst ? (
              <Loading />
            ) : worst.length === 0 ? (
              <Empty>Nothing measured yet.</Empty>
            ) : (
              // Same row metrics as the Parked by reason bar list beside it
              // (12.5px text, 4px to the bar, an 8px bar slot, 8px between
              // rows), so the two cards' rows line up across the page.
              <ol className="mt-1 grid gap-2" aria-label="Worst controls">
                {worst.map((c) => {
                  const d = controlHistory.data ? rangeDelta(perControl.get(c.id) ?? []) : undefined;
                  return (
                    <li key={c.id} className="grid gap-1 text-[12.5px]">
                      <div className="flex items-baseline justify-between gap-3">
                        <span className="min-w-0 truncate">
                          <Link to="/controls/$id" params={{ id: c.id }} search={scoped} className="font-mono font-medium hover:text-primary">
                            {c.id}
                          </Link>
                          <em className="ml-1.5 text-[11px] text-muted-foreground not-italic">{c.type}</em>
                        </span>
                        <b className="font-medium tabular-nums whitespace-nowrap">
                          {formatPercent(c.compliance.percent)}
                          {/* A fixed-width delta keeps the percentages in one column. */}
                          {d ? <span className="ml-1 inline-block min-w-[4.5rem] text-right text-xs font-normal"><DeltaText delta={d} /></span> : null}
                        </b>
                      </div>
                      <div className="flex h-2 flex-col justify-center">
                        <PostureMeter posture={c.compliance} label={`${c.id} compliance`} showValue={false} />
                      </div>
                    </li>
                  );
                })}
              </ol>
            )}
          </Panel>
          <Panel
            title="Parked by reason"
            subtitle={
              <>
                {/* One line, like its neighbours: a wrapped subtitle drops this list below the Worst controls rows. */}
                Why repositories are out of measurement{org ? ", every org" : ""}.
              </>
            }
          >
            {s.parked_by_reason.length === 0 ? <Empty>Nothing parked.</Empty> : <BarList label="Parked repositories by reason" color="var(--color-warning)" items={s.parked_by_reason.map((p) => ({ label: humanize(p.reason), value: p.count }))} />}
          </Panel>
          <Panel title="Assignments" subtitle="Every assignment lands in exactly one bucket.">
            <PostureBar posture={posture} label="Fleet assignments by bucket" />
          </Panel>
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          <Panel
            title="Compliance by control"
            subtitle={
              <>
                From <code>/controls</code>; sparkline from <code>aggregate=control</code> over the range.
              </>
            }
            bodyClassName="p-0"
            right={<ExportButton name="controls" filename="repo-guardian-controls" filter={{ org }} columns={controlsCsv} rows={controls.data?.items} />}
          >
            {controls.data ? <DataTable sortable={false} bare columns={byControlColumns(perControl)} data={controls.data.items} getRowId={(c) => c.id} empty="No control is assigned yet." label="Controls" /> : <Loading />}
          </Panel>
          {org ? (
            <Panel
              title="Exclusions by reason"
              subtitle={
                <>
                  What the policy leaves out in {org}, and why (<code>OrgDetail.exclusions</code>).
                </>
              }
              bodyClassName="p-0"
              right={<ExportButton name="exclusions" filename="repo-guardian-exclusions" filter={{ org }} columns={exclusionsCsv} rows={orgDetail.data?.exclusions} />}
            >
              {!orgDetail.data ? <Loading /> : orgDetail.data.exclusions.length === 0 ? <Empty>Nothing excluded in {org}.</Empty> : <DataTable sortable={false} bare columns={exclusionColumns} data={orgDetail.data.exclusions} getRowId={(e) => `${e.repository}/${e.control_id ?? ""}`} label="Exclusions" />}
            </Panel>
          ) : (
            <Panel
              title="Orgs"
              subtitle={
                <>
                  From <code>/orgs</code>: tracked, non-compliant and parked per org you can see.
                </>
              }
              bodyClassName="p-0"
              right={<ExportButton name="orgs" filename="repo-guardian-orgs" columns={orgsCsv} rows={orgs.data?.items} />}
            >
              {orgs.data ? <DataTable sortable={false} bare columns={orgColumns} data={[...orgs.data.items].sort((a, b) => b.tracked - a.tracked)} getRowId={(o) => o.org} empty="No org is visible to you." label="Orgs" /> : <Loading />}
            </Panel>
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

// ResultBadges is a control's measured results as two toned badges,
// compliant and non-compliant, each named in full for the tooltip and a
// screen reader; a zero reads muted. Two count columns read as a run of
// bare numbers beside the percentage.
function ResultBadges({ posture: p }: { posture: Schemas["ControlSummary"]["compliance"] }) {
  const one = (n: number, tone: "success" | "destructive", glyph: string, label: string) => (
    <Badge variant={n === 0 ? "muted" : tone} title={`${n.toLocaleString()} ${label}`}>
      <span aria-hidden="true">{glyph} </span>
      {n.toLocaleString()}
      <span className="sr-only"> {label}</span>
    </Badge>
  );
  return (
    <span className="inline-flex gap-1.5 whitespace-nowrap">
      {one(p.compliant, "success", "✓", "compliant")}
      {one(p.non_compliant, "destructive", "✗", "non-compliant")}
    </span>
  );
}

// byControlColumns is the Compliance-by-control table: the results as
// badges, the percentage and a sparkline from the one aggregate=control history.
// No Type (the Controls page has it, and it mostly repeats the id) and
// no N/A (the Assignments panel has it): the card is half the page and
// must never scroll sideways.
export function byControlColumns(perControl: ReadonlyMap<string, SeriesPoint[]>): DataTableColumn<Schemas["ControlSummary"]>[] {
  return [
    {
      id: "id",
      header: "Control",
      cell: ({ row: { original: c } }) => (
        <Link to="/controls/$id" params={{ id: c.id }} className="font-mono font-medium hover:text-primary">
          {c.id}
        </Link>
      ),
    },
    { id: "results", header: "Results", cell: ({ row: { original: c } }) => <ResultBadges posture={c.compliance} /> },
    // The percentage is the row's answer, in the heaviest weight.
    { id: "percent", header: "%", cell: ({ row: { original: c } }) => <span className="text-[14px] font-semibold">{formatPercent(c.compliance.percent)}</span>, meta: { numeric: true } },
    { id: "trend", header: "Trend", cell: ({ row: { original: c } }) => <Sparkline values={(perControl.get(c.id) ?? []).map((p) => p.value)} /> },
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
      <BarBuckets buckets={buckets.map((b) => ({ label: b.bucket, value: b.count }))} series="open PRs" height={160} label="Open pull requests by age" />
      <p className="mt-2 text-xs text-muted-foreground">
        {stale.toLocaleString()} over threshold →{" "}
        <Link to="/findings" search={{ pr_stale: true, ...(org ? { org } : {}) }} className="text-primary hover:underline">
          findings?pr_stale=true
        </Link>
      </p>
    </>
  );
  return (
    <ChartCard
      title="Open PRs by age"
      description={
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
      description={
        <>
          Pull-request remediations opened vs closed per day (<code>/remediations/activity</code>).
        </>
      }
      table={pairTable(pairs, labels)}
      {...(className === undefined ? {} : { className })}
    >
      <div data-gated={state}>
        {state === "content" ? days ? <PairBars days={pairs} series={labels} height={160} label="Pull requests opened and closed per day" /> : <Loading /> : state === "loading" ? <Loading /> : <Empty>{state === "not_enabled" ? NOT_ENABLED : "No pull request activity in 14 days."}</Empty>}
      </div>
    </ChartCard>
  );
}

export const controlColumns: DataTableColumn<Schemas["ControlSummary"]>[] = [
  {
    id: "id",
    header: "Control",
    cell: ({ row: { original: c } }) => (
      <Link to="/controls/$id" params={{ id: c.id }} className="font-mono font-medium hover:text-primary">
        {c.id}
      </Link>
    ),
  },
  { id: "type", header: "Type", cell: ({ row: { original: c } }) => <span className="font-mono text-xs text-muted-foreground">{c.type}</span>, meta: { hideBelow: "md" } },
  { id: "compliance", header: "Compliance", cell: ({ row: { original: c } }) => <PostureMeter posture={c.compliance} label={`${c.id} compliance`} showValue={false} />, meta: { className: "w-[40%]" } },
  { id: "coverage", header: "Coverage", cell: ({ row: { original: c } }) => <CoverageText posture={c.compliance} />, meta: { numeric: true } },
];

export const controlsCsv: CsvColumns<Schemas["ControlSummary"]> = {
  head: ["control", "type", ...postureCsvHead],
  row: (c) => [c.id, c.type, ...postureCsvRow(c.compliance)],
};

export const orgsCsv: CsvColumns<Schemas["OrgSummary"]> = {
  head: ["org", "tracked", "parked", "open_prs", ...postureCsvHead],
  row: (o) => [o.org, o.tracked, o.parked, o.open_prs, ...postureCsvRow(o.compliance)],
};

const orgColumns: DataTableColumn<Schemas["OrgSummary"]>[] = [
  {
    id: "org",
    header: "Org",
    cell: ({ row: { original: o } }) => (
      <Link to="/orgs/$org" params={{ org: o.org }} className="font-mono font-medium hover:text-primary">
        {o.org}
      </Link>
    ),
  },
  { id: "tracked", header: "Repos", cell: ({ row: { original: o } }) => o.tracked.toLocaleString(), meta: { numeric: true } },
  { id: "compliance", header: "Compliance", cell: ({ row: { original: o } }) => <PostureMeter posture={o.compliance} label={`${o.org} compliance`} />, meta: { className: "w-[45%]" } },
];
