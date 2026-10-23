import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useRef } from "react";

import type { Schemas } from "../api/client";
import { exportCap, fetchAllResults, pageItems, type ResultsFilter, useControls, useOrgs, useRemediationEnabled, useResults } from "../api/queries";
import { RemediationBadge } from "../components/badges";
import { Empty, Loading } from "../components/common";
import { ExportButton } from "../components/ExportButton";
import { FilterChips, type FilterValues } from "../components/filters";
import { NOT_ENABLED } from "../components/GatedPanel";
import { LoadMore } from "../components/LoadMore";
import { OrgScope, useOrgScope } from "../components/page/OrgScope";
import { PageHeader } from "../components/page/PageHeader";
import { Time } from "../components/time";
import { Badge } from "../components/ui/badge";
import { Card } from "../components/ui/card";
import { type Column, DataTable } from "../components/ui/data-table";
import { Tabs } from "../components/ui/tabs";
import type { CsvColumns } from "../lib/csv";
import { humanize } from "../lib/format";
import { patchSearch } from "../lib/search";
import { SIDE_BY_SIDE, useMediaQuery } from "../lib/use-media-query";
import { cn } from "../lib/utils";
import { FindingDrawer, findingKey } from "./findings/drawer";

export type FindingsSort = NonNullable<ResultsFilter["sort"]>;

// FindingsSearch is the findings filter, kept in the URL (each key is a
// /results query parameter), plus `sort` and `open`, the drawer's
// finding as <repository_id>/<control>.
export interface FindingsSearch extends FilterValues {
  sort?: FindingsSort;
  open?: string;
}

const textFilters = ["control", "rule", "org", "decision", "open"] as const;
const sorts: readonly FindingsSort[] = ["since_asc", "since_desc", "pr_age"];

// defaultSort is oldest first: a findings page is a worklist.
export const defaultSort: FindingsSort = "since_asc";

export function parseFindingsSearch(s: Record<string, unknown>): FindingsSearch {
  const out: FindingsSearch = {};
  for (const k of textFilters) {
    const v = s[k];
    if (typeof v === "string" && v !== "") out[k] = v;
  }
  if (s.pr_stale === true || s.pr_stale === "true") out.pr_stale = true;
  if (s.pr_stale === false || s.pr_stale === "false") out.pr_stale = false;
  if (typeof s.sort === "string" && (sorts as readonly string[]).includes(s.sort) && s.sort !== defaultSort) out.sort = s.sort as FindingsSort;
  return out;
}

// resultsFilter is the /results request for a search: always the
// non-compliant bucket (a finding is a non-compliant result), the
// scope's org when the chips name none, and the sort made explicit.
export function resultsFilter(search: FindingsSearch, scopeOrg: string | undefined): ResultsFilter {
  const { open: _open, sort, ...filters } = search;
  const f: ResultsFilter = { bucket: "non_compliant", sort: sort ?? defaultSort };
  for (const k of ["control", "rule", "org", "decision"] as const) {
    const v = filters[k];
    if (v) f[k] = v;
  }
  if (!f.org && scopeOrg) f.org = scopeOrg;
  if (filters.pr_stale !== undefined) f.pr_stale = filters.pr_stale;
  return f;
}

// controlRules lists the rule ids seen failing for the chosen control
// in the loaded rows: /results is the only API that names a control's
// rules, and a rule that never fails yields no finding to filter to.
export function controlRules(rows: readonly Schemas["ResultRow"][], control: string | undefined): string[] {
  if (!control) return [];
  return [...new Set(rows.filter((r) => r.control_id === control).flatMap((r) => r.failing_rules))].sort();
}

const route = getRouteApi("/app/findings");

export const findingsCsv: CsvColumns<Schemas["ResultRow"]> = {
  head: ["org", "repository", "control", "version", "decision", "failing_rules", "reason", "remediation_state", "pr_number", "pr_url", "since"],
  row: (r) => [r.org, r.repository, r.control_id, r.control_version, r.decision, r.failing_rules.join(" "), r.reason ?? "", r.remediation?.state ?? "", r.remediation?.pr_number, r.remediation?.pr_url ?? "", r.last_changed_at],
};

