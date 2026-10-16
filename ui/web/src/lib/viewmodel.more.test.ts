import { describe, expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { cadenceWord, complianceSeries, deltaFrom, failingRuleCount, fleetSeries, heatOpacity, initials, orgCountLabel, type SeriesPoint } from "./viewmodel";

// The IMPL-0031 1.10 additions; the older helpers are in viewmodel.test.ts.

const counts = (c: Partial<Schemas["StatusCounts"]> = {}): Schemas["StatusCounts"] => ({ compliant: 0, non_compliant: 0, not_applicable: 0, unknown: 0, ...c });

describe("fleetSeries", () => {
  const point = (org: string, at: string, c: Partial<Schemas["StatusCounts"]>): Schemas["HistoryPoint"] => ({
    org,
    kind: "file",
    rule: "r",
    snapshot_at: at,
    counts: counts(c),
    compliant_percent: null,
  });

  test("sums all four counts per snapshot and floors the percent", () => {
    const s = fleetSeries([
      point("a", "2026-09-02", { compliant: 2, non_compliant: 1, not_applicable: 4 }),
      point("b", "2026-09-02", { unknown: 3 }),
      point("a", "2026-09-01", { compliant: 1 }),
    ]);
    expect(s).toEqual([
      { at: "2026-09-01", percent: 100, counts: counts({ compliant: 1 }) },
      { at: "2026-09-02", percent: 66.6, counts: counts({ compliant: 2, non_compliant: 1, not_applicable: 4, unknown: 3 }) },
    ]);
    expect(complianceSeries([point("a", "2026-09-01", { compliant: 1 })])).toEqual([{ at: "2026-09-01", percent: 100 }]);
  });
});

describe("deltaFrom", () => {
  const s = (...p: (number | null)[]): SeriesPoint[] => p.map((percent, i) => ({ at: `2026-09-0${i + 1}`, percent }));

  test.each([
    ["one point", s(80), null],
    ["two points", s(80, 82.4), 2.4],
    ["a fall", s(90, 85.5), -4.5],
    ["a gap at the range edge skips to the last measured pair", s(80, 82, null), 2],
    ["a gap in the middle", s(80, null, 81), 1],
    ["one measured point among gaps", s(null, 80, null), null],
    ["empty", s(), null],
  ])("%s", (_name, series, want) => {
    expect(deltaFrom(series)).toBe(want);
  });
});

describe("cadenceWord", () => {
  const at = (days: number[]): SeriesPoint[] => days.map((d) => ({ at: new Date(Date.UTC(2026, 8, 1 + d)).toISOString(), percent: 1 }));

  test.each([
    ["one point", at([0]), "—"],
    ["empty", at([]), "—"],
    ["daily", at([0, 1, 2, 3]), "daily"],
    ["every 7d", at([0, 7, 14]), "every 7d"],
    ["median ignores one long gap", at([0, 1, 2, 3, 30]), "daily"],
    ["even count takes the middle pair", at([0, 2, 4, 8, 16]), "every 3d"],
  ])("%s", (_name, series, want) => {
    expect(cadenceWord(series)).toBe(want);
  });

  test("sub-daily snapshots read as daily", () => {
    const hourly = [0, 1, 2].map((h) => ({ at: new Date(Date.UTC(2026, 8, 1, h)).toISOString(), percent: 1 }));
    expect(cadenceWord(hourly)).toBe("daily");
  });
});

test("heatOpacity is zero for an empty cell and climbs from 8% to 50%", () => {
  expect(heatOpacity(0, 100)).toBe(0);
  expect(heatOpacity(5, 0)).toBe(0);
  expect(heatOpacity(1, 100)).toBe(0.08);
  expect(heatOpacity(50, 100)).toBe(0.29);
  expect(heatOpacity(100, 100)).toBe(0.5);
  expect(heatOpacity(150, 100)).toBe(0.5);
});

test("initials come from the first two words, else the subject", () => {
  expect(initials("Ada Lovelace", "u1")).toBe("AL");
  expect(initials("ada", "u1")).toBe("A");
  expect(initials("Ada  Byron King", "u1")).toBe("AB");
  expect(initials("", "github|12345")).toBe("GI");
  expect(initials("   ", "x")).toBe("X");
});

test("orgCountLabel counts the API's orgs for all_orgs, else /me's", () => {
  const me = (allOrgs: boolean, orgs: string[]): Schemas["Me"] => ({ subject: "u1", name: "Ada", all_orgs: allOrgs, orgs });
  const all = [{ org: "a" }, { org: "b" }, { org: "c" }] as Schemas["OrgSummary"][];
  expect(orgCountLabel(me(true, []), all)).toBe("All orgs (3)");
  expect(orgCountLabel(me(true, []), undefined)).toBe("All orgs (0)");
  expect(orgCountLabel(me(false, ["a", "b"]), all)).toBe("All orgs (2)");
});

test("failingRuleCount counts rules with any failure", () => {
  expect(failingRuleCount([{ counts: counts({ compliant: 1 }) }, { counts: counts({ non_compliant: 3 }) }, { counts: counts({ non_compliant: 1 }) }])).toBe(2);
  expect(failingRuleCount([])).toBe(0);
});
