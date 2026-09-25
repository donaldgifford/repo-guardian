import { getRouteApi, useNavigate } from "@tanstack/react-router";

import { useHistory, useRules } from "../api/queries";
import { Empty, Loading, Section, TrendChart } from "../components/common";
import { patchSearch } from "../lib/search";
import { seriesByOrg } from "../lib/viewmodel";

// HistorySearch picks the rule whose series is shown; none means every
// rule, summed.
export interface HistorySearch {
  kind?: string;
  rule?: string;
}

export function parseHistorySearch(s: Record<string, unknown>): HistorySearch {
  const out: HistorySearch = {};
  if (typeof s.kind === "string" && s.kind !== "") out.kind = s.kind;
  if (typeof s.rule === "string" && s.rule !== "") out.rule = s.rule;
  return out;
}

const route = getRouteApi("/app/history");
const app = getRouteApi("/app");

export function HistoryView() {
  const search = route.useSearch();
  const { org } = app.useSearch();
  const navigate = useNavigate();
  const rules = useRules();
  const history = useHistory({ ...search, ...(org ? { org } : {}) });
  const selected = search.kind && search.rule ? `${search.kind}/${search.rule}` : "";

  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">History</h1>
        <select
          className="ml-auto h-8 rounded-md border border-border bg-background px-2 text-sm"
          value={selected}
          onChange={(e) => {
            const [kind, ...rest] = e.target.value.split("/");
            const rule = rest.join("/");
            void navigate({ to: ".", search: (prev) => patchSearch(prev, { kind, rule }) });
          }}
        >
          <option value="">All rules</option>
          {rules.data?.items.map((r) => (
            <option key={`${r.kind}/${r.name}`} value={`${r.kind}/${r.name}`}>
              {r.name} ({r.kind})
            </option>
          ))}
        </select>
      </div>
      {!history.data ? (
        <Loading />
      ) : history.data.items.length === 0 ? (
        <Empty>No snapshots yet.</Empty>
      ) : (
        <>
          {history.data.truncated ? <p className="text-sm text-muted-foreground">Showing the first pages only; narrow the filter for the full range.</p> : null}
          <div className="grid gap-6 md:grid-cols-2">
            {[...seriesByOrg(history.data.items)].map(([o, series]) => (
              <Section key={o} title={o}>
                <TrendChart series={series} height={160} />
              </Section>
            ))}
          </div>
        </>
      )}
    </>
  );
}
