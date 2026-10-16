import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";

import { useComplianceHistory, useControls } from "../api/queries";
import { TrendChart } from "../components/charts";
import { Loading } from "../components/common";
import { PageHeader } from "../components/page/PageHeader";
import { Card } from "../components/ui/card";
import { Tabs } from "../components/ui/tabs";
import { formatPercent } from "../lib/format";
import { historySeries } from "../lib/posture";
import { patchSearch } from "../lib/search";

const ranges = [
  { value: "7", label: "7d" },
  { value: "30", label: "30d" },
  { value: "90", label: "90d" },
] as const;

type RangeValue = (typeof ranges)[number]["value"];

export interface HistorySearch {
  control?: string;
  days?: RangeValue;
}

export function parseHistorySearch(s: Record<string, unknown>): HistorySearch {
  const out: HistorySearch = {};
  if (typeof s.control === "string" && s.control !== "") out.control = s.control;
  if (s.days === "7" || s.days === "90") out.days = s.days;
  return out;
}

const route = getRouteApi("/app/history");

// HistoryView is compliance over time from the daily snapshots, for the
// fleet or one control: every org and lineage summed per snapshot.
export function HistoryView() {
  const search = route.useSearch();
  const navigate = useNavigate();
  const days = search.days ?? "30";
  const controls = useControls();
  const history = useComplianceHistory({ days: Number(days), ...(search.control ? { control: search.control } : {}) });
  const series = useMemo(() => historySeries(history.data?.items ?? []), [history.data]);
  const setSearch = (patch: Record<string, string | undefined>) => void navigate({ to: ".", search: (prev: HistorySearch) => patchSearch(prev, patch) });
  const last = series[series.length - 1];
  return (
    <>
      <PageHeader
        title="History"
        subtitle={
          <>
            Each UTC day's last snapshot · <code>GET /compliance/history</code>
          </>
        }
        controls={
          <>
            <select
              aria-label="Control"
              value={search.control ?? ""}
              onChange={(e) => setSearch({ control: e.target.value || undefined })}
              className="h-7 rounded-lg border border-border bg-panel px-2 text-xs"
            >
              <option value="">every control</option>
              {(controls.data?.items ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.id}
                </option>
              ))}
            </select>
            <Tabs label="Range" options={ranges} value={days} onChange={(v) => setSearch({ days: v === "30" ? undefined : v })} />
          </>
        }
      />
      <Card title={search.control ?? "Every control"} subtitle={last ? `latest ${formatPercent(last.percent)}` : "No snapshot in range."}>
        {history.data ? <TrendChart series={series} rangeDays={Number(days)} aria-label="Compliance over time" /> : <Loading />}
      </Card>
    </>
  );
}
