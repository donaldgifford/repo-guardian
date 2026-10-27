import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { BarList, foldBarList } from "./BarList";
import { ChartCard } from "./ChartCard";
import { DistBar } from "./DistBar";
import { fitTicks, niceBounds, tickLabel, xTicksFor } from "./scale";
import { Sparkline } from "./Sparkline";
import { rowShares, StackedBars, stackTable } from "./StackedBars";
import { trendTable } from "./TrendChart";

describe("niceBounds", () => {
  test("a narrow percent band does not start at zero", () => {
    expect(niceBounds([97.2, 98.1, 98.9], { percent: true })).toEqual({ min: 90, max: 100, ticks: [90, 92.5, 95, 97.5, 100] });
  });

  test("a wide band brackets the data on the step", () => {
    const b = niceBounds([23, 71.4, 55], { percent: true });
    expect(b.min).toBe(20);
    expect(b.max).toBe(80);
  });

  test("the full range stays within 0 and 100", () => {
    const b = niceBounds([0, 100], { percent: true });
    expect(b).toEqual({ min: 0, max: 100, ticks: [0, 25, 50, 75, 100] });
  });

  test("a single point gets a band around it", () => {
    expect(niceBounds([87.5], { percent: true })).toMatchObject({ min: 80, max: 90 });
    expect(niceBounds([100], { percent: true })).toMatchObject({ min: 90, max: 100 });
  });

  test("empty or all-null data gives the full range", () => {
    expect(niceBounds([], { percent: true })).toMatchObject({ min: 0, max: 100 });
    expect(niceBounds([null, undefined], { percent: true })).toMatchObject({ min: 0, max: 100 });
    expect(niceBounds([])).toMatchObject({ min: 0, max: 10 });
  });

  test("counts never go below zero and gaps are ignored", () => {
    expect(niceBounds([3, null, 12])).toMatchObject({ min: 0, max: 20 });
  });
});

describe("tick fitting", () => {
  test.each([
    [1, 6, "time"],
    [7, 7, "date"],
    [30, 6, "date"],
    [90, 6, "date"],
  ] as const)("%dd → %d %s ticks", (days, want, style) => {
    expect(xTicksFor(days)).toEqual({ want, style });
  });

  test("fitTicks steps evenly back from the last point and never repeats", () => {
    expect(fitTicks(30, 6)).toEqual([5, 11, 17, 23, 29]);
    // Eight daily snapshots: every other day, never an uneven skip.
    expect(fitTicks(8, 6)).toEqual([1, 3, 5, 7]);
    expect(fitTicks(7, 7)).toEqual([0, 1, 2, 3, 4, 5, 6]);
    expect(fitTicks(3, 7)).toEqual([0, 1, 2]);
    expect(fitTicks(1, 6)).toEqual([0]);
    expect(fitTicks(0, 6)).toEqual([]);
    expect(fitTicks(2, 6)).toEqual([0, 1]);
  });

  test("tickLabel is UTC", () => {
    expect(tickLabel("2026-03-05T23:30:00Z", "date")).toBe("Mar 5");
    expect(tickLabel("2026-03-05T23:30:00Z", "time")).toBe("23:30");
    expect(tickLabel("nope", "date")).toBe("nope");
  });
});

describe("BarList", () => {
  const nine = Array.from({ length: 9 }, (_, i) => ({ label: `r${i}`, value: 10 - i }));

  test("folds the ninth row into other", () => {
    const rows = foldBarList(nine);
    expect(rows).toHaveLength(9);
    expect(rows[8]).toEqual({ label: "other", value: 2, sub: "1 more" });
    expect(rows.slice(0, 8).map((r) => r.label)).toEqual(["r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7"]);
  });

  test("eight rows are not folded and sort descending", () => {
    const rows = foldBarList([
      { label: "b", value: 1 },
      { label: "a", value: 5 },
      { label: "c", value: 1 },
    ]);
    expect(rows.map((r) => r.label)).toEqual(["a", "b", "c"]);
    expect(foldBarList(nine.slice(0, 8))).toHaveLength(8);
  });

  test("renders count, share and a bar scaled to the largest", () => {
    const html = renderToStaticMarkup(
      <BarList
        aria-label="parked by reason"
        items={[
          { label: "archived", value: 30 },
          { label: "fork", value: 10 },
        ]}
      />,
    );
    expect(html).toContain("width:100%");
    expect(html).toMatch(/width:33\.3+6?%/);
    expect(html).toContain("· 75%");
    expect(html).toContain("· 25%");
  });
});

