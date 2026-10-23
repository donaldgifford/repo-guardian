import type { Schemas } from "../../api/client";
import type { GridCell } from "../../components/ui/group-grid";
import { ageDays } from "../../lib/format";
import { addPosture, emptyPosture, type Posture } from "../../lib/posture";

export type OrgsTab = "compliance" | "parked" | "pr";

export const ORGS_TABS: readonly { value: OrgsTab; label: string }[] = [
  { value: "compliance", label: "Compliance" },
  { value: "parked", label: "Parked" },
  { value: "pr", label: "PR activity" },
];

// OrgsSearch is the tree's URL state: the tab, the repository filter
// applied to loaded children, the expanded orgs (comma-joined) and the
// header's org scope.
export interface OrgsSearch {
  tab?: OrgsTab;
  q?: string;
  open?: string;
  org?: string;
}

export function parseOrgsSearch(s: Record<string, unknown>): OrgsSearch {
  const out: OrgsSearch = {};
  if (typeof s.tab === "string" && s.tab !== "compliance" && ORGS_TABS.some((t) => t.value === s.tab)) out.tab = s.tab as OrgsTab;
  for (const k of ["q", "open", "org"] as const) {
    const v = s[k];
    if (typeof v === "string" && v !== "") out[k] = v;
  }
  return out;
}

// openOrgs reads `?open=acme,globex`.
export function openOrgs(open: string | undefined): string[] {
  return open ? open.split(",").filter((o) => o !== "") : [];
}

export function joinOpen(open: readonly string[]): string | undefined {
  return open.length === 0 ? undefined : open.join(",");
}

export function toggleOpen(open: readonly string[], key: string): string[] {
  return open.includes(key) ? open.filter((o) => o !== key) : [...open, key];
}

// measuredOf is a posture's denominator for shares and shading: the
// assignments that measured something. Unknown, not-applicable and the
// other unmeasured buckets never make a percentage (DESIGN-0037 OQ4).
export function measuredOf(p: Posture): number {
  return p.compliant + p.non_compliant;
}

// postureCells is the Compliant and Non-compliant pair of a row, each
// over the same measured denominator.
export function postureCells(p: Posture): { compliant: GridCell; non_compliant: GridCell } {
  const of = measuredOf(p);
  return { compliant: { n: p.compliant, of }, non_compliant: { n: p.non_compliant, of } };
}

// sumOrgs totals the visible org rows: the footer, which must equal
// what /orgs reports for the same orgs.
export function sumOrgs(orgs: readonly Schemas["OrgSummary"][]): { tracked: number; parked: number; open_prs: number; compliance: Posture } {
  return orgs.reduce(
    (acc, o) => ({ tracked: acc.tracked + o.tracked, parked: acc.parked + o.parked, open_prs: acc.open_prs + o.open_prs, compliance: addPosture(acc.compliance, o.compliance) }),
    { tracked: 0, parked: 0, open_prs: 0, compliance: emptyPosture() },
  );
}

// matchesFilter is the repository filter: a case-insensitive substring
// of the name, applied to the children already loaded.
export function matchesFilter(name: string, q: string | undefined): boolean {
  return !q || name.toLowerCase().includes(q.toLowerCase());
}

// countByRepository tallies open pull requests per repository id.
export function countByRepository(prs: readonly Pick<Schemas["Remediation"], "repository_id">[]): Map<number, number> {
  const out = new Map<number, number>();
  for (const pr of prs) out.set(pr.repository_id, (out.get(pr.repository_id) ?? 0) + 1);
  return out;
}

export interface OrgPRActivity {
  org: string;
  open: number;
  // stale is open 30 days or more, the API's pr_stale.
  stale: number;
  oldestDays: number | null;
}

export const staleAfterDays = 30;

// prActivity groups open pull requests by org: how many, how many are
// stale and the oldest's age. Every org in `orgs` has a row, zeros for
// none.
export function prActivity(orgs: readonly Pick<Schemas["OrgSummary"], "org">[], prs: readonly Pick<Schemas["Remediation"], "org" | "created_at">[], now = new Date()): OrgPRActivity[] {
  const rows = new Map(orgs.map((o) => [o.org, { org: o.org, open: 0, stale: 0, oldestDays: null as number | null }]));
  for (const pr of prs) {
    const row = rows.get(pr.org);
    if (!row) continue;
    const age = ageDays(pr.created_at, now);
    row.open += 1;
    if (age >= staleAfterDays) row.stale += 1;
    row.oldestDays = row.oldestDays === null ? age : Math.max(row.oldestDays, age);
  }
  return [...rows.values()];
}
