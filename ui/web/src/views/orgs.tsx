import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useHistory, useOrg, useOrgs } from "../api/queries";
import { KindBadge } from "../components/badges";
import { ChartCard, TrendChart, trendTable } from "../components/charts";
import { Empty, Loading } from "../components/common";
import { OrgScope, PageHeader, RangeTabs } from "../components/page";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { GridChildRows, type GridColumn, type GridRow, GroupGrid, gridTotals } from "../components/ui/group-grid";
import { Kpi } from "../components/ui/kpi";
import { Meter } from "../components/ui/meter";
import { Tabs } from "../components/ui/tabs";
import { downloadCsv, tableCsv } from "../lib/csv";
import { compliantPercent, formatPercent } from "../lib/format";
import { parseRange, rangeDays, rangeFrom } from "../lib/range";
import { patchSearch } from "../lib/search";
import { complianceSeries, deltaFrom, failingRuleCount, parkedTotal } from "../lib/viewmodel";
import { NotApplicableCard, ParkedCard, sumCounts } from "./fleet";

const orgsApi = getRouteApi("/app/orgs");
const orgApi = getRouteApi("/app/orgs/$org");

const HEATS = ["compliance", "parked", "prs"] as const;
export type Heat = (typeof HEATS)[number];

export interface OrgsSearch {
  heat?: Heat;
  parked?: "hide";
  // open is the comma-separated list of expanded orgs.
  open?: string;
}

// parseOrgsSearch reads the grid's state from the URL; anything it does
// not recognise falls back to the default.
export function parseOrgsSearch(s: Record<string, unknown>): OrgsSearch {
  return {
    ...(HEATS.includes(s.heat as Heat) ? { heat: s.heat as Heat } : {}),
    ...(s.parked === "hide" ? { parked: "hide" as const } : {}),
    ...(typeof s.open === "string" && s.open !== "" ? { open: s.open } : {}),
  };
}

// openList splits `?open=`; joinOpen writes it back, dropping the key
// when nothing is open.
export function openList(open: string | undefined): string[] {
  return open ? open.split(",").filter((s) => s !== "") : [];
}

export function joinOpen(keys: readonly string[]): string | undefined {
  return keys.length === 0 ? undefined : keys.join(",");
}

const COLUMNS: readonly GridColumn[] = [
  { key: "tracked", header: "Tracked", tone: "mute" },
  { key: "compliant", header: "Compliant", tone: "ok", percent: true },
  { key: "failing", header: "Failing", tone: "bad", percent: true },
  { key: "parked", header: "Parked", tone: "warn" },
  { key: "prs", header: "Open PRs", tone: "info" },
];

// heatColumns names the columns a heat tab shades.
const heatColumns: Record<Heat, readonly string[]> = {
  compliance: ["compliant", "failing"],
  parked: ["parked"],
  prs: ["prs"],
};

// orgRow maps one /orgs item onto a grid row; the sub line and badge
// are the design's `<pct>% compliant · <n> parked` and repo count.
export function orgRow(o: Schemas["OrgSummary"]): GridRow {
  return {
    key: o.org,
    label: (
      <Link to="/orgs/$org" params={{ org: o.org }} className="hover:underline">
        {o.org}
      </Link>
    ),
    sub: `${formatPercent(o.compliant_percent)} compliant · ${o.parked} parked`,
    badge: <Badge tone="outline">{o.tracked} repos</Badge>,
    cells: { tracked: o.tracked, compliant: o.counts.compliant, failing: o.counts.non_compliant, parked: o.parked, prs: o.open_prs },
    total: o.tracked,
  };
}

// ruleRow maps one rule of /orgs/{org} onto a child row with the same
// columns; a rule has no parked or open-PR count, so those cells stay 0.
export function ruleRow(org: string, r: Schemas["RuleCompliance"]): GridRow {
  const total = r.counts.compliant + r.counts.non_compliant + r.counts.not_applicable + r.counts.unknown;
  return {
    key: `${org}/${r.kind}/${r.name}`,
    label: (
      <Link to="/rules/$kind/$name" params={{ kind: r.kind, name: r.name }} search={{ org }} className="font-mono hover:underline">
        {r.name}
      </Link>
    ),
    badge: <KindBadge kind={r.kind} />,
    cells: { tracked: total, compliant: r.counts.compliant, failing: r.counts.non_compliant, parked: 0, prs: 0 },
    total,
  };
}

