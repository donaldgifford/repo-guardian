import type { Schemas } from "../../api/client";
import type { RemediationFilter } from "../../api/queries";
import type { CsvColumns } from "../../lib/csv";
import { ageDays } from "../../lib/format";
import { BRANCH_HOLDS, HOLDS } from "../../lib/holds";

export type RemediationsView = "all" | "open" | "recommended" | "held" | "failed";

export const VIEWS: readonly { value: RemediationsView; label: string }[] = [
  { value: "all", label: "All" },
  { value: "open", label: "Open PRs" },
  { value: "recommended", label: "Recommendations" },
  { value: "held", label: "Held" },
  { value: "failed", label: "Failed" },
];

export const KINDS = ["pull_request", "api"] as const;

// STATES is the schema's closed set, read as open.
export const STATES = ["reserved", "open", "merged", "closed_compliant", "closed_by_user", "closed_withdrawn", "recommended", "superseded", "withdrawn", "resolved", "applied", "failed"] as const;

// OUTSTANDING are the states the nav count and Summary.remediation_backlog
// count: work not yet landed.
export const OUTSTANDING: readonly string[] = ["reserved", "open", "recommended", "failed"];

export const staleAfterDays = 30;

// RemediationsSearch is the page's URL state: the view preset, the
// filters (each a /remediations query parameter; `older_than` is
// `older_than_days`), the header's org scope and the drawer's row.
export interface RemediationsSearch {
  view?: RemediationsView;
  kind?: string;
  state?: string;
  org?: string;
  control?: string;
  hold?: string;
  older_than?: number;
  open?: string;
}

export function parseRemediationsSearch(s: Record<string, unknown>): RemediationsSearch {
  const out: RemediationsSearch = {};
  if (typeof s.view === "string" && s.view !== "all" && VIEWS.some((v) => v.value === s.view)) out.view = s.view as RemediationsView;
  for (const k of ["kind", "state", "org", "control", "hold", "open"] as const) {
    const v = s[k];
    if (typeof v === "string" && v !== "") out[k] = v;
  }
  const older = typeof s.older_than === "string" ? Number(s.older_than) : s.older_than;
  if (typeof older === "number" && Number.isInteger(older) && older > 0) out.older_than = older;
  return out;
}

// viewPreset is what a segmented view asks /remediations for; the Held
// view names every hold so the branch-held results list beside the
// permission-held remediations.
export function viewPreset(view: RemediationsView): RemediationFilter {
  switch (view) {
    case "open":
      return { kind: "pull_request", state: "open" };
    case "recommended":
      return { state: "recommended" };
    case "held":
      return { hold: [...HOLDS] };
    case "failed":
      return { state: "failed" };
    default:
      return {};
  }
}

// remediationFilter is the request: the view's preset, then the chips
// over it, then the scope's org when no chip names one.
export function remediationFilter(search: RemediationsSearch, scopeOrg: string | undefined): RemediationFilter & { older_than_days?: number } {
  const f: RemediationFilter & { older_than_days?: number } = { ...viewPreset(search.view ?? "all") };
  if (search.kind) f.kind = search.kind;
  if (search.state) f.state = search.state;
  if (search.control) f.control = search.control;
  if (search.hold) f.hold = [search.hold];
  if (search.org) f.org = search.org;
  else if (scopeOrg) f.org = scopeOrg;
  if (search.older_than !== undefined) f.older_than_days = search.older_than;
  return f;
}

// remediationKey identifies a row in the table and in `?open=`: the
// remediation id, or `<repository_id>/<control>` for a held result,
// which has none.
export function remediationKey(r: Pick<Schemas["Remediation"], "id" | "repository_id" | "control_id">): string {
  return r.id === undefined || r.id === 0 ? `${r.repository_id}/${r.control_id}` : String(r.id);
}

export type OpenRemediation = { id: number } | { repositoryId: number; control: string };

export function parseRemediationKey(open: string | undefined): OpenRemediation | undefined {
  if (!open) return undefined;
  if (/^[1-9]\d*$/.test(open)) return { id: Number(open) };
  const slash = open.indexOf("/");
  if (slash <= 0) return undefined;
  const repositoryId = Number(open.slice(0, slash));
  const control = open.slice(slash + 1);
  if (!Number.isSafeInteger(repositoryId) || repositoryId <= 0 || control === "") return undefined;
  return { repositoryId, control };
}

export function matchesKey(r: Schemas["Remediation"], key: OpenRemediation): boolean {
  return "id" in key ? r.id === key.id : r.repository_id === key.repositoryId && r.control_id === key.control && (r.id === undefined || r.id === 0);
}

// remediationAge is how long the row has been what it is: a held
// result since its hold began, else since it was created.
export function remediationAge(r: Pick<Schemas["Remediation"], "created_at" | "hold_since" | "state">, now = new Date()): number {
  return ageDays(r.state === undefined && r.hold_since ? r.hold_since : r.created_at, now);
}

// isStale is an open pull request at the stale threshold.
export function isStale(r: Pick<Schemas["Remediation"], "kind" | "state" | "created_at" | "hold_since">, now = new Date()): boolean {
  return r.kind === "pull_request" && r.state === "open" && remediationAge(r, now) >= staleAfterDays;
}

export function isHeld(r: Pick<Schemas["Remediation"], "hold">): boolean {
  return r.hold !== undefined && r.hold !== "";
}

export function isBranchHold(hold: string): boolean {
  return (BRANCH_HOLDS as readonly string[]).includes(hold);
}

export interface RemediationKpis {
  open: number;
  stale: number;
  recommended: number;
  held: number;
  heldByReason: { hold: string; count: number }[];
  failed: number;
  outstanding: number;
}

// kpis sums the page's numbers from the scope's remediations and its
// branch-held results (two listings, since the latter only appear
// under a hold filter). A row is counted once: a permission-held
// remediation is in `rows`, a branch-held result in `held`.
export function kpis(rows: readonly Schemas["Remediation"][], held: readonly Schemas["Remediation"][], now = new Date()): RemediationKpis {
  const byReason = new Map<string, number>();
  const all = [...rows, ...held.filter((h) => h.id === undefined || h.id === 0)];
  let open = 0;
  let stale = 0;
  let recommended = 0;
  let failed = 0;
  let outstanding = 0;
  for (const r of all) {
    if (r.state === "open" && r.kind === "pull_request") open += 1;
    if (isStale(r, now)) stale += 1;
    if (r.state === "recommended") recommended += 1;
    if (r.state === "failed") failed += 1;
    if (r.state !== undefined && OUTSTANDING.includes(r.state)) outstanding += 1;
    if (isHeld(r)) byReason.set(r.hold!, (byReason.get(r.hold!) ?? 0) + 1);
  }
  const heldByReason = [...byReason.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([hold, count]) => ({ hold, count }));
  return { open, stale, recommended, held: heldByReason.reduce((n, h) => n + h.count, 0), heldByReason, failed, outstanding };
}

export const remediationsCsv: CsvColumns<Schemas["Remediation"]> = {
  head: ["org", "repository", "control", "kind", "state", "pr_number", "pr_url", "hold", "age_days", "created_at", "last_run_at", "closed_at"],
  row: (r) => [r.org, r.repository, r.control_id, r.kind, r.state ?? "", r.pr_number, r.pr_url ?? "", r.hold ?? "", remediationAge(r), r.created_at, r.last_run_at ?? "", r.closed_at ?? ""],
};
