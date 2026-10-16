import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useHistory, useOrg, useOrgs, useRules, useSummary } from "../api/queries";
import { ChartCard, formatValue, TrendChart, trendTable } from "../components/charts";
import { Loading, Section } from "../components/common";
import { OrgScope, PageHeader, RangeTabs } from "../components/page";
import { Card, CardContent } from "../components/ui/card";
import { DeltaText } from "../components/ui/kpi";
import { Pill, PillGroup } from "../components/ui/pill";
import { StatStrip } from "../components/ui/stat-strip";
import { formatPercent, humanize } from "../lib/format";
import { parseRange, rangeDays, rangeFrom, type Range } from "../lib/range";
import { patchSearch } from "../lib/search";
import {
  cadenceWord,
  deltaFrom,
  deltaOver,
  failingRuleCount,
  FLEET_SERIES,
  type FleetPoint,
  fleetSeries,
  type FleetSeriesKey,
  fleetSeriesFor,
  isFleetSeriesKey,
  latestValue,
  parkedTotal,
  worstRules,
} from "../lib/viewmodel";

const fleet = getRouteApi("/app/");

export interface FleetSearch {
  series?: FleetSeriesKey;
}

// parseFleetSearch reads `?series=`; an unknown value draws the default.
export function parseFleetSearch(s: Record<string, unknown>): FleetSearch {
  return isFleetSeriesKey(s.series) ? { series: s.series } : {};
}

const DEFAULT_SERIES: FleetSeriesKey = "compliant";

// FleetView is the landing page (DESIGN-0036 §Fleet). With `?org=` the
// same layout reads from the org endpoints.
export function FleetView() {
  const { org, range: rawRange, series } = fleet.useSearch();
  const range = parseRange(rawRange);
  const key = series ?? DEFAULT_SERIES;
  return org ? <OrgFleet org={org} range={range} series={key} /> : <WholeFleet range={range} series={key} />;
}

function WholeFleet({ range, series }: { range: Range; series: FleetSeriesKey }) {
  const summary = useSummary();
  const rules = useRules();
  const history = useHistory({ from: rangeFrom(range) });
  if (!summary.data) {
    return <Loading />;
  }
  const s = summary.data;
  const points = fleetSeries(history.data?.items ?? []);
  return (
    <>
      <PageHeader
        title="Fleet"
        subtitle={
          <>
            Fleet compliance from <code>/summary</code> and <code>/compliance/history</code>.
          </>
        }
        controls={
          <>
            <OrgScope />
            <RangeTabs />
          </>
        }
      />
      <div className="grid gap-4 lg:grid-cols-3">
        <ComplianceCard
          className="lg:col-span-2"
          range={range}
          series={series}
          points={points}
          truncated={history.data?.truncated ?? false}
          tracked={s.tracked}
          percent={s.compliant_percent}
          findings={s.findings}
          parked={s.parked}
          rules={rules.data?.items}
        />
        <Section title="Open PRs by age">
          <div className="flex flex-wrap gap-4 text-sm">
            {s.open_prs.map((b) => (
              <div key={b.bucket}>
                <span className="text-muted-foreground">{b.bucket}</span> <span className="font-medium tabular-nums">{b.count}</span>
              </div>
            ))}
          </div>
          {s.stale_prs > 0 ? (
            <Link to="/findings" search={{ pr_stale: true }} className="text-sm underline underline-offset-2">
              {s.stale_prs} PRs older than {s.stale_after}
            </Link>
          ) : null}
        </Section>
      </div>
      <div className="grid gap-6 md:grid-cols-2">
        <WorstRules />
        <Section title="Parked by reason">
          <ParkedList parked={s.parked} />
        </Section>
      </div>
    </>
  );
}

function OrgFleet({ org, range, series }: { org: string; range: Range; series: FleetSeriesKey }) {
  const detail = useOrg(org);
  const orgs = useOrgs();
  const history = useHistory({ org, from: rangeFrom(range) });
  if (!detail.data) {
    return <Loading />;
  }
  const d = detail.data;
  const summary = orgs.data?.items.find((o) => o.org.toLowerCase() === org.toLowerCase());
  const points = fleetSeries(history.data?.items ?? []);
  return (
    <>
      <PageHeader
        title={d.org}
        subtitle={
          <>
            Compliance for one org from <code>/orgs/{d.org}</code>.
          </>
        }
        controls={
          <>
            <OrgScope />
            <RangeTabs />
          </>
        }
      />
      <div className="grid gap-4 lg:grid-cols-3">
        <ComplianceCard
          className="lg:col-span-2"
          range={range}
          series={series}
          points={points}
          truncated={history.data?.truncated ?? false}
          tracked={d.tracked}
          percent={summary?.compliant_percent ?? null}
          findings={summary?.counts ?? sumCounts(d.rules)}
          parked={d.parked}
          rules={d.rules}
        />
      </div>
      <div className="grid gap-6 md:grid-cols-2">
        <Section title="Worst rules">
          <RuleBars rules={worstRules(d.rules, 5)} />
        </Section>
        <Section title="Parked by reason">
          <ParkedList parked={d.parked} />
        </Section>
      </div>
    </>
  );
}

