import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import { Legend } from "./ChartCard";
import { fitTicks, niceBounds, tickLabel } from "./scale";
import { ChartTip } from "./tooltip";

const AXIS = { fontFamily: "var(--font-mono)", fontSize: 11, fill: "var(--color-muted-foreground)" } as const;

export interface PairDay {
  // day is an ISO date.
  day: string;
  a: number;
  b: number;
}

// PairBars is the two-series daily bar chart (the mockup's pairBars):
// two bars per day, at most 10px wide, a shared y axis bracketed by
// niceBounds, a legend naming the pair, and a per-day tooltip.
export function PairBars({
  days,
  labels,
  colors = ["var(--color-accent)", "var(--color-ok)"],
  height = 200,
  "aria-label": ariaLabel,
}: {
  days: readonly PairDay[];
  labels: readonly [string, string];
  colors?: readonly [string, string];
  height?: number;
  "aria-label": string;
}) {
  const bounds = niceBounds(days.flatMap((d) => [d.a * 1.1, d.b * 1.1]), { pad: 0 });
  const ticks = fitTicks(days.length, 7).map((i) => days[i]?.day ?? "");
  return (
    <>
      <div role="img" aria-label={ariaLabel} style={{ height }}>
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={days.map((d) => ({ ...d }))} margin={{ top: 14, right: 10, bottom: 0, left: 0 }} barCategoryGap="30%" barGap={2}>
            <CartesianGrid stroke="var(--color-border)" vertical={false} />
            <XAxis dataKey="day" ticks={ticks} tickFormatter={(v: string) => tickLabel(v, "date")} tick={AXIS} axisLine={false} tickLine={false} interval={0} />
            <YAxis domain={[0, bounds.max]} ticks={bounds.ticks} allowDecimals={false} tick={AXIS} axisLine={false} tickLine={false} width={34} />
            <Tooltip
              cursor={{ fill: "var(--color-panel-2)" }}
              isAnimationActive={false}
              content={({ active, payload, label: at }) => {
                if (!active || !payload?.[0]) return null;
                const d = payload[0].payload as PairDay;
                return (
                  <ChartTip
                    heading={tickLabel(String(at), "date")}
                    rows={[
                      { label: labels[0], value: d.a.toLocaleString(), color: colors[0] },
                      { label: labels[1], value: d.b.toLocaleString(), color: colors[1] },
                    ]}
                  />
                );
              }}
            />
            <Bar dataKey="a" fill={colors[0]} maxBarSize={10} isAnimationActive={false} />
            <Bar dataKey="b" fill={colors[1]} maxBarSize={10} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </div>
      <Legend items={[{ label: labels[0], color: colors[0] }, { label: labels[1], color: colors[1] }]} />
    </>
  );
}

export function pairTable(days: readonly PairDay[], labels: readonly [string, string]) {
  return { head: ["Day", labels[0], labels[1]], numeric: [1, 2], rows: days.map((d) => [d.day, d.a.toLocaleString(), d.b.toLocaleString()]) };
}
