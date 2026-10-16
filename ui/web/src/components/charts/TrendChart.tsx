import { useMemo } from "react";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { SeriesPoint } from "../../lib/viewmodel";
import { Empty } from "../common";
import { fitTicks, formatValue, niceBounds, tickLabel, xTicksFor } from "./scale";
import { ChartTip } from "./tooltip";

const AXIS = { fontFamily: "var(--font-mono)", fontSize: 11, fill: "var(--color-muted-foreground)" } as const;

export interface TrendChartProps {
  series: readonly SeriesPoint[];
  unit?: "%" | "count";
  color?: string;
  // rangeDays fits the x ticks: a day gets clock times, longer ranges
  // dates.
  rangeDays?: number;
  height?: number;
  label?: string;
  "aria-label": string;
}

interface EndDotProps {
  cx?: number;
  cy?: number;
  index?: number;
  value?: number | null;
}

// TrendChart is the area chart (DESIGN-0036 §Charts): one series, a
// 2px line over a 10% wash, a 4.5px end dot ringed in the panel colour
// with the current value beside it, a nice-bounded y axis, x ticks
// fitted to the range, and a crosshair tooltip over the full column.
// Unmeasured points are gaps, never zero.
export function TrendChart({ series, unit = "%", color = "var(--color-accent)", rangeDays = 30, height = 230, label = "compliance %", "aria-label": ariaLabel }: TrendChartProps) {
  const bounds = useMemo(() => niceBounds(series.map((p) => p.percent), { percent: unit === "%" }), [series, unit]);
  const { want, style } = xTicksFor(rangeDays);
  const ticks = useMemo(() => fitTicks(series.length, want).map((i) => series[i]?.at ?? ""), [series, want]);
  if (series.length === 0) {
    return <Empty>No snapshots yet.</Empty>;
  }
  const last = series.length - 1;
  const data = series.map((p) => ({ at: p.at, value: p.percent }));

  const endDot = (props: EndDotProps) => {
    const { cx, cy, index, value } = props;
    if (index !== last || cx === undefined || cy === undefined || value === null || value === undefined) {
      return <g key="end-none" />;
    }
    return (
      <g key="end">
        <circle cx={cx} cy={cy} r={4.5} fill={color} stroke="var(--color-panel)" strokeWidth={2} />
        <text x={cx - 10} y={cy - 10} textAnchor="end" fontFamily="var(--font-mono)" fontSize={11} fontWeight={600} fill="var(--color-foreground)">
          {formatValue(value, unit)}
        </text>
      </g>
    );
  };

  return (
    <div role="img" aria-label={ariaLabel} style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 12, right: 14, bottom: 0, left: 0 }}>
          <CartesianGrid stroke="var(--color-border)" vertical={false} />
          <XAxis dataKey="at" ticks={ticks} tickFormatter={(v: string) => tickLabel(v, style)} tick={AXIS} axisLine={false} tickLine={false} interval={0} />
          <YAxis domain={[bounds.min, bounds.max]} ticks={bounds.ticks} tickFormatter={(v: number) => (unit === "%" ? `${v}%` : v.toLocaleString())} tick={AXIS} axisLine={false} tickLine={false} width={44} />
          <Tooltip
            cursor={{ stroke: "var(--color-muted-foreground)", strokeDasharray: "2 3" }}
            isAnimationActive={false}
            content={({ active, payload, label: at }) => {
              if (!active || !payload?.[0]) return null;
              const v = payload[0].value as number | null | undefined;
              return <ChartTip heading={tickLabel(String(at), "date")} rows={[{ label, value: formatValue(v, unit), color }]} />;
            }}
          />
          <Area
            type="monotone"
            dataKey="value"
            stroke={color}
            strokeWidth={2}
            fill={color}
            fillOpacity={0.1}
            dot={endDot}
            activeDot={{ r: 4.5, stroke: "var(--color-panel)", strokeWidth: 2 }}
            connectNulls={false}
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

// trendTable is the ChartCard table twin: newest first, capped at 30.
export function trendTable(series: readonly SeriesPoint[], label: string, unit: "%" | "count" = "%") {
  const rows = [...series].reverse().slice(0, 30);
  return {
    head: ["Snapshot", label],
    numeric: [1],
    rows: rows.map((p) => [p.at.slice(0, 10), formatValue(p.percent, unit)]),
    note: series.length > 30 ? `latest 30 of ${series.length} snapshots` : undefined,
  };
}
