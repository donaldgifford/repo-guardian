import { getRouteApi, Link } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useHistory, useOrg, useOrgs, useRules, useSummary } from "../api/queries";
import { Loading, Section, Tile, TrendChart } from "../components/common";
import { Card, CardContent } from "../components/ui/card";
import { formatPercent, humanize } from "../lib/format";
import { complianceSeries, parkedTotal, worstRules } from "../lib/viewmodel";

const app = getRouteApi("/app");

// ninetyDaysAgo anchors the fleet trend; computed once per render.
function ninetyDaysAgo(): string {
  return new Date(Date.now() - 90 * 86_400_000).toISOString();
}

// FleetView is the landing page (DESIGN-0027 sketch): tiles, trend, PR
// ages, worst rules and parked repositories. With an org filter it
// shows that org, from the org endpoints.
export function FleetView() {
  const { org } = app.useSearch();
  return org ? <OrgFleet org={org} /> : <WholeFleet />;
}

function WholeFleet() {
  const summary = useSummary();
  const history = useHistory({ from: ninetyDaysAgo() });
  if (!summary.data) {
    return <Loading />;
  }
  const s = summary.data;
  return (
    <>
      <h1 className="text-xl font-semibold">Fleet</h1>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Tile title="Tracked" value={s.tracked.toLocaleString()} />
        <Tile title="Compliant" value={formatPercent(s.compliant_percent)} />
        <Tile title="Failing" value={s.findings.non_compliant.toLocaleString()} hint="non-compliant findings" />
        <Tile title="Parked" value={parkedTotal(s.parked).toLocaleString()} />
      </div>
      <div className="grid gap-6 md:grid-cols-2">
        <Section title="Compliance (90d)">
          <TrendChart series={complianceSeries(history.data?.items ?? [])} />
        </Section>
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

function OrgFleet({ org }: { org: string }) {
  const detail = useOrg(org);
  const orgs = useOrgs();
  const history = useHistory({ org, from: ninetyDaysAgo() });
  if (!detail.data) {
    return <Loading />;
  }
  const d = detail.data;
  const summary = orgs.data?.items.find((o) => o.org.toLowerCase() === org.toLowerCase());
  return (
    <>
      <h1 className="text-xl font-semibold">{d.org}</h1>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Tile title="Tracked" value={d.tracked.toLocaleString()} />
        <Tile title="Compliant" value={formatPercent(summary?.compliant_percent)} />
        <Tile title="Failing" value={(summary?.counts.non_compliant ?? 0).toLocaleString()} hint="non-compliant findings" />
        <Tile title="Parked" value={parkedTotal(d.parked).toLocaleString()} />
      </div>
      <Section title="Compliance (90d)">
        <TrendChart series={complianceSeries(history.data?.items ?? [])} />
      </Section>
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

function WorstRules() {
  const rules = useRulesList();
  return <Section title="Worst rules">{rules ? <RuleBars rules={worstRules(rules, 5)} /> : <Loading />}</Section>;
}

function useRulesList() {
  return useRules().data?.items;
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
