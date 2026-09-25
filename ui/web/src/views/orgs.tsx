import { getRouteApi, Link } from "@tanstack/react-router";

import { useOrg, useOrgs } from "../api/queries";
import { Empty, Loading, Section, Tile } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { formatPercent, humanize } from "../lib/format";
import { parkedTotal } from "../lib/viewmodel";

const orgApi = getRouteApi("/app/orgs/$org");

export function OrgsView() {
  const orgs = useOrgs();
  if (!orgs.data) {
    return <Loading />;
  }
  return (
    <>
      <h1 className="text-xl font-semibold">Orgs</h1>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Org</TableHead>
            <TableHead className="text-right">Tracked</TableHead>
            <TableHead className="text-right">Parked</TableHead>
            <TableHead className="text-right">Compliant</TableHead>
            <TableHead className="text-right">Failing</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {orgs.data.items.map((o) => (
            <TableRow key={o.org}>
              <TableCell>
                <Link to="/orgs/$org" params={{ org: o.org }} className="font-medium hover:underline">
                  {o.org}
                </Link>
              </TableCell>
              <TableCell className="text-right tabular-nums">{o.tracked}</TableCell>
              <TableCell className="text-right tabular-nums">{o.parked}</TableCell>
              <TableCell className="text-right tabular-nums">{formatPercent(o.compliant_percent)}</TableCell>
              <TableCell className="text-right tabular-nums">{o.counts.non_compliant}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </>
  );
}

// OrgView is one org's rule table and why rules did not apply to it.
export function OrgView() {
  const { org } = orgApi.useParams();
  const detail = useOrg(org);
  if (!detail.data) {
    return <Loading />;
  }
  const d = detail.data;
  return (
    <>
      <h1 className="text-xl font-semibold">{d.org}</h1>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3">
        <Tile title="Tracked" value={d.tracked} />
        <Tile title="Parked" value={parkedTotal(d.parked)} hint={d.parked.map((p) => `${humanize(p.reason)} ${p.count}`).join(" · ") || undefined} />
        <Tile title="Rules" value={d.rules.length} />
      </div>
      <Section title="Rules">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Rule</TableHead>
              <TableHead className="text-right">Compliant</TableHead>
              <TableHead className="text-right">Failing</TableHead>
              <TableHead className="text-right">n/a</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {d.rules.map((r) => (
              <TableRow key={`${r.kind}/${r.name}`}>
                <TableCell>
                  <Link to="/findings" search={{ org: d.org, kind: r.kind, rule: r.name }} className="hover:underline">
                    {r.name}
                  </Link>
                </TableCell>
                <TableCell className="text-right tabular-nums">{formatPercent(r.compliant_percent)}</TableCell>
                <TableCell className="text-right tabular-nums">{r.counts.non_compliant}</TableCell>
                <TableCell className="text-right tabular-nums">{r.counts.not_applicable}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Section>
      <Section title="Not applicable, by reason">
        {d.not_applicable.length === 0 ? (
          <Empty>Every rule applies to every repository.</Empty>
        ) : (
          <ul className="space-y-1 text-sm">
            {d.not_applicable.map((n) => (
              <li key={n.reason} className="flex max-w-md justify-between">
                <Link to="/findings" search={{ org: d.org, status: "not_applicable", reason: n.reason }} className="font-mono text-xs hover:underline">
                  {n.reason}
                </Link>
                <span className="tabular-nums">{n.count}</span>
              </li>
            ))}
          </ul>
        )}
      </Section>
    </>
  );
}
