import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";

import type { FindingFilters } from "../api/queries";
import { useFindings, useOrgs, useRules } from "../api/queries";
import { StatusBadge } from "../components/badges";
import { Empty, Evidence, Loading, Remediation } from "../components/common";
import { FilterChips } from "../components/filters";
import { OrgScope, PageHeader } from "../components/page";
import { Button } from "../components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { downloadCsv, findingsCsv } from "../lib/csv";
import { relativeTime } from "../lib/format";
import { patchSearch } from "../lib/search";

// FindingsSearch is the findings filter, kept in the URL.
export interface FindingsSearch {
  status?: string;
  reason?: string;
  remediation?: string;
  kind?: string;
  rule?: string;
  pr_stale?: boolean;
}

const textFilters = ["status", "reason", "remediation", "kind", "rule"] as const;

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
  return out;
}

const route = getRouteApi("/app/findings");
const app = getRouteApi("/app");

export function FindingsView() {
  const search = route.useSearch();
  const { org } = app.useSearch();
  const navigate = useNavigate();
  const rules = useRules();
  const orgs = useOrgs();
  const filters: FindingFilters = { ...search, ...(org ? { org } : {}) };
  const findings = useFindings(filters);
  const items = findings.data?.pages.flatMap((p) => p.items) ?? [];
  const ruleNames = [...new Set((rules.data?.items ?? []).map((r) => r.name))].sort();
  const orgNames = (orgs.data?.items ?? []).map((o) => o.org);

  return (
    <>
      <PageHeader
        title="Findings"
        subtitle={<>One row per rule per repository from <code>/findings</code>.</>}
        controls={
          <>
            <OrgScope />
            <Button variant="outline" size="sm" disabled={items.length === 0} onClick={() => downloadCsv("findings.csv", findingsCsv(items))}>
              Export CSV ({items.length})
            </Button>
          </>
        }
      />
      <FilterChips values={filters} onChange={(patch) => void navigate({ to: ".", search: (prev) => patchSearch(prev, patch) })} rules={ruleNames} orgs={orgNames} />
      {!findings.data ? (
        <Loading />
      ) : items.length === 0 ? (
        <Empty>No findings match.</Empty>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Repository</TableHead>
              <TableHead>Rule</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Reason / evidence</TableHead>
              <TableHead>Remediation</TableHead>
              <TableHead>Since</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((f) => (
              <TableRow key={`${f.repository_id}/${f.rule_kind}/${f.rule_name}`}>
                <TableCell>
                  <Link to="/repos/$id" params={{ id: String(f.repository_id) }} className="hover:underline">
                    {f.org}/{f.repository}
                  </Link>
                </TableCell>
                <TableCell>{f.rule_name}</TableCell>
                <TableCell>
                  <StatusBadge status={f.status} />
                </TableCell>
                <TableCell>
                  <Evidence finding={f} />
                </TableCell>
                <TableCell>
                  <Remediation finding={f} />
                </TableCell>
                <TableCell className="whitespace-nowrap">{relativeTime(f.status_since)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {findings.hasNextPage ? (
        <Button variant="outline" onClick={() => void findings.fetchNextPage()} disabled={findings.isFetchingNextPage}>
          {findings.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      ) : null}
    </>
  );
}
