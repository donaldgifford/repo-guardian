import { getRouteApi, useNavigate } from "@tanstack/react-router";

import { useHistory, useRules } from "../api/queries";
import { ChartCard, TrendChart, trendTable } from "../components/charts";
import { Empty, Loading } from "../components/common";
import { OrgScope, PageHeader, RangeTabs } from "../components/page";
import { parseRange, rangeDays, rangeFrom } from "../lib/range";
import { patchSearch } from "../lib/search";
import { seriesByOrg } from "../lib/viewmodel";

// HistorySearch picks the rule whose series is shown; none means every
// rule, summed. The range rides on the shared `?range=`.
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

// HistoryView is small multiples: one compliance trend per org over the
// chosen range, optionally for one rule.
export function HistoryView() {
  const { org, range: rawRange, ...search } = route.useSearch();
  const range = parseRange(rawRange);
  const navigate = useNavigate();
  const rules = useRules();
  const history = useHistory({ ...search, ...(org ? { org } : {}), from: rangeFrom(range) });
  const selected = search.kind && search.rule ? `${search.kind}/${search.rule}` : "";
  const ruleLabel = search.rule ? `${search.rule} (${search.kind})` : "all rules";

  return (
    <>
      <PageHeader
        title="History"
        subtitle={
          <>
            Compliance snapshots over time from <code>/compliance/history</code>, one chart per org.
          </>
        }
        controls={
          <>
            <OrgScope />
            <RangeTabs />
            <label className="inline-flex items-center overflow-hidden rounded-lg border border-border text-xs">
              <span className="px-2.5 py-[5px] text-muted-foreground">Rule</span>
              <select
                aria-label="Rule"
                className="h-full bg-panel-2 px-2.5 py-[5px] text-xs text-foreground outline-none"
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
            </label>
          </>
        }
      />
      {!history.data ? (
        <Loading />
      ) : history.data.items.length === 0 ? (
        <Empty>No snapshots in this range.</Empty>
      ) : (
        <>
          {history.data.truncated ? <p className="text-sm text-muted-foreground">History is longer than the page cap; each chart shows its first pages only. Narrow the range or the rule.</p> : null}
          <div className="grid gap-4 lg:grid-cols-2">
            {[...seriesByOrg(history.data.items)].map(([o, series]) => (
              <ChartCard key={o} title={o} subtitle={`compliance % · ${ruleLabel}`} table={trendTable(series, "compliance %")}>
                <TrendChart series={series} rangeDays={rangeDays(range)} height={180} aria-label={`${o} compliance % over ${range}`} />
              </ChartCard>
            ))}
          </div>
        </>
      )}
    </>
  );
}
