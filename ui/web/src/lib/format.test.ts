import { describe, expect, test } from "bun:test";

import golden from "../../../../api/testdata/compliant_percent.json";
import { ageDays, compliantPercent, formatPercent, humanizeDuration, relativeTime, toneFor, trend } from "./format";

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
  test("null is no data, never 0% or 100%", () => {
    expect(formatPercent(null)).toBe("no data");
    expect(formatPercent(undefined)).toBe("no data");
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

describe("toneFor", () => {
  // Every documented value of each open enum, plus one the table has
  // never heard of (DESIGN-0036 §Badges).
  test.each([
    ["status", "compliant", "ok"],
    ["status", "non_compliant", "bad"],
    ["status", "not_applicable", "mute"],
    ["status", "unknown", "warn"],
    ["status", "quarantined", "mute"],
    ["rule", "pass", "ok"],
    ["rule", "fail", "bad"],
    ["rule", "error", "bad"],
    ["rule", "unknown", "warn"],
    ["rule", "not_applicable", "mute"],
    ["rule", "skipped", "mute"],
    ["bucket", "compliant", "ok"],
    ["bucket", "non_compliant", "bad"],
    ["bucket", "pending", "info"],
    ["bucket", "stale", "warn"],
    ["bucket", "unmeasurable_access_denied", "warn"],
    ["bucket", "unmeasurable_suspended", "warn"],
    ["bucket", "unmeasurable_conflict", "warn"],
    ["bucket", "excluded", "mute"],
    ["decision", "baseline", "outline"],
    ["decision", "org_added", "outline"],
    ["remediation", "pr_open", "info"],
    ["remediation", "applied", "ok"],
    ["remediation", "foreign_pr", "warn"],
    ["remediation", "disabled", "mute"],
    ["remediation", "dry_run", "mute"],
    ["remediation", "none", "mute"],
    ["remediation", "teleported", "mute"],
    ["reason", "non_compliant", "warn"],
    ["reason", "compliant", "mute"],
    ["reason", "not_applicable", "mute"],
    ["reason", "unknown", "mute"],
    ["reason", "quarantined", "mute"],
    ["state", "operational", "ok"],
    ["state", "degraded", "warn"],
    ["state", "down", "bad"],
    ["state", "unknown", "bad"],
    ["state", "on_fire", "bad"],
    ["kind", "file", "outline"],
    ["kind", "setting", "outline"],
    ["kind", "branch_protection", "outline"],
    ["kind", "webhook", "outline"],
  ] as const)("%s %s → %s", (field, value, want) => {
    expect(toneFor(field, value)).toBe(want);
  });
});

test.each([
  ["720h", "30d"],
  ["720h0m0s", "30d"],
  ["24h", "1d"],
  ["36h", "36h"],
  ["0h", "0h"],
  ["90m", "90m"],
  ["", ""],
])("humanizeDuration(%p) → %p", (input, want) => {
  expect(humanizeDuration(input)).toBe(want);
});
