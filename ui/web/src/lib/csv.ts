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

// tableCsv renders any head + rows through csvCell, for grids whose
// visible columns the page chooses.
export function tableCsv(head: readonly string[], rows: readonly (readonly (string | number | boolean | undefined | null)[])[]): string {
  return [head.map(csvCell).join(","), ...rows.map((r) => r.map(csvCell).join(","))].join("\r\n") + "\r\n";
}

const columns = ["org", "repository", "rule_kind", "rule_name", "status", "reason", "remediation", "status_since", "pr_number", "pr_stale"] as const;

// downloadCsv saves text as a CSV file, client-side.
export function downloadCsv(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

// findingsCsv renders the fetched findings as CSV, client-side.
export function findingsCsv(findings: Schemas["Finding"][]): string {
  const rows = findings.map((f) =>
    [f.org, f.repository, f.rule_kind, f.rule_name, f.status, f.reason, f.remediation, f.status_since, f.pr?.number, f.pr_stale].map(csvCell).join(","),
  );
  return [columns.join(","), ...rows].join("\r\n") + "\r\n";
}