// gridCsv writes the visible grid (its columns, every org and the
// totals row) through the defused CSV helper.
export function gridCsv(rows: readonly GridRow[], columns: readonly GridColumn[], names: readonly string[]): string {
  const total = gridTotals(rows, columns);
  const line = (name: string, r: GridRow) => [name, ...columns.map((c) => r.cells[c.key] ?? 0)];
  return tableCsv(
    ["org", ...columns.map((c) => c.key)],
    [...rows.map((r, i) => line(names[i] ?? r.key, r)), line("total", total)],
  );
}

export function OrgsView() {
  const orgs = useOrgs();
  const { heat = "compliance", parked, open } = orgsApi.useSearch();
  const navigate = useNavigate();
  if (!orgs.data) {
    return <Loading />;
  }
  const items = orgs.data.items;
  const columns = parked === "hide" ? COLUMNS.filter((c) => c.key !== "parked") : COLUMNS;
  const rows = items.map(orgRow);
  const opened = openList(open);
  const totals = gridTotals(rows, COLUMNS);
  const set = (patch: Record<string, string | undefined>) => void navigate({ to: ".", search: (prev) => patchSearch(prev, patch) });
  const toggle = (key: string) => set({ open: joinOpen(opened.includes(key) ? opened.filter((k) => k !== key) : [...opened, key]) });

  return (
    <>
      <PageHeader title="Orgs" subtitle={<>Every visible org with its tracked, parked and failing counts from <code>/orgs</code>.</>} controls={<OrgScope />} />
      <div className="flex flex-wrap items-center gap-2" aria-label="Totals">
        <Badge tone="outline">{items.length} orgs</Badge>
        <Badge tone="mute">Tracked {totals.cells.tracked}</Badge>
        <Badge tone="bad">Failing {totals.cells.failing}</Badge>
        <Badge tone="warn">Parked {totals.cells.parked}</Badge>
        <Badge tone="info">Open PRs {totals.cells.prs}</Badge>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Tabs
          label="Heat"
          options={[
            { value: "compliance", label: "Compliance" },
            { value: "parked", label: "Parked" },
            { value: "prs", label: "PR activity" },
          ]}
          value={heat}
          onChange={(next) => set({ heat: next === "compliance" ? undefined : next })}
        />
        <Tabs
          label="Parked"
          options={[
            { value: "all", label: "All" },
            { value: "hide", label: "Hide parked" },
          ]}
          value={parked ?? "all"}
          onChange={(next) => set({ parked: next === "all" ? undefined : next })}
        />
        <div className="ml-auto flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => set({ open: joinOpen(items.map((o) => o.org)) })} disabled={opened.length === items.length}>
            Expand all
          </Button>
          <Button variant="outline" size="sm" onClick={() => set({ open: undefined })} disabled={opened.length === 0}>
            Collapse all
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={items.length === 0}
            onClick={() =>
              downloadCsv(
                "orgs.csv",
                gridCsv(
                  rows,
                  columns,
                  items.map((o) => o.org),
                ),
              )
            }
          >
            Export CSV
          </Button>
        </div>
      </div>
      {items.length === 0 ? (
        <Empty>No orgs are visible to you.</Empty>
      ) : (
        <GroupGrid columns={columns} rows={rows} heat={heatColumns[heat]} open={opened} onToggle={toggle} aria-label="Orgs">
          {(row) => <OrgRules org={row.key} columns={columns} heat={heatColumns[heat]} />}
        </GroupGrid>
      )}
    </>
  );
}

// OrgRules is an expanded org: its rules from /orgs/{org}, fetched when
// the row opens and cached by the query client after that.
function OrgRules({ org, columns, heat }: { org: string; columns: readonly GridColumn[]; heat: readonly string[] }) {
  const detail = useOrg(org);
  return (
    <GridChildRows
      columns={columns}
      heat={heat}
      loading={detail.isPending}
      rows={(detail.data?.rules ?? []).map((r) => ruleRow(org, r))}
      empty={detail.isError ? "Could not load this org's rules." : "No rules apply in this org."}
    />
  );
}


