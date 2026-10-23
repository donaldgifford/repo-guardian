// CSV export, client-side (DESIGN-0037 §Export). Every table card
// offers it; the rows are whatever the page already holds.

export type CsvValue = string | number | boolean | undefined | null;

// csvCell quotes a value for RFC 4180 and defuses spreadsheet formulas:
// a cell starting with = + - @ (or a tab/CR) is prefixed with a quote,
// because repository names and evidence are repository-controlled.
export function csvCell(v: CsvValue): string {
  let s = v === undefined || v === null ? "" : String(v);
  if (/^[=+\-@\t\r]/.test(s)) {
    s = `'${s}`;
  }
  return /[",\r\n]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s;
}

// tableCsv renders any head + rows through csvCell, for grids whose
// visible columns the page chooses.
export function tableCsv(head: readonly string[], rows: readonly (readonly CsvValue[])[]): string {
  return [head.map(csvCell).join(","), ...rows.map((r) => r.map(csvCell).join(","))].join("\r\n") + "\r\n";
}

// CsvColumns names a table's export: the header row and how one row
// of the page's data becomes cells. Pages declare one beside their
// DataTable columns, so the export never reads rendered markup.
export interface CsvColumns<T> {
  head: readonly string[];
  row: (item: T) => readonly CsvValue[];
}

export function csvTable<T>(columns: CsvColumns<T>, items: readonly T[]): string {
  return tableCsv(
    columns.head,
    items.map((i) => columns.row(i)),
  );
}

// exportFilename is `repo-guardian-<page>[-<key>-<value>…]-<date>.csv`:
// the page, every set filter and the day, each lowercased and reduced
// to [a-z0-9._-] so a repository-controlled value never names a path.
export function exportFilename(page: string, filter: Record<string, CsvValue> = {}, now: Date = new Date()): string {
  const parts = [page];
  for (const [k, v] of Object.entries(filter)) {
    if (v === undefined || v === null || v === "") continue;
    parts.push(k, String(v));
  }
  parts.push(now.toISOString().slice(0, 10));
  const safe = parts.map((p) => p.toLowerCase().replaceAll(/[^a-z0-9._-]+/g, "_"));
  return `repo-guardian-${safe.join("-")}.csv`;
}

// downloadCsv saves text as a CSV file, client-side.
export function downloadCsv(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}
