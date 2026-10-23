import { type CsvColumns, csvTable, type CsvValue, downloadCsv, exportFilename } from "../lib/csv";
import { Button } from "./ui/button";

// ExportButton sits in a table card's header and downloads the rows
// the page holds as CSV (DESIGN-0037 §Export): the filename names the
// page, the filters in force and the day. It is disabled until the rows
// have loaded and when there are none. `download` is the file sink,
// replaceable by a test.
export function ExportButton<T>({
  page,
  filter,
  columns,
  rows,
  download = downloadCsv,
}: {
  page: string;
  filter?: Record<string, CsvValue>;
  columns: CsvColumns<T>;
  rows: readonly T[] | undefined;
  download?: (name: string, text: string) => void;
}) {
  const ready = rows !== undefined && rows.length > 0;
  return (
    <Button
      variant="ghost"
      size="sm"
      aria-label={`Export ${page} as CSV`}
      disabled={!ready}
      onClick={() => {
        if (!rows) return;
        download(exportFilename(page, filter), csvTable(columns, rows));
      }}
    >
      Export
    </Button>
  );
}
