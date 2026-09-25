import { getRouteApi, Link } from "@tanstack/react-router";

import { useRule, useRules } from "../api/queries";
import { Empty, Evidence, Loading, Section, StatusBadge, Tile } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { formatPercent, humanize, relativeTime, trend } from "../lib/format";

const app = getRouteApi("/app");
const ruleApi = getRouteApi("/app/rules/$kind/$name");

// RulesView lists every rule's compliance with its trend against the
// previous snapshot.
export function RulesView() {
  const rules = useRules();
  if (!rules.data) {
    return <Loading />;
  }
  return (
    <>
      <h1 className="text-xl font-semibold">Rules</h1>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Rule</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead className="text-right">Compliant</TableHead>
            <TableHead className="text-right">Trend</TableHead>
            <TableHead className="text-right">Failing</TableHead>
            <TableHead className="text-right">n/a</TableHead>
            <TableHead className="text-right">Unknown</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rules.data.items.map((r) => {
            const t = trend(r.compliant_percent, r.previous_percent);
            return (
              <TableRow key={`${r.kind}/${r.name}`}>
                <TableCell>
                  <Link to="/rules/$kind/$name" params={{ kind: r.kind, name: r.name }} className="font-medium underline-offset-2 hover:underline">
                    {r.name}
                  </Link>
                </TableCell>
                <TableCell>{humanize(r.kind)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatPercent(r.compliant_percent)}</TableCell>
                <TableCell className="text-right tabular-nums">{t === null ? "—" : `${t > 0 ? "▲" : t < 0 ? "▼" : "="} ${Math.abs(t).toFixed(1)}`}</TableCell>
                <TableCell className="text-right tabular-nums">{r.counts.non_compliant}</TableCell>
                <TableCell className="text-right tabular-nums">{r.counts.not_applicable}</TableCell>
                <TableCell className="text-right tabular-nums">{r.counts.unknown}</TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </>
  );
}

// RuleView is one rule across the visible orgs: per-org breakdown, top
// reasons and the longest failures.
export function RuleView() {
  const { kind, name } = ruleApi.useParams();
  const { org } = app.useSearch();
  const rule = useRule(kind, name);
  if (!rule.data) {
    return <Loading />;
  }
  const r = rule.data;
  return (
    <>
      <div>
        <h1 className="text-xl font-semibold">{r.name}</h1>
        <p className="text-sm text-muted-foreground">{humanize(r.kind)} rule</p>
        {r.description ? <p className="mt-2 text-sm">{r.description}</p> : null}
      </div>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Tile title="Compliant" value={formatPercent(r.compliant_percent)} />
        <Tile title="Failing" value={r.counts.non_compliant} />
        <Tile title="Not applicable" value={r.counts.not_applicable} />
        <Tile title="Unknown" value={r.counts.unknown} />
      </div>
      <div className="grid gap-6 md:grid-cols-2">
        <Section title="By org">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Org</TableHead>
                <TableHead className="text-right">Compliant</TableHead>
                <TableHead className="text-right">Failing</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {r.orgs.map((o) => (
                <TableRow key={o.org} className={o.org.toLowerCase() === org?.toLowerCase() ? "bg-muted" : undefined}>
                  <TableCell>
                    <Link to="/findings" search={{ org: o.org, kind: r.kind, rule: r.name, status: "non_compliant" }} className="hover:underline">
                      {o.org}
                    </Link>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{formatPercent(o.compliant_percent)}</TableCell>
                  <TableCell className="text-right tabular-nums">{o.counts.non_compliant}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
        <Section title="Top reasons">
          {r.top_reasons.length === 0 ? (
            <Empty>No failing or skipped findings.</Empty>
          ) : (
            <ul className="space-y-1 text-sm">
              {r.top_reasons.map((t) => (
                <li key={t.reason} className="flex justify-between">
                  <Link to="/findings" search={{ kind: r.kind, rule: r.name, reason: t.reason }} className="font-mono text-xs hover:underline">
                    {t.reason}
                  </Link>
                  <span className="tabular-nums">{t.count}</span>
                </li>
              ))}
            </ul>
          )}
        </Section>
      </div>
      <Section title="Longest failing">
        {r.oldest_failures.length === 0 ? (
          <Empty>Nothing failing.</Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Repository</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Evidence</TableHead>
                <TableHead>Since</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {r.oldest_failures.map((f) => (
                <TableRow key={f.repository_id}>
                  <TableCell>
                    <Link to="/repos/$id" params={{ id: String(f.repository_id) }} className="hover:underline">
                      {f.org}/{f.repository}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={f.status} />
                  </TableCell>
                  <TableCell>
                    <Evidence finding={f} />
                  </TableCell>
                  <TableCell className="whitespace-nowrap">{relativeTime(f.status_since)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>
    </>
  );
}
