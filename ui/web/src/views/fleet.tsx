import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useHistory, useOrg, useOrgs, useRules, useSummary } from "../api/queries";
import { KindBadge } from "../components/badges";
import {
  BarBuckets,
  BarList,
  barListTable,
  bucketsTable,
  ChartCard,
  DistBar,
  type DistSegment,
  formatValue,
  type Segment,
  StackedBars,
  stackTable,
  TrendChart,
  trendTable,
} from "../components/charts";
import { Empty, Loading } from "../components/common";
import { OrgScope, PageHeader, RangeTabs } from "../components/page";
import { Card } from "../components/ui/card";
import { DeltaText } from "../components/ui/kpi";
import { Meter } from "../components/ui/meter";
import { Pill, PillGroup } from "../components/ui/pill";
import { StatStrip } from "../components/ui/stat-strip";
import { flags } from "../lib/flags";
import { formatPercent, humanizeDuration, trend } from "../lib/format";
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

// STATUS_SEGS is the one status palette every stacked bar uses: tones
// by meaning, never a categorical hue (DESIGN-0036 §Charts).
export const STATUS_SEGS: readonly Segment[] = [
  { key: "compliant", label: "compliant", color: "var(--color-ok)" },
  { key: "non_compliant", label: "non-compliant", color: "var(--color-bad)" },
  { key: "not_applicable", label: "not applicable", color: "var(--color-muted-foreground)" },
  { key: "unknown", label: "unknown", color: "var(--color-warn)" },
];

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
  const orgs = useOrgs();
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
        <OpenPRsCard summary={s} />
      </div>
      {flags.findingAge ? <FindingAgeCard buckets={[]} /> : null}
      <div className="grid gap-4 lg:grid-cols-3">
        <WorstRulesCard rules={rules.data?.items} />
        <ParkedCard parked={s.parked} />
        <FindingsByStatusCard counts={s.findings} />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <RulesTable rules={rules.data?.items} />
        <OrgsTable orgs={orgs.data?.items} />
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
  const counts = summary?.counts ?? sumCounts(d.rules);
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
          findings={counts}
          parked={d.parked}
          rules={d.rules}
        />
        <FindingsByStatusCard counts={counts} />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <WorstRulesCard rules={d.rules} />
        <ParkedCard parked={d.parked} />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <RulesTable rules={d.rules} />
        <NotApplicableCard reasons={d.not_applicable} />
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

// OpenPRsCard is the bar chart over summary.open_prs in bucket order,
// with the stale one-liner linking to the stale findings.
function OpenPRsCard({ summary }: { summary: Schemas["Summary"] }) {
  const buckets = summary.open_prs.map((b) => ({ label: b.bucket, value: b.count }));
  const threshold = humanizeDuration(summary.stale_after);
  return (
    <ChartCard
      title="Open PRs by age"
      subtitle={
        <>
          Remediation PRs opened by repo-guardian[bot]. <code>{threshold}</code>+ counts as stale.
        </>
      }
      table={bucketsTable(buckets, "open PRs")}
    >
      <BarBuckets buckets={buckets} label="open PRs" height={170} aria-label="open PRs by age bucket" />
      <p className="mt-2 text-xs text-muted-foreground">
        {summary.stale_prs > 0 ? (
          <Link to="/findings" search={{ pr_stale: true }} className="text-foreground hover:underline">
            {summary.stale_prs.toLocaleString()} {summary.stale_prs === 1 ? "PR" : "PRs"} over threshold →
          </Link>
        ) : (
          "0 PRs over threshold"
        )}
      </p>
    </ChartCard>
  );
}

// FindingAgeCard is the distribution of non-compliant finding ages. It
// needs finding_age on /summary (IMPL-0031 7.3) and stays hidden
// behind flags.findingAge until then.
export interface AgeBucket {
  label: string;
  count: number;
}

const AGE_COLORS = ["var(--color-ok)", "var(--color-ok)", "var(--color-ok)", "var(--color-warn)", "var(--color-bad)", "var(--color-bad)"];

