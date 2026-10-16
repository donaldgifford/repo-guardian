import { expect, test } from "bun:test";

import { parseRange, rangeDays, rangeFrom } from "./range";

test("parseRange accepts the four ranges and defaults the rest to 90d", () => {
  expect(parseRange("7d")).toBe("7d");
  expect(parseRange("1d")).toBe("1d");
  expect(parseRange("2d")).toBe("90d");
  expect(parseRange(undefined)).toBe("90d");
  expect(parseRange(7)).toBe("90d");
});

test("rangeDays reads the number", () => {
  expect(rangeDays("1d")).toBe(1);
  expect(rangeDays("90d")).toBe(90);
});

test("rangeFrom rounds a week or more to the day and a day to the hour", () => {
  const now = Date.parse("2026-10-16T13:45:12.345Z");
  expect(rangeFrom("90d", now)).toBe("2026-07-18T00:00:00.000Z");
  expect(rangeFrom("7d", now)).toBe("2026-10-09T00:00:00.000Z");
  expect(rangeFrom("1d", now)).toBe("2026-10-15T13:00:00.000Z");
  // Stable within the hour, so the query key does not churn.
  expect(rangeFrom("1d", now + 10 * 60_000)).toBe(rangeFrom("1d", now));
});