describe("StackedBars", () => {
  const segs = [
    { key: "compliant", label: "compliant", color: "var(--color-ok)" },
    { key: "non_compliant", label: "failing", color: "var(--color-bad)" },
  ];

  test("rowShares is percent of the row, not of the largest row", () => {
    expect(rowShares({ label: "a", counts: { compliant: 3, non_compliant: 1 } }, segs)).toEqual({
      total: 4,
      shares: [
        { key: "compliant", value: 3, percent: 75 },
        { key: "non_compliant", value: 1, percent: 25 },
      ],
    });
    expect(rowShares({ label: "empty", counts: {} }, segs)).toEqual({
      total: 0,
      shares: [
        { key: "compliant", value: 0, percent: 0 },
        { key: "non_compliant", value: 0, percent: 0 },
      ],
    });
    expect(rowShares({ label: "thirds", counts: { compliant: 2, non_compliant: 1 } }, segs).shares[0]?.percent).toBe(66.7);
  });

  test("scales rows to the largest total and prints totals", () => {
    const rows = [
      { label: "renovate", counts: { compliant: 8, non_compliant: 2 } },
      { label: "codeowners", counts: { compliant: 4, non_compliant: 1 } },
    ];
    const html = renderToStaticMarkup(<StackedBars aria-label="status by rule" rows={rows} segs={segs} />);
    expect(html).toContain("width:100%");
    expect(html).toContain("width:50%");
    expect(html).toContain(">10</span>");
    expect(html).toContain('title="failing 2 (20%)"');
    expect(stackTable(rows, segs)).toEqual({ head: ["Rule", "compliant", "failing"], numeric: [1, 2], rows: [["renovate", "8", "2"], ["codeowners", "4", "1"]] });
  });
});

test("ChartCard starts on the plot and names the table toggle", () => {
  const html = renderToStaticMarkup(
    <ChartCard title="Compliance" table={{ head: ["Snapshot", "%"], rows: [["2026-03-05", "87.0%"]], numeric: [1] }} legend={<div>legend</div>}>
      <div>plot</div>
    </ChartCard>,
  );
  expect(html).toContain(">plot<");
  expect(html).toContain(">legend<");
  expect(html).not.toContain("<table");
  expect(html).toMatch(/aria-pressed="false"[^>]*>table</);
});

test("trendTable is newest first and capped at 30", () => {
  const series = Array.from({ length: 40 }, (_, i) => ({ at: `2026-01-${String(i + 1).padStart(2, "0")}T00:00:00Z`, percent: i === 5 ? null : 50 + i }));
  const t = trendTable(series, "compliance %");
  expect(t.rows).toHaveLength(30);
  expect(t.rows[0]).toEqual(["2026-01-40", "89.0%"]);
  expect(t.rows[29]).toEqual(["2026-01-11", "60.0%"]);
  expect(t.note).toBe("latest 30 of 40 snapshots");
  expect(trendTable(series.slice(0, 6), "x").rows[0]).toEqual(["2026-01-06", "—"]);
});

test("Sparkline needs two measured points and skips gaps", () => {
  expect(renderToStaticMarkup(<Sparkline values={[1, null]} />)).toContain("—");
  const html = renderToStaticMarkup(<Sparkline values={[1, null, 3]} />);
  expect(html).toContain("<svg");
  expect(html).toMatch(/d="M2\.0 20\.0 L82\.0 2\.0"/);
  expect(html).toContain("<circle");
});

test("DistBar draws only non-empty segments and lists all in the legend", () => {
  const html = renderToStaticMarkup(
    <DistBar
      aria-label="fleet"
      segments={[
        { label: "compliant", value: 3, color: "var(--color-ok)" },
        { label: "failing", value: 0, color: "var(--color-bad)" },
      ]}
    />,
  );
  expect(html.match(/<i title=/g)).toHaveLength(1);
  expect(html).toContain("failing 0");
});
