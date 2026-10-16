import { getRouteApi, Link } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useRule, useRules } from "../api/queries";
import { KindBadge } from "../components/badges";
import { BarList, barListTable, ChartCard, type StackRow, StackedBars, stackTable } from "../components/charts";
import { Empty, Loading } from "../components/common";
import { OrgScope, PageHeader } from "../components/page";
import { Time } from "../components/time";
import { Button } from "../components/ui/button";
import { Card } from "../components/ui/card";
import { DeltaText, Kpi } from "../components/ui/kpi";
import { Meter } from "../components/ui/meter";
import { SearchTabs } from "../components/ui/tabs";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { ageDays, formatPercent, humanize, trend } from "../lib/format";
import { ruleKinds, worstRules } from "../lib/viewmodel";
import { STATUS_SEGS } from "./fleet";

const app = getRouteApi("/app");
const rulesApi = getRouteApi("/app/rules");
const ruleApi = getRouteApi("/app/rules/$kind/$name");

export interface RulesSearch {
  kind?: string;
}

// parseRulesSearch reads the kind tab; "all" and absence are the same.
export function parseRulesSearch(s: Record<string, unknown>): RulesSearch {
  return typeof s.kind === "string" && s.kind !== "" && s.kind !== "all" ? { kind: s.kind } : {};
}

// RulesView is every rule's compliance (DESIGN-0036 §Rules): kind tabs
// from the kinds present, the four worst as KPIs, a stacked bar per
// rule, then the full table. All from /rules.
export function RulesView() {
  const { kind } = rulesApi.useSearch();
  const rules = useRules();
  if (!rules.data) {
    return <Loading />;
  }
  const all = rules.data.items;
  const kinds = ruleKinds(all);
  const shown = kind ? all.filter((r) => r.kind === kind) : all;
  const rows: StackRow[] = shown.map((r) => ({
    label: r.name,
    counts: r.counts,
    href: (
      <Link to="/rules/$kind/$name" params={{ kind: r.kind, name: r.name }} className="hover:underline">
        {r.name}
      </Link>
    ),
  }));
  return (
    <>
      <PageHeader
        title="Rules"
        subtitle={
          <>
            Every policy rule with its fleet counts from <code>/rules</code>.
          </>
        }
        controls={
          <>
            <OrgScope />
            <SearchTabs label="Kind" param="kind" defaultValue="all" options={[{ value: "all", label: "All" }, ...kinds.map((k) => ({ value: k, label: humanize(k) }))]} />
          </>
        }
      />
      {shown.length === 0 ? (
        <Empty>No rules of this kind.</Empty>
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            {worstRules(shown, 4).map((r) => {
              const t = trend(r.compliant_percent, r.previous_percent);
              return (
                <Link key={`${r.kind}/${r.name}`} to="/rules/$kind/$name" params={{ kind: r.kind, name: r.name }} className="block rounded-lg hover:[&>div]:bg-panel-2">
                  <Kpi
                    label={
                      <span className="flex items-center gap-2">
                        <span className="truncate font-medium text-foreground">{r.name}</span>
                        <KindBadge kind={r.kind} />
                      </span>
                    }
                    value={formatPercent(r.compliant_percent)}
                    delta={t === null ? null : { value: t, goodUp: true, unit: " pts" }}
                    subline={`${r.counts.non_compliant.toLocaleString()} failing · ${r.counts.not_applicable.toLocaleString()} n/a`}
                  />
                </Link>
              );
            })}
          </div>
          <ChartCard title="Findings by rule" subtitle="One bar per rule, scaled to the largest; compliance % counts only the first two segments." table={stackTable(rows, STATUS_SEGS)}>
            <StackedBars rows={rows} segs={STATUS_SEGS} aria-label="findings by rule and status" />
          </ChartCard>
          <RulesFullTable rules={shown} />
        </>
      )}
    </>
  );
}