// findingColumns is the findings table (DESIGN-0037 §Findings). The
// Remediation column reads "—" with the not-enabled reason without the
// Remediation App. Since hides when the drawer shares the row.
export function findingColumns(remediation: boolean | undefined): Column<Schemas["ResultRow"]>[] {
  return [
    {
      key: "repository",
      header: "Repository",
      cell: (r) => (
        <>
          <Link to="/repositories/$id" params={{ id: String(r.repository_id) }} className="font-mono font-medium hover:text-accent">
            {r.repository}
          </Link>
          <span className="block text-xs text-muted-foreground">{r.org}</span>
        </>
      ),
    },
    {
      key: "control",
      header: "Control",
      cell: (r) => (
        <>
          <span className="font-mono">{r.control_id}</span>
          <span className="block text-xs text-muted-foreground">
            v{r.control_version}
            {r.decision !== "baseline" ? ` · ${humanize(r.decision)}` : ""}
          </span>
        </>
      ),
    },
    {
      key: "rules",
      header: "Failing rules",
      // `reason` is the first failing rule's evidence headline, as
      // describeEvidence would print it; ResultRow carries no evidence.
      cell: (r) => (
        <div className="max-w-[380px] space-y-1 whitespace-normal">
          <div className="flex flex-wrap gap-1">
            {r.failing_rules.map((rule) => (
              <Badge key={rule} tone="bad">
                {rule}
              </Badge>
            ))}
          </div>
          {r.reason ? <div className="font-mono text-[11px] text-muted-foreground">{r.reason}</div> : null}
        </div>
      ),
    },
    {
      key: "remediation",
      header: "Remediation",
      cell: (r) =>
        remediation === false ? (
          <span className="text-muted-foreground" title={NOT_ENABLED} data-gated="not_enabled">
            —
          </span>
        ) : (
          <RemediationBadge remediation={r.remediation} />
        ),
    },
    { key: "since", header: "Since", cell: (r) => <Time iso={r.last_changed_at} style="relative" />, hideBelow: "compact", className: "whitespace-nowrap" },
  ];
}

const sortOptions = (remediation: boolean | undefined) => [
  { value: "since_asc" as const, label: "oldest first" },
  { value: "since_desc" as const, label: "newest first" },
  ...(remediation ? [{ value: "pr_age" as const, label: "PR age" }] : []),
];

// FindingsView lists non-compliant results: a control that failed on a
// repository (DESIGN-0037 §Findings). Filters map 1:1 to /results
// query parameters and live in the URL; the drawer is `?open=`.
export function FindingsView() {
  const search = route.useSearch();
  const { open } = search;
  const scopeOrg = useOrgScope();
  const navigate = useNavigate();
  const remediation = useRemediationEnabled();
  const filter = resultsFilter(search, scopeOrg);
  const results = useResults(filter);
  const controls = useControls(filter.org);
  const orgs = useOrgs();
  const items = pageItems(results.data);
  const total = results.data?.pages[0]?.total;
  const columns = findingColumns(remediation);
  const patch = (p: Record<string, string | boolean | undefined>) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => patchSearch(prev, p) });
  const openFinding = (key: string | undefined) => patch({ open: key });
  const sideBySide = useMediaQuery(SIDE_BY_SIDE);
  const tableRef = useRef<HTMLDivElement>(null);
  const inline = sideBySide && open !== undefined;
  const rowFor = (key: string | undefined) => (key === undefined ? null : tableRef.current?.querySelector<HTMLElement>(`tr[data-key="${CSS.escape(key)}"]`));
  const { open: _o, ...filterValues } = search;
  const sort = search.sort ?? defaultSort;

  return (
    <>
      <PageHeader
        title="Findings"
        subtitle={
          <>
            Non-compliant results: a control that failed on a repository. Filters map 1:1 to <code>GET /results</code>.
          </>
        }
        controls={
          <>
            <OrgScope />
            <ExportButton page="findings" filter={{ control: filter.control, org: filter.org, rule: filter.rule }} columns={findingsCsv} rows={items} all={{ fetch: () => fetchAllResults(filter), cap: exportCap }} />
          </>
        }
      />
      <div className={cn("grid gap-4", inline && "grid-cols-[minmax(0,1fr)_470px]")} data-layout={inline ? "side-by-side" : "stacked"}>
        <div ref={tableRef} className="min-w-0 space-y-3">
          <Card className="flex flex-wrap items-center gap-2 px-3.5 py-2.5">
            <FilterChips values={filterValues} onChange={patch} options={{ controls: (controls.data?.items ?? []).map((c) => c.id), rules: controlRules(items ?? [], search.control), orgs: (orgs.data?.items ?? []).map((o) => o.org) }} remediation={remediation} />
            <span className="flex-1" />
            <Tabs label="Sort" options={sortOptions(remediation)} value={sort} onChange={(next) => patch({ sort: next === defaultSort ? undefined : next })} />
          </Card>
          <Card bodyClassName="p-0" title="Findings" subtitle={total !== undefined ? `${(items ?? []).length.toLocaleString()} loaded of ${total.toLocaleString()}` : undefined}>
            {!items ? (
              <Loading />
            ) : items.length === 0 ? (
              <Empty>No findings match.</Empty>
            ) : (
              <>
                <DataTable columns={columns} rows={items} rowKey={findingKey} {...(open === undefined ? {} : { selectedKey: open })} onActivate={(r) => openFinding(findingKey(r))} compact={inline} aria-label="Findings" />
                <LoadMore loaded={items.length} total={total} hasMore={results.hasNextPage} loading={results.isFetchingNextPage} onMore={() => void results.fetchNextPage()} noun="findings" />
              </>
            )}
          </Card>
        </div>
        {open !== undefined ? <FindingDrawer open={open} inline={inline} onClose={() => openFinding(undefined)} returnFocusTo={() => rowFor(open)} /> : null}
      </div>
    </>
  );
}
