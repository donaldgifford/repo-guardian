import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { rangeDelta, ruleCounts } from "./controls";

test("rangeDelta is last against first measured snapshot, in points, null until two exist", () => {
  expect(rangeDelta([])).toBeNull();
  expect(rangeDelta([{ at: "a", percent: 50 }])).toBeNull();
  expect(rangeDelta([{ at: "a", percent: null }, { at: "b", percent: 50 }])).toBeNull();
  expect(rangeDelta([{ at: "a", percent: 50 }, { at: "b", percent: null }, { at: "c", percent: 61.24 }])).toEqual({ value: 11.2, goodUp: true, unit: " pts" });
  expect(rangeDelta([{ at: "a", percent: 50 }, { at: "b", percent: 50 }])).toEqual({ value: 0, goodUp: true, unit: " pts" });
});

test("ruleCounts tallies loaded findings by failing rule", () => {
  const row = (failing_rules: string[]) => ({ failing_rules }) as Schemas["ResultRow"];
  expect(ruleCounts([row(["present", "valid"]), row(["valid"]), row([])])).toEqual([
    { label: "present", value: 1 },
    { label: "valid", value: 2 },
  ]);
  expect(ruleCounts([])).toEqual([]);
});
