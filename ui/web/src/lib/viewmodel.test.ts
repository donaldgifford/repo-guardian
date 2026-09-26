import { describe, expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { complianceSeries, hasNoAccess, parkedTotal, seriesByOrg, visibleOrgs, worstRules } from "./viewmodel";

const counts = (compliant: number, nonCompliant: number) => ({ compliant, non_compliant: nonCompliant, not_applicable: 0, unknown: 0 });

const rule = (name: string, p: number | null): Schemas["RuleCompliance"] => ({ kind: "file", name, counts: counts(0, 0), compliant_percent: p });

test("worstRules orders lowest first, unmeasured last", () => {
  const got = worstRules([rule("a", 90), rule("b", null), rule("c", 61), rule("d", 78)], 3).map((r) => r.name);
  expect(got).toEqual(["c", "d", "a"]);
});

describe("complianceSeries", () => {
  const point = (org: string, at: string, compliant: number, nonCompliant: number): Schemas["HistoryPoint"] => ({
    org,
    kind: "file",
    rule: "r",
    snapshot_at: at,
    counts: counts(compliant, nonCompliant),
    compliant_percent: null,
  });

  test("sums counts per snapshot and floors, never averages percentages", () => {
    // A tiny org at 0% and a big one at 99% average to 49.5, but the
    // fleet is 98.9% (990 of 1,001).
    const s = complianceSeries([point("big", "2026-09-02", 990, 10), point("tiny", "2026-09-02", 0, 1), point("big", "2026-09-01", 1, 2)]);
    expect(s).toEqual([
      { at: "2026-09-01", percent: 33.3 },
      { at: "2026-09-02", percent: 98.9 },
    ]);
  });

  test("a snapshot with nothing measured is a gap, not zero", () => {
    expect(complianceSeries([point("a", "2026-09-01", 0, 0)])).toEqual([{ at: "2026-09-01", percent: null }]);
  });

  test("seriesByOrg splits per org, sorted", () => {
    const m = seriesByOrg([point("zeta", "2026-09-01", 1, 0), point("acme", "2026-09-01", 0, 1)]);
    expect([...m.keys()]).toEqual(["acme", "zeta"]);
    expect(m.get("zeta")).toEqual([{ at: "2026-09-01", percent: 100 }]);
  });
});

test("parkedTotal sums every reason", () => {
  expect(parkedTotal([{ reason: "archived", count: 150 }, { reason: "fork", count: 48 }])).toBe(198);
});

describe("access", () => {
  const me = (allOrgs: boolean, orgs: string[]): Schemas["Me"] => ({ subject: "u1", name: "Ada", all_orgs: allOrgs, orgs });

  test("the org filter lists /me's orgs, or every org for all_orgs", () => {
    expect(visibleOrgs(me(false, ["zeta", "acme", "acme"]), undefined)).toEqual(["acme", "zeta"]);
    const all = [{ org: "b" }, { org: "a" }] as Schemas["OrgSummary"][];
    expect(visibleOrgs(me(true, []), all)).toEqual(["a", "b"]);
  });

  test("a caller with no org gets the ask-for-access page", () => {
    expect(hasNoAccess(me(false, []))).toBe(true);
    expect(hasNoAccess(me(false, ["acme"]))).toBe(false);
    expect(hasNoAccess(me(true, []))).toBe(false);
  });
});
