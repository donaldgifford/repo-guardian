import { useState } from "react";

import { type CsvColumns, csvTable, type CsvValue, downloadCsv, exportFilename } from "../lib/csv";
import { Button } from "./ui/button";

export interface ExportFetch<T> {
  // fetch pages the whole result set for the export; truncated says the
  // cap cut it short (DESIGN-0037 OQ6).
  fetch: () => Promise<{ rows: readonly T[]; truncated: boolean }>;
  cap: number;
}

// ExportButton sits in a table card's header and downloads the rows
// the page holds as CSV (DESIGN-0037 §Export): the filename names the
// page, the filters in force and the day. It is disabled until the rows
// have loaded and when there are none. With `all`, the click pages the
// whole set instead and says when the cap cut it short. `download` is
// the file sink, replaceable by a test.
export function ExportButton<T>({
  page,
  filter,
  columns,
  rows,
  all,
  download = downloadCsv,
}: {
  page: string;
  filter?: Record<string, CsvValue>;
  columns: CsvColumns<T>;
  rows: readonly T[] | undefined;
  all?: ExportFetch<T>;
  download?: (name: string, text: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | undefined>(undefined);
  const ready = rows !== undefined && rows.length > 0;
  const save = (items: readonly T[]) => download(exportFilename(page, filter), csvTable(columns, items));
  const onClick = async () => {
    if (!rows) return;
    if (!all) {
      save(rows);
      return;
    }
    setBusy(true);
    setNotice(undefined);
    try {
      const { rows: items, truncated } = await all.fetch();
      save(items);
      if (truncated) setNotice(`Export cut short at ${all.cap.toLocaleString()} rows; narrow the filters for the rest.`);
    } catch (e) {
      setNotice(e instanceof Error ? e.message : "Export failed.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <span className="inline-flex items-center gap-2">
      {notice ? (
        <span role="status" className="text-xs text-warn">
          {notice}
        </span>
      ) : null}
      <Button variant="ghost" size="sm" aria-label={`Export ${page} as CSV`} disabled={!ready || busy} onClick={() => void onClick()}>
        {busy ? "Exporting…" : "Export"}
      </Button>
    </span>
  );
}