// sumCounts adds a rule list's counts into one StatusCounts.
export function sumCounts(rules: readonly Pick<Schemas["RuleCompliance"], "counts">[]): Schemas["StatusCounts"] {
  const out = { compliant: 0, non_compliant: 0, not_applicable: 0, unknown: 0 };
  for (const r of rules) {
    out.compliant += r.counts.compliant;
    out.non_compliant += r.counts.non_compliant;
    out.not_applicable += r.counts.not_applicable;
    out.unknown += r.counts.unknown;
  }
  return out;
}

// ComplianceCard is the fleet's main card (DESIGN-0036 §Fleet): a stat
// strip, the series pills as a radio group, and the one area chart for
// the selected series. Deltas come from the same points the chart
// draws, so the strip and the chart cannot disagree.
function ComplianceCard({
  range,
  series,
  points,
  truncated,
  tracked,
  percent,
  findings,
  parked,
  rules,
  className,
}: {
  range: Range;
  series: FleetSeriesKey;
  points: FleetPoint[];
  truncated: boolean;
  tracked: number;
  percent: number | null;
  findings: Schemas["StatusCounts"];
  parked: Schemas["ParkedCount"][];
  rules: Schemas["RuleCompliance"][] | undefined;
  className?: string;
}) {
  const def = FLEET_SERIES.find((s) => s.key === series) ?? FLEET_SERIES[0]!;
  const drawn = fleetSeriesFor(points, series);
  const compliantDelta = deltaFrom(fleetSeriesFor(points, "compliant"));
  const failingDelta = deltaOver(fleetSeriesFor(points, "failing"), 7);
  const strip = (
    <StatStrip
      cells={[
        { label: "Tracked", value: tracked.toLocaleString(), subline: "repositories" },
        {
          label: "Compliant",
          value: formatPercent(percent),
          subline: <DeltaText delta={compliantDelta === null ? null : { value: compliantDelta, goodUp: true, unit: " pts" }} />,
        },
        {
          label: "Failing",
          value: findings.non_compliant.toLocaleString(),
          subline: (
            <>
              {rules ? `${failingRuleCount(rules)} rules` : "findings"}
              {failingDelta === null ? null : (
                <>
                  {" · "}
                  <DeltaText delta={{ value: failingDelta, goodUp: false }} /> 7d
                </>
              )}
            </>
          ),
        },
        { label: "Parked", value: parkedTotal(parked).toLocaleString(), subline: `${parked.length} ${parked.length === 1 ? "reason" : "reasons"}` },
        { label: "Snapshots", value: points.length.toLocaleString(), subline: cadenceWord(points) },
      ]}
    />
  );
  return (
    <ChartCard
      {...(className === undefined ? {} : { className })}
      title={`Compliance (${range})`}
      subtitle={
        <>
          Fleet compliance % from <code>/compliance/history</code>; compliant ÷ (compliant + non_compliant) over active repositories.
        </>
      }
      right={
        <Link to="/history" className="text-xs text-muted-foreground hover:text-foreground">
          full history →
        </Link>
      }
      strip={strip}
      legend={<SeriesPills points={points} series={series} />}
      table={trendTable(drawn, def.label, def.unit)}
    >
      <TrendChart series={drawn} unit={def.unit} color={def.color} rangeDays={rangeDays(range)} label={def.label} aria-label={`${def.label} over ${range}`} />
      {truncated ? <p className="mt-2 text-xs text-muted-foreground">History is longer than the page cap; the series shows its first pages only.</p> : null}
    </ChartCard>
  );
}

// SeriesPills is the legend as a radio group: exactly one series is
// drawn, and each pill shows its series' current value.
function SeriesPills({ points, series }: { points: FleetPoint[]; series: FleetSeriesKey }) {
  const navigate = useNavigate();
  const select = (key: FleetSeriesKey) =>
    void navigate({ to: ".", search: (prev: Record<string, unknown>) => patchSearch(prev, { series: key === DEFAULT_SERIES ? undefined : key }) });
  return (
    <PillGroup label="Series" className="mt-3">
      {FLEET_SERIES.map((s) => (
        <Pill key={s.key} color={s.color} label={s.label} value={formatValue(latestValue(fleetSeriesFor(points, s.key)), s.unit)} checked={s.key === series} onSelect={() => select(s.key)} />
      ))}
    </PillGroup>
  );
}

function WorstRules() {
  const rules = useRules().data?.items;
  return <Section title="Worst rules">{rules ? <RuleBars rules={worstRules(rules, 5)} /> : <Loading />}</Section>;
}

function RuleBars({ rules }: { rules: Schemas["RuleCompliance"][] }) {
  return (
    <Card>
      <CardContent className="divide-y divide-border p-0">
        {rules.map((r) => (
          <Link
            key={`${r.kind}/${r.name}`}
            to="/rules/$kind/$name"
            params={{ kind: r.kind, name: r.name }}
            className="flex items-center justify-between px-4 py-2 text-sm hover:bg-muted/50"
          >
            <span>{r.name}</span>
            <span className="tabular-nums">{formatPercent(r.compliant_percent)} ▸</span>
          </Link>
        ))}
      </CardContent>
    </Card>
  );
}

function ParkedList({ parked }: { parked: Schemas["ParkedCount"][] }) {
  if (parked.length === 0) {
    return <p className="text-sm text-muted-foreground">Nothing parked.</p>;
  }
  return (
    <p className="text-sm">
      {parked.map((p, i) => (
        <span key={p.reason}>
          {i > 0 ? " · " : null}
          {humanize(p.reason)} <span className="font-medium tabular-nums">{p.count}</span>
        </span>
      ))}
    </p>
  );
}