function FindingAgeCard({ buckets }: { buckets: readonly AgeBucket[] }) {
  const segments: DistSegment[] = buckets.map((b, i) => ({ label: b.label, value: b.count, color: AGE_COLORS[i] ?? "var(--color-bad)" }));
  const total = buckets.reduce((n, b) => n + b.count, 0);
  const under30 = buckets.slice(0, 3).reduce((n, b) => n + b.count, 0);
  const share = total === 0 ? null : Math.round((100 * under30) / total);
  return (
    <ChartCard
      title="Finding age distribution"
      subtitle="How long non-compliant findings have been open. Under 30 days is the healthy threshold."
      right={share === null ? null : <span className="rounded-lg border border-border px-2 py-0.5 text-xs">{share}% under 30d · healthy threshold</span>}
      table={{ head: ["Age", "Findings"], numeric: [1], rows: buckets.map((b) => [b.label, b.count.toLocaleString()]) }}
    >
      <DistBar segments={segments} aria-label="non-compliant findings by age" />
    </ChartCard>
  );
}

// WorstRulesCard lists the five lowest rules with a trend and a meter;
// rows link to the rule.
function WorstRulesCard({ rules }: { rules: Schemas["RuleCompliance"][] | undefined }) {
  return (
    <Card title="Worst rules" subtitle="Lowest compliance first; unmeasured rules last." bodyClassName="p-0">
      {rules === undefined ? (
        <p className="px-3.5 py-2.5 text-sm text-muted-foreground">Loading…</p>
      ) : rules.length === 0 ? (
        <p className="px-3.5 py-2.5 text-sm text-muted-foreground">No rules in the policy.</p>
      ) : (
        <ul className="divide-y divide-border">
          {worstRules(rules, 5).map((r) => {
            const t = trend(r.compliant_percent, r.previous_percent);
            return (
              <li key={`${r.kind}/${r.name}`}>
                <Link to="/rules/$kind/$name" params={{ kind: r.kind, name: r.name }} className="grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-1 px-3.5 py-2 text-[13px] hover:bg-panel-2">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-medium">{r.name}</span>
                    <KindBadge kind={r.kind} />
                  </span>
                  <span className="text-right tabular-nums">
                    {formatPercent(r.compliant_percent)}
                    {t === null ? null : <span className="ml-1.5 text-xs text-muted-foreground">{t > 0 ? "▲" : t < 0 ? "▼" : "•"} {Math.abs(t).toFixed(1)}</span>}
                  </span>
                  <Meter percent={r.compliant_percent} showValue={false} className="col-span-2" />
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </Card>
  );
}

// ParkedCard is the bar list over summary.parked[]. Parking is a
// repository state, not a finding, so rows are static (OQ11).
function ParkedCard({ parked }: { parked: Schemas["ParkedCount"][] }) {
  const items = parked.map((p) => ({ label: p.reason, value: p.count }));
  return (
    <ChartCard title="Parked by reason" subtitle="Repositories set aside by the checker; parking is a repository state, not a finding." table={barListTable(items, "Reason")}>
      {items.length === 0 ? <Empty>Nothing parked.</Empty> : <BarList items={items} aria-label="parked repositories by reason" color="var(--color-muted-foreground)" />}
    </ChartCard>
  );
}

const th = "h-8 px-3 text-left text-[11px] font-semibold tracking-[0.04em] text-muted-foreground uppercase";
const td = "px-3 py-[9px] text-[13px] align-middle";
const num = "text-right tabular-nums";

// RulesTable is Compliance by rule: every rule's counts and percent,
// rows linking to the rule. The trend sparkline needs the fleet
// aggregate (7.1) and stays behind flags.ruleTrend.
function RulesTable({ rules }: { rules: Schemas["RuleCompliance"][] | undefined }) {
  return (
    <Card
      title="Compliance by rule"
      subtitle={
        <>
          From <code>/rules</code>; percent is compliant ÷ (compliant + non_compliant).
        </>
      }
      bodyClassName="p-0"
    >
      {rules === undefined ? (
        <p className="px-3.5 py-2.5 text-sm text-muted-foreground">Loading…</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-border">
                <th className={th}>Rule</th>
                <th className={th}>Kind</th>
                <th className={`${th} ${num}`}>Compliant</th>
                <th className={`${th} ${num}`}>Failing</th>
                <th className={`${th} ${num}`}>N/A</th>
                <th className={`${th} ${num}`}>%</th>
                {flags.ruleTrend ? <th className={`${th} ${num}`}>Trend</th> : null}
              </tr>
            </thead>
            <tbody>
              {rules.map((r) => (
                <tr key={`${r.kind}/${r.name}`} className="border-b border-border last:border-0 hover:bg-panel-2">
                  <td className={td}>
                    <Link to="/rules/$kind/$name" params={{ kind: r.kind, name: r.name }} className="font-medium hover:underline">
                      {r.name}
                    </Link>
                  </td>
                  <td className={td}>
                    <KindBadge kind={r.kind} />
                  </td>
                  <td className={`${td} ${num}`}>{r.counts.compliant.toLocaleString()}</td>
                  <td className={`${td} ${num}`}>{r.counts.non_compliant.toLocaleString()}</td>
                  <td className={`${td} ${num}`}>{r.counts.not_applicable.toLocaleString()}</td>
                  <td className={`${td} ${num}`}>{formatPercent(r.compliant_percent)}</td>
                  {flags.ruleTrend ? <td className={`${td} ${num}`}>—</td> : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

// OrgsTable is every visible org with a compliance meter; rows link to
// the org.
function OrgsTable({ orgs }: { orgs: Schemas["OrgSummary"][] | undefined }) {
  return (
    <Card
      title="Orgs"
      subtitle={
        <>
          From <code>/orgs</code>; tracked excludes parked repositories.
        </>
      }
      bodyClassName="p-0"
    >
      {orgs === undefined ? (
        <p className="px-3.5 py-2.5 text-sm text-muted-foreground">Loading…</p>
      ) : orgs.length === 0 ? (
        <p className="px-3.5 py-2.5 text-sm text-muted-foreground">No orgs visible.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-border">
                <th className={th}>Org</th>
                <th className={`${th} ${num}`}>Tracked</th>
                <th className={`${th} ${num}`}>Failing</th>
                <th className={`${th} ${num}`}>Parked</th>
                <th className={`${th} w-[200px]`}>Compliance</th>
              </tr>
            </thead>
            <tbody>
              {orgs.map((o) => (
                <tr key={o.org} className="border-b border-border last:border-0 hover:bg-panel-2">
                  <td className={td}>
                    <Link to="/orgs/$org" params={{ org: o.org }} className="font-mono font-medium hover:underline">
                      {o.org}
                    </Link>
                  </td>
                  <td className={`${td} ${num}`}>{o.tracked.toLocaleString()}</td>
                  <td className={`${td} ${num}`}>{o.counts.non_compliant.toLocaleString()}</td>
                  <td className={`${td} ${num}`}>{o.parked.toLocaleString()}</td>
                  <td className={td}>
                    <Meter percent={o.compliant_percent} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

// NotApplicableCard replaces the Orgs table under ?org=: why rules did
// not apply to this org's repositories.
function NotApplicableCard({ reasons }: { reasons: Schemas["ReasonCount"][] }) {
  const items = reasons.map((r) => ({ label: r.reason, value: r.count }));
  return (
    <ChartCard title="Not applicable by reason" subtitle="Findings outside the compliance percent, by the reason the rule did not apply." table={barListTable(items, "Reason")}>
      {items.length === 0 ? <Empty>Every rule applied.</Empty> : <BarList items={items} aria-label="not applicable findings by reason" color="var(--color-muted-foreground)" />}
    </ChartCard>
  );
}

// FindingsByStatusCard is one stacked bar over the four status counts.
// Its subtitle is the sentence that stops "why is 84% not 25,000 of
// 33,000": the percent counts only the first two.
function FindingsByStatusCard({ counts }: { counts: Schemas["StatusCounts"] }) {
  const rows = [{ label: "fleet", counts }];
  return (
    <ChartCard title="Findings by status" subtitle="Compliance % counts only compliant and non-compliant; not applicable and unknown sit outside it." table={stackTable(rows, STATUS_SEGS)}>
      <StackedBars rows={rows} segs={STATUS_SEGS} aria-label="findings by status" />
    </ChartCard>
  );
}
