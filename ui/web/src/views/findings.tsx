import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useRef } from "react";

import type { Schemas } from "../api/client";
import type { FindingFilters } from "../api/queries";
import { useFindings, useOrgs, useRules, useSummary, useUIConfig } from "../api/queries";
import { KindBadge, ReasonBadge, RemediationBadge, StatusBadge } from "../components/badges";
import { Empty, ExternalLink, Loading } from "../components/common";
import { FilterChips } from "../components/filters";
import { OrgScope, PageHeader } from "../components/page";
import { Time } from "../components/time";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Chip } from "../components/ui/chip";
import { type Column, DataTable } from "../components/ui/data-table";
import { downloadCsv, findingsCsv } from "../lib/csv";
import { ageDays } from "../lib/format";
import { prLink } from "../lib/pr";
import { patchSearch } from "../lib/search";
import { SIDE_BY_SIDE, useMediaQuery } from "../lib/use-media-query";
import { cn } from "../lib/utils";
import { FindingDrawer } from "./findings/drawer";

// FindingsSearch is the findings filter, kept in the URL, plus `open`,
// the drawer's finding as <repository_id>/<kind>/<name>.
export interface FindingsSearch {
  status?: string;
  reason?: string;
  remediation?: string;
  kind?: string;
  rule?: string;
  pr_stale?: boolean;
  // sort=pr_age orders by PR age, oldest first (IMPL-0031 7.2).
  sort?: "pr_age";
  open?: string;
}

const textFilters = ["status", "reason", "remediation", "kind", "rule", "open"] as const;

export function parseFindingsSearch(s: Record<string, unknown>): FindingsSearch {
  const out: FindingsSearch = {};
  for (const k of textFilters) {
    const v = s[k];
    if (typeof v === "string" && v !== "") {
      out[k] = v;
    }
  }
  if (s.pr_stale === true || s.pr_stale === "true") out.pr_stale = true;
  if (s.pr_stale === false || s.pr_stale === "false") out.pr_stale = false;
  if (s.sort === "pr_age") out.sort = "pr_age";
  return out;
}

const route = getRouteApi("/app/findings");
const app = getRouteApi("/app");

// findingKey is a finding's identity in the table and in `?open=`.
export function findingKey(f: Pick<Schemas["Finding"], "repository_id" | "rule_kind" | "rule_name">): string {
  return `${f.repository_id}/${f.rule_kind}/${f.rule_name}`;
}

// resultCount is the line under the chips: "<loaded> of <total>" when
// the status chip is non_compliant (the one total /summary knows),
// else just what is loaded, with "more" when another page exists.
export function resultCount(loaded: number, status: string | undefined, nonCompliant: number | undefined, more: boolean): string {
  if (status === "non_compliant" && nonCompliant !== undefined) {
    return `${loaded.toLocaleString()} of ${nonCompliant.toLocaleString()} findings`;
  }
  return `${loaded.toLocaleString()} ${loaded === 1 ? "finding" : "findings"}${more ? " loaded, more available" : ""}`;
}

function PRCell({ f, host }: { f: Schemas["Finding"]; host: string | undefined }) {
  if (!f.pr) return <span className="text-muted-foreground">—</span>;
  const href = host ? prLink(host, f.org, f.repository, f.pr.number) : undefined;
  const label = `#${f.pr.number}`;
  return href ? <ExternalLink href={href}>{label}</ExternalLink> : <span className="font-mono">{label}</span>;
}

function AgeCell({ f }: { f: Schemas["Finding"] }) {
  if (!f.pr?.created_at) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="inline-flex items-center gap-1.5 tabular-nums">
      {ageDays(f.pr.created_at)}d{f.pr_stale ? <Badge tone="warn">stale</Badge> : null}
    </span>
  );
}

