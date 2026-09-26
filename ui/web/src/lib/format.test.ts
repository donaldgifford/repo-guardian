import { describe, expect, test } from "bun:test";

import golden from "../../../../api/testdata/compliant_percent.json";
import { ageDays, compliantPercent, formatPercent, relativeTime, statusTone, trend } from "./format";

describe("compliantPercent", () => {
  // The same table drives internal/store's TestCompliantPercent_Golden,
  // so the UI floors exactly as the API does.
  test.each(golden.map((c) => [c.compliant, c.non_compliant, c.percent] as const))("%d compliant, %d failing → %p", (compliant, nonCompliant, want) => {
    expect(compliantPercent({ compliant, non_compliant: nonCompliant })).toBe(want);
  });

  test("floors rather than rounds", () => {
    expect(compliantPercent({ compliant: 2, non_compliant: 1 })).toBe(66.6);
  });
});

describe("formatPercent", () => {
  test("null is unmeasured, never 0% or 100%", () => {
    expect(formatPercent(null)).toBe("unmeasured");
    expect(formatPercent(undefined)).toBe("unmeasured");
  });

  test("renders one decimal", () => {
    expect(formatPercent(87)).toBe("87.0%");
    expect(formatPercent(99.9)).toBe("99.9%");
  });
});

test("trend is the change against the previous snapshot, or null", () => {
  expect(trend(80.5, 78.2)).toBe(2.3);
  expect(trend(70, 75)).toBe(-5);
  expect(trend(null, 75)).toBeNull();
  expect(trend(70, null)).toBeNull();
  expect(trend(70, undefined)).toBeNull();
});

test("relativeTime", () => {
  const now = new Date("2026-09-25T12:00:00Z");
  expect(relativeTime("2026-09-25T09:00:00Z", now)).toBe("3 hours ago");
  expect(relativeTime("2026-09-23T12:00:00Z", now)).toBe("2 days ago");
  expect(relativeTime("2026-09-25T11:59:30Z", now)).toBe("30 seconds ago");
  expect(relativeTime(undefined, now)).toBe("never");
});

test("ageDays counts whole days", () => {
  expect(ageDays("2026-09-16T13:00:00Z", new Date("2026-09-25T12:00:00Z"))).toBe(8);
});

test("statusTone treats an unknown status as neutral", () => {
  expect(statusTone("non_compliant")).toBe("non_compliant");
  expect(statusTone("quarantined")).toBe("neutral");
});
