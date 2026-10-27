import { expect, test } from "bun:test";

import { parseOrgSearch, parseScopeSearch } from "./scope";

test("parseOrgSearch keeps a non-empty org string and nothing else", () => {
  expect(parseOrgSearch({ org: "acme" })).toEqual({ org: "acme" });
  expect(parseOrgSearch({ org: "" })).toEqual({});
  expect(parseOrgSearch({ org: 7 })).toEqual({});
  expect(parseOrgSearch({})).toEqual({});
});

test("parseScopeSearch round-trips org as a string and days as a number", () => {
  expect(parseScopeSearch({ org: "acme", days: "7" })).toEqual({ org: "acme", days: 7 });
  expect(parseScopeSearch({ days: "30", q: "x" })).toEqual({});
});
