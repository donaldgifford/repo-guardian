import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { hasNoAccess, heatOpacity, initials } from "./viewmodel";

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