// findingColumns is the findings table (DESIGN-0036 §Findings). Age and
// Since hide when the drawer shares the row.
export function findingColumns(host: string | undefined): Column<Schemas["Finding"]>[] {
  return [
    {
      key: "repository",
      header: "Repository",
      cell: (f) => (
        <>
          <Link to="/repos/$id" params={{ id: String(f.repository_id) }} className="font-mono font-medium hover:underline">
            {f.org}/{f.repository}
          </Link>
          <span className="block text-xs text-muted-foreground">{f.org}</span>
        </>
      ),
    },
    {
      key: "rule",
      header: "Rule",
      cell: (f) => (
        <span className="inline-flex items-center gap-2">
          <span className="font-mono">{f.rule_name}</span>
          <KindBadge kind={f.rule_kind} />
        </span>
      ),
    },
    { key: "status", header: "Status", cell: (f) => <StatusBadge status={f.status} /> },
    { key: "reason", header: "Reason", cell: (f) => (f.reason ? <ReasonBadge reason={f.reason} status={f.status} /> : <span className="text-muted-foreground">—</span>) },
    { key: "remediation", header: "Remediation", cell: (f) => <RemediationBadge remediation={f.remediation} table /> },
    { key: "pr", header: "PR", cell: (f) => <PRCell f={f} host={host} /> },
    { key: "age", header: "Age", cell: (f) => <AgeCell f={f} />, numeric: true, hideBelow: "compact" },
    { key: "since", header: "Since", cell: (f) => <Time iso={f.status_since} style="relative" />, hideBelow: "compact", className: "whitespace-nowrap" },
  ];
}

export function FindingsView() {
  const { open, ...search } = route.useSearch();
  const { org } = app.useSearch();
  const navigate = useNavigate();
  const rules = useRules();
  const orgs = useOrgs();
  const summary = useSummary();
  const config = useUIConfig();
  const filters: FindingFilters = { ...search, ...(org ? { org } : {}) };
  const findings = useFindings(filters);
  const items = findings.data?.pages.flatMap((p) => p.items) ?? [];
  const ruleNames = [...new Set((rules.data?.items ?? []).map((r) => r.name))].sort();
  const orgNames = (orgs.data?.items ?? []).map((o) => o.org);
  const columns = findingColumns(config.data?.github_host);
  const openFinding = (key: string | undefined) => void navigate({ to: ".", search: (prev) => patchSearch(prev, { open: key }) });
  const sideBySide = useMediaQuery(SIDE_BY_SIDE);
  const tableRef = useRef<HTMLDivElement>(null);
  const drawerOpen = open !== undefined && items.length > 0;
  const inline = sideBySide && drawerOpen;
  const rowFor = (key: string | undefined) => (key === undefined ? null : tableRef.current?.querySelector<HTMLElement>(`tr[data-key="${CSS.escape(key)}"]`));

  return (
    <>
      <PageHeader
        title="Findings"
        subtitle={<>One row per rule per repository from <code>/findings</code>.</>}
        controls={
          <>
            <OrgScope />
            <Button variant="outline" size="sm" disabled={items.length === 0} onClick={() => downloadCsv("findings.csv", findingsCsv(items))}>
              Export CSV
            </Button>
          </>
        }
      />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <FilterChips values={filters} onChange={(patch) => void navigate({ to: ".", search: (prev) => patchSearch(prev, patch) })} rules={ruleNames} orgs={orgNames} />
          <Chip
            name="sort"
            value="pr_age"
            aria-pressed={search.sort === "pr_age"}
            className={cn(search.sort === "pr_age" && "border-accent text-foreground")}
            onClick={() => void navigate({ to: ".", search: (prev) => patchSearch(prev, { sort: search.sort === "pr_age" ? undefined : "pr_age" }) })}
          />
        </div>
        {findings.data ? <span className="text-xs text-muted-foreground tabular-nums">{resultCount(items.length, search.status, summary.data?.findings.non_compliant, findings.hasNextPage)}</span> : null}
      </div>
      <div className={cn("grid gap-4", inline && "grid-cols-[minmax(0,1fr)_470px]")} data-layout={inline ? "side-by-side" : "stacked"}>
        <div ref={tableRef} className="min-w-0 space-y-4">
          {!findings.data ? (
            <Loading />
          ) : items.length === 0 ? (
            <Empty>No findings match.</Empty>
          ) : (
            <DataTable
              columns={columns}
              rows={items}
              rowKey={findingKey}
              {...(open === undefined ? {} : { selectedKey: open })}
              onActivate={(f) => openFinding(findingKey(f))}
              compact={inline}
              aria-label="Findings"
              className="rounded-lg border border-border bg-panel"
            />
          )}
          {findings.hasNextPage ? (
            <Button variant="outline" onClick={() => void findings.fetchNextPage()} disabled={findings.isFetchingNextPage}>
              {findings.isFetchingNextPage ? "Loading…" : "Load more"}
            </Button>
          ) : null}
        </div>
        {open !== undefined && findings.data ? <FindingDrawer open={open} items={items} inline={inline} onClose={() => openFinding(undefined)} returnFocusTo={() => rowFor(open)} /> : null}
      </div>
    </>
  );
}