function RulesFullTable({ rules }: { rules: Schemas["RuleCompliance"][] }) {
  return (
    <Card title="All rules" subtitle="Trend is the change in points against the previous snapshot." bodyClassName="p-0">
      <Table className="text-[13px]">
        <TableHeader>
          <TableRow>
            <TableHead>Rule</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead className="text-right">Compliant</TableHead>
            <TableHead className="text-right">Failing</TableHead>
            <TableHead className="text-right">N/A</TableHead>
            <TableHead className="text-right">Unknown</TableHead>
            <TableHead className="w-[180px]">Compliance</TableHead>
            <TableHead className="text-right">Trend</TableHead>
            <TableHead>Previous snapshot</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rules.map((r) => {
            const t = trend(r.compliant_percent, r.previous_percent);
            return (
              <TableRow key={`${r.kind}/${r.name}`} className="hover:bg-panel-2">
                <TableCell className="align-middle">
                  <Link to="/rules/$kind/$name" params={{ kind: r.kind, name: r.name }} className="font-medium underline-offset-2 hover:underline">
                    {r.name}
                  </Link>
                </TableCell>
                <TableCell className="align-middle">
                  <KindBadge kind={r.kind} />
                </TableCell>
                <TableCell className="text-right align-middle tabular-nums">{r.counts.compliant.toLocaleString()}</TableCell>
                <TableCell className="text-right align-middle tabular-nums">{r.counts.non_compliant.toLocaleString()}</TableCell>
                <TableCell className="text-right align-middle tabular-nums">{r.counts.not_applicable.toLocaleString()}</TableCell>
                <TableCell className="text-right align-middle tabular-nums">{r.counts.unknown.toLocaleString()}</TableCell>
                <TableCell className="align-middle">
                  <Meter percent={r.compliant_percent} />
                </TableCell>
                <TableCell className="text-right align-middle tabular-nums">{t === null ? <span className="text-muted-foreground">—</span> : <DeltaText delta={{ value: t, goodUp: true, unit: " pts" }} />}</TableCell>
                <TableCell className="align-middle whitespace-nowrap text-muted-foreground">
                  <Time iso={r.previous_at} style="date" />
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </Card>
  );
}

// RuleView is one rule across the visible orgs (DESIGN-0036 §Rule
// detail): KPIs, the per-org table with meters, top reasons and the
// oldest failures. The trend comes from the cached /rules list, since
// the detail endpoint carries no previous snapshot.
export function RuleView() {
  const { kind, name } = ruleApi.useParams();
  const { org } = app.useSearch();
  const rule = useRule(kind, name);
  const rules = useRules();
  if (!rule.data) {
    return <Loading />;
  }
  const r = rule.data;
  const listed = rules.data?.items.find((x) => x.kind === r.kind && x.name === r.name);
  const t = listed ? trend(r.compliant_percent, listed.previous_percent) : null;
  const oldest = r.oldest_failures[0];
  const reasons = r.top_reasons.map((x) => ({
    label: x.reason,
    value: x.count,
    render: (label: string) => (
      <Link to="/findings" search={{ kind: r.kind, rule: r.name, reason: x.reason }} className="hover:underline">
        {label}
      </Link>
    ),
  }));
  return (
    <>
      <PageHeader
        title={r.name}
        badge={<KindBadge kind={r.kind} />}
        subtitle={r.description || `${humanize(r.kind)} rule`}
        controls={
          <Link to="/findings" search={{ kind: r.kind, rule: r.name }}>
            <Button tabIndex={-1}>Findings for this rule</Button>
          </Link>
        }
      />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Kpi label="Compliance" value={formatPercent(r.compliant_percent)} delta={t === null ? null : { value: t, goodUp: true, unit: " pts" }} />
        <Kpi label="Compliant" value={r.counts.compliant.toLocaleString()} subline="repositories" />
        <Kpi label="Failing" value={r.counts.non_compliant.toLocaleString()} subline={oldest ? `oldest: ${ageDays(oldest.status_since)} days` : "nothing failing"} />
        <Kpi label="Not applicable" value={r.counts.not_applicable.toLocaleString()} subline={`${r.counts.unknown.toLocaleString()} unknown`} />
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        <Card title="By org" subtitle="Each visible org's compliance with this rule; rows open its failing findings." bodyClassName="p-0" className="lg:col-span-2">
          <Table className="text-[13px]">
            <TableHeader>
              <TableRow>
                <TableHead>Org</TableHead>
                <TableHead className="text-right">Compliant</TableHead>
                <TableHead className="text-right">Failing</TableHead>
                <TableHead className="text-right">N/A</TableHead>
                <TableHead className="w-[220px]">Compliance</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {r.orgs.map((o) => (
                <TableRow key={o.org} className={o.org.toLowerCase() === org?.toLowerCase() ? "bg-panel-2" : "hover:bg-panel-2"}>
                  <TableCell className="align-middle">
                    <Link to="/findings" search={{ org: o.org, kind: r.kind, rule: r.name, status: "non_compliant" }} className="font-mono hover:underline">
                      {o.org}
                    </Link>
                  </TableCell>
                  <TableCell className="text-right align-middle tabular-nums">{o.counts.compliant.toLocaleString()}</TableCell>
                  <TableCell className="text-right align-middle tabular-nums">{o.counts.non_compliant.toLocaleString()}</TableCell>
                  <TableCell className="text-right align-middle tabular-nums">{o.counts.not_applicable.toLocaleString()}</TableCell>
                  <TableCell className="align-middle">
                    <Meter percent={o.compliant_percent} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
        <div className="grid gap-4">
          <ChartCard title="Top reasons" subtitle="Why repositories fail or skip this rule." table={barListTable(reasons, "Reason")}>
            {reasons.length === 0 ? <Empty>No failing or skipped findings.</Empty> : <BarList items={reasons} aria-label="findings by reason" color="var(--color-bad)" />}
          </ChartCard>
          <Card title="Oldest failures" subtitle="Longest-standing non-compliant repositories." bodyClassName="p-0">
            {r.oldest_failures.length === 0 ? (
              <p className="px-3.5 py-2.5 text-sm text-muted-foreground">Nothing failing.</p>
            ) : (
              <ul className="divide-y divide-border">
                {r.oldest_failures.map((f) => (
                  <li key={f.repository_id}>
                    <Link to="/repos/$id" params={{ id: String(f.repository_id) }} className="flex items-center justify-between gap-3 px-3.5 py-2 text-[13px] hover:bg-panel-2">
                      <span className="min-w-0 truncate font-mono">
                        {f.org}/{f.repository}
                      </span>
                      <span className="whitespace-nowrap text-xs text-muted-foreground">failing for {ageDays(f.status_since)} days</span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      </div>
    </>
  );
}
