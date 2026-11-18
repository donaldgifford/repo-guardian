import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";

import { useComplianceHistory, useControls } from "../api/queries";
import { TrendChart } from "@donaldgifford/design-system/blocks/trend-chart";
import { Loading } from "../components/common";
import { OrgScope, useOrgScope } from "../components/page/OrgScope";
import { PageHeader } from "../components/page/PageHeader";
import { RangeTabs, useRangeDays } from "../components/page/RangeTabs";
import { Panel } from "../components/Panel";
import { historySeries } from "../lib/posture";
import { parseScopeSearch, type ScopeSearch } from "../lib/scope";
import { patchSearch } from "../lib/search";
import { formatPercent } from "@donaldgifford/design-system/lib/percent";

// HistorySearch keeps days a number, so the URL reads ?days=7 rather
// than the router's JSON-quoted string.
export interface HistorySearch extends ScopeSearch {
  control?: string;
}

export function parseHistorySearch(s: Record<string, unknown>): HistorySearch {
  const out: HistorySearch = { ...parseScopeSearch(s) };
  if (typeof s.control === "string" && s.control !== "") out.control = s.control;
  return out;
}

const route = getRouteApi("/app/history");

// HistoryView is compliance over time from the daily snapshots, for the
// fleet or one control: every org and lineage summed per snapshot.
export function HistoryView() {
  const search = route.useSearch();
  const navigate = useNavigate();
  const days = useRangeDays();
  const org = useOrgScope();
  const controls = useControls(org);
  const history = useComplianceHistory({ days, ...(org ? { org } : {}), ...(search.control ? { control: search.control } : {}) });
  const series = useMemo(() => historySeries(history.data?.items ?? []), [history.data]);
  const setControl = (control: string | undefined) => void navigate({ to: ".", search: (prev: HistorySearch) => patchSearch(prev, { control }) });
  const last = series[series.length - 1];
  return (
    <>
      <PageHeader
        title="History"
        description={
          <>
            Each UTC day's last snapshot · <code>GET /compliance/history</code>
          </>
        }
        actions={
          <>
            <OrgScope />
            <select
              aria-label="Control"
              value={search.control ?? ""}
              onChange={(e) => setControl(e.target.value || undefined)}
              className="h-7 rounded-lg border border-border bg-card px-2 text-xs"
            >
              <option value="">every control</option>
              {(controls.data?.items ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.id}
                </option>
              ))}
            </select>
            <RangeTabs />
          </>
        }
      />
      <Panel title={search.control ?? "Every control"} subtitle={last ? `latest ${formatPercent(last.value)}` : "No snapshot in range."}>
        {history.data ? <TrendChart points={series} series="compliance %" rangeDays={days} label="Compliance over time" /> : <Loading />}
      </Panel>
    </>
  );
}
