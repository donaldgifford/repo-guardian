import { usePolicy } from "../api/queries";
import { Loading, Section, Tile } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { humanize, relativeTime } from "../lib/format";

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
      <h1 className="text-xl font-semibold">Policy</h1>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <Tile title="Version" value={<span className="font-mono text-base break-all">{p.version}</span>} hint={`first seen ${relativeTime(p.first_seen_at)}`} />
        <Tile
          title="Rollout"
          value={humanize(p.rollout_state)}
          hint={p.rollout_completed_at ? `completed ${relativeTime(p.rollout_completed_at)}` : "repositories are re-checked across the rollout window"}
        />
        <Tile title="Rules" value={p.rules.length} />
      </div>
      <Section title="Rules">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Rule</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Check</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>Ignore</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {p.rules.map((r) => (
              <TableRow key={`${r.kind}/${r.name}`}>
                <TableCell>
                  <div className="font-medium">{r.name}</div>
                  {r.description ? <div className="text-xs text-muted-foreground">{r.description}</div> : null}
                </TableCell>
                <TableCell>{humanize(r.kind)}</TableCell>
                <TableCell>{r.check_mode ?? ""}</TableCell>
                <TableCell className="font-mono text-xs">{r.scope?.join(", ") ?? "all"}</TableCell>
                <TableCell className="font-mono text-xs">{r.ignore?.join(", ") ?? ""}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Section>
    </>
  );
}
