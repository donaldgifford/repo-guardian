import type { Schemas } from "../api/client";
import { usePolicies } from "../api/queries";
import { Loading } from "../components/common";
import { PageHeader } from "../components/page/PageHeader";
import { Time } from "../components/time";
import { Badge } from "../components/ui/badge";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { DetailKV } from "../components/ui/detail-drawer";

// PolicyView is the active policy as the caller may see it: the
// enterprise baseline (all-org callers only), each visible org's layer,
// and the catalogue entries those reference.
export function PolicyView() {
  const policies = usePolicies();
  if (!policies.data) {
    return <Loading />;
  }
  const p = policies.data;
  return (
    <>
      <PageHeader
        title="Policy"
        subtitle={
          <>
            The active policy version · <code>GET /policies</code>
          </>
        }
      />
      <div className="space-y-4">
        <div className="grid gap-4 lg:grid-cols-2">
          <Card title="Version">
            <DetailKV
              rows={[
                { key: "version", value: <span className="font-mono text-xs break-all">{p.version}</span> },
                { key: "first seen", value: <Time iso={p.first_seen_at} /> },
                { key: "activated", value: <Time iso={p.activated_at} /> },
                { key: "rollout", value: p.rollout_completed_at ? <>complete <Time iso={p.rollout_completed_at} style="relative" /></> : "in progress" },
              ]}
            />
          </Card>
          {p.enterprise ? (
            <Card title="Enterprise baseline">
              <DetailKV
                rows={[
                  { key: "mode", value: <Badge tone="outline">{p.enterprise.mode}</Badge> },
                  { key: "orgs", value: <span className="font-mono text-xs">{p.enterprise.orgs.join(", ") || "—"}</span> },
                  { key: "controls", value: <span className="font-mono text-xs">{p.enterprise.controls.join(", ") || "—"}</span> },
                ]}
              />
            </Card>
          ) : null}
        </div>
        {p.orgs.map((o) => (
          <OrgLayer key={o.org} org={o} />
        ))}
        <Card title="Catalogue" subtitle="The controls this policy references." bodyClassName="p-0">
          <DataTable columns={catalogueColumns} rows={p.controls} rowKey={(c) => c.id} empty="No control is referenced." aria-label="Catalogue" />
        </Card>
      </div>
    </>
  );
}

function changes(list: readonly Schemas["PolicyChange"][]) {
  if (list.length === 0) return <span className="text-muted-foreground">—</span>;
  return (
    <ul className="space-y-0.5">
      {list.map((c) => (
        <li key={`${c.control}/${c.with ?? ""}`} className="font-mono text-xs">
          {c.control}
          {c.with ? ` → ${c.with}` : ""}
          {c.reason ? <span className="text-muted-foreground"> · {c.reason}</span> : null}
        </li>
      ))}
    </ul>
  );
}

function OrgLayer({ org: o }: { org: Schemas["PolicyOrg"] }) {
  return (
    <Card title={<span className="font-mono">{o.org}</span>} right={o.mode ? <Badge tone="outline">{o.mode}</Badge> : undefined} data-org={o.org}>
      <DetailKV
        rows={[
          { key: "added", value: changes(o.added) },
          { key: "replaced", value: changes(o.replaced) },
          { key: "excluded", value: changes(o.excluded) },
          {
            key: "excluded repos",
            value:
              o.excluded_repos.length === 0 ? (
                <span className="text-muted-foreground">—</span>
              ) : (
                <ul className="space-y-0.5">
                  {o.excluded_repos.map((e) => (
                    <li key={e.match.join(",")} className="font-mono text-xs">
                      {e.match.join(", ")} <span className="text-muted-foreground">· {e.reason}</span>
                    </li>
                  ))}
                </ul>
              ),
          },
          {
            key: "repo groups",
            value:
              o.repos.length === 0 ? (
                <span className="text-muted-foreground">—</span>
              ) : (
                <ul className="space-y-0.5">
                  {o.repos.map((r) => (
                    <li key={r.label} className="font-mono text-xs">
                      {r.label}: {r.match.join(", ")}
                      {r.mode ? <span className="text-muted-foreground"> · {r.mode}</span> : null}
                    </li>
                  ))}
                </ul>
              ),
          },
        ]}
      />
    </Card>
  );
}

const catalogueColumns: Column<Schemas["PolicyControl"]>[] = [
  { key: "id", header: "Control", cell: (c) => <span className="font-mono font-medium">{c.id}</span> },
  { key: "title", header: "Title", cell: (c) => c.title ?? "" },
  { key: "type", header: "Type", cell: (c) => <span className="font-mono text-xs text-muted-foreground">{c.type}</span> },
  { key: "revision", header: "Revision", cell: (c) => <span className="font-mono text-xs">{c.revision.slice(0, 12)}</span>, hideBelow: "md" },
];
