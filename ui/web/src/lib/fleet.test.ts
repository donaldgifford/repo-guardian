import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { byControl, parseFleetSearch, pickSeries, rangeDelta, underDays, worstControls } from "./fleet";
import { emptyPosture, type Posture } from "./posture";

const posture = (p: Partial<Posture>): Posture => ({ ...emptyPosture(), ...p });

test("rangeDelta needs two measured points: none for 0 or 1, last minus first past that", () => {
  expect(rangeDelta([])).toBeNull();
  expect(rangeDelta([{ at: "2026-10-01", percent: 50 }])).toBeNull();
  expect(rangeDelta([{ at: "2026-10-01", percent: null }, { at: "2026-10-02", percent: 50 }])).toBeNull();
  expect(rangeDelta([{ at: "2026-10-01", percent: 50 }, { at: "2026-10-02", percent: null }, { at: "2026-10-03", percent: 61.24 }])).toEqual({ value: 11.2, goodUp: true, unit: " pts" });
  expect(rangeDelta([{ at: "2026-10-01", percent: 50 }, { at: "2026-10-02", percent: 40 }])).toEqual({ value: -10, goodUp: true, unit: " pts" });
});

test("parseFleetSearch keeps the scope and a non-default series", () => {
  expect(parseFleetSearch({})).toEqual({});
  expect(parseFleetSearch({ series: "percent" })).toEqual({});
  expect(parseFleetSearch({ series: "non_compliant", org: "acme", days: 7 })).toEqual({ series: "non_compliant", org: "acme", days: 7 });
  expect(parseFleetSearch({ series: "bogus", days: 5 })).toEqual({});
});

const point = (at: string, control_id: string, p: Partial<Posture>): Schemas["HistoryPoint"] => ({ snapshot_at: at, control_id, compliance: posture(p) });

test("pickSeries sums each day's rows and byControl splits one history per control", () => {
  const points = [
    point("2026-10-01", "a", { compliant: 1, non_compliant: 1, unknown: 2, assigned: 4 }),
    point("2026-10-01", "b", { compliant: 3, non_compliant: 0, not_applicable: 1, assigned: 4 }),
    point("2026-10-02", "a", { compliant: 2, non_compliant: 0, assigned: 2 }),
  ];
  expect(pickSeries(points, "percent")).toEqual([
    { at: "2026-10-01", percent: 80 },
    { at: "2026-10-02", percent: 100 },
  ]);
  expect(pickSeries(points, "non_compliant")).toEqual([
    { at: "2026-10-01", percent: 1 },
    { at: "2026-10-02", percent: 0 },
  ]);
  expect(pickSeries(points, "unknown").map((p) => p.percent)).toEqual([2, 0]);
  expect(pickSeries(points, "not_applicable").map((p) => p.percent)).toEqual([1, 0]);
  const split = byControl(points);
  expect([...split.keys()]).toEqual(["a", "b"]);
  expect(split.get("a")?.map((p) => p.percent)).toEqual([50, 100]);
  expect(split.get("b")?.map((p) => p.percent)).toEqual([100]);
});

test("worstControls ranks the measured only, lowest first, five at most", () => {
  const c = (id: string, percent: number | null) => ({ id, compliance: posture({ percent }) });
  const controls = [c("f", 90), c("a", 10), c("u", null), c("b", 20), c("c", 30), c("d", 40), c("e", 50)];
  expect(worstControls(controls).map((x) => x.id)).toEqual(["a", "b", "c", "d", "e"]);
  expect(worstControls(controls, 2).map((x) => x.id)).toEqual(["a", "b"]);
});

test("underDays is the share in the youngest buckets, null with nothing counted", () => {
  const buckets = [
    { bucket: "<1d", count: 1 },
    { bucket: "1-7d", count: 2 },
    { bucket: "7-30d", count: 3 },
    { bucket: "30-90d", count: 4 },
  ];
  expect(underDays(buckets, 3)).toBe(60);
  expect(underDays(buckets.map((b) => ({ ...b, count: 0 })), 3)).toBeNull();
});
