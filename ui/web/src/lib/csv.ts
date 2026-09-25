import type { Schemas } from "../api/client";

// csvCell quotes a value for RFC 4180 and defuses spreadsheet formulas:
// a cell starting with = + - @ (or a tab/CR) is prefixed with a quote,
// because repository names and evidence are repository-controlled.
export function csvCell(v: string | number | boolean | undefined | null): string {
  let s = v === undefined || v === null ? "" : String(v);
  if (/^[=+\-@\t\r]/.test(s)) {
    s = `'${s}`;
  }
  return /[",\r\n]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s;
}

const columns = ["org", "repository", "rule_kind", "rule_name", "status", "reason", "remediation", "status_since", "pr_number", "pr_stale"] as const;

// findingsCsv renders the fetched findings as CSV, client-side.
export function findingsCsv(findings: Schemas["Finding"][]): string {
  const rows = findings.map((f) =>
    [f.org, f.repository, f.rule_kind, f.rule_name, f.status, f.reason, f.remediation, f.status_since, f.pr?.number, f.pr_stale].map(csvCell).join(","),
  );
  return [columns.join(","), ...rows].join("\r\n") + "\r\n";
}
