import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { hasNoAccess, heatOpacity, initials, orgCountLabel, visibleOrgs } from "./viewmodel";

const me = (allOrgs: boolean, orgs: string[]): Schemas["Me"] => ({ subject: "u1", name: "Ada", all_orgs: allOrgs, orgs });

test("a caller with no org gets the ask-for-access page", () => {
  expect(hasNoAccess(me(false, []))).toBe(true);
  expect(hasNoAccess(me(false, ["acme"]))).toBe(false);
  expect(hasNoAccess(me(true, []))).toBe(false);
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

test("visibleOrgs is /me's list, deduped and sorted, or every org for an all-orgs caller", () => {
  expect(visibleOrgs(me(false, ["zeta", "acme", "acme"]), undefined)).toEqual(["acme", "zeta"]);
  const all = [{ org: "b" }, { org: "a" }] as Schemas["OrgSummary"][];
  expect(visibleOrgs(me(true, []), all)).toEqual(["a", "b"]);
  expect(visibleOrgs(me(true, []), undefined)).toEqual([]);
});

test("orgCountLabel counts /me's orgs, or the API's for an all-orgs caller", () => {
  expect(orgCountLabel(me(false, ["acme", "zeta"]), undefined)).toBe("All orgs (2)");
  expect(orgCountLabel(me(true, []), [{ org: "a" }, { org: "b" }, { org: "c" }] as Schemas["OrgSummary"][])).toBe("All orgs (3)");
  expect(orgCountLabel(me(true, []), undefined)).toBe("All orgs (0)");
});