// OrgView is one org (DESIGN-0036 §Orgs and org detail): KPIs, the
// compliance trend over the org's history beside its rules with
// meters, and why repositories fell out (not applicable, parked).
export function OrgView() {
  const { org } = orgApi.useParams();
  const { range: rawRange } = orgApi.useSearch();
  const range = parseRange(rawRange);
  const detail = useOrg(org);
  const orgs = useOrgs();
  const history = useHistory({ org, aggregate: "org", from: rangeFrom(range) });
  if (!detail.data) {
    return <Loading />;
  }
  const d = detail.data;
  const summary = orgs.data?.items.find((o) => o.org.toLowerCase() === org.toLowerCase());
  const counts = summary?.counts ?? sumCounts(d.rules);
  const percent = summary?.compliant_percent ?? compliantPercent(counts);
  const series = complianceSeries(history.data?.items ?? []);
  const delta = deltaFrom(series);
  const parked = parkedTotal(d.parked);
  return (
    <>
      <PageHeader
        title={d.org}
        badge={<Badge tone="outline">org</Badge>}
        subtitle={
          <>
            Compliance by rule for one org from <code>/orgs/{d.org}</code>.
          </>
        }
        controls={
          <>
            <RangeTabs />
            <Link to="/findings" search={{ org: d.org }}>
              <Button variant="outline" size="sm" tabIndex={-1}>
                Findings in this org
              </Button>
            </Link>
          </>
        }
      />
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Kpi label="Tracked" value={d.tracked.toLocaleString()} subline="repositories" />
        <Kpi label="Compliance" value={formatPercent(percent)} delta={delta === null ? null : { value: delta, goodUp: true, unit: " pts" }} />
        <Kpi label="Failing" value={counts.non_compliant.toLocaleString()} subline={`${failingRuleCount(d.rules)} rules`} />
        <Kpi label="Parked" value={parked.toLocaleString()} subline={`${d.parked.length} ${d.parked.length === 1 ? "reason" : "reasons"}`} />
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <ChartCard
            title={`Compliance (${range})`}
            subtitle={
              <>
                From <code>/compliance/history?org={d.org}</code>; compliant ÷ (compliant + non_compliant) over active repositories.
              </>
            }
            right={
              <Link to="/history" search={{ org: d.org }} className="text-xs text-muted-foreground hover:text-foreground">
                full history →
              </Link>
            }
            table={trendTable(series, "compliance %")}
          >
            <TrendChart series={series} rangeDays={rangeDays(range)} aria-label={`compliance % over ${range}`} />
          </ChartCard>
          <OrgRulesCard org={d.org} rules={d.rules} />
        </div>
        <div className="space-y-4">
          <NotApplicableCard reasons={d.not_applicable} />
          <ParkedCard parked={d.parked} />
        </div>
      </div>
    </>
  );
}

const ruleColumns = (org: string): Column<Schemas["RuleCompliance"]>[] => [
  {
    key: "rule",
    header: "Rule",
    cell: (r) => (
      <Link to="/rules/$kind/$name" params={{ kind: r.kind, name: r.name }} search={{ org }} className="font-mono font-medium hover:underline">
        {r.name}
      </Link>
    ),
  },
  { key: "kind", header: "Kind", cell: (r) => <KindBadge kind={r.kind} /> },
  { key: "compliant", header: "Compliant", cell: (r) => r.counts.compliant.toLocaleString(), numeric: true },
  {
    key: "failing",
    header: "Failing",
    cell: (r) =>
      r.counts.non_compliant === 0 ? (
        "0"
      ) : (
        <Link to="/findings" search={{ org, kind: r.kind, rule: r.name, status: "non_compliant" }} className="hover:underline">
          {r.counts.non_compliant.toLocaleString()}
        </Link>
      ),
    numeric: true,
  },
  { key: "na", header: "n/a", cell: (r) => r.counts.not_applicable.toLocaleString(), numeric: true, hideBelow: "md" },
  { key: "meter", header: "Compliance", cell: (r) => <Meter percent={r.compliant_percent} />, width: "200px" },
];

// OrgRulesCard is the org's rules with meters; the failing count links
// to the findings behind it.
function OrgRulesCard({ org, rules }: { org: string; rules: Schemas["RuleCompliance"][] }) {
  return (
    <Card
      title="Compliance by rule"
      subtitle={
        <>
          Every rule evaluated in <code>{org}</code>, sorted as the API returns them.
        </>
      }
      bodyClassName="p-0"
    >
      <DataTable columns={ruleColumns(org)} rows={rules} rowKey={(r) => `${r.kind}/${r.name}`} empty="No rules apply in this org." aria-label="Compliance by rule" />
    </Card>
  );
}
