import { Bar, BarChart, CartesianGrid, LabelList, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import { niceBounds } from "./scale";
import { ChartTip } from "./tooltip";

const AXIS = { fontFamily: "var(--font-mono)", fontSize: 11, fill: "var(--color-muted-foreground)" } as const;

export interface Bucket {
  label: string;
  value: number;
}

// BarBuckets is the single-series bar chart: bars at most 24px wide
// with a 4px rounded data end, the count on each cap, a per-bar
// tooltip over the whole column, and a y axis bracketed by niceBounds.
export function BarBuckets({
  buckets,
  color = "var(--color-accent)",
  label = "count",
  height = 200,
  "aria-label": ariaLabel,
}: {
  buckets: readonly Bucket[];
  color?: string;
  label?: string;
  height?: number;
  "aria-label": string;
}) {
  const bounds = niceBounds(buckets.map((b) => b.value * 1.1), { pad: 0 });
  return (
    <div role="img" aria-label={ariaLabel} style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={buckets.map((b) => ({ ...b }))} margin={{ top: 18, right: 10, bottom: 0, left: 0 }} barCategoryGap="40%">
          <CartesianGrid stroke="var(--color-border)" vertical={false} />
          <XAxis dataKey="label" tick={AXIS} axisLine={false} tickLine={false} interval={0} />
          <YAxis domain={[0, bounds.max]} ticks={bounds.ticks} allowDecimals={false} tick={AXIS} axisLine={false} tickLine={false} width={34} />
          <Tooltip
            cursor={{ fill: "var(--color-panel-2)" }}
            isAnimationActive={false}
            content={({ active, payload, label: at }) => {
              if (!active || !payload?.[0]) return null;
              return <ChartTip heading={String(at)} rows={[{ label, value: Number(payload[0].value).toLocaleString(), color }]} />;
            }}
          />
          <Bar dataKey="value" fill={color} maxBarSize={24} radius={[4, 4, 0, 0]} isAnimationActive={false}>
            <LabelList dataKey="value" position="top" offset={6} style={{ ...AXIS, fill: "var(--color-foreground)" }} formatter={(v) => (typeof v === "number" ? v.toLocaleString() : v)} />
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

export function bucketsTable(buckets: readonly Bucket[], label = "count") {
  return { head: ["Bucket", label], numeric: [1], rows: buckets.map((b) => [b.label, b.value.toLocaleString()]) };
}
