import { expect, test } from "bun:test";

import type { Schemas } from "../../api/client";
import { share } from "../../components/ui/group-grid";
import { addPosture, emptyPosture, type Posture } from "../../lib/posture";
import { countByRepository, joinOpen, matchesFilter, measuredOf, openOrgs, parseOrgsSearch, postureCells, prActivity, sumOrgs, toggleOpen } from "./tree";

const posture = (p: Partial<Posture>): Posture => ({ ...emptyPosture(), ...p });

const orgs: Schemas["OrgSummary"][] = [
  { org: "acme", tracked: 12, parked: 2, open_prs: 3, compliance: posture({ assigned: 20, compliant: 8, non_compliant: 2, unknown: 10, percent: 80, coverage: 50 }) },
  { org: "globex", tracked: 4, parked: 0, open_prs: 1, compliance: posture({ assigned: 4, compliant: 1, non_compliant: 3, percent: 25, coverage: 100 }) },
];

test("parseOrgsSearch keeps the tab (not the default), q, open and org", () => {
  expect(parseOrgsSearch({})).toEqual({});
  expect(parseOrgsSearch({ tab: "parked", q: "web", open: "acme,globex", org: "acme" })).toEqual({ tab: "parked", q: "web", open: "acme,globex", org: "acme" });
  expect(parseOrgsSearch({ tab: "compliance", q: "", open: 3 })).toEqual({});
  expect(parseOrgsSearch({ tab: "bogus" })).toEqual({});
});

test("the open list round-trips through ?open= and toggles", () => {
  expect(openOrgs(undefined)).toEqual([]);
  expect(openOrgs("acme,,globex")).toEqual(["acme", "globex"]);
  expect(joinOpen([])).toBeUndefined();
  expect(joinOpen(toggleOpen(["acme"], "globex"))).toBe("acme,globex");
  expect(toggleOpen(["acme", "globex"], "acme")).toEqual(["globex"]);
});

test("the footer sums the visible org rows and equals /orgs' totals", () => {
  const total = sumOrgs(orgs);
  expect(total.tracked).toBe(16);
  expect(total.parked).toBe(2);
  expect(total.open_prs).toBe(4);
  // addPosture recomputes the percentage from the sums: 9 of 14 measured.
  expect(total.compliance).toEqual(addPosture(orgs[0]!.compliance, orgs[1]!.compliance));
  expect(total.compliance.compliant).toBe(9);
  expect(total.compliance.non_compliant).toBe(5);
  expect(total.compliance.percent).toBe(64.2);
  expect(sumOrgs([]).compliance.percent).toBeNull();
});

test("the shading denominator is the measured assignments, never the unknown bucket", () => {
  const p = orgs[0]!.compliance;
  expect(measuredOf(p)).toBe(10);
  const cells = postureCells(p);
  expect(cells.compliant).toEqual({ n: 8, of: 10 });
  expect(share({ n: 8, of: 10 })).toBe(0.8);
  // Tracked is 12 and assigned 20: neither is the denominator.
  expect(measuredOf(p)).not.toBe(p.assigned);
});

test("an excluded repository measures nothing and its cells have no share", () => {
  const excluded = postureCells(posture({ assigned: 0 }));
  expect(share(excluded.compliant as { n: number; of: number })).toBeNull();
  expect(share(excluded.non_compliant as { n: number; of: number })).toBeNull();
  // All-unknown reads the same: nothing measured, never 0% or 100%.
  expect(share(postureCells(posture({ assigned: 3, unknown: 3 })).compliant as { n: number; of: number })).toBeNull();
});

test("matchesFilter is a case-insensitive substring and empty matches all", () => {
  expect(matchesFilter("Web-App", "web")).toBe(true);
  expect(matchesFilter("api", "web")).toBe(false);
  expect(matchesFilter("api", undefined)).toBe(true);
  expect(matchesFilter("api", "")).toBe(true);
});

test("open pull requests count per repository and per org with staleness and the oldest age", () => {
  const now = new Date("2026-10-23T00:00:00Z");
  const pr = (org: string, repository_id: number, daysAgo: number) => ({ org, repository_id, created_at: new Date(now.getTime() - daysAgo * 86_400_000).toISOString() });
  const prs = [pr("acme", 1, 3), pr("acme", 1, 45), pr("acme", 2, 10), pr("globex", 9, 31)];
  expect([...countByRepository(prs).entries()]).toEqual([
    [1, 2],
    [2, 1],
    [9, 1],
  ]);
  expect(prActivity([{ org: "acme" }, { org: "initech" }], prs, now)).toEqual([
    { org: "acme", open: 3, stale: 1, oldestDays: 45 },
    { org: "initech", open: 0, stale: 0, oldestDays: null },
  ]);
});
