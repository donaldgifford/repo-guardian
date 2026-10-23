import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { ruleCounts } from "./controls";

test("ruleCounts tallies loaded findings by failing rule", () => {
  const row = (failing_rules: string[]) => ({ failing_rules }) as Schemas["ResultRow"];
  expect(ruleCounts([row(["present", "valid"]), row(["valid"]), row([])])).toEqual([
    { label: "present", value: 1 },
    { label: "valid", value: 2 },
  ]);
  expect(ruleCounts([])).toEqual([]);
});
