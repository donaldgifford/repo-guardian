import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";

import type { FindingFilters } from "../api/queries";
import { useFindings } from "../api/queries";
import { Empty, Evidence, Loading, Remediation, StatusBadge } from "../components/common";
import { Button } from "../components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { findingsCsv } from "../lib/csv";
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

const options: Record<(typeof textFilters)[number], string[]> = {
  status: ["compliant", "non_compliant", "not_applicable", "unknown"],
  reason: [
    "file_missing",
    "assertion_failed",
    "content_differs",
    "forbidden_present",
    "setting_mismatch",
    "ruleset_missing",
    "ruleset_mismatch",
    "out_of_scope_policy",
    "out_of_scope_rule",
    "ignored_global",
    "ignored_rule",
    "gate_closed",
    "branch_missing",
    "empty_repository",
    "gate_error",
    "foreign_pr_open",
    "migrated_from_v1",
  ],
  remediation: ["none", "pr_open", "foreign_pr", "applied", "dry_run", "disabled"],
  kind: ["file", "setting", "branch_protection"],
  rule: [],
};

// download saves text as a file, client-side.
function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

export function FindingsView() {
  const search = route.useSearch();
  const { org } = app.useSearch();
  const navigate = useNavigate();
  const filters: FindingFilters = { ...search, ...(org ? { org } : {}) };
  const findings = useFindings(filters);
  const items = findings.data?.pages.flatMap((p) => p.items) ?? [];

  const set = (key: keyof FindingsSearch, value: string) => {
    void navigate({ to: ".", search: (prev) => patchSearch(prev, { [key]: key === "pr_stale" && value !== "" ? value === "true" : value }) });
  };

  return (
    <>
      <h1 className="text-xl font-semibold">Findings</h1>
      <div className="flex flex-wrap items-end gap-3 text-sm">
        {textFilters.map((k) =>
          k === "rule" ? (
            <label key={k} className="flex flex-col gap-1">
              <span className="text-muted-foreground">rule</span>
              <input
                className="h-8 rounded-md border border-border px-2"
                defaultValue={search.rule ?? ""}
                onKeyDown={(e) => {
                  if (e.key === "Enter") set("rule", e.currentTarget.value);
                }}
                onBlur={(e) => set("rule", e.currentTarget.value)}
              />
            </label>
          ) : (
            <label key={k} className="flex flex-col gap-1">
              <span className="text-muted-foreground">{k}</span>
              <select className="h-8 rounded-md border border-border bg-background px-2" value={search[k] ?? ""} onChange={(e) => set(k, e.target.value)}>
                <option value="">any</option>
                {options[k].map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
            </label>
          ),
        )}
        <label className="flex flex-col gap-1">
          <span className="text-muted-foreground">stale PR</span>
          <select
            className="h-8 rounded-md border border-border bg-background px-2"
            value={search.pr_stale === undefined ? "" : String(search.pr_stale)}
            onChange={(e) => set("pr_stale", e.target.value)}
          >
            <option value="">any</option>
            <option value="true">stale</option>
            <option value="false">not stale</option>
          </select>
        </label>
        <Button variant="outline" size="sm" className="ml-auto" disabled={items.length === 0} onClick={() => download("findings.csv", findingsCsv(items))}>
          Export CSV ({items.length})
        </Button>
      </div>
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
