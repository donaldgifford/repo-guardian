import { expect, test } from "bun:test";

import { parseDays, parseRangeSearch } from "./range";

test("parseDays accepts the four ranges as numbers or URL strings and defaults the rest to 30", () => {
  expect(parseDays(7)).toBe(7);
  expect(parseDays("7")).toBe(7);
  expect(parseDays("90")).toBe(90);
  expect(parseDays(1)).toBe(1);
  expect(parseDays("2")).toBe(30);
  expect(parseDays("7d")).toBe(30);
  expect(parseDays("")).toBe(30);
  expect(parseDays(undefined)).toBe(30);
});

test("parseRangeSearch keeps a number in the search and never writes the default", () => {
  expect(parseRangeSearch({ days: "7" })).toEqual({ days: 7 });
  expect(parseRangeSearch({ days: 90 })).toEqual({ days: 90 });
  expect(parseRangeSearch({ days: 30 })).toEqual({});
  expect(parseRangeSearch({ days: "nope" })).toEqual({});
  expect(parseRangeSearch({})).toEqual({});
});
