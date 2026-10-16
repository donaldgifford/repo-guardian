import type { Schemas } from "../api/client";
import { usePolicy } from "../api/queries";
import { KindBadge } from "../components/badges";
import { Loading } from "../components/common";
import { PageHeader } from "../components/page";
import { Time } from "../components/time";
import { Badge } from "../components/ui/badge";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { Kpi } from "../components/ui/kpi";
import { humanize } from "../lib/format";

// PolicyView shows the current policy version, its rollout and its
// rules, read-only.
export function PolicyView() {
  const policy = usePolicy();
  if (!policy.data) {
    return <Loading />;
  }
  const p = policy.data;
  return (
    <>
      <PageHeader
        title="Policy"
        badge={<Badge tone={p.rollout_state === "complete" ? "ok" : "warn"}>{humanize(p.rollout_state)}</Badge>}
        subtitle={
          <>
            The active policy version and its rollout from <code>/policy</code>.
          </>
        }
      />
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Kpi label="Version" value={<span className="font-mono text-sm break-all">{p.version}</span>} />
        <Kpi label="First seen" value={<Time iso={p.first_seen_at} style="date" />} subline={<Time iso={p.first_seen_at} style="relative" />} />
        <Kpi
          label="Rollout"
          value={humanize(p.rollout_state)}
          subline={
            p.rollout_completed_at ? (
              <>
                completed <Time iso={p.rollout_completed_at} style="relative" />
              </>
            ) : (
              "repositories are re-checked across the rollout window"
            )
          }
        />
        <Kpi label="Rules" value={p.rules.length.toLocaleString()} subline="in this version" />
      </div>
      <Card title="Rules" subtitle="Every rule the policy declares, with its scope and ignore patterns." bodyClassName="p-0">
        <DataTable columns={ruleColumns} rows={p.rules} rowKey={(r) => `${r.kind}/${r.name}`} empty="This policy declares no rules." aria-label="Policy rules" />
      </Card>
    </>
  );
}

const ruleColumns: Column<Schemas["PolicyRule"]>[] = [
  {
    key: "rule",
    header: "Rule",
    cell: (r) => (
      <>
        <span className="font-mono font-medium">{r.name}</span>
        {r.description ? <span className="block text-xs text-muted-foreground">{r.description}</span> : null}
      </>
    ),
  },
  { key: "kind", header: "Kind", cell: (r) => <KindBadge kind={r.kind} /> },
  { key: "check", header: "Check", cell: (r) => (r.check_mode ? <span className="font-mono text-xs">{r.check_mode}</span> : <span className="text-muted-foreground">—</span>) },
  { key: "scope", header: "Scope", cell: (r) => <span className="font-mono text-xs">{r.scope?.join(", ") ?? "all"}</span>, hideBelow: "md" },
  { key: "ignore", header: "Ignore", cell: (r) => (r.ignore && r.ignore.length > 0 ? <span className="font-mono text-xs">{r.ignore.join(", ")}</span> : <span className="text-muted-foreground">—</span>), hideBelow: "lg" },
];
